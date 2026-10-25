package integration_test

import (
	"context"
	"fmt"
	"testing"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cli "github.com/scitq/scitq/cli"
)

// TestRecruitersShareStepRounds: all recruiters of a step share one rounds
// value (docs/reference/recruitment.md). Creating a recruiter with another
// value is refused with a message naming the step and the value in use;
// updating rounds on one recruiter applies it to the whole step.
func TestRecruitersShareStepRounds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	wfID := createWorkflowViaCLI(t, c, "wf-rounds")
	stepID := createStepViaCLI(t, c, wfID, "s")

	concurrency, prefetch := int32(2), int32(0)
	recruiter := func(rank, rounds int32) *pb.Recruiter {
		return &pb.Recruiter{StepId: stepID, Rank: rank, Protofilter: "cpu>=2",
			Concurrency: &concurrency, Prefetch: &prefetch, Rounds: rounds, Timeout: 10}
	}

	_, err = qc.CreateRecruiter(ctx, recruiter(1, 2))
	require.NoError(t, err)

	_, err = qc.CreateRecruiter(ctx, recruiter(2, 3))
	require.Error(t, err, "a second recruiter with different rounds must be refused")
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), fmt.Sprintf("step %d already recruits with rounds=2 (rank 1)", stepID))

	_, err = qc.CreateRecruiter(ctx, recruiter(2, 2))
	require.NoError(t, err, "a second recruiter with the step's rounds is accepted")

	four := int32(4)
	_, err = qc.UpdateRecruiter(ctx, &pb.RecruiterUpdate{StepId: stepID, Rank: 1, Rounds: &four})
	require.NoError(t, err)

	list, err := qc.ListRecruiters(ctx, &pb.RecruiterFilter{StepId: &stepID})
	require.NoError(t, err)
	require.Len(t, list.Recruiters, 2)
	for _, r := range list.Recruiters {
		require.EqualValues(t, 4, r.Rounds, "rank %d keeps the step's rounds", r.Rank)
	}
}
