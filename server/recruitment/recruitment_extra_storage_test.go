package recruitment

import "testing"

func i32Ptr(v int32) *int32 { return &v }
func strPtr(v string) *string { return &v }

// TestSelectWorkers_ExtraMatch_BothUnset: default state — neither
// recruiter nor worker asks for extra storage. Worker is eligible.
func TestSelectWorkers_ExtraMatch_BothUnset(t *testing.T) {
	r := Recruiter{StepID: 1, WorkerConcurrency: intPtr(1)}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1, Scope: "G",
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 1 {
		t.Fatalf("both-unset should recycle: got %v", selected)
	}
}

// TestSelectWorkers_ExtraMatch_SameSizeAndType: explicit match on
// both size and type passes recycling.
func TestSelectWorkers_ExtraMatch_SameSizeAndType(t *testing.T) {
	r := Recruiter{
		StepID: 1, WorkerConcurrency: intPtr(1),
		ExtraStorageGB:   i32Ptr(500),
		ExtraStorageType: strPtr("high-speed"),
	}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1, Scope: "G",
		ExtraStorageGB:   i32Ptr(500),
		ExtraStorageType: strPtr("high-speed"),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 1 {
		t.Fatalf("matching 500GB high-speed should recycle: got %v", selected)
	}
}

// TestSelectWorkers_ExtraMismatch_Size: a size difference
// disqualifies the worker — volumes aren't reshapable mid-flight.
func TestSelectWorkers_ExtraMismatch_Size(t *testing.T) {
	r := Recruiter{
		StepID: 1, WorkerConcurrency: intPtr(1),
		ExtraStorageGB: i32Ptr(500),
	}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1, Scope: "G",
		ExtraStorageGB: i32Ptr(1000),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 0 {
		t.Fatalf("size mismatch must not recycle: got %v", selected)
	}
}

// TestSelectWorkers_ExtraMismatch_Type: same size but different
// volume class — reject.
func TestSelectWorkers_ExtraMismatch_Type(t *testing.T) {
	r := Recruiter{
		StepID: 1, WorkerConcurrency: intPtr(1),
		ExtraStorageGB:   i32Ptr(500),
		ExtraStorageType: strPtr("classic"),
	}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1, Scope: "G",
		ExtraStorageGB:   i32Ptr(500),
		ExtraStorageType: strPtr("high-speed"),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 0 {
		t.Fatalf("type mismatch must not recycle: got %v", selected)
	}
}

// TestSelectWorkers_ExtraMismatch_RecruiterWantsNone: a recruiter
// that asks for NO extra storage cannot recycle a worker that was
// born with one — the /scratch volume would still be mounted.
func TestSelectWorkers_ExtraMismatch_RecruiterWantsNone(t *testing.T) {
	r := Recruiter{StepID: 1, WorkerConcurrency: intPtr(1)}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1, Scope: "G",
		ExtraStorageGB: i32Ptr(500),
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 0 {
		t.Fatalf("recruiter=none vs worker=500GB must not recycle: got %v", selected)
	}
}

// TestSelectWorkers_ExtraMatch_SizeZeroIsNone: an explicit 0 on
// either side is equivalent to unset (both resolve to "no extra
// volume").
func TestSelectWorkers_ExtraMatch_SizeZeroIsNone(t *testing.T) {
	r := Recruiter{
		StepID: 1, WorkerConcurrency: intPtr(1),
		ExtraStorageGB: i32Ptr(0),
	}
	w := RecyclableWorker{
		WorkerID: 42, FlavorID: 10, RegionID: 20,
		Concurrency: 1, Scope: "G",
		// ExtraStorageGB nil → treated as 0
	}
	selected := selectWorkersForRecruiter(
		[]RecyclableWorker{w},
		map[int32]struct{}{10: {}},
		map[int32]struct{}{20: {}},
		1, 999, 7, r, 0.10,
	)
	if len(selected) != 1 {
		t.Fatalf("0 and nil must both resolve to 'no extra storage': got %v", selected)
	}
}
