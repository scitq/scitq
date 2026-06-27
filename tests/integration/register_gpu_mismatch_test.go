package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestRegisterWorker_GPUCountMismatchEmitsEvent: when a worker
// reports a different gpu_count than its flavor catalog says, the
// server emits a W-level worker_event so the operator sees the
// drift on the worker's badge. The mismatch is informational only —
// the catalog row is NOT auto-overridden (a single per-VM hardware
// fault on one host of an otherwise healthy flavor must not become
// a fleet-wide downgrade).
//
// Setup: bind a worker to a flavor with gpu_count=2, then re-register
// the same worker reporting measured gpu_count=1. Assert the event.
// Same-name register path is what permanent workers hit on every
// boot (bioit / bigbrother / …), so this exercises the production
// drift-detection flow directly.
func TestRegisterWorker_GPUCountMismatchEmitsEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	// Flavor that the catalog believes has 2 GPUs.
	var flavorID int32
	flavorName := fmt.Sprintf("two-gpu-%d", time.Now().UnixNano())
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu, gpu_count)
		VALUES (NULL, $1, 8, 32, 100, TRUE, 2) RETURNING flavor_id
	`, flavorName).Scan(&flavorID))

	// First register: no gpu_count reported. The worker row is
	// created at this point; we then attach it to the flavor so
	// the subsequent re-register has something to compare against.
	workerName := fmt.Sprintf("w-%s", flavorName)
	one := int32(1)
	res, err := qc.RegisterWorker(ctx, &pb.WorkerInfo{
		Name:        workerName,
		Concurrency: &one,
	})
	require.NoError(t, err)
	workerID := res.WorkerId
	_, err = db.Exec(`UPDATE worker SET flavor_id = $1 WHERE worker_id = $2`, flavorID, workerID)
	require.NoError(t, err)

	// Re-register with a measured count of 1 — different from the
	// flavor's 2. The server should emit a W-level event but NOT
	// overwrite flavor.gpu_count.
	measured := int32(1)
	_, err = qc.RegisterWorker(ctx, &pb.WorkerInfo{
		Name:        workerName,
		Concurrency: &one,
		GpuCount:    &measured,
	})
	require.NoError(t, err)

	// Catalog row must stay at 2 — we never auto-override.
	var catalog int32
	require.NoError(t, db.QueryRow(`SELECT gpu_count FROM flavor WHERE flavor_id = $1`, flavorID).Scan(&catalog))
	require.Equal(t, int32(2), catalog,
		"flavor.gpu_count must not be auto-overridden by a worker's report")

	// Event must exist: W-level, event_class='gpu_count_mismatch',
	// details_json contains both numbers.
	var (
		count    int
		message  string
		details  string
		level    string
		eventCls string
	)
	require.NoError(t, db.QueryRow(`
		SELECT count(*) FROM worker_event
		WHERE worker_id = $1 AND event_class = 'gpu_count_mismatch'
	`, workerID).Scan(&count))
	require.Equal(t, 1, count,
		"exactly one gpu_count_mismatch event must be recorded for the worker")

	require.NoError(t, db.QueryRow(`
		SELECT level, event_class, message, details_json::text
		FROM worker_event
		WHERE worker_id = $1 AND event_class = 'gpu_count_mismatch'
	`, workerID).Scan(&level, &eventCls, &message, &details))
	require.Equal(t, "W", level)
	require.Contains(t, message, "gpu_count=2", "message must cite the theoretical value")
	require.Contains(t, message, "reports 1", "message must cite the measured value")
	require.Contains(t, details, `"theoretical": 2`)
	require.Contains(t, details, `"measured": 1`)

	// A second re-register at the same measured count emits a
	// second event (no in-server throttle — workers don't
	// re-register frequently, and the operator clearing the
	// badge via Reset warnings should reset the count too). This
	// is intentional and documented in the handler — we test the
	// observed behaviour so future refactors don't silently
	// change it.
	_, err = qc.RegisterWorker(ctx, &pb.WorkerInfo{
		Name:        workerName,
		Concurrency: &one,
		GpuCount:    &measured,
	})
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`
		SELECT count(*) FROM worker_event
		WHERE worker_id = $1 AND event_class = 'gpu_count_mismatch'
	`, workerID).Scan(&count))
	require.Equal(t, 2, count,
		"re-registering still emits the mismatch event (no in-server throttle)")
}

// TestRegisterWorker_GPUCountMatch_NoEvent: when the report agrees
// with the catalog, no event fires. Mirrors the happy path on a
// properly-catalogued fleet so the operator's badge stays clean.
func TestRegisterWorker_GPUCountMatch_NoEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	var flavorID int32
	flavorName := fmt.Sprintf("match-%d", time.Now().UnixNano())
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu, gpu_count)
		VALUES (NULL, $1, 4, 16, 100, TRUE, 1) RETURNING flavor_id
	`, flavorName).Scan(&flavorID))

	workerName := fmt.Sprintf("w-%s", flavorName)
	one := int32(1)
	res, err := qc.RegisterWorker(ctx, &pb.WorkerInfo{
		Name:        workerName,
		Concurrency: &one,
	})
	require.NoError(t, err)
	workerID := res.WorkerId
	_, err = db.Exec(`UPDATE worker SET flavor_id = $1 WHERE worker_id = $2`, flavorID, workerID)
	require.NoError(t, err)

	// Re-register with the matching count → no event.
	measured := int32(1)
	_, err = qc.RegisterWorker(ctx, &pb.WorkerInfo{
		Name:        workerName,
		Concurrency: &one,
		GpuCount:    &measured,
	})
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(`
		SELECT count(*) FROM worker_event
		WHERE worker_id = $1 AND event_class = 'gpu_count_mismatch'
	`, workerID).Scan(&count))
	require.Equal(t, 0, count,
		"no mismatch event expected when measured == theoretical")
}
