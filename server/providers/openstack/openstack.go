package openstack

// OpenStack provider for scitq2, tested with OVH Public Cloud (OpenStack).
//
// It implements the generic providers.Provider interface used in this project:
//
//   type Provider interface {
//       Create(workerName, flavor, location string, jobId uint32) (string, error)
//       List() (map[string]string, error)
//       Restart(workerName string) error
//       Delete(workerName string) error
//   }
//
// Design notes
// - Auth: uses standard OpenStack env vars (incl. Application Credentials)
//   Prefer OS_APPLICATION_CREDENTIAL_ID / OS_APPLICATION_CREDENTIAL_SECRET when present.
// - Region: provided by the `location` argument of Create(), or by OS_REGION_NAME
// - Image: choose via env OPENSTACK_IMAGE (name or ID). Defaults to latest Ubuntu 22.04 if resolvable.
// - Network (tenant/private): env OPENSTACK_NETWORK_ID (recommended) or first non-external network.
// - External network for Floating IPs: env OPENSTACK_EXTNET_ID or first external network.
// - User data (cloud‑init):
//     * env OPENSTACK_USERDATA_FILE: path to a file whose raw contents are passed as cloud‑init user-data
//     * env OPENSTACK_USERDATA: inline string for small snippets
// - Metadata: tags the server with {"scitq": "1", "job_id": jobId}
// - Return value of Create: the instance public IP (floating IP) if one was attached, otherwise the
//   first private IPv4 address found.
//
// Notes for OVH:
// - Regions look like: GRA7, SBG5, DE1, etc. Use the same string as your Public Cloud region.
// - Images vary by project; name resolution is done server-side. You can also pass the image ID.
// - You usually must provide your own keypair name (OPENSTACK_KEYPAIR) that already exists in the region.

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/images"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/external"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/pagination"
	"github.com/scitq/scitq/server/config"
)

// Provider implements basic OpenStack interactions.
// Fields are mostly optional; when empty, values are discovered via env or API lookups.
// Keep this struct very lightweight so it can be created easily in tests.
type Provider struct {
	C config.OpenstackConfig
	// Defaults used when arguments are empty
	DefaultRegion string // fallback region when Create() location is empty
	Image         string // name or ID (env OPENSTACK_IMAGE)
	NetworkID     string // tenant network ID (env OPENSTACK_NETWORK_ID)
	ExtNetworkID  string // external network ID for FIPs (env OPENSTACK_EXTNET_ID)
	Keypair       string // existing keypair name (env OPENSTACK_KEYPAIR)
	UserData      []byte // cloud-init content (from env or file)
}

// NewFromConfig constructs a Provider from YAML OpenstackConfig (no env required).
func NewFromConfig(c config.OpenstackConfig) (*Provider, error) {
	p := &Provider{
		C:             c,
		DefaultRegion: c.DefaultRegion,
		Image:         c.ImageID, // supports name or ID
		NetworkID:     c.NetworkID,
		ExtNetworkID:  "",
		Keypair:       "",
	}
	if c.Custom != nil {
		if v, ok := c.Custom["ext_network_id"].(string); ok {
			p.ExtNetworkID = v
		}
		if v, ok := c.Custom["keypair"].(string); ok {
			p.Keypair = v
		}
		if path, ok := c.Custom["userdata_file"].(string); ok && path != "" {
			if b, err := ioutil.ReadFile(path); err == nil {
				p.UserData = b
			}
		} else if s, ok := c.Custom["userdata"].(string); ok && s != "" {
			p.UserData = []byte(s)
		}
	}
	return p, nil
}

// NewFromEnv constructs a Provider using environment variables (non-fatal if some are missing).
func NewFromEnv() (*Provider, error) {
	p := &Provider{
		DefaultRegion: os.Getenv("OS_REGION_NAME"),
		Image:         getenvAny("OPENSTACK_IMAGE", "OS_IMAGE"),
		NetworkID:     os.Getenv("OPENSTACK_NETWORK_ID"),
		ExtNetworkID:  os.Getenv("OPENSTACK_EXTNET_ID"),
		Keypair:       getenvAny("OPENSTACK_KEYPAIR", "OS_KEYPAIR"),
	}
	// user-data: file takes precedence over inline string
	if f := os.Getenv("OPENSTACK_USERDATA_FILE"); f != "" {
		b, err := ioutil.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read OPENSTACK_USERDATA_FILE: %w", err)
		}
		p.UserData = b
	} else if s := os.Getenv("OPENSTACK_USERDATA"); s != "" {
		p.UserData = []byte(s)
	}
	return p, nil
}

func getenvAny(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// ===== helpers to create scoped service clients =====

func (p *Provider) newProviderClient(region string) (*gophercloud.ProviderClient, error) {
	// Prefer YAML config (portable across OpenStack clouds)
	c := p.C
	if c.AuthURL != "" {
		ao := gophercloud.AuthOptions{
			IdentityEndpoint: c.AuthURL,
			Username:         c.Username,
			Password:         c.Password,
			DomainName:       c.DomainName,
			TenantID:         c.ProjectID,
			TenantName:       firstNonEmpty(c.ProjectName, c.TenantName),
			AllowReauth:      true,
		}
		pc, err := openstack.AuthenticatedClient(ao)
		if err != nil {
			return nil, err
		}
		return pc, nil
	}
	// Fallback: legacy env-based auth
	ao, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, err
	}
	pc, err := openstack.AuthenticatedClient(ao)
	if err != nil {
		return nil, err
	}
	return pc, nil
}

func (p *Provider) computeClient(region string) (*gophercloud.ServiceClient, error) {
	pc, err := p.newProviderClient(region)
	if err != nil {
		return nil, err
	}
	return openstack.NewComputeV2(pc, gophercloud.EndpointOpts{Region: region})
}

func (p *Provider) networkClient(region string) (*gophercloud.ServiceClient, error) {
	pc, err := p.newProviderClient(region)
	if err != nil {
		return nil, err
	}
	return openstack.NewNetworkV2(pc, gophercloud.EndpointOpts{Region: region})
}

// ===== implementation of providers.Provider =====

// Create boots a VM and attaches a floating IP when an external network is available.
// Returns the chosen public IP, or a private IP if no FIP could be allocated.
func (p *Provider) Create(workerName, flavorName, location string, jobId uint32) (string, error) {
	region := firstNonEmpty(location, p.DefaultRegion)
	if region == "" {
		return "", errors.New("region is required (pass location or set DefaultRegion in OpenstackConfig)")
	}

	cc, err := p.computeClient(region)
	if err != nil {
		return "", fmt.Errorf("compute client: %w", err)
	}
	nc, err := p.networkClient(region)
	if err != nil {
		return "", fmt.Errorf("network client: %w", err)
	}

	// Resolve flavor ID
	flvID, err := p.findFlavorID(cc, flavorName)
	if err != nil {
		return "", err
	}

	// Resolve image ID
	imgID, err := p.findImageID(cc, p.Image)
	if err != nil {
		return "", err
	}

	// Resolve tenant network ID to plug NIC
	netID := p.NetworkID
	if netID == "" {
		id, err := p.findFirstTenantNetworkID(nc)
		if err != nil {
			return "", err
		}
		netID = id
	}

	// Assemble NICs
	nics := []servers.Network{{UUID: netID}}

	// Build metadata and user-data
	metadata := map[string]string{
		"scitq":  "1",
		"job_id": strconv.Itoa(int(jobId)),
	}

	createOpts := servers.CreateOpts{
		Name:      workerName,
		FlavorRef: flvID,
		ImageRef:  imgID,
		Networks:  nics,
		Metadata:  metadata,
	}
	if len(p.UserData) > 0 {
		createOpts.UserData = p.UserData
	}

	// Wrap with keypairs extension if a keypair is specified
	var createBuilder servers.CreateOptsBuilder = createOpts
	if p.Keypair != "" {
		createBuilder = keypairs.CreateOptsExt{
			CreateOptsBuilder: createOpts,
			KeyName:           p.Keypair,
		}
	}

	// Create instance
	server, err := servers.Create(cc, createBuilder).Extract()
	if err != nil {
		return "", fmt.Errorf("create server: %w", err)
	}

	// Wait for ACTIVE
	if err := waitForStatus(cc, server.ID, "ACTIVE", 600*time.Second); err != nil {
		return "", err
	}

	// Try to allocate + associate a floating IP from an external network.
	pubIP := ""
	if ext := firstNonEmpty(p.ExtNetworkID); ext == "" {
		if id, err := p.findFirstExternalNetworkID(nc); err == nil {
			pubIP, _ = p.attachFIP(nc, cc, server.ID, id)
		}
	} else {
		pubIP, _ = p.attachFIP(nc, cc, server.ID, p.ExtNetworkID)
	}

	if pubIP != "" {
		return pubIP, nil
	}
	// Fallback to first private IPv4
	if ip, err := p.firstIPv4(cc, server.ID); err == nil && ip != "" {
		return ip, nil
	}
	return "", errors.New("instance created but no IP address could be determined")
}

// List returns a map of server name -> preferred IP (public if available, else private).
func (p *Provider) List() (map[string]string, error) {
	region := p.DefaultRegion
	if region == "" {
		return nil, errors.New("default region is required for List(); set it in OpenstackConfig.region or pass location")
	}
	cc, err := p.computeClient(region)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	pager := servers.List(cc, servers.ListOpts{})
	err = pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		for _, s := range list {
			ip := pickBestIP(s)
			out[s.Name] = ip
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Provider) Restart(workerName string) error {
	cc, err := p.computeClient(p.DefaultRegion)
	if err != nil {
		return err
	}
	s, err := p.findServerByName(cc, workerName)
	if err != nil {
		return err
	}
	return servers.Reboot(cc, s.ID, servers.RebootOpts{Type: servers.SoftReboot}).ExtractErr()
}

func (p *Provider) Delete(workerName string) error {
	region := p.DefaultRegion
	if region == "" {
		return errors.New("default region is required for Delete(); set it in OpenstackConfig.region or pass location")
	}

	cc, err := p.computeClient(region)
	if err != nil {
		return err
	}
	nc, err := p.networkClient(region)
	if err != nil {
		return err
	}

	s, err := p.findServerByName(cc, workerName)
	if err != nil {
		return err
	}

	// Best effort: detach & delete any floating IPs pointing to this server's ports
	_ = p.detachAndDeleteFIPs(nc, cc, s.ID)

	return servers.Delete(cc, s.ID).ExtractErr()
}

// ===== internal helpers =====

func (p *Provider) findFlavorID(cc *gophercloud.ServiceClient, nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", errors.New("flavor is required")
	}
	// Try direct ID first
	if looksLikeUUID(nameOrID) {
		return nameOrID, nil
	}

	var matchID string
	pager := flavors.ListDetail(cc, flavors.ListOpts{})
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := flavors.ExtractFlavors(page)
		if err != nil {
			return false, err
		}
		for _, f := range list {
			if strings.EqualFold(f.Name, nameOrID) {
				matchID = f.ID
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if matchID == "" {
		return "", fmt.Errorf("flavor not found: %s", nameOrID)
	}
	return matchID, nil
}

func (p *Provider) findImageID(cc *gophercloud.ServiceClient, nameOrID string) (string, error) {
	if nameOrID == "" {
		// Fallback strategy: list images, pick the newest Ubuntu 22.04
		return newestUbuntu2204ImageID(cc)
	}
	if looksLikeUUID(nameOrID) {
		return nameOrID, nil
	}

	pager := images.ListDetail(cc, images.ListOpts{Name: nameOrID})
	id := ""
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := images.ExtractImages(page)
		if err != nil {
			return false, err
		}
		for _, im := range list {
			if strings.EqualFold(im.Name, nameOrID) {
				id = im.ID
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("image not found: %s", nameOrID)
	}
	return id, nil
}

func (p *Provider) findFirstTenantNetworkID(nc *gophercloud.ServiceClient) (string, error) {
	trueVal := true
	pager := networks.List(nc, networks.ListOpts{AdminStateUp: &trueVal})
	var id string
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := networks.ExtractNetworks(page)
		if err != nil {
			return false, err
		}
		for _, n := range list {
			// Fetch external-net extension data for this network via ExtractInto
			var ne struct{ external.NetworkExternalExt }
			if err := networks.Get(nc, n.ID).ExtractInto(&ne); err != nil {
				// If extension is unavailable, skip gracefully
				continue
			}
			if !ne.External {
				id = n.ID
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("no tenant (non-external) network found; set OPENSTACK_NETWORK_ID")
	}
	return id, nil
}

func (p *Provider) findFirstExternalNetworkID(nc *gophercloud.ServiceClient) (string, error) {
	pager := networks.List(nc, networks.ListOpts{})
	var id string
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := networks.ExtractNetworks(page)
		if err != nil {
			return false, err
		}
		for _, n := range list {
			var ne struct{ external.NetworkExternalExt }
			if err := networks.Get(nc, n.ID).ExtractInto(&ne); err != nil {
				continue
			}
			if ne.External {
				id = n.ID
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("no external network found; set OPENSTACK_EXTNET_ID to enable Floating IPs")
	}
	return id, nil
}

func (p *Provider) attachFIP(nc, cc *gophercloud.ServiceClient, serverID, extNetID string) (string, error) {
	// Find the first port of the server
	prts, err := p.portsOfServer(nc, serverID)
	if err != nil || len(prts) == 0 {
		return "", fmt.Errorf("lookup ports: %w", err)
	}
	portID := prts[0].ID

	// Allocate a floating IP on the external network
	fip, err := floatingips.Create(nc, floatingips.CreateOpts{FloatingNetworkID: extNetID}).Extract()
	if err != nil {
		return "", fmt.Errorf("alloc FIP: %w", err)
	}

	// Associate to the instance port
	_, err = floatingips.Update(nc, fip.ID, floatingips.UpdateOpts{PortID: &portID}).Extract()
	if err != nil {
		return "", fmt.Errorf("associate FIP: %w", err)
	}
	return fip.FloatingIP, nil
}

func (p *Provider) detachAndDeleteFIPs(nc, cc *gophercloud.ServiceClient, serverID string) error {
	prts, err := p.portsOfServer(nc, serverID)
	if err != nil {
		return err
	}
	// List all project floating IPs and delete the ones attached to these ports
	pager := floatingips.List(nc, floatingips.ListOpts{})
	err = pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := floatingips.ExtractFloatingIPs(page)
		if err != nil {
			return false, err
		}
		for _, f := range list {
			for _, pt := range prts {
				if f.PortID == pt.ID {
					_, _ = floatingips.Update(nc, f.ID, floatingips.UpdateOpts{PortID: nil}).Extract()
					_ = floatingips.Delete(nc, f.ID).ExtractErr()
				}
			}
		}
		return true, nil
	})
	return err
}

func (p *Provider) portsOfServer(nc *gophercloud.ServiceClient, serverID string) ([]ports.Port, error) {
	pager := ports.List(nc, ports.ListOpts{DeviceID: serverID})
	var res []ports.Port
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := ports.ExtractPorts(page)
		if err != nil {
			return false, err
		}
		res = append(res, list...)
		return true, nil
	})
	return res, err
}

func waitForStatus(cc *gophercloud.ServiceClient, serverID, target string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s, err := servers.Get(cc, serverID).Extract()
		if err != nil {
			return err
		}
		if strings.EqualFold(s.Status, target) {
			return nil
		}
		if strings.EqualFold(s.Status, "ERROR") {
			return fmt.Errorf("instance entered ERROR state")
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("timeout waiting for status=%s", target)
}

func (p *Provider) firstIPv4(cc *gophercloud.ServiceClient, serverID string) (string, error) {
	s, err := servers.Get(cc, serverID).Extract()
	if err != nil {
		return "", err
	}
	for _, addrs := range s.Addresses {
		for _, a := range addrs.([]interface{}) {
			m := a.(map[string]interface{})
			if ipstr, _ := m["addr"].(string); ipstr != "" {
				ip := net.ParseIP(ipstr)
				if ip != nil && ip.To4() != nil {
					return ip.String(), nil
				}
			}
		}
	}
	return "", errors.New("no IPv4 address found")
}

func pickBestIP(s servers.Server) string {
	// Prefer floating/public IPs if present in Addresses; otherwise first private IPv4.
	cand := []string{}
	for _, addrs := range s.Addresses {
		for _, a := range addrs.([]interface{}) {
			m := a.(map[string]interface{})
			if ipstr, _ := m["addr"].(string); ipstr != "" {
				cand = append(cand, ipstr)
			}
		}
	}
	// Sort to keep deterministic order, IPv4 before IPv6
	sort.SliceStable(cand, func(i, j int) bool {
		ipI := net.ParseIP(cand[i])
		ipJ := net.ParseIP(cand[j])
		if (ipI != nil && ipI.To4() != nil) && (ipJ != nil && ipJ.To4() == nil) {
			return true
		}
		return cand[i] < cand[j]
	})
	if len(cand) > 0 {
		return cand[0]
	}
	return ""
}

func (p *Provider) findServerByName(cc *gophercloud.ServiceClient, name string) (*servers.Server, error) {
	pager := servers.List(cc, servers.ListOpts{Name: name})
	var res *servers.Server
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		for _, s := range list {
			if s.Name == name {
				res = &s
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("server not found: %s", name)
	}
	return res, nil
}

func looksLikeUUID(s string) bool {
	// Very light check
	return len(s) >= 8 && strings.Count(s, "-") >= 2
}

func newestUbuntu2204ImageID(cc *gophercloud.ServiceClient) (string, error) {
	// Try to find an Ubuntu 22.04 image and pick the most recent by Created time
	var imgs []images.Image
	pager := images.ListDetail(cc, images.ListOpts{})
	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		list, err := images.ExtractImages(page)
		if err != nil {
			return false, err
		}
		for _, im := range list {
			name := strings.ToLower(im.Name)
			if strings.Contains(name, "ubuntu") && (strings.Contains(name, "22.04") || strings.Contains(name, "jammy")) {
				imgs = append(imgs, im)
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if len(imgs) == 0 {
		return "", errors.New("no Ubuntu 22.04 image found; set OPENSTACK_IMAGE explicitly")
	}
	// Sort by Created descending if available (may be empty on some clouds); fallback to name
	sort.Slice(imgs, func(i, j int) bool {
		ci := imgs[i].Created
		cj := imgs[j].Created
		if ci != "" && cj != "" {
			return ci > cj
		}
		return imgs[i].Name > imgs[j].Name
	})
	return imgs[0].ID, nil
}

// firstNonEmpty returns the first non-empty string in vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
