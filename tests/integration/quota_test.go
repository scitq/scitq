package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestListQuotas checks that a region reports its configured limits, what
// its workers consume, and the provider's recent refusals, and that the MCP
// tool derives the region's state from them.
func TestListQuotas(t *testing.T) {
	ctx := context.Background()

	override := &config.Config{}
	override.Providers.Fake = map[string]*config.FakeProviderConfig{
		"quota": {
			DefaultRegion: "r1",
			Regions:       []string{"r1", "r2"},
			Quotas:        map[string]config.Quota{"r1": {MaxCPU: 48, MaxMemGB: 200, MaxInstances: 3}},
		},
	}
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()

	qc := openAdminGRPC(t, serverAddr, adminUser, adminPassword)
	defer qc.Close()
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	wid := seedWorker(t, ctx, qc, db, "quota", "f-quota-32")

	find := func(region string) *pb.RegionQuota {
		res, err := qc.Client.ListQuotas(ctx, &emptypb.Empty{})
		require.NoError(t, err)
		for _, q := range res.Quotas {
			if q.Provider == "fake.quota" && q.Region == region {
				return q
			}
		}
		t.Fatalf("region %s missing from ListQuotas", region)
		return nil
	}

	r1 := find("r1")
	require.True(t, r1.Configured)
	require.Equal(t, int32(48), r1.MaxCpu)
	require.Equal(t, int32(32), r1.UsedCpu)
	require.Equal(t, float32(200), r1.MaxMemGb)
	require.Equal(t, float32(128), r1.UsedMemGb)
	require.Equal(t, int32(3), r1.MaxInstances)
	require.Equal(t, int32(1), r1.UsedInstances)
	require.Zero(t, r1.RecentRefusals)
	require.Equal(t, "available", r1.State)
	r2 := find("r2")
	require.False(t, r2.Configured, "r2 has no quota in the configuration")
	require.Equal(t, "unconfigured", r2.State)

	// A creation the provider refused for capacity in the last hour.
	_, err = db.Exec(`
		INSERT INTO job (worker_id, region_id, action, status, error_class, error_message)
		SELECT $1, region_id, 'C', 'F', 'capacity', 'SkuNotAvailable' FROM worker WHERE worker_id = $1`, wid)
	require.NoError(t, err)
	r1 = find("r1")
	require.Equal(t, int32(1), r1.RecentRefusals)
	require.Equal(t, "capacity", r1.GetLastRefusalClass())
	require.Equal(t, "SkuNotAvailable", r1.GetLastRefusalMessage())
	require.NotEmpty(t, r1.GetLastRefusalAt())
	require.Equal(t, "refusing", r1.State)

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)
	out, err = runCLICommand(c, []string{"quota", "list", "--long"})
	require.NoError(t, err, out)
	require.Regexp(t, `fake\.quota\s+r1\s+refusing\s+32/48\s+128/200\s+1/3\s+1`, out)
	require.Contains(t, out, "[capacity]: SkuNotAvailable")

	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", httpPortForAddr(t, serverAddr))
	sessionID := mcpSession(t, mcpURL, "quota-test", adminUser, adminPassword)
	resp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: map[string]any{"name": "list_quotas", "arguments": map[string]any{}},
	})
	require.Nil(t, resp.Error)
	var result struct {
		Content []struct{ Text string } `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	var regions []struct {
		Provider      string   `json:"provider"`
		Region        string   `json:"region"`
		State         string   `json:"state"`
		FreeCPU       int32    `json:"free_cpu"`
		FreeMemGB     *float32 `json:"free_mem_gb"`
		FreeInstances *int32   `json:"free_instances"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &regions), result.Content[0].Text)
	states := map[string]string{}
	for _, r := range regions {
		if r.Provider != "fake.quota" {
			continue
		}
		states[r.Region] = r.State
		if r.Region == "r1" {
			require.Equal(t, int32(16), r.FreeCPU)
			require.NotNil(t, r.FreeMemGB)
			require.Equal(t, float32(72), *r.FreeMemGB)
			require.NotNil(t, r.FreeInstances)
			require.Equal(t, int32(2), *r.FreeInstances)
		}
	}
	require.Equal(t, map[string]string{"r1": "refusing", "r2": "unconfigured"}, states)
}
