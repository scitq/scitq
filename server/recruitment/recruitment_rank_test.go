package recruitment

import (
	"context"
	"testing"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

type fakeCreator struct{ flavors []int32 }

func (f *fakeCreator) CreateWorker(_ context.Context, req *pb.WorkerRequest) (*pb.WorkerIds, error) {
	f.flavors = append(f.flavors, req.FlavorId)
	return &pb.WorkerIds{}, nil
}

const (
	rank1Flavor = int32(11)
	rank2Flavor = int32(22)
)

// rankFixture builds a step with two cloud recruiters, each deploying a
// distinct flavor in its own region, and no recyclable workers. Each
// deployed worker runs 4 tasks; the step is short of 8.
func rankFixture(rank1Quota bool, rank1Max *int, stepWorkers int) ([]Recruiter, *stepContext, *fakeCreator) {
	four := 4
	mk := func(rank int, max *int) Recruiter {
		return Recruiter{
			StepID: 1, Rank: rank, TimeoutSeconds: 30, WorkerConcurrency: &four,
			MaximumWorkers: max, CurrentWorkers: stepWorkers,
			TargetTaskrate: 8, PotentialTaskrate: 0, WorkflowID: 1,
		}
	}
	recruiters := []Recruiter{mk(1, rank1Max), mk(2, nil)}

	qm := &QuotaManager{Quotas: map[string]RegionalQuota{
		quotaKey("r2", "cloud"): {Region: "r2", Provider: "cloud", MaxCPU: 1000},
	}}
	if rank1Quota {
		qm.Quotas[quotaKey("r1", "cloud")] = RegionalQuota{Region: "r1", Provider: "cloud", MaxCPU: 1000}
	}
	creator := &fakeCreator{}
	sc := &stepContext{
		qm:      qm,
		creator: creator,
		recruitmentMap: map[RecruiterKey][]FlavorDetail{
			{StepID: 1, Rank: 1}: {{FlavorID: rank1Flavor, Name: "f1", RegionID: 1, Cpu: 8, Memory: 16}},
			{StepID: 1, Rank: 2}: {{FlavorID: rank2Flavor, Name: "f2", RegionID: 2, Cpu: 8, Memory: 16}},
		},
		regionInfoMap: map[int32]RegionInfo{1: {Name: "r1", Provider: "cloud"}, 2: {Name: "r2", Provider: "cloud"}},
		timers:        map[RecruiterKey]RecruiterState{},
		wfcMem:        map[int32]WorkflowCounter{},
	}
	return recruiters, sc, creator
}

func (sc *stepContext) cycle(recruiters []Recruiter, at time.Time) {
	sc.now = at
	sc.eligible = map[RecruiterKey]struct{}{}
	sc.recruitStep(context.Background(), recruiters)
	pruneRecruiterTimers(sc.timers, sc.eligible)
}

func TestRankTwoWaitsWhileRankOneTimesOut(t *testing.T) {
	recruiters, sc, creator := rankFixture(true, nil, 0)
	t0 := time.Now()
	// A stale rank-2 timer from an old activation must not let rank 2 jump in.
	sc.timers[RecruiterKey{StepID: 1, Rank: 2}] = RecruiterState{LastTrigger: t0.Add(-8 * time.Hour)}

	sc.cycle(recruiters, t0)
	if len(creator.flavors) != 0 {
		t.Fatalf("nothing may deploy during rank 1's timeout, got %v", creator.flavors)
	}
	if _, ok := sc.timers[RecruiterKey{StepID: 1, Rank: 2}]; ok {
		t.Fatalf("rank 2 is not eligible while rank 1 waits; its timer must be cleared")
	}

	sc.cycle(recruiters, t0.Add(31*time.Second))
	if len(creator.flavors) != 2 || creator.flavors[0] != rank1Flavor || creator.flavors[1] != rank1Flavor {
		t.Fatalf("rank 1 should cover the shortfall alone with 2 workers, got %v", creator.flavors)
	}
}

func TestRankTwoActsAfterRankOneIsDoneForQuota(t *testing.T) {
	recruiters, sc, creator := rankFixture(false, nil, 0)
	t0 := time.Now()

	sc.cycle(recruiters, t0)
	if len(creator.flavors) != 0 {
		t.Fatalf("rank 2 must wait its own timeout once eligible, got %v", creator.flavors)
	}
	if _, ok := sc.timers[RecruiterKey{StepID: 1, Rank: 2}]; !ok {
		t.Fatalf("rank 1 is done (no quota), so rank 2's timer should be running")
	}

	sc.cycle(recruiters, t0.Add(31*time.Second))
	if len(creator.flavors) != 2 || creator.flavors[0] != rank2Flavor {
		t.Fatalf("rank 2 should deploy after its timeout, got %v", creator.flavors)
	}
}

func TestRankOneDoneWhenStepAtItsCap(t *testing.T) {
	two := 2
	recruiters, sc, creator := rankFixture(true, &two, 2)
	t0 := time.Now()

	sc.cycle(recruiters, t0)
	if _, ok := sc.timers[RecruiterKey{StepID: 1, Rank: 1}]; ok {
		t.Fatalf("rank 1 is at the step cap and must not start a timer")
	}
	sc.cycle(recruiters, t0.Add(31*time.Second))
	if len(creator.flavors) == 0 || creator.flavors[0] != rank2Flavor {
		t.Fatalf("rank 2 should deploy once rank 1 is capped, got %v", creator.flavors)
	}
}
