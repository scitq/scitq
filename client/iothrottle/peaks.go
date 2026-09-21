package iothrottle

import "sync"

// PeakTracker records the maximum cpu%, mem%, and iowait% observed
// since the last Drain(). The sampler goroutine feeds one sample per
// second; the ping loop calls Drain when it builds a WorkerStats, so
// each ping's peak_* fields reflect the true worst-case value between
// this ping and the previous one — not just whatever happened to be
// true at ping o'clock (which is what the raw cpu_usage_percent /
// mem_usage_percent already report).
//
// Zero values on Drain mean "no samples arrived since the last drain",
// which is what the server persists — the summary aggregation then
// treats them as NULL.
//
// Concurrency: Observe() and Drain() may run from different
// goroutines (sampler tick vs. ping loop). The mutex is fine-grained
// and non-contended in practice — the sampler at 1 Hz and the ping at
// ~0.2 Hz — so we don't bother with atomics.
type PeakTracker struct {
	mu               sync.Mutex
	cpuPct           float32
	memPct           float32
	iowaitPct        float32
	sawSample        bool
}

// NewPeakTracker returns a zeroed tracker.
func NewPeakTracker() *PeakTracker {
	return &PeakTracker{}
}

// Observe folds one sample into the running maxes. Negative values are
// clamped to 0 so a gopsutil hiccup can't poison the peak. NaN is
// rejected because it would poison the max via comparison (>NaN is
// always false so NaN would stick as the max forever); NaN never
// arrives from gopsutil in normal operation but we guard anyway.
func (p *PeakTracker) Observe(cpu, mem, iowait float32) {
	if isNaNf(cpu) || isNaNf(mem) || isNaNf(iowait) {
		return
	}
	if cpu < 0 {
		cpu = 0
	}
	if mem < 0 {
		mem = 0
	}
	if iowait < 0 {
		iowait = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if cpu > p.cpuPct {
		p.cpuPct = cpu
	}
	if mem > p.memPct {
		p.memPct = mem
	}
	if iowait > p.iowaitPct {
		p.iowaitPct = iowait
	}
	p.sawSample = true
}

// Drain returns the current maxes and resets the tracker. `hasData` is
// false when no Observe has landed since the last Drain — the caller
// then leaves the ping's peak_* fields unset rather than writing a
// misleading 0.
func (p *PeakTracker) Drain() (cpu, mem, iowait float32, hasData bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cpu, mem, iowait, hasData = p.cpuPct, p.memPct, p.iowaitPct, p.sawSample
	p.cpuPct, p.memPct, p.iowaitPct, p.sawSample = 0, 0, 0, false
	return
}

// isNaNf tests float32 NaN without pulling in math.IsNaN(float64).
func isNaNf(f float32) bool { return f != f }
