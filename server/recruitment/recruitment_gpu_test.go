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

// Regression for the fresh-deploy branch: the RecyclableWorker built
// inline at server/recruitment/recruitment.go:~1053 must include
// GpuCount, otherwise the GPU ratio silently drops out of the min and
// single-GPU flavors end up at cpu-derived concurrency (2+ tasks on
// one GPU, contended). This test simulates the fresh-deploy path by
// constructing a RecyclableWorker with ONLY the fields the fresh-
// deploy site populates from a FlavorDetail (Cpu, Memory, Disk,
// GpuCount) — nothing else set — and asserts the GPU ratio caps the
// result. If a future refactor drops GpuCount from that struct
// literal again, this test fails.
func TestComputeConcurrency_FreshDeploy_GpuRatioApplied(t *testing.T) {
	// NC16_T4: 16 CPU / 110 GB / 176 GB disk / 1 GPU.
	// bin-step recruiter used cpu_per_task=4, mem_per_task=24,
	// gpu_per_task=1 — expected concurrency = min(4, ~4.5, 1) = 1.
	// With GpuCount missing, the min would collapse to floor(min(4,4.5))
	// = 4 (then clamped by concurrency_max=2 on the real recruiter,
	// which is the concurrency=2 bug that hit workflow 3190).
	r := Recruiter{
		CpuPerTask:    intPtr(4),
		MemoryPerTask: f32Ptr(24),
		DiskPerTask:   f32Ptr(100),
		GpuPerTask:    intPtr(1),
	}
	cpu := int32(16)
	mem := float64(110)
	disk := float64(176)
	gpuCount := int32(1)
	w := RecyclableWorker{
		Cpu:      &cpu,
		Memory:   &mem,
		Disk:     &disk,
		GpuCount: &gpuCount,
	}
	if got := computeConcurrencyForRecruiterWorker(r, w); got != 1 {
		t.Fatalf("expected concurrency=1 (single-GPU flavor gated by gpu ratio), got %d — regression: fresh-deploy path dropped GpuCount", got)
	}
}
