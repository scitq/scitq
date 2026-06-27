package client

import (
	"testing"
)

// TestGPUAllocator_BasicLifecycle covers the contract callers depend
// on: Allocate succeeds on a fresh allocator, Release frees the
// devices, double-Release is idempotent, and over-allocation fails
// without partial reservation (the "fit said yes but free count
// changed" race we never want to leak slots from).
func TestGPUAllocator_BasicLifecycle(t *testing.T) {
	a := NewGPUAllocator(4)
	if a.DeviceCount() != 4 {
		t.Fatalf("DeviceCount: want 4, got %d", a.DeviceCount())
	}
	if a.FreeCount() != 4 {
		t.Fatalf("FreeCount on fresh allocator: want 4, got %d", a.FreeCount())
	}

	// Two single-GPU tasks → both succeed, distinct devices, ascending.
	devs1, ok := a.Allocate(101, 1)
	if !ok || len(devs1) != 1 || devs1[0] != 0 {
		t.Fatalf("first Allocate(101, 1): want [0], got %v ok=%v", devs1, ok)
	}
	devs2, ok := a.Allocate(102, 1)
	if !ok || len(devs2) != 1 || devs2[0] != 1 {
		t.Fatalf("second Allocate(102, 1): want [1], got %v ok=%v", devs2, ok)
	}
	if a.FreeCount() != 2 {
		t.Fatalf("FreeCount after 2 allocs: want 2, got %d", a.FreeCount())
	}

	// Over-allocation: ask for 3, only 2 remain → fail, no partial.
	devs3, ok := a.Allocate(103, 3)
	if ok || devs3 != nil {
		t.Fatalf("over-allocation must fail without partial reservation; got %v ok=%v", devs3, ok)
	}
	if a.FreeCount() != 2 {
		t.Fatalf("FreeCount must be unchanged after failed Allocate; want 2, got %d", a.FreeCount())
	}

	// Release one task, the other stays.
	a.Release(101)
	if a.FreeCount() != 3 {
		t.Fatalf("FreeCount after Release(101): want 3, got %d", a.FreeCount())
	}

	// Double-release is harmless.
	a.Release(101)
	if a.FreeCount() != 3 {
		t.Fatalf("double Release must be idempotent; FreeCount: want 3, got %d", a.FreeCount())
	}

	// Release of a task that never allocated is also a no-op.
	a.Release(999)
	if a.FreeCount() != 3 {
		t.Fatalf("Release of unallocated task must be no-op; FreeCount: want 3, got %d", a.FreeCount())
	}
}

// TestGPUAllocator_MultiDeviceTask: a task asking for 2 devices gets
// adjacent indices (the ascending-ID pick policy), and a follow-up
// 1-device request lands on the lowest still-free slot.
func TestGPUAllocator_MultiDeviceTask(t *testing.T) {
	a := NewGPUAllocator(4)
	devs, ok := a.Allocate(201, 2)
	if !ok || len(devs) != 2 || devs[0] != 0 || devs[1] != 1 {
		t.Fatalf("Allocate(201, 2): want [0 1], got %v ok=%v", devs, ok)
	}
	devs, ok = a.Allocate(202, 1)
	if !ok || len(devs) != 1 || devs[0] != 2 {
		t.Fatalf("Allocate(202, 1): want [2], got %v ok=%v", devs, ok)
	}
	devs, ok = a.Allocate(203, 1)
	if !ok || len(devs) != 1 || devs[0] != 3 {
		t.Fatalf("Allocate(203, 1): want [3], got %v ok=%v", devs, ok)
	}
	// Pool exhausted — next allocate fails.
	if _, ok := a.Allocate(204, 1); ok {
		t.Fatalf("Allocate on exhausted pool must fail")
	}

	// After releasing the 2-device task, the freed indices are
	// reusable in ID order.
	a.Release(201)
	devs, ok = a.Allocate(205, 2)
	if !ok || len(devs) != 2 || devs[0] != 0 || devs[1] != 1 {
		t.Fatalf("re-Allocate after Release(201): want [0 1], got %v ok=%v", devs, ok)
	}
}

// TestGPUAllocator_NilAndZero: nil / zero-count allocator is safe
// (used on CPU-only workers so callers can hold a non-nil reference
// unconditionally).
func TestGPUAllocator_NilAndZero(t *testing.T) {
	var a *GPUAllocator
	if a.DeviceCount() != 0 {
		t.Fatalf("nil allocator DeviceCount: want 0, got %d", a.DeviceCount())
	}
	if a.FreeCount() != 0 {
		t.Fatalf("nil allocator FreeCount: want 0, got %d", a.FreeCount())
	}
	if devs, ok := a.Allocate(1, 1); ok || devs != nil {
		t.Fatalf("nil allocator Allocate must fail; got %v ok=%v", devs, ok)
	}
	a.Release(1) // must not panic

	z := NewGPUAllocator(0)
	if z.DeviceCount() != 0 {
		t.Fatalf("zero allocator DeviceCount: want 0, got %d", z.DeviceCount())
	}
	if devs, ok := z.Allocate(1, 1); ok || devs != nil {
		t.Fatalf("zero allocator Allocate must fail; got %v ok=%v", devs, ok)
	}
}

// TestVisibleDevicesEnv: CUDA_VISIBLE_DEVICES formatting — empty
// for no devices, comma-joined for many. The exact format matters
// because both the CUDA runtime and the operator's bash branches
// (`if [ -n "$CUDA_VISIBLE_DEVICES" ]`) read it.
func TestVisibleDevicesEnv(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{}, ""},
		{[]int{0}, "0"},
		{[]int{0, 1}, "0,1"},
		{[]int{2, 7, 3}, "2,7,3"}, // VisibleDevicesEnv preserves caller-given order
	}
	for _, c := range cases {
		got := VisibleDevicesEnv(c.in)
		if got != c.want {
			t.Errorf("VisibleDevicesEnv(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}
