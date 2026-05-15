package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// NumaNode describes one NUMA node on the host. CPUList is kept as the
// raw cpulist string from /sys (e.g. "0-5,12-17") so we can feed it
// straight into docker `--cpuset-cpus` without parsing.
type NumaNode struct {
	ID      int
	CPUList string // ready for --cpuset-cpus
}

// NumaTopology is the host's NUMA layout. nil = NUMA-unaware host (sysfs
// reported one or zero nodes, or wasn't readable). Treat a nil topology
// as "the worker cannot honour task.numa" — pinning becomes a no-op
// with a single warning logged at dispatch time.
type NumaTopology struct {
	Nodes []NumaNode
}

// DiscoverNumaTopology reads /sys/devices/system/node/node*/cpulist
// once at startup. Returns nil (not an error) on a host with no NUMA
// information — the worker can still run, just without binding.
func DiscoverNumaTopology() *NumaTopology {
	root := "/sys/devices/system/node"
	entries, err := os.ReadDir(root)
	if err != nil {
		// /sys not mounted (e.g. macOS dev box), or no NUMA support.
		// Silent: a non-Linux dev environment is the common case and
		// doesn't deserve a warning every boot.
		return nil
	}
	var nodes []NumaNode
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "node") {
			continue
		}
		idStr := strings.TrimPrefix(name, "node")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		cpulistPath := filepath.Join(root, name, "cpulist")
		data, err := os.ReadFile(cpulistPath)
		if err != nil {
			continue
		}
		cpus := strings.TrimSpace(string(data))
		if cpus == "" {
			// Node is offline or has no CPUs (memory-only nodes exist
			// on some systems). Skip — cpuset binding without CPUs is
			// meaningless.
			continue
		}
		nodes = append(nodes, NumaNode{ID: id, CPUList: cpus})
	}
	if len(nodes) <= 1 {
		// Single-node hosts have no real NUMA dimension. Returning nil
		// here makes the rest of the code treat task.numa requests on
		// such hosts as no-ops, which is what we want.
		return nil
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return &NumaTopology{Nodes: nodes}
}

// NumaAllocator tracks which NUMA node currently hosts which task. The
// map is purely in-memory: a worker restart rebuilds it from
// `docker inspect` of still-running scitq containers (see
// RebuildFromDocker). All methods are concurrency-safe — both the
// fetcher goroutine and the executer goroutine touch this.
type NumaAllocator struct {
	mu       sync.Mutex
	topology *NumaTopology
	slot     map[int]int32 // node ID → task ID (0 = free)
}

func NewNumaAllocator(topo *NumaTopology) *NumaAllocator {
	a := &NumaAllocator{topology: topo, slot: map[int]int32{}}
	if topo != nil {
		for _, n := range topo.Nodes {
			a.slot[n.ID] = 0
		}
	}
	return a
}

// NumaNodeCount returns the number of NUMA nodes the worker can pin to,
// or 0 if the host is NUMA-unaware. Callers use this to derive
// concurrency from task_spec.numa: concurrency = floor(node_count / numa).
func (a *NumaAllocator) NumaNodeCount() int {
	if a == nil || a.topology == nil {
		return 0
	}
	return len(a.topology.Nodes)
}

// Allocation describes what NumaAllocator.Allocate handed out. Fields
// map directly onto docker flags (Cpuset → --cpuset-cpus, Memset →
// --cpuset-mems) plus the cpu count so the executor can override the
// per-task $CPU env var to match.
type Allocation struct {
	Cpuset   string // ready for --cpuset-cpus
	Memset   string // ready for --cpuset-mems
	CPUCount int    // number of CPUs in Cpuset
	Nodes    []int  // node IDs (for logging / debugging)
}

// Allocate finds `n` free NUMA nodes and binds them to `taskID`. Returns
// the docker --cpuset-cpus / --cpuset-mems values plus the CPU count,
// or (zero, false) if allocation isn't possible (n exceeds the
// topology, no free slots, or the host has no NUMA topology at all).
// Caller is responsible for releasing the slots via Release on task
// completion.
func (a *NumaAllocator) Allocate(taskID int32, n int) (Allocation, bool) {
	if a == nil || a.topology == nil || n < 1 {
		return Allocation{}, false
	}
	if n > len(a.topology.Nodes) {
		return Allocation{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// Pick the first N free nodes in ID order. Adjacency is preferred
	// (cross-die memory traffic is cheaper between neighbouring dies on
	// Naples/Rome than across the socket), and ID order is a decent
	// proxy — kernels number nodes within a socket consecutively.
	var picked []NumaNode
	for _, node := range a.topology.Nodes {
		if a.slot[node.ID] == 0 {
			picked = append(picked, node)
			if len(picked) == n {
				break
			}
		}
	}
	if len(picked) < n {
		return Allocation{}, false
	}

	cpus := make([]string, 0, n)
	mems := make([]string, 0, n)
	nodeIDs := make([]int, 0, n)
	cpuCount := 0
	for _, node := range picked {
		a.slot[node.ID] = taskID
		cpus = append(cpus, node.CPUList)
		mems = append(mems, strconv.Itoa(node.ID))
		nodeIDs = append(nodeIDs, node.ID)
		cpuCount += countCPUsInList(node.CPUList)
	}
	return Allocation{
		Cpuset:   strings.Join(cpus, ","),
		Memset:   strings.Join(mems, ","),
		CPUCount: cpuCount,
		Nodes:    nodeIDs,
	}, true
}

// countCPUsInList counts CPU indices in a Linux cpulist string like
// "0-5,12-17" → 12. Robust to whitespace and out-of-order ranges.
func countCPUsInList(s string) int {
	n := 0
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			lo, errLo := strconv.Atoi(strings.TrimSpace(bounds[0]))
			hi, errHi := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if errLo == nil && errHi == nil && hi >= lo {
				n += (hi - lo + 1)
			}
		} else {
			if _, err := strconv.Atoi(part); err == nil {
				n++
			}
		}
	}
	return n
}

// Release frees every NUMA node currently bound to taskID. No-op if the
// task wasn't bound (host had no topology, or allocation failed and
// we ran unbound).
func (a *NumaAllocator) Release(taskID int32) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, owner := range a.slot {
		if owner == taskID {
			a.slot[id] = 0
		}
	}
}

// MarkAllocated forces nodes into the bound state. Used by restart
// recovery: when the worker comes back up, it consults docker for
// containers still running and records their NUMA assignment so the
// allocator doesn't hand the same slots out again.
func (a *NumaAllocator) MarkAllocated(taskID int32, nodeIDs []int) {
	if a == nil || a.topology == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range nodeIDs {
		if _, ok := a.slot[id]; ok {
			a.slot[id] = taskID
		} else {
			log.Printf("⚠️ NUMA recovery: task %d claims node %d which is not in the host topology — skipping", taskID, id)
		}
	}
}

// RebuildFromDocker scans this worker's still-running scitq containers
// and re-claims any NUMA slots they hold. Used on worker startup so a
// process restart in the middle of a task doesn't release node IDs that
// are still genuinely in use by a running container.
//
// Container name format (set in executeTask): "scitq-<workerName>-task-<id>".
// We read HostConfig.CpusetMems and translate it back into node IDs.
func (a *NumaAllocator) RebuildFromDocker(ctx context.Context, workerName string) {
	if a == nil || a.topology == nil {
		return
	}
	// `docker ps -q` is cheap and avoids parsing free-form `ps` output.
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "name=scitq-"+workerName+"-task-",
	).Output()
	if err != nil {
		log.Printf("⚠️ NUMA recovery: docker ps failed: %v — proceeding with empty slot map", err)
		return
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return
	}
	args := append([]string{"inspect", "--format",
		`{{.Name}}|{{if .HostConfig}}{{.HostConfig.CpusetMems}}{{end}}`,
	}, ids...)
	out, err = exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		log.Printf("⚠️ NUMA recovery: docker inspect failed: %v", err)
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimPrefix(parts[0], "/")
		mems := parts[1]
		if mems == "" {
			continue // container has no cpuset-mems = not NUMA-bound
		}
		// Name shape: scitq-<workerName>-task-<id>
		idx := strings.LastIndex(name, "-task-")
		if idx < 0 {
			continue
		}
		taskID64, err := strconv.ParseInt(name[idx+len("-task-"):], 10, 32)
		if err != nil {
			continue
		}
		nodeIDs, err := ParseCpusetMems(mems)
		if err != nil {
			log.Printf("⚠️ NUMA recovery: bad cpuset-mems %q on %s: %v", mems, name, err)
			continue
		}
		if len(nodeIDs) == 0 {
			continue
		}
		log.Printf("🧩 NUMA recovery: task %d still holds nodes %v", int32(taskID64), nodeIDs)
		a.MarkAllocated(int32(taskID64), nodeIDs)
	}
}

// ParseCpusetMems parses docker's --cpuset-mems output ("0,2,3") back
// into a slice of node IDs. Used by restart recovery.
func ParseCpusetMems(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var ids []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			// Range form, e.g. "0-3"
			bounds := strings.SplitN(part, "-", 2)
			lo, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, fmt.Errorf("invalid cpuset-mems range: %q", part)
			}
			hi, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, fmt.Errorf("invalid cpuset-mems range: %q", part)
			}
			for i := lo; i <= hi; i++ {
				ids = append(ids, i)
			}
		} else {
			id, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid cpuset-mems id: %q", part)
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}
