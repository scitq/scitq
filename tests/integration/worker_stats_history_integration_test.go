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
	// the aggregate.
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
