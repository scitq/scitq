package openstack

// OVH Public Cloud updater (flavors + prices) — Go port of your v1 Python logic
// -----------------------------------------------------------------------------
// This implementation:
//   • Lists flavors via OVH's own Cloud API (not generic OpenStack):
//       GET /cloud/project/{serviceName}/flavor
//   • Scrapes OVH Public Cloud pricing page to attach hourly price per flavor
//       https://www.ovhcloud.com/fr/public-cloud/prices/
//   • Parses disk, NVMe tag, and GPU info from the pricing tables similarly to the Python.
//   • Upserts flavors and (flavor,region) metrics via updater.GenericProvider.
//
// Minimum env/config expected (env names mirror your Python):
//   OVH_APPLICATIONKEY, OVH_APPLICATIONSECRET, OVH_CONSUMERKEY (mandatory)
//   OS_PROJECT_ID      — OVH "project/service name" (mandatory)
//   OVH_REGIONS        — optional (space-separated). If empty, we query regions from OVH API.
//   OVH_ENDPOINT       — optional; defaults to "ovh-eu"
//
// Wire this in updater dispatcher (server/updater/run/run.go):
//   case *config.OpenstackConfig: return openstack.Run(cfg, *c)
// (or a new dedicated OVH config if you prefer).

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	ovh "github.com/ovh/go-ovh/ovh"

	"github.com/scitq/scitq/server/config"
	"github.com/scitq/scitq/server/updater"
)

// ---------- Public entrypoint ----------
func Run(cfg config.Config, c config.OpenstackConfig) error {
	client, serviceName, regions, err := newOVHClientFromConfig(c)
	if err != nil {
		return err
	}

	gp, err := updater.NewGenericProvider(cfg, c.GetName())
	if err != nil {
		return err
	}
	defer gp.Close()

	// 1) Flavors
	log.Printf("Fetching OVH flavors for project %q in regions: %v", serviceName, regions)
	flavors, metrics, err := fetchOVHFlavors(client, gp.ProviderID, serviceName, regions)
	if err != nil {
		return err
	}

	// Build index of flavors by name for price/GPU enrichment (mutations to these pointers will be persisted)
	log.Printf("Fetched %d OVH flavors; enriching with prices before persisting", len(flavors))
	idxFlavors := make(map[string]*updater.Flavor, len(flavors))
	for _, f := range flavors {
		idxFlavors[f.Name] = f
	}

	// 2) Prices + disk/GPU/NVMe tags from public page (mutates flavors in idxFlavors)
	log.Printf("Fetching OVH pricing page to enrich %d (flavor,region) metrics", len(metrics))
	if err := enrichMetricsWithOVHPrices(metrics, regions, idxFlavors); err != nil {
		return err
	}

	// Summarize GPU/NVMe flags before persisting flavors
	gpuCount := 0
	nvmeCount := 0
	for _, f := range flavors {
		if f.HasGPU {
			gpuCount++
		}
		if f.HasQuickDisk {
			nvmeCount++
		}
	}
	log.Printf("DEBUG flavor summary before persist: total=%d gpu=%d nvme=%d", len(flavors), gpuCount, nvmeCount)

	// Now persist flavors with updated GPU/NVMe flags
	if err := gp.UpdateFlavors(flavors); err != nil {
		return err
	}

	// Diagnostics: summarize metrics and detect in-batch dups before filtering
	log.Printf("DEBUG metrics collected: total=%d", len(metrics))
	dupCount := 0
	seenAll := make(map[string]int, len(metrics))
	for _, v := range metrics {
		k := fmt.Sprintf("%d|%s|%s", v.ProviderID, v.FlavorName, v.RegionName)
		seenAll[k]++
	}
	for k, c := range seenAll {
		if c > 1 {
			dupCount += c - 1
			log.Printf("DEBUG in-batch duplicate x%d for %s", c, k)
		}
	}
	if dupCount == 0 {
		log.Printf("DEBUG no in-batch duplicates detected before filtering")
	}

	// 3) Persist metrics (only those with cost > 0 like the Python)
	var filtered []*updater.FlavorMetrics
	seen := make(map[string]struct{}, len(metrics))
	for _, v := range metrics {
		log.Printf("TEMP Metric: flavor=%s region=%s cost=%.4f", v.FlavorName, v.RegionName, v.Cost)
		if v.Cost > 0 {
			k := fmt.Sprintf("%d|%s|%s", v.ProviderID, v.FlavorName, v.RegionName)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			filtered = append(filtered, v)
		}
	}
	log.Printf("DEBUG filtered metrics to update: n=%d", len(filtered))
	countPreview := 0
	for _, v := range filtered {
		if countPreview < 50 { // cap preview to avoid log noise
			log.Printf("DEBUG will upsert: %d|%s|%s cost=%.4f", v.ProviderID, v.FlavorName, v.RegionName, v.Cost)
			countPreview++
		}
	}
	return gp.UpdateFlavorMetrics(filtered)
}

// ---------- OVH API ----------

type ovhFlavor struct {
	Name              string  `json:"name"`
	Region            string  `json:"region"`
	VCPUs             int     `json:"vcpus"`
	RAMMB             float64 `json:"ram"`
	DiskGB            float64 `json:"disk"`
	InboundBandwidthK float64 `json:"inboundBandwidth"`
	Available         bool    `json:"available"`
	OsType            string  `json:"osType"`
}

// newOVHClientFromConfig builds an OVH API client using YAML config values.
// Generic OpenStack fields remain in OpenstackConfig; OVH commercial API creds live under c.Custom:
//
//	custom:
//	  ovh_endpoint: "ovh-eu"
//	  ovh_application_key: "<appKey>"
//	  ovh_application_secret: "<appSecret>"
//	  ovh_consumer_key: "<consumerKey>"
//	  ovh_project_id: "<serviceName UUID>"
func newOVHClientFromConfig(c config.OpenstackConfig) (*ovh.Client, string, []string, error) {
	// Extract OVH-specific values from Custom
	getStr := func(key string) string {
		if c.Custom == nil {
			return ""
		}
		if v, ok := c.Custom[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	getSlice := func(key string) []string {
		if c.Custom == nil {
			return nil
		}
		if v, ok := c.Custom[key]; ok {
			switch vv := v.(type) {
			case []string:
				return vv
			case string:
				f := strings.Fields(vv)
				if len(f) > 0 {
					return f
				}
			case []interface{}:
				out := make([]string, 0, len(vv))
				for _, x := range vv {
					if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
						out = append(out, strings.TrimSpace(s))
					}
				}
				if len(out) > 0 {
					return out
				}
			}
		}
		return nil
	}
	app := getStr("ovh_application_key")
	sec := getStr("ovh_application_secret")
	ck := getStr("ovh_consumer_key")
	if app == "" || sec == "" || ck == "" {
		return nil, "", nil, errors.New("missing OVH credentials in config.openstack.custom: ovh_application_key / ovh_application_secret / ovh_consumer_key")
	}

	// Project/Service name (aka OS_PROJECT_ID in v1); prefer ProjectID, else TenantName
	service := c.ProjectID
	if service == "" {
		service = c.TenantName
	}
	if service == "" {
		// As a last resort, attempt discovery: if the account has exactly one project, use it.
		log.Printf("⚠️ OVH project ID not set in config.openstack.project_id or tenant_name; attempting discovery")
		client, _ := ovh.NewClient(getStr("ovh_endpoint"), app, sec, ck)
		var projects []string
		if err := client.Get("/cloud/project", &projects); err == nil && len(projects) == 1 {
			service = projects[0]
		}
	}
	if service == "" {
		return nil, "", nil, errors.New("missing OVH project identifier: set custom.ovh_project_id or project_id or tenant_name (or ensure a single project on the OVH account)")
	}

	endpoint := getStr("ovh_endpoint")
	if endpoint == "" {
		endpoint = "ovh-eu"
	}

	client, err := ovh.NewClient(endpoint, app, sec, ck)
	if err != nil {
		return nil, "", nil, fmt.Errorf("ovh client: %w", err)
	}

	// Regions: STRICTLY honor YAML if provided; else try custom.ovh_regions; else discover via OVH API
	regions := c.GetRegions()
	log.Printf("DEBUG OVH regions from config: %v", regions)
	if len(regions) == 0 {
		return nil, "", nil, fmt.Errorf("no OVH regions configured: set providers.openstack.<provider>.regions or quotas within %v", getSlice("ovh_regions"))
	}
	return client, service, regions, nil
}

// fetchOVHFlavors gets flavors and builds Flavor + FlavorMetrics slices (empty cost initially)
func fetchOVHFlavors(client *ovh.Client, providerId uint32, service string, regions []string) ([]*updater.Flavor, []*updater.FlavorMetrics, error) {
	var raw []ovhFlavor
	if err := client.Get(fmt.Sprintf("/cloud/project/%s/flavor", service), &raw); err != nil {
		return nil, nil, fmt.Errorf("list flavors: %w", err)
	}

	flavors := map[string]*updater.Flavor{}
	var metrics []*updater.FlavorMetrics
	// local index for deduplication by flavor|region
	metricIndex := map[string]struct{}{}

	for _, f := range raw {
		if !f.Available || strings.EqualFold(f.OsType, "windows") || strings.Contains(f.Name, "flex") || !stringSliceContains(regions, f.Region) {
			continue
		}
		// OVH bug quirk from Python: t1-90 sometimes reports 16 vcpus; fix to 18
		vcpus := f.VCPUs
		if f.Name == "t1-90" && vcpus == 16 {
			log.Printf("⚠️ OVH t1-90 flavor reports 16 vcpus; correcting to 18")
			vcpus = 18
		}

		if _, ok := flavors[f.Name]; !ok {
			flavors[f.Name] = &updater.Flavor{
				ProviderID:   int(providerId),
				Name:         f.Name,
				CPU:          vcpus,
				Mem:          f.RAMMB,
				Disk:         f.DiskGB,
				Bandwidth:    int(f.InboundBandwidthK / 1000),
				HasGPU:       false,
				HasQuickDisk: false,
			}
		}
		key := keyFR(f.Name, f.Region)
		if _, ok := metricIndex[key]; !ok {
			metrics = append(metrics, &updater.FlavorMetrics{
				FlavorName: f.Name,
				RegionName: f.Region,
				ProviderID: int(providerId),
				Cost:       0,
				Eviction:   0,
			})
			metricIndex[key] = struct{}{}
		}
	}

	// deterministic order for UpsertFlavors
	names := make([]string, 0, len(flavors))
	for n := range flavors {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*updater.Flavor, 0, len(names))
	for _, n := range names {
		out = append(out, flavors[n])
	}
	return out, metrics, nil
}

// ---------- Pricing page scraping (French locale tables) ----------

var (
	rePriceEURhr = regexp.MustCompile(`([0-9]+(?:,[0-9]+)?).*€.*HT/heure`)
	reDisk       = regexp.MustCompile(`(?i)((?P<n>[0-9]+)\s*x\s*)?(?P<value>[0-9]+(?:\.[0-9]+)?)\s*(?P<unit>T|G|M)o`)
	reGPUmem     = regexp.MustCompile(`(?i)((?P<n>[0-9]+)\s*)?.+\s(?P<value>[0-9]+)\s*Go`)
	gpuNIdx      int
	gpuVIdx      int
)

func init() {
	gpuNIdx = reGPUmem.SubexpIndex("n")
	gpuVIdx = reGPUmem.SubexpIndex("value")
}

func enrichMetricsWithOVHPrices(metrics []*updater.FlavorMetrics, regions []string, flavorsByName map[string]*updater.Flavor) error {
	// Build index flavor|region -> *FlavorMetrics
	idxMetrics := make(map[string]*updater.FlavorMetrics, len(metrics))
	for _, fm := range metrics {
		idxMetrics[keyFR(fm.FlavorName, fm.RegionName)] = fm
	}
	// Also index metrics by flavor name → all its region entries
	idxByFlavor := make(map[string][]*updater.FlavorMetrics)
	for _, fm := range metrics {
		idxByFlavor[fm.FlavorName] = append(idxByFlavor[fm.FlavorName], fm)
		// NBSP-tolerant key (in case HTML uses \u00A0); helps when matching from table names
		k := strings.ReplaceAll(fm.FlavorName, "\u00a0", " ")
		if k != fm.FlavorName {
			idxByFlavor[k] = append(idxByFlavor[k], fm)
		}
	}

	url := "https://www.ovhcloud.com/fr/public-cloud/prices/"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "scitq-updater/1.0")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET prices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("prices HTTP %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return fmt.Errorf("parse html: %w", err)
	}

	// We look for tables that contain headers including Nom / Prix / Stockage / GPU
	doc.Find("table").Each(func(_ int, table *goquery.Selection) {
		headers := extractHeaders(table)
		if !hasAny(headers, []string{"Nom"}) || !hasAny(headers, []string{"Prix"}) || !hasAny(headers, []string{"Stockage"}) {
			return // not a matching table
		}
		log.Printf("TEMP Found pricing table with headers: %v", headers)

		// Build column index map
		idx := map[string]int{}
		for i, h := range headers {
			idx[strings.TrimSpace(h)] = i
		}
		// GPU column presence
		if gi, ok := idx["GPU"]; ok {
			log.Printf("DEBUG table has GPU column at index %d", gi)
		} else {
			log.Printf("DEBUG table has NO GPU column")
		}

		// DEBUG: dump headers and try approximate matches for key columns
		log.Printf("DEBUG headers count=%d", len(headers))
		for i, h := range headers {
			log.Printf("DEBUG header[%d]=%q", i, h)
		}
		if _, ok := idx["Prix"]; !ok {
			for i, h := range headers {
				if strings.Contains(strings.ToLower(h), "prix") {
					log.Printf("DEBUG approximate match for 'Prix' -> header[%d]=%q", i, h)
				}
			}
		}
		if _, ok := idx["Stockage"]; !ok {
			for i, h := range headers {
				if strings.Contains(strings.ToLower(h), "stockage") {
					log.Printf("DEBUG approximate match for 'Stockage' -> header[%d]=%q", i, h)
				}
			}
		}

		rowsLogged := 0

		table.Find("tbody tr").Each(func(_ int, tr *goquery.Selection) {
			cells := tr.Find("td")
			// Build a per-row map from header label to cell text, tolerating fewer cells than headers (due to hidden/optional cols)
			rowMap := make(map[string]string, cells.Length())
			limit := len(headers)
			if cells.Length() < limit {
				limit = cells.Length()
			}
			for i := 0; i < limit; i++ {
				key := strings.TrimSpace(headers[i])
				val := strings.TrimSpace(cells.Eq(i).Text())
				rowMap[key] = val
			}

			if rowsLogged < 5 {
				log.Printf("DEBUG row zipped: headers=%d cells=%d limit=%d", len(headers), cells.Length(), limit)
				for i := 0; i < limit; i++ {
					txt := rowMap[headers[i]]
					if len(txt) > 120 {
						txt = txt[:120] + "…"
					}
					log.Printf("  zip[%d] %q => %q", i, headers[i], txt)
				}
				rowsLogged++
			}

			textAt := func(col string) string {
				if v, ok := rowMap[col]; ok && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
				// fuzzy: look for a header that contains the requested label (case-insensitive)
				want := strings.ToLower(col)
				for k, v := range rowMap {
					if strings.Contains(strings.ToLower(k), want) && strings.TrimSpace(v) != "" {
						if rowsLogged < 10 {
							log.Printf("DEBUG fuzzy textAt(%s) via header %q -> %q", col, k, func() string {
								if len(v) > 120 {
									return strings.TrimSpace(v)[:120] + "…"
								}
								return strings.TrimSpace(v)
							}())
						}
						return strings.TrimSpace(v)
					}
				}
				if rowsLogged < 10 {
					log.Printf("DEBUG textAt(%s) not found in rowMap keys=%v", col, keysOf(rowMap))
				}
				return ""
			}

			name := textAt("Nom")
			if name == "" {
				return
			}
			// Use a tolerant key that replaces NBSP with regular spaces; keeps ASCII intact
			nameKey := strings.ReplaceAll(name, "\u00a0", " ")

			// Probe GPU cell even if we might skip later; helps diagnose empty cells
			gpuProbe := strings.TrimSpace(strings.ReplaceAll(textAt("GPU"), "\u00a0", " "))
			if gpuProbe == "" {
				log.Printf("DEBUG GPU empty for flavor %s in this row (headers=%v)", nameKey, headers)
			} else {
				log.Printf("DEBUG GPU probe for flavor %s: %q", nameKey, gpuProbe)
			}

			// Price
			if priceStr := textAt("Prix"); priceStr != "" {
				log.Printf("TEMP Found price string for flavor %s: %q", nameKey, priceStr)
				// Normalize non-breaking spaces which \s does not match in Go regex
				priceNorm := strings.ReplaceAll(priceStr, "\u00a0", " ")
				if m := rePriceEURhr.FindStringSubmatch(priceNorm); m != nil {
					p, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
					// Set price for all existing (flavor,region) entries for this flavor
					if list, ok := idxByFlavor[nameKey]; ok {
						for _, fm := range list {
							fm.Cost = p
						}
						log.Printf("DEBUG set price %.4f for %d regions of flavor %s", p, len(list), nameKey)
					} else {
						// Fallback: try raw name if NBSP-normalization changed it
						if list, ok := idxByFlavor[strings.TrimSpace(name)]; ok {
							for _, fm := range list {
								fm.Cost = p
							}
							log.Printf("DEBUG set price %.4f for %d regions of flavor %s (fallback raw)", p, len(list), name)
						} else {
							log.Printf("DEBUG no FlavorMetrics entries found for flavor %q to set price", nameKey)
						}
					}
				} else {
					log.Printf("DEBUG price regex did not match after NBSP normalization: %q", priceNorm)
				}
			}

			// Disk + NVMe tag
			if stor := textAt("Stockage"); stor != "" {
				log.Printf("TEMP Found storage string for flavor %s: %q", nameKey, stor)
				storNorm := strings.ReplaceAll(stor, "\u00a0", " ")
				// Disk numbers like "2 x 200 Go" or "1 To"
				matches := reDisk.FindAllStringSubmatch(storNorm, -1)
				if len(matches) == 0 {
					log.Printf("DEBUG storage regex found no tokens after NBSP normalization: %q", storNorm)
				}
				for _, m := range matches {
					n := 1
					if m[2] != "" { // group n
						if v, err := strconv.Atoi(strings.TrimSpace(m[2])); err == nil {
							n = v
						}
					}
					val, _ := strconv.ParseFloat(strings.TrimSpace(m[3]), 64)
					unit := strings.ToUpper(strings.TrimSpace(m[4])) // G or T or M
					switch unit {
					case "M":
						val = val / 1024.0
					case "G":
						// val = val
					case "T":
						val = val * 1024.0
					}
					diskGB := int(float64(n) * val)
					// We cannot access the Flavor by name alone here; leave disk enrichment to higher layer if needed
					_ = diskGB // kept for parity; actual flavor disk is already populated from OVH API
				}
				if strings.Contains(strings.ToUpper(stor), "NVME") {
					if fl, ok := flavorsByName[nameKey]; ok {
						fl.HasQuickDisk = true
					} else if fl, ok := flavorsByName[strings.TrimSpace(name)]; ok { // fallback raw
						fl.HasQuickDisk = true
					} else {
						log.Printf("DEBUG NVMe: flavor lookup failed for %q (%q)", nameKey, name)
					}
				}
			}

			// GPU column (optional)
			if _, hasGPUCol := idx["GPU"]; hasGPUCol {
				log.Printf("DEBUG GPU column detected; attempting extraction for flavor %s", nameKey)
				gpu := textAt("GPU")
				if gpu != "" {
					log.Printf("DEBUG GPU string for flavor %s: %q", nameKey, gpu)
					if fl, ok := flavorsByName[nameKey]; ok {
						fl.HasGPU = true
						fl.GPU = gpu // GPU is a string field in Flavor
						gpuNorm := strings.ReplaceAll(gpu, "\u00a0", " ")
						total := 0
						for _, m := range reGPUmem.FindAllStringSubmatch(gpuNorm, -1) {
							n := 1
							if gpuNIdx > 0 && gpuNIdx < len(m) && strings.TrimSpace(m[gpuNIdx]) != "" {
								if v, err := strconv.Atoi(strings.TrimSpace(m[gpuNIdx])); err == nil {
									n = v
								}
							}
							if gpuVIdx > 0 && gpuVIdx < len(m) {
								if val, err := strconv.Atoi(strings.TrimSpace(m[gpuVIdx])); err == nil {
									total += n * val
								}
							}
						}
						if total > 0 {
							fl.GPUMem = total
							log.Printf("DEBUG parsed GPUMem=%d for flavor %s (raw=%q)", total, nameKey, gpu)
						}
					} else if fl, ok := flavorsByName[strings.TrimSpace(name)]; ok {
						// fallback
						fl.HasGPU = true
						fl.GPU = gpu
					} else {
						log.Printf("DEBUG GPU: flavor lookup failed for %q (%q)", nameKey, name)
					}
				} else {
					log.Printf("DEBUG GPU cell is empty for flavor %s despite GPU column", nameKey)
				}
			}
		})
	})

	return nil
}

func extractHeaders(table *goquery.Selection) []string {
	headers := []string{}
	table.Find("thead th").Each(func(_ int, th *goquery.Selection) {
		headers = append(headers, strings.TrimSpace(th.Text()))
	})
	return headers
}

func hasAny(list []string, targets []string) bool {
	for _, t := range targets {
		for _, s := range list {
			if strings.EqualFold(strings.TrimSpace(s), t) {
				return true
			}
		}
	}
	return false
}

// ---------- small helpers ----------

func keyFR(flavor, region string) string { return flavor + "|" + region }

// ---------- optional: debug pretty-print ----------
func pp(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// stringSliceContains returns true if s is present in slice
func stringSliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
