package recruitment

import (
	"log"
	"sync"

	"github.com/scitq/scitq/server/config"
)

type RegionalQuota struct {
	Region   string
	Provider string
	MaxCPU   int32
	MaxMemGB float32
}

type RegionalUsage struct {
	Region    string
	Provider  string
	UsedCPU   int32
	UsedMemGB float32
}

type QuotaManager struct {
	Quotas map[string]RegionalQuota // keyed by region/provider
	Usage  sync.Map                 // key = region/provider string, value = RegionalUsage
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
		log.Printf("[DEBUG] QuotaManager: cannot launch in %s/%s, usage CPU %d/%d, Mem %.1f/%.1f GB",
			region, provider,
			usage.UsedCPU, quota.MaxCPU,
			usage.UsedMemGB, quota.MaxMemGB,
		)
		return false
	}

	if quota.MaxMemGB > 0 && usage.UsedMemGB+float32(memGB) > quota.MaxMemGB {
		log.Printf("[DEBUG] QuotaManager: cannot launch in %s/%s, usage CPU %d/%d, Mem %.1f/%.1f GB",
			region, provider,
			usage.UsedCPU, quota.MaxCPU,
			usage.UsedMemGB, quota.MaxMemGB,
		)
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

	log.Printf("[DEBUG] QuotaManager: after launch in %s/%s, usage is now CPU %d/%d, Mem %.1f/%.1f GB",
		region, provider,
		usage.UsedCPU, qm.Quotas[key].MaxCPU,
		usage.UsedMemGB, qm.Quotas[key].MaxMemGB,
	)
	qm.Usage.Store(key, usage)
}

func (qm *QuotaManager) RegisterDelete(region, provider string, cpu int32, memGB float32) {
	key := quotaKey(region, provider)
	val, _ := qm.Usage.LoadOrStore(key, RegionalUsage{Region: region, Provider: provider})
	usage := val.(RegionalUsage)

	usage.UsedCPU -= cpu
	usage.UsedMemGB -= memGB
	if usage.UsedCPU < 0 {
		usage.UsedCPU = 0
	}
	if usage.UsedMemGB < 0 {
		usage.UsedMemGB = 0
	}

	qm.Usage.Store(key, usage)
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
			log.Printf("  region %s: CPU %d, Mem %.1f GB", region, quota.MaxCPU, quota.MaxMemGB)
			qm.Quotas[key] = RegionalQuota{
				Region:   region,
				Provider: name,
				MaxCPU:   quota.MaxCPU,
				MaxMemGB: quota.MaxMemGB,
			}
		}
	}
	return qm
}
