package server

import (
	"context"
	"log"
	"time"

	"github.com/scitq/scitq/fetch"
)

// lifetimeSweepTimeout caps how long the workflow-terminal cleanup can
// hold rclone in aggregate. The sweep runs per-task-output serially
// (cheap for small workflows, order-of-N-outputs for large ones), and
// each Purge itself is unbounded — rclone honours ctx cancellation.
// If we exceed the timeout the remaining outputs are simply not cleaned;
// they stay as normal workspace files, which is the pre-feature
// behaviour, so this fails safe.
const lifetimeSweepTimeout = 15 * time.Minute

// sweepWorkflowLifetimeOutputs is called (in a goroutine) when a
// workflow transitions to S. It enumerates every task output URI
// belonging to a step whose author declared output_lifetime='W' — the
// "intermediate data" tag — and Purges each from the workspace.
//
// Semantics:
//   - Only runs on newStatus == "S". On F, callers skip this so that
//     debugging output stays available.
//   - Publish paths are NOT touched. task.output is the WORKSPACE URI
//     (publish_mode=move puts nothing there, so Purge is a no-op;
//     publish_mode=copy puts the workspace copy there, which is
//     exactly what the author wants swept). Any explicit publish URI
//     is stored in task.publish and never enters this sweep.
//   - Hidden tasks (superseded retry-parents) are excluded — they
//     point at the SAME URI as their visible clones. Purging via the
//     visible clone handles the whole prefix anyway.
//   - Failure is best-effort: we log at warn level and keep going.
//     A workflow shouldn't rewind to R because rclone hiccupped on
//     cleanup, and cleanup can be retried by hand from the operator
//     side if needed.
//
// Fire-and-forget: caller owns nothing about this goroutine. We take a
// fresh context with a bounded timeout so a cancelled request context
// upstream doesn't preempt the sweep partway.
func (s *taskQueueServer) sweepWorkflowLifetimeOutputs(workflowID int32) {
	ctx, cancel := context.WithTimeout(context.Background(), lifetimeSweepTimeout)
	defer cancel()

	// DISTINCT because two visible clones with the same output URI
	// (very unlikely but not modelled as impossible) would otherwise
	// yield two Purge calls on the same prefix — harmless but noisy.
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT t.output
		  FROM task t
		  JOIN step s ON s.step_id = t.step_id
		 WHERE s.workflow_id     = $1
		   AND s.output_lifetime = 'W'
		   AND t.output          <> ''
		   AND NOT t.hidden
	`, workflowID)
	if err != nil {
		log.Printf("⚠️ lifetime sweep: workflow %d: enumerate outputs: %v", workflowID, err)
		return
	}
	defer rows.Close()

	var uris []string
	for rows.Next() {
		var uri string
		if err := rows.Scan(&uri); err != nil {
			continue
		}
		uris = append(uris, uri)
	}
	if len(uris) == 0 {
		return
	}

	log.Printf("🧹 lifetime sweep: workflow %d: purging %d workspace output prefix(es)", workflowID, len(uris))
	var ok, failed int
	for _, uri := range uris {
		if err := fetch.Purge(ctx, uri); err != nil {
			// Timeout / context cancellation halts the loop — the
			// remaining URIs stay as workspace clutter, no harm done.
			if ctx.Err() != nil {
				log.Printf("⚠️ lifetime sweep: workflow %d: aborting after %d/%d (%s)",
					workflowID, ok+failed, len(uris), ctx.Err())
				return
			}
			log.Printf("⚠️ lifetime sweep: workflow %d: purge %q failed: %v",
				workflowID, uri, err)
			failed++
			continue
		}
		ok++
	}
	log.Printf("✅ lifetime sweep: workflow %d: %d purged, %d failed", workflowID, ok, failed)
}
