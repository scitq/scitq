package server

import "testing"

// A task that ran in under a second records 0: it is the minimum, and the
// next, longer durations keep it.
func TestAccumulatorKeepsZeroMinimum(t *testing.T) {
	var a Accumulator
	for _, d := range []float64{0, 719, 128, 0, 1030} {
		a.Add(d)
	}
	got := toStepRunStats(a)
	want := stepRunStats{Count: 5, Average: float32(1877.0 / 5), Min: 0, Max: 1030}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAccumulatorFirstValueSetsBounds(t *testing.T) {
	var a Accumulator
	a.Add(300)
	if a.Min != 300 || a.Max != 300 || a.Count != 1 {
		t.Fatalf("got %+v", a)
	}
}
