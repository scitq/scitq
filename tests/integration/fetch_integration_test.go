package integration_test

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

func TestFetchIntegration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// --- 1️⃣ Prepare config override with a fake provider ---
	override := &config.Config{}
	override.Rclone = map[string]map[string]string{
		"s3test": {
			"type":      "s3",
			"provider":  "AWS",
			"us-west-2": "false",
			"region":    "us-west-2",
		},
	}
	override.Scitq.NewWorkerIdleTimeout = 300 // seconds

	// --- 2️⃣ Boot server with fake provider ---
	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()
	time.Sleep(2 * time.Second) // Give server time to start

	// --- 3️⃣ Login as admin and get token ---
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := trimNewline(out)
	c.Attr.Token = token

	// --- 4️⃣ Connect to gRPC API ---
	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// 2. Create task that fetches and hashes file
	three := int32(3)
	pbTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "md5sum /input/doi.txt",
		Input:     []string{"s3test://nasanex/NEX-DCP30/doi.txt"},
		Container: "alpine",
		Retry:     &three,
	})
	require.NoError(t, err, "failed to create task")

	// launch worker (client)
	cleanupClient, _ := startClientForTest(t, serverAddr, "test-worker-1", workerToken, 1)
	defer cleanupClient()

	// 6) Wait a moment and list visible tasks
	require.Eventually(t, func() bool {
		out, _ := runCLICommand(c, []string{"task", "list"})
		t.Logf("Current task list output:\n%s", out)

		reNew := regexp.MustCompile(fmt.Sprintf(`ID:\s*%d.*Status:\s*S.*`, pbTask.TaskId))
		return reNew.MatchString(out)
	}, 5*time.Second, 2*time.Second, fmt.Sprintf("downloading task %d did not succeed yet", pbTask.TaskId))
	log.Printf("Task %d succeeded", pbTask.TaskId)

	// 3. Fetch result checksum from /output/md5.txt
	serverMD5output, err := runCLICommand(c, []string{"task", "output", "--id", fmt.Sprintf("%d", pbTask.TaskId)})
	require.NoError(t, err, "failed to read task output")
	reMD5 := regexp.MustCompile("([0-9a-f]{32})")
	matches := reMD5.FindStringSubmatch(serverMD5output)
	require.NotNil(t, matches, "failed to extract MD5 from server output")
	serverMD5 := matches[1]
	log.Printf("MD5 detected to be %s", serverMD5)

	// 4. Local CLI copy
	tmp := t.TempDir()
	cliPath := filepath.Join(tmp, "doi.txt")
	_, err = runCLICommand(c, []string{"file", "copy", "s3test://nasanex/NEX-DCP30/doi.txt", cliPath})
	require.NoError(t, err, "failed to copy file with CLI s3test://nasanex/NEX-DCP30/doi.txt → "+cliPath)

	cliMD5 := computeMD5(t, cliPath)
	require.Equal(t, cliMD5, serverMD5)
	log.Printf("Local download identical to task download")

}

func computeMD5(t *testing.T, path string) string {
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	require.NoError(t, err)

	return fmt.Sprintf("%x", hash.Sum(nil))
}
