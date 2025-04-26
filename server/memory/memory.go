package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
)

// LoadMemory loads a JSON memory blob from the database into dest (must be a pointer).
func LoadMemory(ctx context.Context, db *sql.DB, key string, dest any) error {
	var raw []byte

	err := db.QueryRowContext(ctx, `
        SELECT value
        FROM memory_store
        WHERE key = $1
    `, key).Scan(&raw)

	if err == sql.ErrNoRows {
		return fmt.Errorf("memory key %q not found", key)
	} else if err != nil {
		return fmt.Errorf("failed loading memory key %q: %w", key, err)
	}

	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("failed decoding memory key %q: %w", key, err)
	}

	return nil
}

// SaveMemory saves a Go struct as JSON into the database.
func SaveMemory(ctx context.Context, db *sql.DB, key string, src any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("failed encoding memory key %q: %w", key, err)
	}

	_, err = db.ExecContext(ctx, `
        INSERT INTO memory_store (key, value)
        VALUES ($1, $2)
        ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
    `, key, data)
	if err != nil {
		return fmt.Errorf("failed saving memory key %q: %w", key, err)
	}

	return nil
}

func serializeWeightMemory(m *sync.Map) map[int]map[int]float64 {
	out := make(map[int]map[int]float64)
	m.Range(func(workerID, tasksAny any) bool {
		tasks := tasksAny.(*sync.Map)
		taskMap := make(map[int]float64)
		tasks.Range(func(taskIDAny, weightAny any) bool {
			taskID := taskIDAny.(int)
			weight := weightAny.(float64)
			taskMap[taskID] = weight
			return true
		})
		out[workerID.(int)] = taskMap
		return true
	})
	return out
}

func deserializeWeightMemory(m map[int]map[int]float64) *sync.Map {
	out := &sync.Map{}
	for workerID, tasks := range m {
		taskSync := &sync.Map{}
		for taskID, weight := range tasks {
			taskSync.Store(taskID, weight)
		}
		out.Store(workerID, taskSync)
	}
	return out
}

func LoadWeightMemory(ctx context.Context, db *sql.DB, key string) (*sync.Map, error) {
	var swm map[int]map[int]float64
	err := LoadMemory(ctx, db, key, &swm)
	if err != nil {
		return nil, fmt.Errorf("failed loading weight memory key %q: %w", key, err)
	}
	wm := deserializeWeightMemory(swm)
	return wm, nil
}

func SaveWeightMemory(ctx context.Context, db *sql.DB, key string, wm *sync.Map) error {
	swm := serializeWeightMemory(wm)
	err := SaveMemory(ctx, db, key, swm)
	if err != nil {
		return fmt.Errorf("failed saving weight memory key %q: %w", key, err)
	}
	return nil
}
