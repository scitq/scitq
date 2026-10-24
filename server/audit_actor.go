package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/audit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type auditChannelKey struct{}

type auditSchedulerKey struct{}

// withSchedulerActor marks a context as carrying a decision scitq takes on
// its own (e.g. the idle watchdog deleting a worker).
func withSchedulerActor(ctx context.Context, component string) context.Context {
	return context.WithValue(ctx, auditSchedulerKey{}, component)
}

type auditChannel struct {
	channel string
	client  string
}

// withAuditChannel marks a context as coming through a given channel. The
// MCP handler sets it for its in-process calls; gRPC calls are classified
// from their metadata instead.
func withAuditChannel(ctx context.Context, channel, client string) context.Context {
	return context.WithValue(ctx, auditChannelKey{}, auditChannel{channel: channel, client: client})
}

// auditActor returns who is behind a request: a scheduler component when the
// context carries one, otherwise the authenticated user. It returns false for
// calls made with the worker token (worker-reported lifecycle is not audited)
// and for internal calls that carry no decision of their own.
func auditActor(ctx context.Context) (audit.Actor, bool) {
	if component, ok := ctx.Value(auditSchedulerKey{}).(string); ok {
		return audit.Scheduler(component), true
	}
	u := GetUserFromContext(ctx)
	if u == nil {
		return audit.Actor{}, false
	}
	a := audit.Actor{Kind: "user", Name: u.Username, Channel: "cli"}
	if c, ok := ctx.Value(auditChannelKey{}).(auditChannel); ok {
		a.Channel, a.Client = c.channel, c.client
	} else if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get("x-grpc-web")) > 0 {
		a.Channel = "ui"
	}
	return a, true
}

// audit records a decision taken by the actor behind ctx (see auditActor).
func (s *taskQueueServer) audit(ctx context.Context, action, objectKind string, objectID int32, details map[string]any) {
	if a, ok := auditActor(ctx); ok {
		audit.Record(s.db, a, action, objectKind, objectID, details)
	}
}

// requestDetails returns the fields a request actually carried, keyed by
// their proto names, for use as audit details.
func requestDetails(m proto.Message) map[string]any {
	d := map[string]any{}
	if b, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(m); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	return d
}

// ListAuditEvents returns audit events, newest first, filtered by object,
// actor and time.
func (s *taskQueueServer) ListAuditEvents(ctx context.Context, req *pb.AuditFilter) (*pb.AuditEventList, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.ObjectId != nil && req.ObjectKind == nil {
		return nil, status.Error(codes.InvalidArgument, "object_id requires object_kind")
	}
	limit := int32(100)
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT audit_id, created_at, actor_kind, actor_name, channel, client,
		       action, object_kind, object_id, details::text
		FROM audit_event
		WHERE ($1::TEXT IS NULL OR object_kind = $1)
		  AND ($2::INT IS NULL OR object_id = $2)
		  AND ($3::TEXT IS NULL OR actor_name = $3)
		  AND ($4::BIGINT IS NULL OR created_at >= to_timestamp($4))
		ORDER BY created_at DESC, audit_id DESC
		LIMIT $5`,
		req.ObjectKind, req.ObjectId, req.Actor, req.SinceEpoch, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit events: %w", err)
	}
	defer rows.Close()

	res := &pb.AuditEventList{}
	for rows.Next() {
		var e pb.AuditEvent
		var createdAt time.Time
		var client sql.NullString
		if err := rows.Scan(&e.AuditId, &createdAt, &e.ActorKind, &e.ActorName, &e.Channel, &client,
			&e.Action, &e.ObjectKind, &e.ObjectId, &e.DetailsJson); err != nil {
			return nil, fmt.Errorf("failed to scan audit event: %w", err)
		}
		e.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		if client.Valid {
			e.Client = &client.String
		}
		res.Events = append(res.Events, &e)
	}
	return res, rows.Err()
}

// PruneAuditEvents deletes audit events older than the given number of days.
func (s *taskQueueServer) PruneAuditEvents(ctx context.Context, req *pb.AuditPruneRequest) (*pb.AuditPruneResult, error) {
	if !IsAdmin(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin privileges required")
	}
	if req.OlderThanDays <= 0 {
		return nil, status.Error(codes.InvalidArgument, "older_than_days must be > 0")
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM audit_event WHERE created_at < NOW() - make_interval(days => $1)`, req.OlderThanDays)
	if err != nil {
		return nil, fmt.Errorf("failed to prune audit events: %w", err)
	}
	n, _ := res.RowsAffected()
	return &pb.AuditPruneResult{Deleted: n}, nil
}
