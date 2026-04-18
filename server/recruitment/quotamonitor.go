package recruitment

import (
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/scitq/scitq/server/config"
)

// InstanceLimitCooldown is the minimum time scitq waits after a provider
// reports an instance/IP quota error before trying to deploy in the same
// region again. It exists because cloud providers (notably Azure) take
// minutes to reclaim a public IP after a failed create or a worker delete,
// and retrying too soon just produces another orphan.
//
// Observed on Azure: even 6 minutes after `pipPoller.PollUntilDone` returns
// success for the orphan IP, a retry can still hit PublicIPCountLimitReached —
// the region-level quota counter has its own propagation delay on top of the
// IP-delete API. 10 minutes is conservative; override via InstanceLimitCooldownOverride
// if a real workload needs to retry faster.
var InstanceLimitCooldown = 10 * time.Minute

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
	Quotas      map[string]RegionalQuota // keyed by region/provider
	Usage       sync.Map                 // key = region/provider string, value = RegionalUsage
	BadFlavors  sync.Map                 // key = region/provider/flavorName, value = bool (blacklisted)
	CooldownEnd sync.Map                 // key = region/provider, value = time.Time (no launches until then)
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

	if v, ok := qm.CooldownEnd.Load(key); ok {
		if until := v.(time.Time); time.Now().Before(until) {
			log.Printf("[DEBUG] QuotaManager: %s/%s under cooldown for %s (provider quota was hit recently)",
				region, provider, time.Until(until).Truncate(time.Second))
			return false
		}
		qm.CooldownEnd.Delete(key)
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
// error (e.g. PublicIPCountLimitReached). It learns the limit from the actual
// DB-observed worker count so future deploys are blocked before hitting the
// provider API. Using the DB count rather than the in-memory Usage avoids
// a post-restart footgun where Usage starts at 0 and an early failure teaches
// an absurdly low cap while many workers from a previous server lifetime are
// still running.
func (qm *QuotaManager) RegisterInstanceLimit(db *sql.DB, region, provider string) {
	key := quotaKey(region, provider)

	var dbCount int32
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM worker w
		JOIN region   rg ON rg.region_id = w.region_id
		JOIN provider p  ON p.provider_id = rg.provider_id
		WHERE w.deleted_at IS NULL
		  AND rg.region_name = $1
		  AND (p.provider_name || '.' || p.config_name) = $2`,
		region, provider).Scan(&dbCount); err != nil {
		log.Printf("⚠️ QuotaManager.RegisterInstanceLimit: DB count failed for %s/%s: %v (falling back to in-memory usage)", region, provider, err)
		if val, ok := qm.Usage.Load(key); ok {
			dbCount = val.(RegionalUsage).Instances
		}
	}
	if dbCount <= 0 {
		// Nothing to learn; don't accidentally pin MaxInstances to 0 (which is "unlimited" in the semantics).
		log.Printf("⚠️ QuotaManager.RegisterInstanceLimit: DB count is 0 for %s/%s — not pinning a limit", region, provider)
		return
	}

	quota := qm.Quotas[key]
	quota.MaxInstances = dbCount
	qm.Quotas[key] = quota
	qm.CooldownEnd.Store(key, time.Now().Add(InstanceLimitCooldown))
	log.Printf("⚠️ QuotaManager: learned instance limit for %s/%s: %d (from DB worker count); cooling down for %s to let provider reclaim resources",
		region, provider, dbCount, InstanceLimitCooldown)
}

// ReconcileFromDB rebuilds the in-memory Usage map from the current worker table
// so that a fresh server process sees the true number of live instances, CPU and
// memory already consumed. Call this once after NewQuotaManager, before the
// recruiter loop can trigger any CanLaunch checks.
func (qm *QuotaManager) ReconcileFromDB(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT rg.region_name,
		       p.provider_name || '.' || p.config_name      AS provider_key,
		       COUNT(*)::int                                  AS instances,
		       COALESCE(SUM(f.cpu), 0)::int                   AS cpu,
		       COALESCE(SUM(f.memory), 0)::float              AS mem_gb
		FROM worker w
		JOIN flavor   f  ON f.flavor_id  = w.flavor_id
		JOIN region   rg ON rg.region_id = w.region_id
		JOIN provider p  ON p.provider_id = rg.provider_id
		WHERE w.deleted_at IS NULL
		GROUP BY rg.region_name, p.provider_name, p.config_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var region, provider string
		var instances, cpu int32
		var memGB float64
		if err := rows.Scan(&region, &provider, &instances, &cpu, &memGB); err != nil {
			return err
		}
		key := quotaKey(region, provider)
		qm.Usage.Store(key, RegionalUsage{
			Region:    region,
			Provider:  provider,
			UsedCPU:   cpu,
			UsedMemGB: float32(memGB),
			Instances: instances,
		})
		log.Printf("QuotaManager: reconciled %s/%s — instances=%d cpu=%d mem=%.1f GB (from DB)", region, provider, instances, cpu, memGB)
	}
	return rows.Err()
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
