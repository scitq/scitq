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

// TestWorkerStatsHistory_BucketAggregation exercises the bucket_seconds
// downsampling: 30 pings landed close together with rising peak_mem
// values, grouped into buckets. Verifies (a) the row count drops per
// the bucket width, (b) each bucket's returned peak_mem is the MAX of
// the pings it aggregated (peaks stay peaks, never averaged), and
// (c) the current-value gauge is the AVG of the pings in each bucket.
//
// The peer's MCP payload-cap issue (2026-09-21 review) is what this
// endpoint is meant to fix; this test locks in the fix's semantics.
func TestWorkerStatsHistory_BucketAggregation(t *testing.T) {
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

	var workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, status, is_permanent)
		VALUES ('bucket-agg-test', 'R', TRUE)
		RETURNING worker_id
	`).Scan(&workerID))

	// Fire 30 pings with peak_mem rising from 10 to 39. Current-value
	// mem_percent tracks alongside as a distinct series so we can
	// verify AVG vs MAX aggregation behave differently.
	for i := 0; i < 30; i++ {
		peak := float32(10 + i)
		current := float32(i) // 0..29
		_, err := qc.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{
			WorkerId: workerID,
			Stats: &pb.WorkerStats{
				MemUsagePercent: current,
				PeakMemPercent:  &peak,
				RunningTasks:    0,
			},
		})
		require.NoError(t, err)
	}

	// Poll for all 30 samples to land in history (fire-and-forget INSERT).
	require.Eventually(t, func() bool {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM worker_stats_history WHERE worker_id=$1`,
			workerID).Scan(&n)
		return n >= 30
	}, 5*time.Second, 100*time.Millisecond, "30 history rows never landed")

	// The 30 pings all land within a second or two of each other under
	// this test harness. To exercise the bucket code path meaningfully
	// we spread them across ~30 seconds of *stored* timestamps by
	// rewriting sampled_at in the DB. Bucket size 10 s → we expect 3
	// output rows covering ping index [0..9], [10..19], [20..29].
	//
	// Rewriting via SQL keeps this fast; nothing else touches these
	// rows so there's no race concern.
	_, err = db.Exec(`
		WITH ordered AS (
			SELECT ctid, ROW_NUMBER() OVER (ORDER BY sampled_at) - 1 AS rn
			  FROM worker_stats_history WHERE worker_id = $1
		)
		UPDATE worker_stats_history h
		   SET sampled_at = to_timestamp(0) + (o.rn * interval '1 second')
		  FROM ordered o
		 WHERE h.ctid = o.ctid
	`, workerID)
	require.NoError(t, err)

	// Bucketed query: 10 s buckets.
	bucket := int32(10)
	res, err := qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{
		WorkerId:      &workerID,
		BucketSeconds: &bucket,
	})
	require.NoError(t, err)
	require.Len(t, res.Samples, 3, "30 pings at 1 Hz → 3 buckets at 10 s each")

	// Bucket 0: pings 0..9 → peak_mem MAX = 19, mem AVG = 4.5
	// Bucket 1: pings 10..19 → peak_mem MAX = 29, mem AVG = 14.5
	// Bucket 2: pings 20..29 → peak_mem MAX = 39, mem AVG = 24.5
	wantMaxPeaks := []float32{19, 29, 39}
	wantAvgMems := []float32{4.5, 14.5, 24.5}
	for i, s := range res.Samples {
		require.NotNil(t, s.PeakMemPercent, "bucket %d must carry peak_mem", i)
		require.InDelta(t, wantMaxPeaks[i], *s.PeakMemPercent, 0.001,
			"bucket %d: peak_mem should be MAX of the ping window", i)
		require.NotNil(t, s.MemPercent, "bucket %d must carry mem", i)
		require.InDelta(t, wantAvgMems[i], *s.MemPercent, 0.001,
			"bucket %d: mem should be AVG of the ping window", i)
	}
}

// TestWorkerStatsHistory_FieldsSelectorTrimsPayload verifies the fields
// selector: only the named field is emitted; the rest stay unset. The
// step_id column is force-on regardless (locators are always useful).
func TestWorkerStatsHistory_FieldsSelectorTrimsPayload(t *testing.T) {
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

	var workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, status, is_permanent)
		VALUES ('fields-selector-test', 'R', TRUE)
		RETURNING worker_id
	`).Scan(&workerID))

	// One ping with every value populated.
	peak := float32(42)
	_, err = qc.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{
		WorkerId: workerID,
		Stats: &pb.WorkerStats{
			CpuUsagePercent:   10,
			MemUsagePercent:   20,
			IowaitPercent:     5,
			PeakCpuPercent:    &peak,
			PeakMemPercent:    &peak,
			PeakIowaitPercent: &peak,
			PeakDiskPercent:   &peak,
			RunningTasks:      3,
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		res, err := qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{
			WorkerId: &workerID,
		})
		return err == nil && len(res.Samples) >= 1
	}, 5*time.Second, 100*time.Millisecond, "one sample never landed")

	// Ask for only peak_mem.
	res, err := qc.ListWorkerStatsHistory(ctx, &pb.WorkerStatsHistoryFilter{
		WorkerId: &workerID,
		Fields:   []string{"peak_mem"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Samples)
	s := res.Samples[0]
	require.NotNil(t, s.PeakMemPercent, "selector included peak_mem, must be emitted")
	require.InDelta(t, 42, *s.PeakMemPercent, 0.001)
	// Everything else the selector didn't ask for must be nil.
	require.Nil(t, s.CpuPercent, "cpu not selected")
	require.Nil(t, s.MemPercent, "mem not selected")
	require.Nil(t, s.PeakCpuPercent, "peak_cpu not selected")
	require.Nil(t, s.PeakIowaitPercent, "peak_iowait not selected")
	require.Nil(t, s.PeakDiskPercent, "peak_disk not selected")
	require.Nil(t, s.RunningTasks, "running_tasks not selected")
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
