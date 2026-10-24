package server

import (
	"context"
	"testing"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"google.golang.org/grpc/metadata"
)

func TestAuditActor(t *testing.T) {
	user := context.WithValue(context.Background(), userContextKey, &AuthenticatedUser{UserID: 7, Username: "alice"})

	cases := []struct {
		name                 string
		ctx                  context.Context
		ok                   bool
		kind, actor, channel string
		client               string
	}{
		{"worker token", context.Background(), false, "", "", "", ""},
		{"cli", user, true, "user", "alice", "cli", ""},
		{"ui", metadata.NewIncomingContext(user, metadata.Pairs("x-grpc-web", "1")), true, "user", "alice", "ui", ""},
		{"mcp", withAuditChannel(user, "mcp", "claude-code"), true, "user", "alice", "mcp", "claude-code"},
		{"scheduler", withSchedulerActor(context.Background(), "idle watchdog"), true, "scheduler", "idle watchdog", "scheduler", ""},
	}
	for _, tc := range cases {
		a, ok := auditActor(tc.ctx)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if a.Kind != tc.kind || a.Name != tc.actor || a.Channel != tc.channel || a.Client != tc.client {
			t.Errorf("%s: got %+v, want kind=%s name=%s channel=%s client=%q", tc.name, a, tc.kind, tc.actor, tc.channel, tc.client)
		}
	}
}

func TestRequestDetailsKeepsOnlySentFields(t *testing.T) {
	step := int32(75230)
	d := requestDetails(&pb.WorkerUpdateRequest{WorkerId: 5223, StepId: &step})
	if d["worker_id"] != float64(5223) || d["step_id"] != float64(75230) {
		t.Fatalf("expected worker_id and step_id, got %v", d)
	}
	if _, present := d["concurrency"]; present {
		t.Fatalf("unset optional fields must be omitted, got %v", d)
	}
}
