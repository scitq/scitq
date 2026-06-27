package client

import (
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DiscoverGPUCount returns the number of NVIDIA GPUs visible on the
// host (counted via `nvidia-smi -L`). Returns 0 when nvidia-smi is
// missing or fails — the typical state on CPU-only flavors. Logs the
// outcome at INFO level so it lands in worker stdout for ops grep.
//
// Why nvidia-smi instead of reading /dev/nvidia*: nvidia-smi works
// regardless of how the device files are named (some images use
// /dev/nvidiactl + /dev/nvidia-uvm without per-GPU node files), and
// the line count IS the gospel definition Azure / the kernel agrees
// on. Cheap (< 100 ms) and only runs once at startup.
func DiscoverGPUCount() int {
	out, err := exec.Command("nvidia-smi", "-L").Output()
	if err != nil {
		log.Printf("🧮 GPU discovery: nvidia-smi -L failed (%v) — assuming 0 GPUs", err)
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "GPU ") {
			n++
		}
	}
	log.Printf("🧮 GPU discovery: nvidia-smi reports %d device(s)", n)
	return n
}

// GPUAllocator partitions a worker's GPU devices into per-task slots
// driven by task_spec.gpu. Mirrors NumaAllocator's shape: in-memory
// only, concurrency-safe, slots keyed by device index 0..N-1. A worker
// restart re-zeros the map — same risk profile as the NUMA allocator
// (a crashed worker could orphan device assignments until the next
// restart). For typical scitq deploys this is fine because the worker
// is bounced when the binary or the host driver changes.
//
// The allocator's contract:
//   Allocate(taskID, n) reserves N free devices for taskID and
//     returns their indices. Returns (nil, false) if there aren't N
//     free devices, or if the worker has no GPUs at all. Caller is
//     responsible for calling Release(taskID) when the task ends.
//   Release(taskID) frees every device that was reserved for taskID.
//     Idempotent — safe to call on tasks that never got an allocation
//     (e.g. CPU-only tasks).
//   DeviceCount() returns the worker's total GPU count (the size of
//     the slot map). 0 on CPU-only workers — callers use it to gate
//     whether to attempt allocation at all.
type GPUAllocator struct {
	mu    sync.Mutex
	slot  map[int]int32 // device index → task ID (0 = free)
	count int
}

// NewGPUAllocator builds an allocator for a worker with `count` GPU
// devices (0..count-1). count=0 yields a no-op allocator that always
// returns (nil, false) on Allocate — used on CPU-only workers so
// callers can hold a non-nil reference unconditionally.
func NewGPUAllocator(count int) *GPUAllocator {
	a := &GPUAllocator{slot: map[int]int32{}, count: count}
	for i := 0; i < count; i++ {
		a.slot[i] = 0
	}
	return a
}

// DeviceCount returns the worker's total GPU count.
func (a *GPUAllocator) DeviceCount() int {
	if a == nil {
		return 0
	}
	return a.count
}

// Allocate finds `n` free GPU devices and binds them to `taskID`. The
// returned slice is the list of device indices (ascending) the task
// owns. Caller MUST call Release(taskID) on completion to free them.
// Returns (nil, false) if n exceeds the worker's GPU count, no free
// slots, or n < 1.
func (a *GPUAllocator) Allocate(taskID int32, n int) ([]int, bool) {
	if a == nil || a.count == 0 || n < 1 || n > a.count {
		return nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// Pick the first n free devices in ID order. Affinity isn't a
	// concern at this layer — most NVLink topologies are linear and
	// the CUDA scheduler handles peer routing — but ID-order picks
	// give deterministic CUDA_VISIBLE_DEVICES strings which makes
	// debug logs greppable.
	devs := make([]int, 0, n)
	ids := make([]int, 0, a.count)
	for id := range a.slot {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if a.slot[id] == 0 {
			devs = append(devs, id)
			if len(devs) == n {
				break
			}
		}
	}
	if len(devs) < n {
		return nil, false
	}
	for _, id := range devs {
		a.slot[id] = taskID
	}
	return devs, true
}

// Release frees every device currently bound to `taskID`. Safe to
// call on a task that never allocated (e.g. CPU-only tasks).
func (a *GPUAllocator) Release(taskID int32) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, t := range a.slot {
		if t == taskID {
			a.slot[id] = 0
		}
	}
}

// FreeCount returns the number of currently-free GPU devices. Used by
// the workerLoop's fetch-side capacity check to avoid pulling more
// GPU-needing tasks than the worker can host.
func (a *GPUAllocator) FreeCount() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	free := 0
	for _, t := range a.slot {
		if t == 0 {
			free++
		}
	}
	return free
}

// VisibleDevicesEnv formats a slice of device indices as the value
// for CUDA_VISIBLE_DEVICES (comma-separated, ascending). Returns the
// empty string for an empty slice.
func VisibleDevicesEnv(devs []int) string {
	if len(devs) == 0 {
		return ""
	}
	parts := make([]string, len(devs))
	for i, d := range devs {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

// String summarises the allocator state for logging.
func (a *GPUAllocator) String() string {
	if a == nil {
		return "GPUAllocator{nil}"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	parts := []string{}
	for id := 0; id < a.count; id++ {
		t, ok := a.slot[id]
		if !ok {
			continue
		}
		if t == 0 {
			parts = append(parts, fmt.Sprintf("[%d]free", id))
		} else {
			parts = append(parts, fmt.Sprintf("[%d]task=%d", id, t))
		}
	}
	return "GPUAllocator{" + strings.Join(parts, ", ") + "}"
}
