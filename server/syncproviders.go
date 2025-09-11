package server

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/scitq/scitq/server/providers/azure"
	"github.com/scitq/scitq/server/providers/openstack"
)

func (s *taskQueueServer) checkProviders() error {
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Ensure "local" provider and region exist unconditionally
	var localProviderId int32
	err = tx.QueryRow(`INSERT INTO provider (provider_name, config_name)
                   VALUES ('local', 'local')
                   ON CONFLICT (provider_name, config_name)
                   DO UPDATE SET provider_name = EXCLUDED.provider_name
                   RETURNING provider_id`).Scan(&localProviderId)
	if err != nil {
		log.Printf("⚠️ Failed to insert or get local provider: %v", err)
		return fmt.Errorf("failed to ensure local provider: %w", err)
	}

	_, err = tx.Exec(`INSERT INTO region (provider_id, region_name, is_default)
					  VALUES ($1, 'local', true)
					  ON CONFLICT (provider_id, region_name) DO UPDATE SET is_default = true`, localProviderId)
	if err != nil {
		log.Printf("⚠️ Failed to insert or update local region: %v", err)
		return fmt.Errorf("failed to ensure local region: %w", err)
	}

	// First scanning for known providers
	rows, err := tx.Query(`SELECT provider_id, provider_name, config_name FROM provider ORDER BY provider_id`)
	if err != nil {
		log.Printf("⚠️ Failed to list providers: %v", err)
		return fmt.Errorf("failed to list providers: %w", err)
	}
	defer rows.Close()

	// ✅ Store rows into memory before processing (to avoid querying while iterating)
	type ProviderInfo struct {
		ProviderID   int32
		ProviderName string
		ConfigName   string
	}
	var providers []ProviderInfo

	for rows.Next() {
		var p ProviderInfo
		if err := rows.Scan(&p.ProviderID, &p.ProviderName, &p.ConfigName); err != nil {
			log.Printf("⚠️ Failed to scan provider: %v", err)
			continue
		}
		providers = append(providers, p)
	}
	rows.Close() // ✅ Ensure rows are fully processed before executing new queries

	// ✅ Now process each provider safely
	mappedConfig := make(map[string]map[string]bool)
	for _, p := range providers {
		switch p.ProviderName {
		case "azure":
			for paramConfigName, config := range s.cfg.Providers.Azure {
				if p.ConfigName == paramConfigName {
					provider := azure.New(*config, s.cfg)
					s.providers[p.ProviderID] = provider
					s.providerConfig[fmt.Sprintf("azure.%s", paramConfigName)] = config

					if mappedConfig[p.ProviderName] == nil {
						mappedConfig[p.ProviderName] = make(map[string]bool)
					}
					mappedConfig[p.ProviderName][p.ConfigName] = true

					// ✅ Now it's safe to sync regions inside this loop
					if err := s.syncRegions(tx, p.ProviderID, config.Regions, config.DefaultRegion); err != nil {
						log.Printf("⚠️ Failed to sync regions for provider %s: %v", p.ConfigName, err)
					}
				}
				log.Printf("Azure provider %s: %v", p.ProviderName, paramConfigName)
			}
		case "openstack":
			for paramConfigName, config := range s.cfg.Providers.Openstack {
				if p.ConfigName == paramConfigName {
					provider, err := openstack.NewFromConfig(s.cfg, *config)
					if err != nil {
						log.Printf("⚠️ Failed to create openstack provider from config %s: %v", paramConfigName, err)
						continue
					}
					s.providers[p.ProviderID] = provider
					s.providerConfig[fmt.Sprintf("openstack.%s", paramConfigName)] = config

					if mappedConfig[p.ProviderName] == nil {
						mappedConfig[p.ProviderName] = make(map[string]bool)
					}
					mappedConfig[p.ProviderName][p.ConfigName] = true
					// ✅ Now it's safe to sync regions inside this loop
					if err := s.syncRegions(tx, p.ProviderID, config.Regions, config.DefaultRegion); err != nil {
						log.Printf("⚠️ Failed to sync regions for provider %s: %v", p.ConfigName, err)
					}
				}
				log.Printf("Openstack provider %s: %v", p.ProviderName, paramConfigName)
			}
		case "local":
			// Do nothing: local is registered unconditionally
			log.Printf("✅ Detected local provider in DB: %q", p.ConfigName)
		default:
			return fmt.Errorf("unknown provider %s", p.ProviderName)
		}
	}

	// Then adding new providers
	for configName, config := range s.cfg.Providers.Azure {
		if _, ok := mappedConfig["azure"][configName]; !ok {
			var providerId int32
			log.Printf("Adding Azure provider %s: %v", "azure", configName)
			err := tx.QueryRow(`INSERT INTO provider (provider_name, config_name) VALUES ($1, $2) RETURNING provider_id`,
				"azure", configName).Scan(&providerId)
			if err != nil {
				log.Printf("⚠️ Failed to add provider: %v", err)
				continue
			}
			provider := azure.New(*config, s.cfg)
			s.providers[providerId] = provider
			s.providerConfig[fmt.Sprintf("azure.%s", configName)] = config

			// Manage regions for this newly created provider
			if err := s.syncRegions(tx, providerId, config.Regions, config.DefaultRegion); err != nil {
				return fmt.Errorf("failed to sync regions for new provider %s: %w", configName, err)
			}
		}
	}

	for configName, config := range s.cfg.Providers.Openstack {
		if _, ok := mappedConfig["openstack"][configName]; !ok {
			var providerId int32
			log.Printf("Adding Openstack provider %s", configName)
			err := tx.QueryRow(`INSERT INTO provider (provider_name, config_name) VALUES ($1, $2) RETURNING provider_id`,
				"openstack", configName).Scan(&providerId)
			if err != nil {
				log.Printf("⚠️ Failed to add provider: %v", err)
				continue
			}
			provider, err := openstack.NewFromConfig(s.cfg, *config)
			if err != nil {
				return fmt.Errorf("failed to create openstack provider from config %s: %w", configName, err)
			}
			s.providers[providerId] = provider
			s.providerConfig[fmt.Sprintf("openstack.%s", configName)] = config

			// Manage regions for this newly created provider
			if err := s.syncRegions(tx, providerId, config.Regions, config.DefaultRegion); err != nil {
				return fmt.Errorf("failed to sync regions for new provider %s: %w", configName, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (s *taskQueueServer) syncRegions(tx *sql.Tx, providerId int32, configuredRegions []string, defaultRegion string) error {
	log.Printf("🔄 Syncing regions for provider %d : %v", providerId, configuredRegions)
	// Track existing regions
	existingRegions := make(map[string]int32)
	defaultRegions := make(map[string]bool)
	rows, err := tx.Query(`SELECT region_id, region_name, is_default FROM region WHERE provider_id = $1`, providerId)
	if err != nil {
		log.Printf("⚠️ Failed to list regions for provider %d: %v", providerId, err)
		return fmt.Errorf("failed to list regions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var regionId int32
		var regionName string
		var isDefault bool
		if err := rows.Scan(&regionId, &regionName, &isDefault); err != nil {
			log.Printf("⚠️ Failed to scan region: %v", err)
			continue
		}
		existingRegions[regionName] = regionId
		defaultRegions[regionName] = isDefault
	}

	// Track configured regions
	configuredRegionSet := make(map[string]bool)
	for _, region := range configuredRegions {
		configuredRegionSet[region] = true
		if _, exists := existingRegions[region]; !exists {
			// Insert missing region
			_, err := tx.Exec(`INSERT INTO region (provider_id, region_name, is_default) VALUES ($1, $2, $3)`, providerId, region, region == defaultRegion)
			if err != nil {
				log.Printf("⚠️ Failed to insert region %s: %v", region, err)
				return fmt.Errorf("failed to insert region %s: %w", region, err)
			}
			log.Printf("✅ Added new region %s for provider %d", region, providerId)
		}
	}

	// Remove regions that are in DB but not in config
	for region, regionId := range existingRegions {
		if !configuredRegionSet[region] {
			if err := s.cleanupRegion(tx, regionId, region, providerId); err != nil {
				return err
			}
		} else if defaultRegions[region] != (region == defaultRegion) {
			log.Printf("Updating region %s", region)
			_, err = tx.Exec(`UPDATE region SET is_default=$3 WHERE provider_id=$1 AND region_name=$2`, providerId, region, region == defaultRegion)
			if err != nil {
				log.Printf("⚠️ Failed to update region %s: %v", region, err)
				return fmt.Errorf("failed to update region %s: %w", region, err)
			}
		}
	}

	return nil
}

func (s *taskQueueServer) cleanupRegion(tx *sql.Tx, regionId int32, regionName string, providerId int32) error {
	log.Printf("🛑 Removing region %s (ID: %d) for provider %d", regionName, regionId, providerId)

	_, err := tx.Exec(`DELETE FROM region WHERE region_id = $1`, regionId)
	if err != nil {
		log.Printf("⚠️ Failed to delete region %s: %v", regionName, err)
		return fmt.Errorf("failed to delete region %s: %w", regionName, err)
	}

	log.Printf("✅ Successfully deleted region %s (ID: %d)", regionName, regionId)
	return nil
}
