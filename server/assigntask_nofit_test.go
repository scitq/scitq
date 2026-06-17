package server

import (
	"math"
	"strings"
	"testing"
	"time"
)

// TestWhyDoesNotFit checks each dimension's failure message and the
// NaN-bypass semantics (legacy tasks without min_*, or legacy workers
// without flavor rows, always "fit" — we only emit the specific
// dimension when both sides have a real number).
func TestWhyDoesNotFit(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		name string
		req  taskMins
		caps workerCaps
		want string
	}{
		{
			name: "cpu short",
			req:  taskMins{cpu: 16, mem: 8, disk: 100},
			caps: workerCaps{cpu: 8, mem: 16, disk: 200},
			want: "cpu=16",
		},
		{
			name: "mem short — production scenario (bigbrother attached to hermes step)",
			req:  taskMins{cpu: 8, mem: 20, disk: nan},
			caps: workerCaps{cpu: 8, mem: 15.6, disk: 1633},
			want: "mem=20",
		},
		{
			name: "disk short",
			req:  taskMins{cpu: 4, mem: 8, disk: 500},
			caps: workerCaps{cpu: 8, mem: 16, disk: 100},
			want: "disk=500",
		},
		{
			name: "all NaN — generic fallback",
			req:  taskMins{cpu: nan, mem: nan, disk: nan},
			caps: workerCaps{cpu: nan, mem: nan, disk: nan},
			want: "resource curves",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := whyDoesNotFit(tc.req, tc.caps)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("whyDoesNotFit returned %q, expected substring %q", got, tc.want)
			}
		})
	}
}

// TestRecordNoFit_Throttle exercises the per-worker throttle memo
// state machine. The four important cases:
//   1. First-time observation → emit (true).
//   2. Same condition within the cooldown window → suppress (false).
//   3. Step change while still no-fit → emit (operator moved the
//      worker; the new mismatch may be different).
//   4. Cooldown elapsed → emit again (reminder so a persistent
//      mismatch doesn't go silent for hours after the first warning).
// Step change without cooldown is the subtle one: an operator who
// detaches and re-attaches a worker should see a fresh warning
// instead of being shadowed by the prior memo.
func TestRecordNoFit_Throttle(t *testing.T) {
	s := &taskQueueServer{}
	t0 := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

	// 1. First observation → emit.
	if !s.recordNoFit(5218, 73865, "mem=20 > 15.6", t0) {
		t.Fatal("first observation should emit")
	}
	// 2. Same workerID/stepID/reason, within cooldown → suppress.
	if s.recordNoFit(5218, 73865, "mem=20 > 15.6", t0.Add(1*time.Minute)) {
		t.Fatal("repeat within cooldown should suppress")
	}
	if s.recordNoFit(5218, 73865, "mem=20 > 15.6", t0.Add(noFitRewarnInterval-time.Second)) {
		t.Fatal("repeat just before cooldown ends should suppress")
	}

	// 3a. Step changes (operator moved worker) → emit fresh warning.
	if !s.recordNoFit(5218, 73870, "mem=20 > 15.6", t0.Add(2*time.Minute)) {
		t.Fatal("step change should emit")
	}
	// 3b. Reason changes (different bottleneck after recruiter
	// adjustment, say cpu is now the short dimension) → emit.
	if !s.recordNoFit(5218, 73870, "cpu=16 > 8", t0.Add(3*time.Minute)) {
		t.Fatal("reason change should emit")
	}

	// 4. Cooldown elapsed with same (step, reason) → emit again.
	stable := t0.Add(10 * time.Minute)
	if !s.recordNoFit(5218, 73900, "mem=64 > 32", stable) {
		t.Fatal("initial observation after stable change should emit")
	}
	if s.recordNoFit(5218, 73900, "mem=64 > 32", stable.Add(5*time.Minute)) {
		t.Fatal("repeat within cooldown should suppress")
	}
	if !s.recordNoFit(5218, 73900, "mem=64 > 32", stable.Add(noFitRewarnInterval+time.Second)) {
		t.Fatal("repeat after cooldown should emit again")
	}
}

// TestRecordNoFit_ClearResets verifies that clearNoFitMemo properly
// drops the memo so a subsequent no-fit emits a fresh warning. This
// is what the assignment loop calls when a worker successfully
// picks up a task — without it, a worker that recovers and then
// regresses (e.g. the operator changed the resource curve) would
// wait out the cooldown before the next warning fired.
func TestRecordNoFit_ClearResets(t *testing.T) {
	s := &taskQueueServer{}
	t0 := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

	if !s.recordNoFit(5218, 73865, "mem=20 > 15.6", t0) {
		t.Fatal("first observation should emit")
	}
	if s.recordNoFit(5218, 73865, "mem=20 > 15.6", t0.Add(1*time.Minute)) {
		t.Fatal("repeat within cooldown should suppress")
	}
	s.clearNoFitMemo(5218)
	if !s.recordNoFit(5218, 73865, "mem=20 > 15.6", t0.Add(2*time.Minute)) {
		t.Fatal("after clear, next observation should emit even within original cooldown window")
	}
}

// TestRecordNoFit_PerWorker confirms two different workers don't
// share throttle state — sync.Map keyed by workerID, but worth
// pinning so a refactor that accidentally widens the key (e.g. to a
// global counter) is caught by a test rather than in production.
func TestRecordNoFit_PerWorker(t *testing.T) {
	s := &taskQueueServer{}
	t0 := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)

	if !s.recordNoFit(5218, 73865, "mem=20", t0) {
		t.Fatal("worker A first observation should emit")
	}
	if !s.recordNoFit(5343, 73865, "mem=20", t0) {
		t.Fatal("worker B first observation should emit independently")
	}
	if s.recordNoFit(5218, 73865, "mem=20", t0.Add(time.Second)) {
		t.Fatal("worker A repeat should suppress")
	}
	if s.recordNoFit(5343, 73865, "mem=20", t0.Add(time.Second)) {
		t.Fatal("worker B repeat should suppress independently")
	}
}
