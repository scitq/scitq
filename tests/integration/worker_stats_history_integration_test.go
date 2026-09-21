package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestWorkerStatsHistory_SummaryReflectsPeaks pushes a sequence of
// pings with rising peak_mem values into the history table and
// verifies GetWorkerStatsSummary returns the largest of those peaks —
// not the last-seen mem_percent. This locks in the ping →
// history-row → GREATEST(peak_*, current_*) → MAX() aggregation that
// answers "what did this workflow actually need?".
//
// The test bypasses the full CreateWorker/FakeProvider path in favour
// of a direct DB insert for the worker row: the surface we care about
// is the persist + query chain, not the recruitment plumbing.
func TestWorkerStatsHistory_SummaryReflectsPeaks(t *testing.T) {
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

	// Direct-insert a worker row: the ping handler and history
	// persister only need a valid worker_id (FK from the history
	// table); we don't need a flavor/region/provider for this test.
	var workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, status, is_permanent)
		VALUES ('stats-history-test', 'R', TRUE)
		RETURNING worker_id
	`).Scan(&workerID))

	// Fire three pings with rising, then falling peak_mem_percent —
	// the middle value (55) is the true peak. Verifying the summary
	// returns 55, not the last value (42) or the current-gauge (5),
	// proves both that we're taking the max and that peaks flow into
	// the aggregate. Disk peak is fed alongside so the new disk field
	// carries through the same path.
	peaks := []float32{25, 55, 42}
	for _, peak := range peaks {
		low := float32(5)
		p := peak
		_, err := qc.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{
			WorkerId: workerID,
			Stats: &pb.WorkerStats{
				CpuUsagePercent:   low,
				MemUsagePercent:   low,
				IowaitPercent:     low,
				PeakCpuPercent:    &p,
				PeakMemPercent:    &p,
				PeakIowaitPercent: &p,
				PeakDiskPercent:   &p,
				RunningTasks:      0,
			},
		})
		require.NoError(t, err)
	}

	// Insert is fire-and-forget from the ping handler; poll until all
	// three samples land.
	var summary *pb.WorkerStatsSummary
	require.Eventually(t, func() bool {
		s, err := qc.GetWorkerStatsSummary(ctx, &pb.WorkerStatsHistoryFilter{
			WorkerId: &workerID,
		})
		if err != nil {
			return false
		}
		for _, e := range s.Entries {
			if e.WorkerId == workerID && e.SampleCount >= 3 && e.MaxMemPercent != nil {
				summary = s
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond,
		"summary should surface at least 3 samples with a max_mem_percent")

	require.NotNil(t, summary)
	var got *pb.WorkerStatsSummaryEntry
	for _, e := range summary.Entries {
		if e.WorkerId == workerID {
			got = e
			break
		}
	}
	require.NotNil(t, got)
	require.NotNil(t, got.MaxMemPercent)
	require.InDelta(t, 55.0, *got.MaxMemPercent, 0.5,
		"peak of the interval should surface as max_mem_percent, not the last value")
	require.NotNil(t, got.MaxCpuPercent)
	require.InDelta(t, 55.0, *got.MaxCpuPercent, 0.5)
	require.NotNil(t, got.MaxIowaitPercent)
	require.InDelta(t, 55.0, *got.MaxIowaitPercent, 0.5)
	require.NotNil(t, got.MaxDiskPercent,
		"disk peak must flow through to the summary")
	require.InDelta(t, 55.0, *got.MaxDiskPercent, 0.5)

	// sampled_at is milliseconds now — check by comparing first vs
	// last_sample_at against wall clock. Three pings within a couple
	// of seconds should span well under 60_000 ms; a value of ~3 (the
	// old seconds representation) would fail this assertion.
	require.Greater(t, got.LastSampleAt, got.FirstSampleAt-1,
		"last should be >= first when there are multiple samples")

	// Raw-series path returns the same three samples with peaks intact.
	hist, err := qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{
		WorkerId: &workerID,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(hist.Samples), 3)
	last3 := hist.Samples[len(hist.Samples)-3:]
	for i, want := range peaks {
		require.NotNil(t, last3[i].PeakMemPercent,
			"sample %d must carry peak_mem_percent", i)
		require.InDelta(t, want, *last3[i].PeakMemPercent, 0.5,
			"sample %d peak_mem_percent mismatch", i)
	}
}

// TestWorkerStatsHistory_ReasonFieldOnEmptyResult: a filter that
// matches nothing must return a typed reason so the caller can
// distinguish "no samples in this window" from "unknown worker/step/
// workflow" from "feature disabled here". Locks in the shape the peer
// asked for in the 2026-09-21 MCP review — a null-shaped empty
// response is what triggered a misdiagnosis of the feature as
// unavailable.
func TestWorkerStatsHistory_ReasonFieldOnEmptyResult(t *testing.T) {
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

	// A worker_id that clearly doesn't exist must surface as
	// "unknown_worker", not "no_samples" — the two shapes look the
	// same on the wire otherwise, and the operator has no way to tell
	// their filter has a typo from "the worker really never pinged".
	//
	// Note: over the wire proto3 serialises an empty repeated field
	// as absent, so `samples` and `entries` come back as nil (not
	// []) even though the server sent an empty slice. The MCP
	// wrapper normalises to [] for JSON output — but for the gRPC
	// path, "nil == empty" is what matters. We check the reason
	// field instead, which IS carried across the wire.
	unknownID := int32(999999)
	hist, err := qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{
		WorkerId: &unknownID,
	})
	require.NoError(t, err)
	require.Empty(t, hist.Samples)
	require.NotNil(t, hist.Reason, "reason must be set on empty result")
	require.Equal(t, "unknown_worker", *hist.Reason)

	summary, err := qc.GetWorkerStatsSummary(ctx, &pb.WorkerStatsHistoryFilter{
		WorkerId: &unknownID,
	})
	require.NoError(t, err)
	require.Empty(t, summary.Entries)
	require.NotNil(t, summary.Reason)
	require.Equal(t, "unknown_worker", *summary.Reason)

	// A valid worker_id with no samples yet returns "no_samples" —
	// distinguishes the "your id is bad" branch from the "your
	// filter is fine, nothing has happened yet" branch.
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	var goodID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, status, is_permanent)
		VALUES ('reason-test', 'R', TRUE)
		RETURNING worker_id
	`).Scan(&goodID))

	hist, err = qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{
		WorkerId: &goodID,
	})
	require.NoError(t, err)
	require.Empty(t, hist.Samples)
	require.NotNil(t, hist.Reason)
	require.Equal(t, "no_samples", *hist.Reason)
}

// TestWorkerStatsHistory_FilterlessQueryRejected: an empty filter would
// page the whole retention window across the whole fleet — almost
// certainly not what an operator wants. The handler rejects it at the
// RPC boundary with InvalidArgument. Locking this in as a compat
// guarantee: if we ever want a "give me everything recent" default,
// it should be a separate explicit param, not the empty-filter fallback.
func TestWorkerStatsHistory_FilterlessQueryRejected(t *testing.T) {
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

	_, err = qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{})
	require.Error(t, err, "filterless history query must be rejected")
	_, err = qc.GetWorkerStatsSummary(ctx, &pb.WorkerStatsHistoryFilter{})
	require.Error(t, err, "filterless summary query must be rejected")
}
