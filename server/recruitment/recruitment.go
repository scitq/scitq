package recruitment

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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

	RecyclableScope RecyclableScope
	LastPing        time.Time

	// Derived at runtime
	IsIdle               bool
	IsCompatibleWithStep func(*StepDemand) bool
	PartialIdleFraction  float64 // 0.0–1.0
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
}

type RecruitmentContext struct {
	StepsByID      map[int]*StepDemand
	WorkersByID    map[int]*WorkerState
	QuotaAvailable int
	Now            time.Time
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
	tokens := strings.Split(filter, ":")
	for _, token := range tokens {
		if token == "" {
			continue
		}

		matches := filterRegex.FindStringSubmatch(token)
		if len(matches) == 0 {
			return false, fmt.Errorf("invalid filter token: %s", token)
		}

		col := strings.ToLower(matches[1])
		op := matches[2]
		val := matches[3]

		if !evaluateWorkerCondition(worker, col, op, val) {
			return false, nil
		}
	}
	return true, nil
}

func evaluateWorkerCondition(worker *WorkerState, col, op, val string) bool {
	switch col {
	case "cpu":
		v, _ := strconv.Atoi(val)
		return compareInt(worker.CPU, op, v)
	case "mem":
		v, _ := strconv.ParseFloat(val, 64)
		return compareFloat(worker.Mem, op, v)
	case "has_gpu":
		return worker.HasGPU
	// Add more fields as needed
	default:
		return false
	}
}
