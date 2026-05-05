package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	srv "github.com/scitq/scitq/server"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// TestStuckDeleteJanitor exercises the stuck-D worker janitor — the
// long-tail counterpart to jobqueue.go's synchronous list-check
// fallback and the orphan_cleanup goroutine. Setup mirrors the
// 2026-05-04 cc_v5 incident: a worker row sits in status='D' with
// deleted_at=NULL and no in-flight 'D' job, while the cloud-side VM
// is genuinely gone. The janitor should call provider.List, see the
// VM is absent, and soft-delete via DeleteWorker(undeployed=true).
func TestStuckDeleteJanitor(t *testing.T) {
	// NOT t.Parallel(): the test seam (TriggerStuckDeleteCleanup) is
	// process-global, and the janitor's audit-event insert path is
	// shared. Running concurrently with other server tests would
	// cross-feed.

	ctx := context.Background()

	override := &config.Config{}
	override.Providers.Fake = map[string]*config.FakeProviderConfig{
		"stuck": {
			DefaultRegion: "r1",
			Regions:       []string{"r1"},
			Quotas:        map[string]config.Quota{"r1": {MaxCPU: 32}},
			AutoLaunch:    false,
		},
	}

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()

	qc := openAdminGRPC(t, serverAddr, adminUser, adminPassword)
	defer qc.Close()

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	wid := seedWorker(t, ctx, qc, db, "stuck", "f-stuck-32")

	// --- Build the wedged shape ---
	// Step 1: do a normal delete so the FakeProvider's in-memory
	// state actually loses the VM (this is what the janitor will
	// observe via provider.List). The DB row will be soft-deleted as
	// a side effect.
	_, err = qc.Client.DeleteWorker(ctx, &pb.WorkerDeletion{WorkerId: wid})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var deletedAt sql.NullTime
		_ = db.QueryRow(`SELECT deleted_at FROM worker WHERE worker_id=$1`, wid).Scan(&deletedAt)
		return deletedAt.Valid
	}, 10*time.Second, 200*time.Millisecond, "normal delete should soft-delete the row")

	// Step 2: re-wedge — clear deleted_at and force status back to
	// 'D'. Now we have the exact shape from the 2026-05-04 incident:
	// row in 'D' / deleted_at=NULL, but the VM is genuinely gone
	// from the provider's perspective.
	_, err = db.Exec(`UPDATE worker SET status='D', deleted_at=NULL WHERE worker_id=$1`, wid)
	require.NoError(t, err)
	// Backdate the most recent 'D' job past the grace period so the
	// janitor's eligibility query lets us through.
	_, err = db.Exec(`UPDATE job SET modified_at = now() - interval '5 minutes'
	                   WHERE worker_id=$1 AND action='D'`, wid)
	require.NoError(t, err)

	// --- Run the janitor ---
	require.NoError(t, srv.TriggerStuckDeleteCleanup())

	// --- Assert: row is properly soft-deleted again ---
	var deletedAt sql.NullTime
	var status string
	require.NoError(t, db.QueryRow(`SELECT status, deleted_at FROM worker WHERE worker_id=$1`, wid).Scan(&status, &deletedAt))
	require.Equal(t, "D", status)
	require.True(t, deletedAt.Valid, "janitor should have set deleted_at")

	// And: an audit-trail event was emitted.
	var eventCount int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*) FROM worker_event
		 WHERE worker_id = $1
		   AND event_class = 'lifecycle'
		   AND message LIKE '%stuck-delete janitor%'
	`, wid).Scan(&eventCount))
	require.GreaterOrEqual(t, eventCount, 1,
		"janitor should emit a lifecycle worker_event for the recovered row")
}

// TestStuckDeleteJanitor_LeavesWorkerAlone_IfVMStillPresent guards the
// safety property: the janitor must NEVER soft-delete a row when the
// provider says the VM is still there. That would mask a cost leak —
// the failure mode `--undeployed` already has, which this janitor
// exists *not* to add.
func TestStuckDeleteJanitor_LeavesWorkerAlone_IfVMStillPresent(t *testing.T) {
	// Not parallel — same global-state argument as above.

	ctx := context.Background()

	override := &config.Config{}
	override.Providers.Fake = map[string]*config.FakeProviderConfig{
		"stuck2": {
			DefaultRegion: "r1",
			Regions:       []string{"r1"},
			Quotas:        map[string]config.Quota{"r1": {MaxCPU: 32}},
			AutoLaunch:    false,
		},
	}

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()

	qc := openAdminGRPC(t, serverAddr, adminUser, adminPassword)
	defer qc.Close()

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	wid := seedWorker(t, ctx, qc, db, "stuck2", "f-stuck2-32")

	// Force the wedged shape WITHOUT removing the VM from the
	// provider. The janitor must reach the provider.List check and
	// then *not* soft-delete because the VM is still present.
	_, err = db.Exec(`UPDATE worker SET status='D', deleted_at=NULL WHERE worker_id=$1`, wid)
	require.NoError(t, err)

	require.NoError(t, srv.TriggerStuckDeleteCleanup())

	var deletedAt sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT deleted_at FROM worker WHERE worker_id=$1`, wid).Scan(&deletedAt))
	require.False(t, deletedAt.Valid,
		"janitor must NOT soft-delete a worker whose VM the provider still reports as present (cost-leak risk)")
}

// openAdminGRPC logs in as admin and returns an authenticated gRPC client.
func openAdminGRPC(t *testing.T, serverAddr, adminUser, adminPassword string) lib.QueueClient {
	t.Helper()
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	tok := extractToken(out)
	qc, err := lib.CreateClient(serverAddr, tok)
	require.NoError(t, err)
	return qc
}

// seedWorker creates a flavor + provisions one worker via CreateWorker
// and waits until the create job succeeds (so the FakeProvider's
// in-memory state contains the VM). Returns the worker ID.
func seedWorker(t *testing.T, ctx context.Context, qc lib.QueueClient, db *sql.DB, configName, flavorName string) int32 {
	t.Helper()

	_, err := qc.Client.CreateFlavor(ctx, &pb.FlavorCreateRequest{
		ProviderName: "fake",
		ConfigName:   configName,
		FlavorName:   flavorName,
		RegionNames:  []string{"r1"},
		Evictions:    []float32{0},
		Costs:        []float32{1},
		Cpu:          32,
		Memory:       128,
		Disk:         512,
	})
	require.NoError(t, err)

	// Look up the IDs we need for CreateWorker (the names-based
	// CreateWorkerByName variant exists, but we go through CreateWorker
	// to keep the test a bit lower-level).
	var flavorID, regionID, providerID int32
	require.NoError(t, db.QueryRow(`
		SELECT f.flavor_id, fr.region_id, p.provider_id
		FROM flavor f
		JOIN flavor_region fr ON fr.flavor_id = f.flavor_id
		JOIN region r ON r.region_id = fr.region_id
		JOIN provider p ON p.provider_id = f.provider_id
		WHERE f.flavor_name = $1 AND p.provider_name = 'fake' AND p.config_name = $2
		LIMIT 1
	`, flavorName, configName).Scan(&flavorID, &regionID, &providerID))

	wresp, err := qc.Client.CreateWorker(ctx, &pb.WorkerRequest{
		ProviderId:  providerID,
		FlavorId:    flavorID,
		RegionId:    regionID,
		Number:      1,
		Concurrency: 1,
	})
	require.NoError(t, err)
	require.Len(t, wresp.WorkersDetails, 1)
	wid := wresp.WorkersDetails[0].WorkerId

	// Wait for create job → success (so the VM is actually in the
	// FakeProvider's in-memory map).
	require.Eventually(t, func() bool {
		var status string
		_ = db.QueryRow(`SELECT status FROM job WHERE worker_id=$1 AND action='C'
		                  ORDER BY job_id DESC LIMIT 1`, wid).Scan(&status)
		return status == "S"
	}, 10*time.Second, 200*time.Millisecond, "create job should reach success")

	return wid
}
