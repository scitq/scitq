// Package iothrottle implements adaptive per-worker concurrency throttling
// based on continuously-sampled iowait%.
//
// The model in one sentence: IO cannot block a worker; it acts as a
// condition to increase concurrency. Every eligible worker runs at least
// one task; higher concurrency unlocks only when the storage subsystem
// tolerates it.
//
// Semantics of the three canonical task shapes (see the design thread in
// the CHANGELOG / MANTRA discussion for the reasoning):
//
//   - No-IO task: iowait% stays low → growth fires at the minimum spread
//     interval → reaches configured ceiling as fast as the physical fit
//     allows.
//
//   - Warmup task (bioinformatics norm — load big ref, then compute): a
//     new task launch spikes iowait during its warmup, growth waits for
//     iowait to fall back below YELLOW, launches next task; warmups
//     naturally stagger across the pool.
//
//   - Continuous-heavy task: iowait never calms → growth never fires →
//     concurrency stays at 1 (or wherever we started). Zero configuration
//     needed — the algorithm produces the correct behaviour on its own.
//
// The controller is intentionally stateless across restarts: a worker
// that reboots starts at effective=1 and re-converges. This keeps the
// design simple; memoization ("this flavor stabilized at N for this
// step") is deferred to a later iteration.
package iothrottle

import (
	"sync"
	"time"
)

// Tunables. Deliberately not exposed through config in this first
// iteration — we want to observe defaults across real workloads before
// giving operators knobs they'd have to guess at. The UI badge exposes
// the live effect so miscalibration is visible.
const (
	// RED: iowait% at which we start pushing back.
	RedThresholdPct = 70.0
	// YELLOW: iowait% above which we stop growing (hold current
	// concurrency). Below YELLOW is GREEN.
	YellowThresholdPct = 40.0

	// Number of consecutive smoothed-RED samples before we throttle.
	// The smoothing window already averages over SmoothingWindow
	// seconds, so a single smoothed-RED reading already implies
	// sustained pressure across roughly that whole window. Requiring
	// additional streak samples on top would just add latency to the
	// throttle without meaningful noise rejection.
	RedStreakToThrottle = 1

	// Minimum time between growth events. Even a completely IO-free
	// task shouldn't ramp faster than one +1 every 2s — that keeps the
	// probe/observe cycle honest on very fast machines.
	MinGrowSpread = 2 * time.Second
	// Ceiling on the doubling growSpread. Beyond ~60s of quiet the
	// signal is well-established; there's no benefit to waiting
	// longer.
	MaxGrowSpread = 60 * time.Second

	// Length of the rolling window (in samples) used to smooth iowait
	// for decision-making. With a 1Hz sampler this is a 5-second
	// window.
	SmoothingWindow = 5
)

// Throttle is the per-worker adaptive concurrency controller. It is
// safe for concurrent access.
type Throttle struct {
	mu sync.Mutex

	// configuredMax is the ceiling the operator (or the server) has
	// pushed. Effective concurrency never exceeds this. Updated via
	// SetCeiling.
	configuredMax int32
	// effective is the current advertised concurrency (1 ≤ effective
	// ≤ configuredMax).
	effective int32

	// growSpread is the minimum quiet interval before the next +1 can
	// fire. Doubles on each successful grow, resets on any throttle.
	growSpread time.Duration
	// lastActionAt is the wall-clock time of the most recent +1 (or
	// controller-start). It gates growth: the controller must observe
	// GREEN for at least growSpread since lastActionAt.
	lastActionAt time.Time
	// lastThrottleAt is the wall-clock time of the most recent -1
	// event. Exposed to the UI so it can blink the IO badge for ~30s
	// after each event.
	lastThrottleAt time.Time

	// redStreak counts consecutive samples above RedThresholdPct;
	// resets to zero on any sample below YELLOW (i.e., GREEN). At
	// RedStreakToThrottle we fire a -1.
	redStreak int
	// window is the ring buffer of the last SmoothingWindow raw
	// iowait samples. smoothed() averages them.
	window    []float32
	windowIdx int
	windowLen int

	// smoothedIOWait caches the most recent smoothed value so
	// GetState can return it cheaply.
	smoothedIOWait float32
}

// New builds a fresh Throttle. The initial ceiling is what the server
// (or the operator) has configured for this worker; the initial
// effective is min(1, configuredMax) — we always start conservative
// and let the sampler ramp up.
func New(configuredMax int32) *Throttle {
	if configuredMax < 1 {
		configuredMax = 1
	}
	t := &Throttle{
		configuredMax: configuredMax,
		effective:     1,
		growSpread:    MinGrowSpread,
		lastActionAt:  time.Now(),
		window:        make([]float32, SmoothingWindow),
	}
	if configuredMax < t.effective {
		t.effective = configuredMax
	}
	return t
}

// SetCeiling updates the configured maximum. If effective exceeds the
// new ceiling it is clamped down immediately.
func (t *Throttle) SetCeiling(newMax int32) {
	if newMax < 1 {
		newMax = 1
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.configuredMax = newMax
	if t.effective > newMax {
		t.effective = newMax
	}
}

// Sample feeds one iowait% sample into the controller. Intended to be
// called at ~1Hz by an external ticker goroutine. Returns the effective
// concurrency that results from this sample.
func (t *Throttle) Sample(iowaitPct float32) int32 {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Feed the smoothing window.
	t.window[t.windowIdx] = iowaitPct
	t.windowIdx = (t.windowIdx + 1) % len(t.window)
	if t.windowLen < len(t.window) {
		t.windowLen++
	}
	var sum float32
	for i := 0; i < t.windowLen; i++ {
		sum += t.window[i]
	}
	t.smoothedIOWait = sum / float32(t.windowLen)

	now := time.Now()

	// Decision on the smoothed signal, not the raw one — spikes
	// smaller than the window shouldn't kick the controller around.
	switch {
	case t.smoothedIOWait >= RedThresholdPct:
		t.redStreak++
		if t.redStreak >= RedStreakToThrottle && t.effective > 1 {
			t.effective--
			t.lastThrottleAt = now
			t.lastActionAt = now
			// Reset the growth ambition: after a throttle, the
			// next +1 has to earn its way back through the
			// minimum spread again.
			t.growSpread = MinGrowSpread
			t.redStreak = 0
		}
	case t.smoothedIOWait >= YellowThresholdPct:
		// YELLOW: hold. Don't count this toward the RED streak
		// (it wasn't a red sample), but also don't allow growth
		// (the condition below fails).
		//
		// We deliberately do NOT reset redStreak here — that
		// way, an environment sitting right at 45-65% iowait
		// with occasional 75% spikes will accumulate its RED
		// streak across a few consecutive spikes rather than
		// requiring three back-to-back RED samples.
	default:
		// GREEN: reset the streak and consider growing.
		t.redStreak = 0
		if t.effective < t.configuredMax && now.Sub(t.lastActionAt) >= t.growSpread {
			t.effective++
			t.lastActionAt = now
			// Widen the next required quiet interval — the
			// further we've grown without incident, the longer
			// we wait before pushing again.
			t.growSpread *= 2
			if t.growSpread > MaxGrowSpread {
				t.growSpread = MaxGrowSpread
			}
		}
	}

	return t.effective
}

// Effective returns the current effective concurrency to advertise.
func (t *Throttle) Effective() int32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.effective
}

// State returns a snapshot of the observable controller state for
// telemetry and the UI badge. lastThrottleUnix is 0 when no throttle
// event has fired yet in this process lifetime.
func (t *Throttle) State() (effective int32, iowaitSmoothed float32, lastThrottleUnix int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var last int64
	if !t.lastThrottleAt.IsZero() {
		last = t.lastThrottleAt.Unix()
	}
	return t.effective, t.smoothedIOWait, last
}
