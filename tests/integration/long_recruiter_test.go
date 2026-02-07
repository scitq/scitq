package integration_test

import (
	"context"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestRecruitmentCycle boots a real server with a fake provider (3 regions)
// to verify that the provider is registered and synced into DB properly.
// It’s the first step before testing adaptive concurrency & recycling logic.
func TestLongRecruiter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// --- 1️⃣ Prepare config override with a fake provider ---
	override := &config.Config{}
	override.Providers.Fake = map[string]*config.FakeProviderConfig{
		"test": {
			DefaultRegion: "r1",
			Regions:       []string{"r1", "r2", "r3"},
			Quotas: map[string]config.Quota{
				"r1": {MaxCPU: 80},
				"r2": {MaxCPU: 80},
				"r3": {MaxCPU: 4},
			},
			AutoLaunch: false,
		},
	}
	override.Scitq.NewWorkerIdleTimeout = 300 // seconds

	// --- 2️⃣ Boot server with fake provider ---
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()
	time.Sleep(2 * time.Second) // Give server time to start

	// --- 3️⃣ Login as admin and get token ---
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := trimNewline(out)

	// --- 4️⃣ Connect to gRPC API ---
	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// --- 5️⃣ Check provider list via gRPC ---
	list, err := qc.ListProviders(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.NotEmpty(t, list.Providers, "no providers returned")

	var found bool
	var providerId int32
	for _, p := range list.Providers {
		if p.ProviderName == "fake" {
			found = true
			providerId = p.ProviderId
			t.Logf("✅ Found fake provider: %v (id=%d, config=%v)", p.ProviderName, providerId, p.ConfigName)
		}
	}
	require.True(t, found, "fake provider not registered")

	// --- 6️⃣ Check regions ---
	regions, err := qc.ListRegions(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	names := []string{}
	regionIds := make(map[string]int32)
	for _, r := range regions.Regions {
		if r.ProviderId == providerId {
			names = append(names, r.RegionName)
			regionIds[r.RegionName] = r.RegionId
		}
	}
	require.ElementsMatch(t, []string{"r1", "r2", "r3"}, names)

	// --- 7️⃣ Wait for recruiter loop ---
	time.Sleep(5 * time.Second)
	t.Log("✅ Fake provider and regions synced correctly — recruiter ready for further tests.")

	// --- 8️⃣ Create flavors on fake provider ---
	fid, err := qc.CreateFlavor(ctx, &pb.FlavorCreateRequest{
		ProviderName: "fake",
		ConfigName:   "test",
		FlavorName:   "cheap8",
		RegionNames:  []string{"r1"},
		Evictions:    []float32{0},
		Costs:        []float32{1},
		Cpu:          8,
		Memory:       30.0,
		Disk:         50.0,
	})
	require.NoError(t, err, "failed to create flavor cheap8")
	cheap8_id := fid.FlavorId
	t.Logf("✅ Created flavor cheap8 (id:%d)", cheap8_id)

	fid, err = qc.CreateFlavor(ctx, &pb.FlavorCreateRequest{
		ProviderName: "fake",
		ConfigName:   "test",
		FlavorName:   "exp16",
		RegionNames:  []string{"r2"},
		Evictions:    []float32{0},
		Costs:        []float32{3},
		Cpu:          16,
		Memory:       60.0,
		Disk:         50.0,
	})
	require.NoError(t, err, "failed to create flavor cheap8")
	exp16_id := fid.FlavorId
	t.Logf("✅ Created flavor exp16 (id:%d)", exp16_id)

	fid, err = qc.CreateFlavor(ctx, &pb.FlavorCreateRequest{
		ProviderName: "fake",
		ConfigName:   "test",
		FlavorName:   "tiny4",
		RegionNames:  []string{"r3"},
		Evictions:    []float32{0},
		Costs:        []float32{0.5},
		Cpu:          4,
		Memory:       15.0,
		Disk:         20.0,
	})
	require.NoError(t, err, "failed to create flavor cheap8")
	tiny4_id := fid.FlavorId
	t.Logf("✅ Created flavor tiny4 (id:%d)", tiny4_id)

	t.Run("RecruitmentFullWorkflow", func(t *testing.T) {
		ctx := context.Background()

		// 1. Create a workflow
		wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{
			Name:           "RecruitmentTest",
			MaximumWorkers: nil,
		})
		require.NoError(t, err)
		wfId := wfResp.WorkflowId
		t.Logf("✅ Created workflow: id=%d", wfId)

		wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{}) // for debug
		require.NoError(t, err)
		t.Logf("Answer detail: wfList %v", wfList)

		// 2. Add two steps
		step1Resp, err := qc.CreateStep(ctx, &pb.StepRequest{
			WorkflowId: &wfId,
			Name:       "hello",
		})
		require.NoError(t, err)
		step1Id := step1Resp.StepId
		t.Logf("✅ Created step 1: id=%d", step1Id)

		step2Resp, err := qc.CreateStep(ctx, &pb.StepRequest{
			WorkflowId: &wfId,
			Name:       "goodbye",
		})
		require.NoError(t, err)
		step2Id := step2Resp.StepId
		t.Logf("✅ Created step 2: id=%d", step2Id)

		// 3. For each step, create 10 tasks.
		step1TaskIds := make([]int32, 50)
		step2TaskIds := make([]int32, 10)
		shell := "sh"
		for i := 0; i < 50; i++ {
			// Step 1 task
			taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
				StepId:    &step1Id,
				Command:   "echo \"hello with CPU $CPU\"",
				Shell:     &shell,
				Container: "alpine:latest", // Use a small image to speed up tests
			})
			require.NoError(t, err)
			step1TaskIds[i] = taskResp.TaskId
			t.Logf("✅ Created step 1 task %d: id=%d", i, taskResp.TaskId)
		}
		for i := 0; i < 10; i++ {
			// Step 2 task, depends on step 1 task
			taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
				StepId:     &step2Id,
				Command:    "echo \"goodbye with CPU $CPU\"",
				Shell:      &shell,
				Dependency: []int32{step1TaskIds[i]},
				Container:  "alpine:latest", // Use a small image to speed up tests
			})
			require.NoError(t, err)
			step2TaskIds[i] = taskResp.TaskId
			t.Logf("✅ Created step 2 task %d: id=%d, depends on task %d", i, taskResp.TaskId, step1TaskIds[i])
		}

		_, err = qc.UpdateWorkflowStatus(ctx, &pb.WorkflowStatusUpdate{
			WorkflowId: wfId,
			Status:     "R",
		})
		require.NoError(t, err)

		// 4. Create recruiters for each step
		//eight := int32(8)
		four := int32(4)
		three := int32(3)
		zero := int32(0)
		rec1Resp, err := qc.CreateRecruiter(ctx, &pb.Recruiter{
			StepId:          step1Id,
			Rank:            1,
			Protofilter:     "cpu>=8",
			CpuPerTask:      &three,
			PrefetchPercent: &zero,
			Rounds:          2,
			Timeout:         3,
		})
		require.NoError(t, err)
		t.Logf("✅ Created recruiter for step 1 : %v", rec1Resp.Success)

		rec2Resp, err := qc.CreateRecruiter(ctx, &pb.Recruiter{
			StepId:          step2Id,
			Rank:            1,
			Protofilter:     "cpu>=8",
			CpuPerTask:      &four,
			PrefetchPercent: &zero,
			Rounds:          1,
			Timeout:         3,
		})
		require.NoError(t, err)
		t.Logf("✅ Created recruiter for step 2: %v", rec2Resp.Success)

		time.Sleep(60 * time.Second) // Wait for recruiter to act

		// check that two workers have been created
		wlist, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		require.NoError(t, err)
		t.Logf("Answer detail: wlist %v", wlist)
		var cheap8Count, exp16count int
		for _, w := range wlist.Workers {
			if w.Flavor == "cheap8" {
				cheap8Count++
				t.Logf("✅ Found cheap8 worker created by recruiter: ID=%d, Name=%s",
					w.WorkerId, w.Name)
			}
			if w.Flavor == "exp16" {
				exp16count++
				t.Logf("✅ Found exp16 worker created by recruiter: ID=%d, Name=%s",
					w.WorkerId, w.Name)
			}
		}
		// 3 cpu/task => each worker has a 2 task/round, 50 tasks in 2 rounds => target task rate is 25, so 13 workers needed but only 10 available in quota
		// so 10 cheap8 (e.g. taskrate 20) and 1 exp16 (taskrate +5)
		if cheap8Count == 10 {
			t.Logf("✅ Expected 10 cheap8 workers, found %d", cheap8Count)
		} else {
			t.Errorf("❌ Expected 10 cheap8 workers, found %d", cheap8Count)
		}
		require.Equal(t, 10, cheap8Count, "expected 10 cheap8 workers")
		if exp16count == 1 {
			t.Logf("✅ Expected 1 exp16 workers, found %d", exp16count)
		} else {
			t.Errorf("❌ Expected 1 exp16 workers, found %d", exp16count)
		}
		require.Equal(t, 1, exp16count, "expected 1 exp16 workers")
	})
}
