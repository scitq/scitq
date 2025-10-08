package integration_test

import (
	"context"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
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
		},
	}

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
			t.Logf("✅ Found fake provider: %v (config=%v)", p.ProviderName, p.ConfigName)
		}
	}
	require.True(t, found, "fake provider not registered")

	// --- 6️⃣ Check regions ---
	regions, err := qc.ListRegions(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	names := []string{}
	for _, r := range regions.Regions {
		if r.ProviderId == providerId {
			names = append(names, r.RegionName)
		}
	}
	require.ElementsMatch(t, []string{"r1", "r2", "r3"}, names)

	// --- 7️⃣ Wait for recruiter loop ---
	time.Sleep(5 * time.Second)
	t.Log("✅ Fake provider and regions synced correctly — recruiter ready for further tests.")
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
