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
func TestRecruitmentCycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// --- 1️⃣ Prepare config override with a fake provider ---
	override := &config.Config{}
	override.Providers.Fake = map[string]*config.FakeProviderConfig{
		"test": {
			DefaultRegion: "r1",
			Regions:       []string{"r1", "r2", "r3"},
			Quotas: map[string]config.Quota{
				"r1": {MaxCPU: 8},
				"r2": {MaxCPU: 16},
				"r3": {MaxCPU: 4},
			},
			AutoLaunch: true,
		},
	}
	override.Scitq.NewWorkerIdleTimeout = 10 // seconds

	// --- 2️⃣ Boot server with fake provider ---
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()

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

	// --- 9️⃣ Manually create a worker to validate fake provider creation path ---
	wresp, err := qc.CreateWorker(ctx, &pb.WorkerRequest{
		ProviderId:  providerId,
		FlavorId:    tiny4_id,
		RegionId:    regionIds["r3"],
		Concurrency: 1,
		Prefetch:    0,
		Number:      1,
	})
	require.NoError(t, err, "failed to create worker on fake provider")
	require.NotNil(t, wresp, "CreateWorker returned nil response")

	t.Logf("Answer detail: wresp %v", wresp.WorkersDetails)
	t.Logf("✅ Worker created successfully: ID=%d, Name=%s, JobID=%d",
		wresp.WorkersDetails[0].WorkerId, wresp.WorkersDetails[0].WorkerName, wresp.WorkersDetails[0].JobId)
	workerName := wresp.WorkersDetails[0].WorkerName

	// Give time for the auto-launched worker to register and become Running
	time.Sleep(5 * time.Second)

	// Verify worker presence via DB-level API
	wlist, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
	require.NoError(t, err)
	var foundWorker bool
	for _, w := range wlist.Workers {
		if w.Name == workerName {
			foundWorker = true
			t.Logf("✅ Verified fake worker found in list (status=%s)", w.Status)
		}
	}
	require.True(t, foundWorker, "fake worker not found in worker list")

	// --- 🔟 Start a real test worker client ---
	// clientCleanup, _ := startClientForTest(t, serverAddr, workerName, token, 1)
	// time.Sleep(2 * time.Second)
	// t.Log("✅ Test worker client successfully started and connected")
	// defer clientCleanup()

	time.Sleep(3 * time.Second) // Wait for worker status update
	wlist2, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
	require.NoError(t, err)
	var foundRunning bool
	for _, w := range wlist2.Workers {
		if w.Name == workerName {
			t.Logf("✅ Worker %s status after client start: %s", workerName, w.Status)
			require.Equal(t, "R", w.Status, "worker should be Running ('R') after client connects")
			foundRunning = true
		}
	}
	require.True(t, foundRunning, "worker with client not found in worker list after client started")
	t.Logf("✅ Worker %s is now Running ('R') as expected", workerName)

	time.Sleep(15 * time.Second) // Wait for worker status update
	wlist3, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
	require.NoError(t, err)
	found = false
	for _, w := range wlist3.Workers {
		if w.Name == workerName {
			t.Logf("⚠️ Worker %s status after client start: %s", workerName, w.Status)
			found = true
		}
	}
	require.True(t, !found, "worker should have been recycled after idle timeout")
	t.Logf("✅ Worker %s is gone as expected", workerName)
	// --- 🔥 Recruitment full workflow scenario ---
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
		step1TaskIds := make([]int32, 10)
		step2TaskIds := make([]int32, 10)
		shell := "sh"
		for i := 0; i < 10; i++ {
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

		// 4. Create recruiters for each step
		eight := int32(8)
		zero := int32(0)
		rec1Resp, err := qc.CreateRecruiter(ctx, &pb.Recruiter{
			StepId:          step1Id,
			Rank:            1,
			Protofilter:     "cpu>=8",
			CpuPerTask:      &eight,
			PrefetchPercent: &zero,
			Rounds:          2,
			Timeout:         3,
		})
		require.NoError(t, err)
		t.Logf("✅ Created recruiter for step 1 : %v", rec1Resp.Success)
		four := int32(4)
		rec2Resp, err := qc.CreateRecruiter(ctx, &pb.Recruiter{
			StepId:          step2Id,
			Rank:            1,
			Protofilter:     "cpu>=8",
			CpuPerTask:      &four,
			PrefetchPercent: &zero,
			Rounds:          2,
			Timeout:         3,
		})
		require.NoError(t, err)
		t.Logf("✅ Created recruiter for step 2: %v", rec2Resp.Success)

		time.Sleep(30 * time.Second) // Wait for recruiter to act
	})
}

// helper
func trimNewline(s string) string {
	if len(s) == 0 {
		return s
	}
	if s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}
