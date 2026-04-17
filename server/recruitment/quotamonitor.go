package recruitment

import (
	"log"
	"sync"

	"github.com/scitq/scitq/server/config"
)

type RegionalQuota struct {
	Region       string
	Provider     string
	MaxCPU       int32
	MaxMemGB     float32
	MaxInstances int32 // 0 = unlimited (learned from failures)
}

type RegionalUsage struct {
	Region    string
	Provider  string
	UsedCPU   int32
	UsedMemGB float32
	Instances int32
}

type QuotaManager struct {
	Quotas     map[string]RegionalQuota // keyed by region/provider
	Usage      sync.Map                 // key = region/provider string, value = RegionalUsage
	BadFlavors sync.Map                 // key = region/provider/flavorName, value = bool (blacklisted)
}

func quotaKey(region, provider string) string {
	return provider + "/" + region
}

func (qm *QuotaManager) CanLaunch(region, provider string, cpu int32, memGB float64) bool {
	key := quotaKey(region, provider)
	quota, ok := qm.Quotas[key]
	if !ok {
		return false // unknown quota, deny by default
	}

	val, _ := qm.Usage.LoadOrStore(key, RegionalUsage{Region: region, Provider: provider})
	usage := val.(RegionalUsage)

	if usage.UsedCPU+cpu > quota.MaxCPU {
		log.Printf("[DEBUG] QuotaManager: cannot launch in %s/%s, CPU %d/%d",
			region, provider, usage.UsedCPU, quota.MaxCPU)
		return false
	}

	if quota.MaxMemGB > 0 && usage.UsedMemGB+float32(memGB) > quota.MaxMemGB {
		log.Printf("[DEBUG] QuotaManager: cannot launch in %s/%s, Mem %.1f/%.1f GB",
			region, provider, usage.UsedMemGB, quota.MaxMemGB)
		return false
	}

	if quota.MaxInstances > 0 && usage.Instances >= quota.MaxInstances {
		log.Printf("[DEBUG] QuotaManager: cannot launch in %s/%s, instances %d/%d",
			region, provider, usage.Instances, quota.MaxInstances)
		return false
	}

	return true
}

func (qm *QuotaManager) RegisterLaunch(region, provider string, cpu int32, memGB float32) {
	key := quotaKey(region, provider)
	val, _ := qm.Usage.LoadOrStore(key, RegionalUsage{Region: region, Provider: provider})
	usage := val.(RegionalUsage)

	usage.UsedCPU += cpu
	usage.UsedMemGB += memGB
	usage.Instances++

	log.Printf("[DEBUG] QuotaManager: after launch in %s/%s, usage is now CPU %d/%d, Mem %.1f/%.1f GB, Instances %d",
		region, provider,
		usage.UsedCPU, qm.Quotas[key].MaxCPU,
		usage.UsedMemGB, qm.Quotas[key].MaxMemGB,
		usage.Instances,
	)
	qm.Usage.Store(key, usage)
}

func (qm *QuotaManager) RegisterDelete(region, provider string, cpu int32, memGB float32) {
	key := quotaKey(region, provider)
	val, _ := qm.Usage.LoadOrStore(key, RegionalUsage{Region: region, Provider: provider})
	usage := val.(RegionalUsage)

	usage.UsedCPU -= cpu
	usage.UsedMemGB -= memGB
	usage.Instances--
	if usage.UsedCPU < 0 {
		usage.UsedCPU = 0
	}
	if usage.UsedMemGB < 0 {
		usage.UsedMemGB = 0
	}
	if usage.Instances < 0 {
		usage.Instances = 0
	}

	qm.Usage.Store(key, usage)
}

// RegisterInstanceLimit is called when a deployment fails with an instance-count
// error (e.g. PublicIPCountLimitReached). It learns the limit from the current
// usage so future deploys are blocked before hitting the Azure API.
func (qm *QuotaManager) RegisterInstanceLimit(region, provider string) {
	key := quotaKey(region, provider)
	val, ok := qm.Usage.Load(key)
	if !ok {
		return
	}
	usage := val.(RegionalUsage)

	quota := qm.Quotas[key]
	quota.MaxInstances = usage.Instances
	qm.Quotas[key] = quota
	log.Printf("⚠️ QuotaManager: learned instance limit for %s/%s: %d (from deployment failure)", region, provider, usage.Instances)
}

// BlacklistFlavor marks a flavor as permanently incompatible with the given
// provider/region (e.g. Azure BadRequest on VM size). The recruiter will skip
// this flavor when selecting candidates for deployment.
func (qm *QuotaManager) BlacklistFlavor(region, provider, flavorName string) {
	if flavorName == "" {
		return
	}
	k := provider + "/" + region + "/" + flavorName
	qm.BadFlavors.Store(k, true)
	log.Printf("⚠️ QuotaManager: blacklisted flavor %s in %s/%s (permanent provider error)", flavorName, provider, region)
}

// IsFlavorBlacklisted returns true if the flavor was previously blacklisted
// for this provider/region via BlacklistFlavor.
func (qm *QuotaManager) IsFlavorBlacklisted(region, provider, flavorName string) bool {
	if flavorName == "" {
		return false
	}
	k := provider + "/" + region + "/" + flavorName
	_, ok := qm.BadFlavors.Load(k)
	return ok
}

// NewQuotaManager builds a QuotaManager from the loaded configuration.
func NewQuotaManager(cfg *config.Config) *QuotaManager {
	qm := &QuotaManager{
		Quotas: make(map[string]RegionalQuota),
	}
	for _, p := range cfg.GetProviders() {
		name := p.GetName()
		log.Printf("QuotaManager: loading quotas for provider %s", name)
		for region, quota := range p.GetQuotas() {
			key := quotaKey(region, name)
			log.Printf("  region %s: CPU %d, Mem %.1f GB, Instances %d", region, quota.MaxCPU, quota.MaxMemGB, quota.MaxInstances)
			qm.Quotas[key] = RegionalQuota{
				Region:       region,
				Provider:     name,
				MaxCPU:       quota.MaxCPU,
				MaxMemGB:     quota.MaxMemGB,
				MaxInstances: quota.MaxInstances,
			}
		}
	}
	return qm
}
