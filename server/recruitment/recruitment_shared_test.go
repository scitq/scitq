package recruitment

import "testing"

// TestComputeConcurrency_MemSharedSubtracts locks in the Reading B math:
// with shared overhead the recruiter subtracts the shared block from
// worker.mem *once* before dividing by per-task cost, so a big shared
// index doesn't collapse concurrency the way a naive
// mem_per_task = shared + per_task would.
//
// Hermes-style example: 15 GB shared index + 5 GB per query on a 60 GB
// worker. Linear model would give floor(60 / 20) = 3. Reading B gives
// floor((60 - 15) / 5) = 9 — the actual concurrency the tool can serve.
func TestComputeConcurrency_MemSharedSubtracts(t *testing.T) {
	r := Recruiter{
		CpuPerTask:          intPtr(1),
		MemoryPerTask:       f32Ptr(5),
		MemorySharedPerTask: f32Ptr(15),
	}
	w := RecyclableWorker{
		Cpu:    int32Ptr(32),
		Memory: f64Ptr(60),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 9 {
		t.Fatalf("expected concurrency=9 ((60-15)/5), got %d", got)
	}
}

// TestComputeConcurrency_DiskSharedSubtracts: same math for the disk
// dimension. 200 GB decompressed reference + 100 GB scratch per task on
// a 1400 GB worker → floor((1400 - 200) / 100) = 12.
func TestComputeConcurrency_DiskSharedSubtracts(t *testing.T) {
	r := Recruiter{
		CpuPerTask:        intPtr(1),
		DiskPerTask:       f32Ptr(100),
		DiskSharedPerTask: f32Ptr(200),
	}
	w := RecyclableWorker{
		Cpu:  int32Ptr(32),
		Disk: f64Ptr(1400),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 12 {
		t.Fatalf("expected concurrency=12 ((1400-200)/100), got %d", got)
	}
}

// TestComputeConcurrency_MemSharedTighterThanCpu: shared model must not
// bypass the cross-dimension min — cpu can still be the bottleneck.
// Here shared math would give (60-15)/5 = 9, but cpu_per_task=8 on a
// 16-vCPU worker caps at 2.
func TestComputeConcurrency_MemSharedTighterThanCpu(t *testing.T) {
	r := Recruiter{
		CpuPerTask:          intPtr(8),
		MemoryPerTask:       f32Ptr(5),
		MemorySharedPerTask: f32Ptr(15),
	}
	w := RecyclableWorker{
		Cpu:    int32Ptr(16),
		Memory: f64Ptr(60),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 2 {
		t.Fatalf("expected concurrency=2 (cpu-bound, 16/8), got %d", got)
	}
}

// TestComputeConcurrency_MemSharedZeroBackCompat: unset shared (nil) is
// the legacy linear model — the pre-feature call sites read a NULL
// column into a nil pointer and MUST see today's behaviour unchanged.
func TestComputeConcurrency_MemSharedZeroBackCompat(t *testing.T) {
	r := Recruiter{
		CpuPerTask:    intPtr(1),
		MemoryPerTask: f32Ptr(20),
		// MemorySharedPerTask deliberately unset.
	}
	w := RecyclableWorker{
		Cpu:    int32Ptr(32),
		Memory: f64Ptr(60),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 3 {
		t.Fatalf("expected concurrency=3 (60/20, linear), got %d", got)
	}
}

// TestComputeConcurrency_SharedExceedsWorkerMem: pathological case
// where the shared block alone exceeds the flavor's mem. Ratio clamps
// to 0, which the outer floor promotes to 1. The recruiter returning 1
// (rather than 0) is deliberate — 0 would break the UI's
// "recruited-but-idle" story; the assignment layer's fitsWorker will
// reject the worker anyway, so no tasks actually land on it. Documents
// the intended graceful-degradation.
func TestComputeConcurrency_SharedExceedsWorkerMem(t *testing.T) {
	r := Recruiter{
		CpuPerTask:          intPtr(1),
		MemoryPerTask:       f32Ptr(5),
		MemorySharedPerTask: f32Ptr(100),
	}
	w := RecyclableWorker{
		Cpu:    int32Ptr(32),
		Memory: f64Ptr(60),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 1 {
		t.Fatalf("expected concurrency=1 (clamped from 0), got %d", got)
	}
}
