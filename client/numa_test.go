package client

import "testing"

func TestCountCPUsInList(t *testing.T) {
	cases := map[string]int{
		"0-5":          6,
		"0-5,12-17":    12,
		"0,2,4":        3,
		"0":            1,
		" 0 - 3 , 5 ":  5,
		"":             0,
		"garbage":      0,
		"0-5,garbage":  6,
	}
	for in, want := range cases {
		if got := countCPUsInList(in); got != want {
			t.Errorf("countCPUsInList(%q) = %d, want %d", in, got, want)
		}
	}
}

func fakeTopology() *NumaTopology {
	return &NumaTopology{Nodes: []NumaNode{
		{ID: 0, CPUList: "0-5"},
		{ID: 1, CPUList: "6-11"},
		{ID: 2, CPUList: "12-17"},
		{ID: 3, CPUList: "18-23"},
	}}
}

func TestAllocator_HappyPath(t *testing.T) {
	a := NewNumaAllocator(fakeTopology())

	alloc, ok := a.Allocate(100, 1)
	if !ok || alloc.CPUCount != 6 || alloc.Cpuset != "0-5" || alloc.Memset != "0" {
		t.Fatalf("first 1-node alloc unexpected: %+v ok=%v", alloc, ok)
	}
	alloc2, ok := a.Allocate(101, 2)
	if !ok || alloc2.CPUCount != 12 || alloc2.Cpuset != "6-11,12-17" || alloc2.Memset != "1,2" {
		t.Fatalf("2-node alloc unexpected: %+v ok=%v", alloc2, ok)
	}
	alloc3, ok := a.Allocate(102, 1)
	if !ok || alloc3.Memset != "3" {
		t.Fatalf("third alloc should pick node 3: %+v ok=%v", alloc3, ok)
	}
	if _, ok := a.Allocate(103, 1); ok {
		t.Fatalf("no slots left; allocate should have failed")
	}

	a.Release(101)
	alloc4, ok := a.Allocate(104, 1)
	if !ok || alloc4.Memset != "1" {
		t.Fatalf("after releasing 101, first free node is 1; got %+v ok=%v", alloc4, ok)
	}
}

func TestAllocator_NoTopology(t *testing.T) {
	a := NewNumaAllocator(nil)
	if _, ok := a.Allocate(1, 1); ok {
		t.Fatalf("nil topology must reject allocation")
	}
	if a.NumaNodeCount() != 0 {
		t.Fatalf("nil topology node count should be 0")
	}
	a.Release(1) // must not panic
}

func TestAllocator_TooManyNodesRequested(t *testing.T) {
	a := NewNumaAllocator(fakeTopology())
	if _, ok := a.Allocate(1, 5); ok {
		t.Fatalf("requesting more nodes than the topology has must fail")
	}
	// Topology is intact — should still serve normal requests after a refusal
	if _, ok := a.Allocate(1, 1); !ok {
		t.Fatalf("topology should still be allocatable after a failed oversize request")
	}
}

