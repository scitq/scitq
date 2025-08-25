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
//   case *config.OpenstackConfig: return openstack.RunOVH(cfg, *c)
// (or a new dedicated OVH config if you prefer).

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
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

// RunOVH mirrors your Python run(): get_flavors() then get_metrics()
func RunOVH(cfg config.Config, _ config.OpenstackConfig) error {
	client, serviceName, regions, err := newOVHClientFromEnv()
	if err != nil {
		return err
	}

	gp, err := updater.NewGenericProvider(cfg, "ovh")
	if err != nil {
		return err
	}
	defer gp.Close()

	// 1) Flavors
	flavors, metrics, err := fetchOVHFlavors(client, serviceName, regions)
	if err != nil {
		return err
	}
	if err := gp.UpdateFlavors(flavors); err != nil {
		return err
	}

	// Build index of flavors by name for price/GPU enrichment
	idxFlavors := make(map[string]*updater.Flavor, len(flavors))
	for _, f := range flavors {
		idxFlavors[f.Name] = f
	}

	// 2) Prices + disk/GPU tags from public page
	if err := enrichMetricsWithOVHPrices(metrics, regions, idxFlavors); err != nil {
		return err
	}

	// 3) Persist metrics (only those with cost > 0 like the Python)
	var filtered []*updater.FlavorMetrics
	for _, v := range metrics {
		if v.Cost > 0 {
			filtered = append(filtered, v)
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

type ovhRegion struct {
	Name string `json:"name"`
}

func newOVHClientFromEnv() (*ovh.Client, string, []string, error) {
	app := os.Getenv("OVH_APPLICATIONKEY")
	sec := os.Getenv("OVH_APPLICATIONSECRET")
	ck := os.Getenv("OVH_CONSUMERKEY")
	if app == "" || sec == "" || ck == "" {
		return nil, "", nil, errors.New("OVH_APPLICATIONKEY/OVH_APPLICATIONSECRET/OVH_CONSUMERKEY must be set")
	}
	service := os.Getenv("OS_PROJECT_ID")
	if service == "" {
		return nil, "", nil, errors.New("OS_PROJECT_ID must be set to your OVH project id (service name)")
	}
	endpoint := os.Getenv("OVH_ENDPOINT")
	if endpoint == "" {
		endpoint = "ovh-eu"
	}

	client, err := ovh.NewClient(endpoint, app, sec, ck)
	if err != nil {
		return nil, "", nil, fmt.Errorf("ovh client: %w", err)
	}

	regions := strings.Fields(os.Getenv("OVH_REGIONS"))
	if len(regions) == 0 {
		var rlist []ovhRegion
		if err := client.Get(fmt.Sprintf("/cloud/project/%s/region", service), &rlist); err != nil {
			return nil, "", nil, fmt.Errorf("list regions: %w", err)
		}
		for _, r := range rlist {
			regions = append(regions, r.Name)
		}
		if len(regions) == 0 {
			return nil, "", nil, errors.New("no regions discovered; set OVH_REGIONS")
		}
	}
	return client, service, regions, nil
}

// fetchOVHFlavors gets flavors and builds Flavor + FlavorMetrics slices (empty cost initially)
func fetchOVHFlavors(client *ovh.Client, service string, regions []string) ([]*updater.Flavor, []*updater.FlavorMetrics, error) {
	var raw []ovhFlavor
	if err := client.Get(fmt.Sprintf("/cloud/project/%s/flavor", service), &raw); err != nil {
		return nil, nil, fmt.Errorf("list flavors: %w", err)
	}

	flavors := map[string]*updater.Flavor{}
	var metrics []*updater.FlavorMetrics
	// local index for deduplication by flavor|region
	metricIndex := map[string]struct{}{}

	for _, f := range raw {
		if !f.Available || strings.EqualFold(f.OsType, "windows") || strings.Contains(f.Name, "flex") {
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
				ProviderID: 0, // set by DB layer
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
	rePriceEURhr = regexp.MustCompile(`([0-9]+(?:,[0-9]+)?)\s*€\s*HT/heure`)
	reDisk       = regexp.MustCompile(`(?i)((?P<n>[0-9]+)\s*x\s*)?(?P<value>[0-9]+(?:\.[0-9]+)?)\s*(?P<unit>T|G)o`)
	reGPUmem     = regexp.MustCompile(`(?i)((?P<n>[0-9]+)\s*)?.+\s(?P<value>[0-9]+)\s*Go`)
)

func enrichMetricsWithOVHPrices(metrics []*updater.FlavorMetrics, regions []string, flavorsByName map[string]*updater.Flavor) error {
	// Build index flavor|region -> *FlavorMetrics
	idxMetrics := make(map[string]*updater.FlavorMetrics, len(metrics))
	for _, fm := range metrics {
		idxMetrics[keyFR(fm.FlavorName, fm.RegionName)] = fm
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

		// Build column index map
		idx := map[string]int{}
		for i, h := range headers {
			idx[strings.TrimSpace(h)] = i
		}

		table.Find("tbody tr").Each(func(_ int, tr *goquery.Selection) {
			cells := tr.Find("td")
			if cells.Length() < len(headers) {
				return
			}

			textAt := func(col string) string {
				if i, ok := idx[col]; ok {
					return strings.TrimSpace(cells.Eq(i).Text())
				}
				return ""
			}

			name := textAt("Nom")
			if name == "" {
				return
			}

			// Price
			if priceStr := textAt("Prix"); priceStr != "" {
				if m := rePriceEURhr.FindStringSubmatch(priceStr); m != nil {
					p, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
					// set price for all regions we track for that flavor
					for _, r := range regions {
						key := keyFR(name, r)
						if fm, ok := idxMetrics[key]; ok {
							fm.Cost = p
						}
					}
				}
			}

			// Disk + NVMe tag
			if stor := textAt("Stockage"); stor != "" {
				// Disk numbers like "2 x 200 Go" or "1 To"
				for _, m := range reDisk.FindAllStringSubmatch(stor, -1) {
					n := 1
					if m[2] != "" { // group n
						if v, err := strconv.Atoi(strings.TrimSpace(m[2])); err == nil {
							n = v
						}
					}
					val, _ := strconv.ParseFloat(strings.TrimSpace(m[3]), 64)
					unit := strings.ToUpper(strings.TrimSpace(m[4])) // G or T
					mult := 1.0
					if unit == "T" {
						mult = 1024.0
					}
					diskGB := int(float64(n) * val * mult)
					// We cannot access the Flavor by name alone here; leave disk enrichment to higher layer if needed
					_ = diskGB // kept for parity; actual flavor disk is already populated from OVH API
				}
				if strings.Contains(strings.ToUpper(stor), "NVME") {
					if fl, ok := flavorsByName[name]; ok {
						fl.HasQuickDisk = true
					}
				}
			}

			// GPU column (optional)
			if gi := idx["GPU"]; gi > 0 {
				if gpu := textAt("GPU"); gpu != "" {
					if fl, ok := flavorsByName[name]; ok {
						fl.HasGPU = true
						fl.GPU = gpu // GPU is a string field in Flavor
						// Parse and accumulate GPU memory (in Go) from patterns like "2 x 24 Go", "48 Go"
						total := 0
						for _, m := range reGPUmem.FindAllStringSubmatch(gpu, -1) {
							n := 1
							if strings.TrimSpace(m[1]) != "" { // group n
								if v, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil {
									n = v
								}
							}
							if val, err := strconv.Atoi(strings.TrimSpace(m[2])); err == nil {
								total += n * val
							}
						}
						if total > 0 {
							fl.GPUMem = total
						}
					}
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
