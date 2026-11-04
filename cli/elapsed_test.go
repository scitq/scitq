package cli

import (
	"testing"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

func TestElapsedSpanSkipsStepsThatDidNotRun(t *testing.T) {
	at := func(v int64) *int64 { return &v }
	stats := []*pb.StepStats{
		{StartTime: at(0), EndTime: at(0)}, // skipped, as servers before 2.0 send it
		{},                                 // skipped
		{StartTime: at(1000), EndTime: at(1300)},
		{StartTime: at(1100), EndTime: at(1500)},
	}
	if got := elapsedSpan(stats); got != 500 {
		t.Fatalf("elapsed %v, want 500", got)
	}
	if got := elapsedSpan(stats[:2]); got != 0 {
		t.Fatalf("elapsed with no step run: %v, want 0", got)
	}
}
