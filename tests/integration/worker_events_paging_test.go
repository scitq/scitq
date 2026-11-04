package integration_test

import (
	"context"
	"database/sql"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// ListWorkerEvents pages newest first with before_event_id, within a level
// filter, until no event remains.
func TestListWorkerEventsPaging(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	qclient, err := lib.CreateClient(serverAddr, extractToken(out))
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	var workerID int32
	require.NoError(t, db.QueryRow(`INSERT INTO worker (worker_name, concurrency) VALUES ('events-w', 1) RETURNING worker_id`).Scan(&workerID))
	// 25 events; every fifth is a warning.
	_, err = db.Exec(`INSERT INTO worker_event (worker_id, event_class, level, message)
		SELECT $1, 'phase', CASE WHEN i % 5 = 0 THEN 'W' ELSE 'I' END, 'event ' || i
		FROM generate_series(1, 25) AS i`, workerID)
	require.NoError(t, err)

	pages := func(level string) (all []string, sizes []int) {
		var before *int32
		for {
			f := &pb.WorkerEventFilter{WorkerId: &workerID, Limit: int32Ptr(10), BeforeEventId: before}
			if level != "" {
				f.Level = &level
			}
			res, err := qc.ListWorkerEvents(ctx, f)
			require.NoError(t, err)
			sizes = append(sizes, len(res.Events))
			for _, e := range res.Events {
				all = append(all, e.Message)
			}
			if len(res.Events) < 10 {
				return all, sizes
			}
			before = &res.Events[len(res.Events)-1].EventId
		}
	}

	all, sizes := pages("")
	require.Equal(t, []int{10, 10, 5}, sizes)
	require.Len(t, all, 25)
	require.Equal(t, "event 25", all[0])
	require.Equal(t, "event 1", all[24])

	warnings, _ := pages("W")
	require.Equal(t, []string{"event 25", "event 20", "event 15", "event 10", "event 5"}, warnings)
}
