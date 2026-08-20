package iothrottle

import (
	"testing"
	"time"
)

// feed drives n samples of the given iowait% into the throttle,
// advancing lastActionAt each iteration so growth spreads don't force
// artificial sleeps in the test.
func feed(t *Throttle, iowait float32, n int) {
	for range n {
		t.Sample(iowait)
	}
}

// advance rewinds lastActionAt so the next GREEN sample is eligible to
// grow, without waiting real wall-clock time.
func advance(t *Throttle) {
	t.mu.Lock()
	t.lastActionAt = time.Now().Add(-2 * MaxGrowSpread)
	t.mu.Unlock()
}

func TestStartsAtOneAndGrowsOnGreen(t *testing.T) {
	th := New(4)
	if got := th.Effective(); got != 1 {
		t.Fatalf("initial effective = %d, want 1", got)
	}
	// Feed the smoothing window with GREEN samples, then rewind
	// lastActionAt and take one more sample to trigger a grow.
	feed(th, 5, SmoothingWindow)
	advance(th)
	th.Sample(5)
	if got := th.Effective(); got != 2 {
		t.Fatalf("after grow, effective = %d, want 2", got)
	}
}

func TestThrottleOnSustainedRed(t *testing.T) {
	th := New(8)
	// Grow to 4 first — a throttle from 1 has nothing to do.
	feed(th, 5, SmoothingWindow)
	for range 3 {
		advance(th)
		th.Sample(5)
	}
	if got := th.Effective(); got != 4 {
		t.Fatalf("pre-throttle setup: effective = %d, want 4", got)
	}
	// Prime the window with RED. The smoothed signal has to cross
	// the RED threshold and the streak count RedStreakToThrottle
	// times before -1 fires.
	feed(th, 90, SmoothingWindow)
	// SmoothingWindow=5, RedStreakToThrottle=3. After the
	// window-priming loop above, the smoothed value already sits at
	// 90 and the streak counter has been advancing. We may already
	// be throttled; check the invariant instead of a precise value.
	if got := th.Effective(); got >= 4 {
		t.Fatalf("after RED streak, effective = %d, want < 4", got)
	}
	// A throttle event must have stamped lastThrottleAt.
	_, _, last := th.State()
	if last == 0 {
		t.Fatalf("lastThrottleAt not set after throttle")
	}
}

func TestNeverBelowOne(t *testing.T) {
	th := New(4)
	// Sustained RED for a long time — effective must floor at 1.
	feed(th, 95, SmoothingWindow*20)
	if got := th.Effective(); got != 1 {
		t.Fatalf("under sustained RED, effective = %d, want 1 (floor)", got)
	}
}

func TestNeverAboveCeiling(t *testing.T) {
	th := New(2)
	feed(th, 5, SmoothingWindow)
	for range 20 {
		advance(th)
		th.Sample(5)
	}
	if got := th.Effective(); got != 2 {
		t.Fatalf("under sustained GREEN, effective = %d, want 2 (ceiling)", got)
	}
}

func TestSetCeilingClampsDown(t *testing.T) {
	th := New(8)
	feed(th, 5, SmoothingWindow)
	for range 5 {
		advance(th)
		th.Sample(5)
	}
	// effective is somewhere between 2..6; assert it climbed at
	// all, then push the ceiling below and check the clamp.
	if got := th.Effective(); got < 2 {
		t.Fatalf("expected some growth before clamp test, got %d", got)
	}
	th.SetCeiling(1)
	if got := th.Effective(); got != 1 {
		t.Fatalf("after SetCeiling(1), effective = %d, want 1", got)
	}
}

func TestYellowHoldsWithoutGrowingOrThrottling(t *testing.T) {
	th := New(4)
	feed(th, 5, SmoothingWindow) // GREEN prime
	advance(th)
	th.Sample(5) // grow to 2
	if got := th.Effective(); got != 2 {
		t.Fatalf("pre-yellow: effective = %d, want 2", got)
	}
	// Feed YELLOW samples — should neither grow nor throttle.
	feed(th, 50, SmoothingWindow*3)
	if got := th.Effective(); got != 2 {
		t.Fatalf("under sustained YELLOW, effective = %d, want 2 (hold)", got)
	}
}
