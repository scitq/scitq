package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/lib/pq"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	ws "github.com/scitq/scitq/server/websocket"
	"google.golang.org/protobuf/proto"
)

// A task whose prerequisite failed for good, through a dependency that does
// not accept failure, can never run. It fails too, with failure class
// "upstream", and so do its own dependents in turn: a dependent that accepts
// failure treats it like any final failure, so a fan-in step proceeds with
// what succeeded. A cascaded task keeps its retry budget; retrying the
// failed prerequisite reopens it (reopenUpstream).

// finalFailure is the SQL condition under which a failed task t stays failed:
// no retry left, or failed because of its prerequisites.
const finalFailure = `(t.retry = 0 OR t.failure_class = 'upstream')`

// cannotRun tells whether a waiting task has a prerequisite that failed for
// good through a dependency that does not accept failure.
func (s *taskQueueServer) cannotRun(ctx context.Context, taskID int32) (bool, error) {
	var doomed bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM task_dependencies d
			JOIN task t ON d.prerequisite_task_id = t.task_id
			WHERE d.dependent_task_id = $1
			  AND t.status = 'F' AND NOT t.hidden AND `+finalFailure+`
			  AND NOT d.accept_failure
		)`, taskID).Scan(&doomed)
	return doomed, err
}

// cascadeUpstream fails the given waiting dependents of failedID.
// UpdateTaskStatus carries the cascade on to their own dependents.
func (s *taskQueueServer) cascadeUpstream(ctx context.Context, failedID int32, dependents []int32) {
	if len(dependents) == 0 {
		return
	}
	actx := withSchedulerActor(context.WithoutCancel(ctx), fmt.Sprintf("failure of prerequisite task %d", failedID))
	for _, id := range dependents {
		log.Printf("⛓️ task %d cannot run: prerequisite task %d failed", id, failedID)
		if _, err := s.UpdateTaskStatus(actx, &pb.TaskStatusUpdate{
			TaskId:       id,
			NewStatus:    "F",
			FailureClass: proto.String("upstream"),
		}); err != nil {
			log.Printf("⚠️ failed to fail task %d after its prerequisite %d: %v", id, failedID, err)
		}
	}
}

// reopenedTask is a task set back from F (upstream) to W.
type reopenedTask struct {
	taskID, stepID, workflowID int32
}

// reopenUpstream sets back to W the given tasks that failed because of a
// prerequisite, with their own cascaded dependents. The caller applies
// afterReopen once the transaction is committed.
func reopenUpstream(ctx context.Context, tx *sql.Tx, taskIDs []int32) ([]reopenedTask, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH RECURSIVE cascaded AS (
			SELECT t.task_id
			FROM task t
			WHERE t.task_id = ANY($1)
			  AND t.status = 'F' AND t.failure_class = 'upstream' AND NOT t.hidden
			UNION
			SELECT d.dependent_task_id
			FROM task_dependencies d
			JOIN cascaded c ON d.prerequisite_task_id = c.task_id
			JOIN task t ON t.task_id = d.dependent_task_id
			WHERE t.status = 'F' AND t.failure_class = 'upstream' AND NOT t.hidden
		)
		UPDATE task t
		SET status = 'W', failure_class = NULL, modified_at = NOW()
		FROM cascaded c
		WHERE t.task_id = c.task_id
		RETURNING t.task_id, COALESCE(t.step_id, 0),
			COALESCE((SELECT workflow_id FROM step WHERE step_id = t.step_id), 0)`, pq.Array(taskIDs))
	if err != nil {
		return nil, fmt.Errorf("reopen tasks %v: %w", taskIDs, err)
	}
	defer rows.Close()
	var reopened []reopenedTask
	for rows.Next() {
		var r reopenedTask
		if err := rows.Scan(&r.taskID, &r.stepID, &r.workflowID); err != nil {
			return nil, err
		}
		reopened = append(reopened, r)
	}
	return reopened, rows.Err()
}

// dependentsOf lists the direct dependents of a task.
func dependentsOf(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, taskID int32) ([]int32, error) {
	rows, err := q.QueryContext(ctx, `SELECT dependent_task_id FROM task_dependencies WHERE prerequisite_task_id = $1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// afterReopen updates the step stats and the workflows of reopened tasks,
// and fails again those that still cannot run. cause is the task whose
// retry or deletion reopened them.
func (s *taskQueueServer) afterReopen(ctx context.Context, cause int32, reopened []reopenedTask) {
	workflows := map[int32]bool{}
	for _, r := range reopened {
		log.Printf("↩️ task %d waits again after task %d was retried or deleted", r.taskID, cause)
		if r.workflowID == 0 {
			continue
		}
		workflows[r.workflowID] = true
		s.stats.Adjust(r.workflowID, r.stepID, func(agg *StepAgg) {
			if agg.ReallyFailed > 0 {
				agg.ReallyFailed--
			}
			agg.Waiting++
		})
	}
	for wf := range workflows {
		s.reopenWorkflow(ctx, wf)
	}
	for _, r := range reopened {
		if doomed, err := s.cannotRun(ctx, r.taskID); err != nil {
			log.Printf("⚠️ failed to check failed prerequisites of task %d: %v", r.taskID, err)
		} else if doomed {
			s.cascadeUpstream(ctx, cause, []int32{r.taskID})
		}
	}
	s.triggerAssign()
}

// reopenWorkflow sets a finished (S or F) workflow back to R, for a task of
// it that goes back to be run: the assignment and the recruiters serve only
// running workflows.
func (s *taskQueueServer) reopenWorkflow(ctx context.Context, workflowID int32) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE workflow SET status = 'R' WHERE workflow_id = $1 AND status IN ('S','F')`, workflowID)
	if err != nil {
		log.Printf("⚠️ failed to set workflow %d back to R: %v", workflowID, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	log.Printf("↩️ workflow %d runs again", workflowID)
	ws.EmitWS("workflow", workflowID, "status", struct {
		WorkflowId int32  `json:"workflowId"`
		Status     string `json:"status"`
	}{WorkflowId: workflowID, Status: "R"})
	s.triggerAssign()
}
