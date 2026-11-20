package recruitment

import "testing"

// idleW returns an idle worker scoped to workflow 1, on its step 10.
func idleW(id int32) RecyclableWorker {
	return RecyclableWorker{WorkerID: id, FlavorID: 100, RegionID: 200, StepID: int32Ptr(10),
		WorkflowID: int32Ptr(1), Concurrency: 4, Scope: "W"}
}

// shortStep is an active recruiter for step 11 of workflow 1, short of
// `need` task slots, giving each recycled worker concurrency 4.
func shortStep(need int, maxWorkers *int) Recruiter {
	return Recruiter{StepID: 11, Rank: 1, WorkflowID: 1, TargetTaskrate: need,
		WorkerConcurrency: intPtr(4), MaximumWorkers: maxWorkers}
}

var lendFlavors = map[RecruiterKey][]FlavorDetail{
	{StepID: 11, Rank: 1}: {{FlavorID: 100, RegionID: 200}},
}

func lendable(ws []RecyclableWorker) map[int32]bool {
	out := map[int32]bool{}
	for _, w := range ws {
		out[w.WorkerID] = w.Lendable
	}
	return out
}

func TestMarkLendable_NoUseInOwnWorkflow(t *testing.T) {
	ws := []RecyclableWorker{idleW(1), idleW(2)}
	markLendable(ws, nil, nil, 0.1)
	if got := lendable(ws); !got[1] || !got[2] {
		t.Fatalf("workflow without a short step should lend its workers, got %v", got)
	}
}

func TestMarkLendable_KeepsOnlyWhatTheShortfallNeeds(t *testing.T) {
	ws := []RecyclableWorker{idleW(1), idleW(2), idleW(3)}
	markLendable(ws, []Recruiter{shortStep(1, nil)}, lendFlavors, 0.1)
	if got := lendable(ws); got[1] || !got[2] || !got[3] {
		t.Fatalf("a step one slot short should keep one worker and lend two, got %v", got)
	}
	ws = []RecyclableWorker{idleW(1), idleW(2), idleW(3)}
	markLendable(ws, []Recruiter{shortStep(5, nil)}, lendFlavors, 0.1)
	if got := lendable(ws); got[1] || got[2] || !got[3] {
		t.Fatalf("a step five slots short should keep two workers of concurrency 4, got %v", got)
	}
}

func TestMarkLendable_KeepsNoMoreThanMaxWorkersAllows(t *testing.T) {
	ws := []RecyclableWorker{idleW(1), idleW(2), idleW(3)}
	r := shortStep(100, intPtr(3))
	r.CurrentWorkers = 2
	markLendable(ws, []Recruiter{r}, lendFlavors, 0.1)
	if got := lendable(ws); got[1] || !got[2] || !got[3] {
		t.Fatalf("a step with room for one more worker should keep one, got %v", got)
	}
}

func TestMarkLendable_LendsWorkersTheShortStepCannotTake(t *testing.T) {
	other := idleW(1)
	other.RegionID = 201
	ws := []RecyclableWorker{other}
	markLendable(ws, []Recruiter{shortStep(10, nil)}, lendFlavors, 0.1)
	if !ws[0].Lendable {
		t.Fatal("a worker outside the short step's filter should be lent")
	}
}

func TestSelectWorkers_OtherWorkflowTakesOnlyLendable(t *testing.T) {
	ws := []RecyclableWorker{idleW(1), idleW(2)}
	ws[1].Lendable = true
	r := Recruiter{StepID: 20, WorkflowID: 2, WorkerConcurrency: intPtr(4)}
	selected := selectWorkersForRecruiter(ws, map[int32]struct{}{100: {}}, map[int32]struct{}{200: {}},
		100, 20, 2, r, 0.1)
	if len(selected) != 1 || selected[0] != 2 {
		t.Fatalf("workflow 2 should recycle only the lent worker 2, got %v", selected)
	}
}
