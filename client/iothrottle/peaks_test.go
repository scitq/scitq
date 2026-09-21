package iothrottle

import (
	"math"
	"testing"
)

// TestPeakTracker_TracksMaxAndResets: after a run of Observe calls, the
// Drain call returns the max of each dimension and resets. The reset
// is what makes each ping's peak_* fields reflect the interval since
// the PREVIOUS ping — without it, the max would grow monotonically and
// every ping would report the same all-time high.
func TestPeakTracker_TracksMaxAndResets(t *testing.T) {
	p := NewPeakTracker()

	// Interval 1: rising values.
	p.Observe(10, 30, 5)
	p.Observe(20, 30, 15)
	p.Observe(15, 40, 10)

	cpu, mem, io, ok := p.Drain()
	if !ok {
		t.Fatal("Drain should report hasData=true after Observe")
	}
	if cpu != 20 || mem != 40 || io != 15 {
		t.Fatalf("expected max (20, 40, 15), got (%v, %v, %v)", cpu, mem, io)
	}

	// Post-drain: no samples yet in the new interval → hasData=false.
	// The ping loop uses this to leave peak_* unset rather than
	// writing a misleading 0.
	_, _, _, ok = p.Drain()
	if ok {
		t.Fatal("Drain should report hasData=false immediately after reset")
	}

	// Interval 2: distinct maxes; nothing carries over from interval 1.
	p.Observe(50, 10, 3)
	cpu, mem, io, ok = p.Drain()
	if !ok || cpu != 50 || mem != 10 || io != 3 {
		t.Fatalf("interval-2 drain: got hasData=%v (%v, %v, %v); want (50, 10, 3)",
			ok, cpu, mem, io)
	}
}

// TestPeakTracker_ClampsNegative: sampler occasionally sees negative
// deltas around counter rollovers. Peaks must not carry those forward.
func TestPeakTracker_ClampsNegative(t *testing.T) {
	p := NewPeakTracker()
	p.Observe(-5, -1, -0.001)
	p.Observe(2, 3, 4)
	cpu, mem, io, ok := p.Drain()
	if !ok {
		t.Fatal("Drain should report hasData=true")
	}
	if cpu != 2 || mem != 3 || io != 4 {
		t.Fatalf("negative samples should clamp to 0; got (%v, %v, %v)", cpu, mem, io)
	}
}

// TestPeakTracker_IgnoresNaN: comparison against NaN is always false,
// so a stray NaN in any dimension would stick as the "max" forever.
// The guard drops the entire Observe call — including the non-NaN
// dimensions — so an isolated bad reading doesn't need per-field
// bookkeeping. Follow-up valid samples still land as expected.
func TestPeakTracker_IgnoresNaN(t *testing.T) {
	p := NewPeakTracker()
	p.Observe(float32(math.NaN()), 42, 5)
	p.Observe(10, 20, 3)
	cpu, mem, io, ok := p.Drain()
	if !ok {
		t.Fatal("Drain should report hasData=true after a valid Observe")
	}
	// The NaN-carrying Observe is rejected as a whole, so mem=42 and
	// io=5 from it do NOT influence the max — only the second call's
	// (10, 20, 3) does.
	if cpu != 10 || mem != 20 || io != 3 {
		t.Fatalf("NaN sample should be dropped as a whole; got (%v, %v, %v)", cpu, mem, io)
	}
}

// TestPeakTracker_DrainWithoutObserve: a tracker with no samples yet
// (e.g., first ping after boot before the sampler has ticked) reports
// hasData=false so the caller writes no peak_* fields.
func TestPeakTracker_DrainWithoutObserve(t *testing.T) {
	p := NewPeakTracker()
	cpu, mem, io, ok := p.Drain()
	if ok {
		t.Fatal("fresh tracker should report hasData=false")
	}
	if cpu != 0 || mem != 0 || io != 0 {
		t.Fatalf("fresh tracker should report zero values; got (%v, %v, %v)", cpu, mem, io)
	}
}
