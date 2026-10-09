package recruitment

import "testing"

func float32Ptr(v float32) *float32 { return &v }
func float64Ptr(v float64) *float64 { return &v }

// TestSelectWorkers_SwapMatch_RecruiterNullWorkerNull: the two most
// common states — neither side overrode — must resolve to the same
// effective swap (= config default) and so the worker is eligible.
func TestSelectWorkers_SwapMatch_RecruiterNullWorkerNull(t *testing.T) {
	r := Recruiter{StepID: 1, WorkerConcurrency: intPtr(1)}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1,
		Scope:       "G",
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 1 || selected[0] != 42 {
		t.Fatalf("expected worker 42 to be selected, got %v", selected)
	}
}

// TestSelectWorkers_SwapMatch_RecruiterNullWorkerEqualsDefault: a worker
// explicitly deployed with swap_proportion=0.10 is equivalent to a
// recruiter whose field is NULL when the cfg default is 0.10. The
// resolver on both sides collapses them to the same number.
func TestSelectWorkers_SwapMatch_RecruiterNullWorkerEqualsDefault(t *testing.T) {
	r := Recruiter{StepID: 1, WorkerConcurrency: intPtr(1)}
	w := RecyclableWorker{
		WorkerID:       42,
		FlavorID:       10,
		RegionID:       20,
		Concurrency:    1,
		Scope:          "G",
		SwapProportion: float64Ptr(0.10),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 1 {
		t.Fatalf("expected eligible worker: got %v", selected)
	}
}

// TestSelectWorkers_SwapMismatch_RejectsDisabledWorker: a recruiter
// using the server default (0.10) cannot recycle a worker whose
// /scratch is sized for swap=0.
func TestSelectWorkers_SwapMismatch_RejectsDisabledWorker(t *testing.T) {
	r := Recruiter{StepID: 1, WorkerConcurrency: intPtr(1)}
	w := RecyclableWorker{
		WorkerID:       42,
		FlavorID:       10,
		RegionID:       20,
		Concurrency:    1,
		Scope:          "G",
		SwapProportion: float64Ptr(0),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 0 {
		t.Fatalf("mismatched-swap worker must not be recycled: got %v", selected)
	}
}

// TestSelectWorkers_SwapMismatch_RecruiterDisabledWorkerDefault: the
// reverse direction — a recruiter asking for swap=0 cannot reuse a
// worker that was deployed with the server default.
func TestSelectWorkers_SwapMismatch_RecruiterDisabledWorkerDefault(t *testing.T) {
	r := Recruiter{
		StepID:            1,
		WorkerConcurrency: intPtr(1),
		SwapProportion:    float32Ptr(0),
	}
	w := RecyclableWorker{
		WorkerID:    42,
		FlavorID:    10,
		RegionID:    20,
		Concurrency: 1,
		Scope:       "G",
		// SwapProportion nil → resolves to cfg default 0.10.
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 0 {
		t.Fatalf("recruiter=0 vs worker=cfg-default must not recycle: got %v", selected)
	}
}

// TestSelectWorkers_SwapMatch_BothExplicitAndEqual: both sides opted
// out of the default to the same value — a worker deployed with 0.20
// is recyclable for another recruiter also asking for 0.20.
func TestSelectWorkers_SwapMatch_BothExplicitAndEqual(t *testing.T) {
	r := Recruiter{
		StepID:            1,
		WorkerConcurrency: intPtr(1),
		SwapProportion:    float32Ptr(0.20),
	}
	w := RecyclableWorker{
		WorkerID:       42,
		FlavorID:       10,
		RegionID:       20,
		Concurrency:    1,
		Scope:          "G",
		SwapProportion: float64Ptr(0.20),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 1 {
		t.Fatalf("matching explicit swap values must recycle: got %v", selected)
	}
}
