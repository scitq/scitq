package integration_test

import (
	"context"
	"strings"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestModuleLibrary exercises the versioned YAML module store end-to-end:
// upload, list (flat + versioned filter + latest-only), download (exact
// version + latest alias), origin lookup, and fork. Regressions here would
// mean an admin can't patch bundled modules cleanly.
func TestModuleLibrary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// --- 1. Upload a v1 at a namespaced path
	v1 := []byte(`name: my_aligner
version: "1.0.0"
description: "Test aligner v1"
container: alpine:3
command: |
  echo v1
`)
	_, err = qc.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: "internal/my_aligner.yaml",
		Content:  v1,
	})
	require.NoError(t, err, "initial upload must succeed")

	// Uploading the same (path, version) without --force must fail
	_, err = qc.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: "internal/my_aligner.yaml",
		Content:  v1,
	})
	require.Error(t, err, "duplicate upload without --force should fail")
	require.Contains(t, err.Error(), "already exists")

	// Uploading with --force must succeed
	_, err = qc.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: "internal/my_aligner.yaml",
		Content:  v1,
		Force:    true,
	})
	require.NoError(t, err, "upload with --force should replace in place")

	// --- 2. Upload a v2 at the same path
	v2 := []byte(`name: my_aligner
version: "1.1.0"
description: "Test aligner v1.1"
container: alpine:3
command: |
  echo v2
`)
	_, err = qc.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: "internal/my_aligner.yaml",
		Content:  v2,
	})
	require.NoError(t, err, "uploading a new version should succeed")

	// --- 3. ListModules (legacy) must return both, with `path@version` strings
	listResp, err := qc.ListModules(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	flat := strings.Join(listResp.Modules, " ")
	require.Contains(t, flat, "internal/my_aligner@1.0.0")
	require.Contains(t, flat, "internal/my_aligner@1.1.0")
	// And populate the structured entries in parallel
	var v10Origin, v11Origin string
	for _, e := range listResp.Entries {
		if e.Path == "internal/my_aligner" && e.Version == "1.0.0" {
			v10Origin = e.Origin
		}
		if e.Path == "internal/my_aligner" && e.Version == "1.1.0" {
			v11Origin = e.Origin
		}
	}
	require.Equal(t, "local", v10Origin, "initial upload should be origin=local")
	require.Equal(t, "local", v11Origin)

	// --- 4. ListModulesFiltered with path filter should give just this path's versions
	pathFilter := "internal/my_aligner"
	fResp, err := qc.ListModulesFiltered(ctx, &pb.ModuleListFilter{Path: &pathFilter})
	require.NoError(t, err)
	require.Len(t, fResp.Entries, 2, "both versions returned for path filter")

	// --- 5. latest_only should pick 1.1.0
	latestOnly := true
	lResp, err := qc.ListModulesFiltered(ctx, &pb.ModuleListFilter{
		Path:       &pathFilter,
		LatestOnly: &latestOnly,
	})
	require.NoError(t, err)
	require.Len(t, lResp.Entries, 1)
	require.Equal(t, "1.1.0", lResp.Entries[0].Version)

	// --- 6. DownloadModule with explicit @version returns that exact content
	dl, err := qc.DownloadModule(ctx, &pb.DownloadModuleRequest{Filename: "internal/my_aligner@1.0.0"})
	require.NoError(t, err)
	require.Equal(t, v1, dl.Content)
	require.Equal(t, "internal/my_aligner@1.0.0", dl.Filename)

	// --- 7. DownloadModule with no version returns highest
	dlLatest, err := qc.DownloadModule(ctx, &pb.DownloadModuleRequest{Filename: "internal/my_aligner"})
	require.NoError(t, err)
	require.Equal(t, v2, dlLatest.Content)
	require.Equal(t, "internal/my_aligner@1.1.0", dlLatest.Filename)

	// Reserved `latest` alias resolves the same way
	dlLatestAlias, err := qc.DownloadModule(ctx, &pb.DownloadModuleRequest{Filename: "internal/my_aligner@latest"})
	require.NoError(t, err)
	require.Equal(t, v2, dlLatestAlias.Content)

	// --- 8. GetModuleOrigin
	originResp, err := qc.GetModuleOrigin(ctx, &pb.ModuleOriginRequest{
		Path:    "internal/my_aligner",
		Version: "1.0.0",
	})
	require.NoError(t, err)
	require.Equal(t, "local", originResp.Origin)
	require.Equal(t, "1.0.0", originResp.Version)
	require.NotEmpty(t, originResp.ContentSha256)
	require.Empty(t, originResp.BundledSha256, "local row has no bundled_sha")
	require.Equal(t, "Test aligner v1", originResp.Description)
	require.False(t, originResp.ForkIsOutdated)

	// --- 9. ForkModule clones a local row to a new version with origin=forked
	_, err = qc.ForkModule(ctx, &pb.ForkModuleRequest{
		SourcePath:    "internal/my_aligner",
		SourceVersion: "1.0.0",
		NewVersion:    "1.0.0-site",
	})
	require.NoError(t, err, "fork should succeed")

	forkOrigin, err := qc.GetModuleOrigin(ctx, &pb.ModuleOriginRequest{
		Path:    "internal/my_aligner",
		Version: "1.0.0-site",
	})
	require.NoError(t, err)
	require.Equal(t, "forked", forkOrigin.Origin)
	require.NotEmpty(t, forkOrigin.BundledSha256, "forked row should record bundled_sha")

	// Content of fork equals content of source
	forkDL, err := qc.DownloadModule(ctx, &pb.DownloadModuleRequest{Filename: "internal/my_aligner@1.0.0-site"})
	require.NoError(t, err)
	require.Equal(t, v1, forkDL.Content)

	// --- 10. Uploading with 'latest' as a version string is rejected
	bogus := []byte(`name: x
version: latest
`)
	_, err = qc.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: "internal/bogus.yaml",
		Content:  bogus,
	})
	require.Error(t, err, "'latest' is a reserved version alias")

	// --- 11. Upload without version: field is rejected
	noVersion := []byte(`name: x
container: alpine:3
`)
	_, err = qc.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: "internal/noversion.yaml",
		Content:  noVersion,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "version:")

	// --- 12. Re-fork at an existing (path, version) is rejected
	_, err = qc.ForkModule(ctx, &pb.ForkModuleRequest{
		SourcePath:    "internal/my_aligner",
		SourceVersion: "1.0.0",
		NewVersion:    "1.0.0-site",
	})
	require.Error(t, err, "duplicate fork target must be rejected")
}
