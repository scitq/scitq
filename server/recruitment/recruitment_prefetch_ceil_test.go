package recruitment

import "testing"

func boolLit(b bool) bool { return b }

// TestPrefetch_FloorIsDefault: without the ceil flag, integer-divide
// gives the original behaviour — including the "small worker rounds to
// 0" case the ceil feature is designed to avoid.
func TestPrefetch_FloorIsDefault(t *testing.T) {
	cases := []struct {
		concurrency int
		percent     int
		want        int
	}{
		{2, 25, 0},   // the motivating case: floor(0.5) = 0
		{3, 25, 0},   // still 0 (floor(0.75))
		{4, 25, 1},   // first concurrency where floor gives >= 1
		{10, 25, 2},  // floor(2.5)
		{2, 50, 1},   // exact multiple
	}
	for _, c := range cases {
		r := Recruiter{PrefetchPercent: intPtr(c.percent)}
		got := computePrefetchForRecruiterWorker(r, c.concurrency)
		if got != c.want {
			t.Errorf("floor: concurrency=%d percent=%d: got %d, want %d",
				c.concurrency, c.percent, got, c.want)
		}
	}
}

// TestPrefetch_CeilRoundsUp: with the flag set, any positive percent
// yields at least 1 prefetch on small workers, and exact multiples are
// unchanged.
func TestPrefetch_CeilRoundsUp(t *testing.T) {
	cases := []struct {
		concurrency int
		percent     int
		want        int
	}{
		{2, 25, 1},   // ceil(0.5) — the motivating fix
		{3, 25, 1},   // ceil(0.75)
		{4, 25, 1},   // exact multiple; same as floor
		{5, 25, 2},   // ceil(1.25); floor would give 1
		{10, 25, 3},  // ceil(2.5); floor would give 2
		{2, 50, 1},   // exact multiple; same as floor
		{2, 100, 2},  // exact multiple; same as floor
	}
	for _, c := range cases {
		r := Recruiter{
			PrefetchPercent:     intPtr(c.percent),
			PrefetchPercentCeil: boolLit(true),
		}
		got := computePrefetchForRecruiterWorker(r, c.concurrency)
		if got != c.want {
			t.Errorf("ceil: concurrency=%d percent=%d: got %d, want %d",
				c.concurrency, c.percent, got, c.want)
		}
	}
}

// TestPrefetch_StaticWorkerPrefetchWins: an explicit WorkerPrefetch (from
// `prefetch: 3`) always wins over the percent path, regardless of the
// ceil flag — the flag only matters when percent arithmetic runs.
func TestPrefetch_StaticWorkerPrefetchWins(t *testing.T) {
	r := Recruiter{
		WorkerPrefetch:      intPtr(3),
		PrefetchPercent:     intPtr(25),
		PrefetchPercentCeil: boolLit(true),
	}
	if got := computePrefetchForRecruiterWorker(r, 10); got != 3 {
		t.Fatalf("static WorkerPrefetch should win: got %d, want 3", got)
	}
}

// TestPrefetch_ZeroPercentStaysZero: a `prefetch: "0%"` opt-out must
// give 0 regardless of the ceil flag. The (0*concurrency+99)/100 branch
// still evaluates to 0.
func TestPrefetch_ZeroPercentStaysZero(t *testing.T) {
	r := Recruiter{
		PrefetchPercent:     intPtr(0),
		PrefetchPercentCeil: boolLit(true),
	}
	if got := computePrefetchForRecruiterWorker(r, 8); got != 0 {
		t.Fatalf("0%% with ceil must stay 0: got %d", got)
	}
}
