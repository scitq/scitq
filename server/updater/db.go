package updater

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/gmtsciencedev/scitq2/server/config"
)

// PostgresSession implements the Session interface using a PostgreSQL database.
// It wraps a *sql.Tx to support atomic operations.
type PostgresSession struct {
	db *sql.DB
	tx *sql.Tx
}

// NewPostgresSession begins a new transaction on the provided *sql.DB.
func NewPostgresSession(cfg config.Config) (*PostgresSession, error) {
	db, err := sql.Open("pgx", cfg.Scitq.DBURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return &PostgresSession{
		db: db,
	}, nil
}

func (ps *PostgresSession) Begin() error {
	tx, err := ps.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	ps.tx = tx
	return nil
}

func (ps *PostgresSession) Rollback() error {
	if err := ps.tx.Rollback(); err != nil {
		return fmt.Errorf("rollback transaction: %w", err)
	}
	return nil
}

func (ps *PostgresSession) GetProviderID(provider string) (uint32, error) {
	query := `
	SELECT provider_id FROM provider WHERE provider_name||'.'||config_name = $1`
	var providerID uint32
	err := ps.tx.QueryRow(query, provider).Scan(&providerID)
	if err != nil {
		return 0, fmt.Errorf("query provider %s for ID: %w", provider, err)
	}
	return providerID, nil
}

// QueryFlavors returns all flavors for a given provider.
func (ps *PostgresSession) QueryFlavors(providerID uint32) ([]*Flavor, error) {
	query := `
	SELECT f.flavor_name, p.provider_name||'.'||p.config_name, f.cpu, f.mem, f.disk, f.bandwidth, f.gpu, f.gpumem, f.has_gpu, f.has_quick_disks
	FROM flavor f
	JOIN provider p ON f.provider_id = p.provider_id
	WHERE f.provider_id = $1`
	rows, err := ps.tx.Query(query, providerID)
	if err != nil {
		return nil, fmt.Errorf("query flavors: %w", err)
	}
	defer rows.Close()

	var flavors []*Flavor
	for rows.Next() {
		var name, prov, gpu sql.NullString
		var cpu, bandwidth, gpumem sql.NullInt64
		var mem, disk sql.NullFloat64
		var has_gpu, has_quick_disks bool
		if err := rows.Scan(&name, &prov, &cpu, &mem, &disk, &bandwidth, &gpu, &gpumem, &has_gpu, &has_quick_disks); err != nil {
			return nil, fmt.Errorf("scan flavor: %w", err)
		}
		flavors = append(flavors, &Flavor{
			Name:         name.String,
			ProviderID:   int(providerID),
			ProviderName: prov.String,
			CPU:          int(cpu.Int64),
			Mem:          mem.Float64,
			Disk:         disk.Float64,
			Bandwidth:    int(bandwidth.Int64),
			GPU:          gpu.String,
			GPUMem:       int(gpumem.Int64),
			HasGPU:       has_gpu,
			HasQuickDisk: has_quick_disks,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return flavors, nil
}

// DeleteFlavor deletes a flavor given its name and provider.
func (ps *PostgresSession) DeleteFlavor(f *Flavor) error {
	query := `
	DELETE FROM flavor
	WHERE flavor_name = $1
	  AND provider_id = $2`
	_, err := ps.tx.Exec(query, f.Name, f.ProviderID)
	if err != nil {
		return fmt.Errorf("delete flavor: %w", err)
	}
	return nil
}

// AddFlavor inserts a new flavor into the database.
func (ps *PostgresSession) AddFlavor(f *Flavor) error {
	query := `
	INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, bandwidth, gpu, gpumem)
	VALUES (
	  $1, $2, $3, $4, $5, $6, $7, $8
	)`
	_, err := ps.tx.Exec(query, f.ProviderID, f.Name, f.CPU, f.Mem, f.Disk, f.Bandwidth, f.GPU, f.GPUMem)
	if err != nil {
		return fmt.Errorf("add flavor: %w", err)
	}
	return nil
}

// UpdateFlavor updates an existing flavor in the database.
func (ps *PostgresSession) UpdateFlavor(f *Flavor) error {
	query := `
	UPDATE flavor
	SET cpu = $3, mem = $4, disk = $5, bandwidth = $6, gpu = $7, gpumem = $8, has_gpu = $9, has_quick_disks = $10
	WHERE flavor_name = $1
	  AND provider_id = $2`
	_, err := ps.tx.Exec(query, f.Name, f.ProviderID, f.CPU, f.Mem, f.Disk, f.Bandwidth, f.GPU, f.GPUMem, f.HasGPU, f.HasQuickDisk)
	if err != nil {
		return fmt.Errorf("update flavor: %w", err)
	}
	//rowsAffected, _ := result.RowsAffected()
	//log.Printf("UpdateFlavor: %d rows affected", rowsAffected)

	return nil
}

// QueryFlavorMetrics returns flavor metrics for a given provider.
func (ps *PostgresSession) QueryFlavorMetrics(providerID int) ([]*FlavorMetrics, error) {
	query := `
	SELECT f.flavor_name, r.region_name, fr.cost, fr.eviction
	FROM flavor_region fr
	JOIN region r ON fr.region_id = r.region_id
	JOIN flavor f ON fr.flavor_id = f.flavor_id
	WHERE f.provider_id = $1`
	rows, err := ps.tx.Query(query, providerID)
	if err != nil {
		return nil, fmt.Errorf("query flavor metrics: %w", err)
	}
	defer rows.Close()

	var metrics []*FlavorMetrics
	for rows.Next() {
		var flavorName, regionName sql.NullString
		var cost, eviction sql.NullFloat64
		if err := rows.Scan(&flavorName, &regionName, &cost, &eviction); err != nil {
			return nil, fmt.Errorf("scan flavor metrics: %w", err)
		}
		metrics = append(metrics, &FlavorMetrics{
			FlavorName: flavorName.String,
			ProviderID: providerID,
			RegionName: regionName.String,
			Cost:       cost.Float64,
			Eviction:   int(eviction.Float64),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metrics, nil
}

// DeleteFlavorMetrics deletes a flavor_metric entry based on flavor name, provider, and region.
func (ps *PostgresSession) DeleteFlavorMetrics(fm *FlavorMetrics) error {
	query := `
	DELETE FROM flavor_region
	WHERE flavor_id = (
	  SELECT f.flavor_id FROM flavor f
	  WHERE f.flavor_name = $1 AND f.provider_id = $2
	)
	  AND region_id = (
	  SELECT r.region_id FROM region r
	  WHERE r.region_name = $3 AND r.provider_ID = $2
	)`
	_, err := ps.tx.Exec(query, fm.FlavorName, fm.ProviderID, fm.RegionName)
	if err != nil {
		return fmt.Errorf("delete flavor metrics: %w", err)
	}
	return nil
}

// AddFlavorMetrics inserts new flavor metrics into the database.
func (ps *PostgresSession) AddFlavorMetrics(fm *FlavorMetrics) error {
	query := `
	INSERT INTO flavor_region (flavor_id, region_id, eviction, cost)
	VALUES (
	  (SELECT f.flavor_id FROM flavor f WHERE f.flavor_name = $1 AND f.provider_id = $2),
	  (SELECT r.region_id FROM region r WHERE r.region_name = $3 AND r.provider_id = $2),
	  $4, $5
	)`
	_, err := ps.tx.Exec(query, fm.FlavorName, fm.ProviderID, fm.RegionName, fm.Eviction, fm.Cost)
	if err != nil {
		return fmt.Errorf("add flavor metrics: %w", err)
	}
	return nil
}

// Commit commits the underlying transaction.
func (ps *PostgresSession) Commit() error {
	if err := ps.tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	log.Printf("Commit successful")
	return nil
}

func (ps *PostgresSession) Close() error {
	if err := ps.db.Close(); err != nil {
		return fmt.Errorf("cannot close database: %w", err)
	}
	return nil
}
