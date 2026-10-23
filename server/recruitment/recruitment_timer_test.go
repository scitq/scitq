package recruitment

import (
	"testing"
	"time"
)

func TestPruneRecruiterTimers(t *testing.T) {
	stale := time.Now().Add(-8 * time.Hour)
	timers := map[RecruiterKey]RecruiterState{
		{StepID: 1, Rank: 1}: {LastTrigger: stale},
		{StepID: 1, Rank: 2}: {LastTrigger: stale},
		{StepID: 2, Rank: 1}: {LastTrigger: stale},
	}
	pruneRecruiterTimers(timers, map[RecruiterKey]struct{}{{StepID: 1, Rank: 2}: {}})

	if len(timers) != 1 {
		t.Fatalf("expected only the eligible recruiter's timer to survive, got %v", timers)
	}
	if _, ok := timers[RecruiterKey{StepID: 1, Rank: 2}]; !ok {
		t.Fatalf("eligible recruiter's timer was dropped: %v", timers)
	}

	pruneRecruiterTimers(timers, nil)
	if len(timers) != 0 {
		t.Fatalf("no eligible recruiters should leave no timers, got %v", timers)
	}
}
