package recruitment

import "testing"

func intPtr(v int) *int        { return &v }
func int32Ptr(v int32) *int32  { return &v }
func f64Ptr(v float64) *float64 { return &v }
func f32Ptr(v float32) *float32 { return &v }

// computeConcurrencyForRecruiterWorker must size a GPU-bound step's
// concurrency from flavor.gpu_count / gpu_per_task — mirroring how
// cpu_per_task and memory_per_task already cap concurrency. With a
// 4-GPU flavor and gpu_per_task=1, the worker should run 4 tasks in
// parallel even though cpu/mem could carry more.
func TestComputeConcurrency_GpuRatioCaps(t *testing.T) {
	r := Recruiter{
		GpuPerTask:    intPtr(1),
		CpuPerTask:    intPtr(1),       // cpu would allow 32
		MemoryPerTask: f32Ptr(1),       // mem would allow 256
	}
	w := RecyclableWorker{
		Cpu:      int32Ptr(32),
		Memory:   f64Ptr(256),
		GpuCount: int32Ptr(4),
	}
	got := computeConcurrencyForRecruiterWorker(r, w)
	if got != 4 {
		t.Fatalf("expected concurrency=4 (4 GPUs / 1 gpu_per_task), got %d", got)
	}
}

// gpu_per_task=2 on a 4-GPU flavor yields 2 concurrent tasks.
func TestComputeConcurrency_GpuRatio_TwoPerTask(t *testing.T) {
	r := Recruiter{
		GpuPerTask:    intPtr(2),
		CpuPerTask:    intPtr(1),
		MemoryPerTask: f32Ptr(1),
	}
	w := RecyclableWorker{
		Cpu:      int32Ptr(32),
		Memory:   f64Ptr(256),
		GpuCount: int32Ptr(4),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 2 {
		t.Fatalf("expected concurrency=2 (4 / 2), got %d", got)
	}
}

// A tighter dimension still wins: cpu_per_task=8 on a 16-vCPU 4-GPU
// flavor caps at 2, even though gpu_per_task=1 alone would allow 4.
// Locks in the min-of-ratios rule.
func TestComputeConcurrency_MinOfRatios_CpuWins(t *testing.T) {
	r := Recruiter{
		GpuPerTask:    intPtr(1),
		CpuPerTask:    intPtr(8),
		MemoryPerTask: f32Ptr(1),
	}
	w := RecyclableWorker{
		Cpu:      int32Ptr(16),
		Memory:   f64Ptr(256),
		GpuCount: int32Ptr(4),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 2 {
		t.Fatalf("expected concurrency=2 (cpu ratio wins over gpu ratio), got %d", got)
	}
}

// When the recruiter declares no gpu_per_task, the GPU ratio is
// silent — concurrency is dictated by cpu/mem alone. Regression
// guard for CPU-only recruiters on GPU-capable hosts.
func TestComputeConcurrency_NoGpuPerTask_GpuIgnored(t *testing.T) {
	r := Recruiter{
		CpuPerTask:    intPtr(4),
		MemoryPerTask: f32Ptr(8),
	}
	w := RecyclableWorker{
		Cpu:      int32Ptr(16),
		Memory:   f64Ptr(64),
		GpuCount: int32Ptr(4),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 4 {
		t.Fatalf("expected concurrency=4 (cpu 16/4), got %d", got)
	}
}

// concurrency_max clamps the GPU-derived value down, matching the
// behaviour cpu_per_task callers already rely on.
func TestComputeConcurrency_GpuRatio_RespectsMaxClamp(t *testing.T) {
	r := Recruiter{
		GpuPerTask:     intPtr(1),
		ConcurrencyMax: intPtr(2),
	}
	w := RecyclableWorker{
		GpuCount: int32Ptr(8),
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 2 {
		t.Fatalf("expected concurrency clamped to 2, got %d", got)
	}
}
