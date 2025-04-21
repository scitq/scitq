package recruitment

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/gmtsciencedev/scitq2/server/config"
	"github.com/gmtsciencedev/scitq2/server/protofilter"
)

// RecyclableScope matches DB values: 'G', 'W', 'T', 'N'
type RecyclableScope string

const (
	RecyclableGlobal    RecyclableScope = "G"
	RecyclableWorkflow  RecyclableScope = "W"
	RecyclableTemporary RecyclableScope = "T"
	RecyclableNever     RecyclableScope = "N"
)

type WorkerState struct {
	ID               int
	WorkflowID       int
	StepID           int
	Region           string
	Provider         string
	Concurrency      int
	CurrentTaskCount int
	Flavor           string
	Status           rune // 'R', 'P', etc.
	Has_GPU          bool
	CPU              int32
	Mem              float32
	Disk             float32
	Bandwidth        int
	GPUMem           int
	Has_QuickDisk    bool

	RecyclableScope RecyclableScope
	LastPing        time.Time

	// Derived at runtime
	IsIdle               bool
	IsCompatibleWithStep func(*StepDemand) bool
	PartialIdleFraction  float32 // 0.0–1.0
}

type FlavorRequirements struct {
	Name  string
	CPU   int32
	MemGB float32
}

type StepDemand struct {
	StepID      int
	WorkflowID  int
	Region      string
	Provider    string
	Concurrency int

	PendingTasks     int
	AnticipatedTasks int // based on waiting tasks and running predecessors

	// Workers that may be used for this step
	AssignedWorkers          []*WorkerState // currently serving this step
	CompatibleIdleWorkers    []*WorkerState // recyclable
	CompatiblePartialWorkers []*WorkerState // partial-recyclable

	Flavor FlavorRequirements // Computed or selected flavor for this step
}

type RecruitmentContext struct {
	StepsByID    sync.Map // map[int]*StepDemand
	WorkersByID  sync.Map // map[int]*WorkerState
	Now          time.Time
	QuotaManager *QuotaManager
}

func NewRecruitmentContext(cfg *config.Config) *RecruitmentContext {
	return &RecruitmentContext{
		StepsByID:    sync.Map{},
		WorkersByID:  sync.Map{},
		Now:          time.Now(),
		QuotaManager: NewQuotaManager(cfg),
	}
}

func (ctx *RecruitmentContext) RegisterWorker(w *WorkerState) bool {
	if ctx.QuotaManager != nil {
		if !ctx.QuotaManager.CanLaunch(w.Region, w.Provider, w.CPU, w.Mem) {
			return false
		}
		ctx.QuotaManager.RegisterLaunch(w.Region, w.Provider, w.CPU, w.Mem)
	}
	ctx.WorkersByID.Store(w.ID, w)
	return true
}

func (ctx *RecruitmentContext) DeleteWorker(w *WorkerState) {
	ctx.WorkersByID.Delete(w.ID)
	if ctx.QuotaManager != nil {
		ctx.QuotaManager.RegisterDelete(w.Region, w.Provider, w.CPU, w.Mem)
	}
}

func (w *WorkerState) CanBeUsedFor(step *StepDemand) bool {
	if w.RecyclableScope == RecyclableNever {
		return false
	}
	if w.RecyclableScope == RecyclableWorkflow && w.WorkflowID != step.WorkflowID {
		return false
	}
	return w.Provider == step.Provider && w.Region == step.Region
}

func IsWorkerCompatibleWithFilter(worker *WorkerState, filter string) (bool, error) {
	return protofilter.EvaluateAgainst(filter, protofilter.NewWorkerStateAdapter(worker))
}

// GetWorkerState retrieves a worker state from the database
// based on the flavor and region.
func GetWorkerState(workerID uint32, db *sql.DB) (*WorkerState, error) {
	var worker WorkerState
	query := `SELECT worker_id, workflow_id, step_id, region_name, provider_name, concurrency, current_task_count,
			flavor, status, has_gpu, cpu, mem, disk, bandwidth, gpu_mem, has_quickdisk,
			recyclable_scope, last_ping
		FROM worker w 
			JOIN flavor f ON f.flavor_id=w.flavor_id 
			JOIN provider p ON p.provider_id=r.provider_id 
		WHERE worker_id=$1`
	err := db.QueryRow(query, workerID).Scan(&worker.ID, &worker.WorkflowID,
		&worker.StepID, &worker.Region, &worker.Provider,
		&worker.Concurrency, &worker.CurrentTaskCount,
		&worker.Flavor, &worker.Status,
		&worker.Has_GPU, &worker.CPU,
		&worker.Mem, &worker.Disk,
		&worker.Bandwidth, &worker.GPUMem,
		&worker.Has_QuickDisk,
		&worker.RecyclableScope,
		&worker.LastPing)
	if err != nil {
		return nil, fmt.Errorf("error retrieving worker state:%w", err)
	}
	return &worker, nil
}

//////////////////////////////////////////////////////
//
//       RECRUITMENT LOOP
//
//////////////////////////////////////////////////////

type RecruitmentActionType string

const (
	RecruitmentWait         RecruitmentActionType = "wait"
	RecruitmentLaunch       RecruitmentActionType = "launch"
	RecruitmentRecycle      RecruitmentActionType = "recycle"
	RecruitmentPartialReuse RecruitmentActionType = "partial-reuse"
)

type RecruitmentAction struct {
	StepID  int
	Action  RecruitmentActionType
	Count   int            // for Launch
	Workers []*WorkerState // for Recycle / PartialReuse
	Reason  string
}

func RunRecruitmentPass(ctx *RecruitmentContext, recruiterFilter map[int]string) []RecruitmentAction {
	var actions []RecruitmentAction

	ctx.StepsByID.Range(func(key, value any) bool {
		stepID := key.(int)
		step := value.(*StepDemand)

		totalNeeded := step.PendingTasks + step.AnticipatedTasks
		neededSlots := totalNeeded
		concurrency := step.Concurrency
		neededWorkers := (neededSlots + concurrency - 1) / concurrency

		active := len(step.AssignedWorkers)
		idle := len(step.CompatibleIdleWorkers)
		partial := len(step.CompatiblePartialWorkers)

		deficit := neededWorkers - active

		if deficit <= 0 {
			actions = append(actions, RecruitmentAction{
				StepID: stepID,
				Action: RecruitmentWait,
				Reason: fmt.Sprintf("Enough active workers (%d), no action needed", active),
			})
			return true
		}

		recycled := []*WorkerState{}
		partiallyRecycled := []*WorkerState{}

		if idle > 0 {
			for _, w := range step.CompatibleIdleWorkers {
				if len(recycled) >= deficit {
					break
				}
				recycled = append(recycled, w)
			}
			deficit -= len(recycled)
			if len(recycled) > 0 {
				actions = append(actions, RecruitmentAction{
					StepID:  stepID,
					Action:  RecruitmentRecycle,
					Workers: recycled,
					Reason:  fmt.Sprintf("Recycling %d idle workers", len(recycled)),
				})
			}
		}

		if deficit > 0 && partial > 0 {
			for _, w := range step.CompatiblePartialWorkers {
				if len(partiallyRecycled) >= deficit {
					break
				}
				partiallyRecycled = append(partiallyRecycled, w)
			}
			deficit -= len(partiallyRecycled)
			if len(partiallyRecycled) > 0 {
				actions = append(actions, RecruitmentAction{
					StepID:  stepID,
					Action:  RecruitmentPartialReuse,
					Workers: partiallyRecycled,
					Reason:  fmt.Sprintf("Partially reusing %d workers", len(partiallyRecycled)),
				})
			}
		}

		if deficit > 0 {
			launchable := 0
			for i := 0; i < deficit; i++ {
				if ctx.QuotaManager.CanLaunch(step.Region, step.Provider, step.Flavor.CPU, step.Flavor.MemGB) {
					ctx.QuotaManager.RegisterLaunch(step.Region, step.Provider, step.Flavor.CPU, step.Flavor.MemGB)
					launchable++
				} else {
					break
				}
			}
			if launchable > 0 {
				actions = append(actions, RecruitmentAction{
					StepID: stepID,
					Action: RecruitmentLaunch,
					Count:  launchable,
					Reason: fmt.Sprintf("Launching %d new workers", launchable),
				})
			} else {
				actions = append(actions, RecruitmentAction{
					StepID: stepID,
					Action: RecruitmentWait,
					Reason: "Quota exhausted; cannot launch new workers",
				})
			}
		}

		return true
	})

	return actions
}
