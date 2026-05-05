package integration_test

import (
	"context"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSetFlavorAvailability covers the operator-driven flavor blacklist:
// `scitq flavor disable` flips `flavor_region.available=false` for a
// specific provider/region (or all regions), and `enable` flips it back.
// Verification is via the RPC's `affected_rows` return value — the field
// itself is not exposed on `ListFlavors` (yet), and reading the DB
// directly from the test infra would require plumbing that's not worth
// adding for this.
func TestSetFlavorAvailability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	override := &config.Config{}
	override.Providers.Fake = map[string]*config.FakeProviderConfig{
		"avail": {
			DefaultRegion: "r1",
			Regions:       []string{"r1", "r2", "r3"},
			Quotas: map[string]config.Quota{
				"r1": {MaxCPU: 8},
				"r2": {MaxCPU: 8},
				"r3": {MaxCPU: 8},
			},
			AutoLaunch: false,
		},
	}

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()

	// Admin gRPC client.
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	tok := extractToken(out)
	qc, err := lib.CreateClient(serverAddr, tok)
	require.NoError(t, err)
	defer qc.Close()

	// Seed a flavor across 3 regions.
	_, err = qc.Client.CreateFlavor(ctx, &pb.FlavorCreateRequest{
		ProviderName: "fake",
		ConfigName:   "avail",
		FlavorName:   "Standard_DC32ads_cc_v5",
		RegionNames:  []string{"r1", "r2", "r3"},
		Evictions:    []float32{0, 0, 0},
		Costs:        []float32{1, 1, 1},
		Cpu:          32,
		Memory:       128,
		Disk:         1200,
	})
	require.NoError(t, err)

	// 1) Disable in a single region.
	r1 := "r1"
	resp, err := qc.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "Standard_DC32ads_cc_v5",
		Provider:   "fake.avail",
		Region:     &r1,
		Available:  false,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), resp.GetAffectedRows(),
		"scoped disable should hit exactly one (flavor, region) row")

	// 2) Disable across all regions. Postgres' UPDATE rowcount counts
	// every row matching the WHERE clause regardless of whether the
	// value actually changed, so this should report 3.
	resp, err = qc.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "Standard_DC32ads_cc_v5",
		Provider:   "fake.avail",
		Available:  false,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), resp.GetAffectedRows(),
		"unscoped disable should hit every region the flavor lives in")

	// 3) Re-enable across all regions.
	resp, err = qc.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "Standard_DC32ads_cc_v5",
		Provider:   "fake.avail",
		Available:  true,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), resp.GetAffectedRows())

	// 4) Validation: bad provider form.
	_, err = qc.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "Standard_DC32ads_cc_v5",
		Provider:   "fake",
		Available:  false,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())

	// 5) Validation: empty flavor_name.
	_, err = qc.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "",
		Provider:   "fake.avail",
		Available:  false,
	})
	require.Error(t, err)
	st, ok = status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())

	// 6) Permission gate: worker token (not admin) is refused.
	wk, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wk.Close()
	_, err = wk.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "Standard_DC32ads_cc_v5",
		Provider:   "fake.avail",
		Available:  false,
	})
	require.Error(t, err)
	st, ok = status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.PermissionDenied, st.Code(),
		"unlike RequestWorkerUpgrade, this RPC stays admin-only — flipping recruitment defaults is operator-level")

	// 7) Unknown flavor: zero rows affected, no error. The RPC is idempotent.
	resp, err = qc.Client.SetFlavorAvailability(ctx, &pb.FlavorAvailability{
		FlavorName: "Standard_NonExistent_v0",
		Provider:   "fake.avail",
		Available:  false,
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), resp.GetAffectedRows(),
		"unknown flavor should silently no-op")
}
