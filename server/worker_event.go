package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

func (s *taskQueueServer) ReportWorkerEvent(ctx context.Context, e *pb.WorkerEvent) (*pb.Ack, error) {
	const q = `
		INSERT INTO worker_event (worker_id, worker_name, level, event_class, message, details_json)
		VALUES ($1,$2,$3,$4,$5, COALESCE($6,'null'::jsonb))`
	var details *string
	if e.DetailsJson != "" {
		details = &e.DetailsJson
	}
	if _, err := s.db.ExecContext(ctx, q,
		e.WorkerId, e.WorkerName, e.Level, e.EventClass, e.Message, details,
	); err != nil {
		// On error, return a negative Ack if your schema supports it; otherwise just return the error.
		// return &pb.Ack{Ok: false, Message: err.Error()}, nil
		return nil, err
	}
	return &pb.Ack{Success: true}, nil // adapt field names to your existing Ack
}

func (s *taskQueueServer) ListWorkerEvents(ctx context.Context, f *pb.WorkerEventFilter) (*pb.WorkerEventList, error) {
	// Defaults
	limit := int32(50)
	if f.Limit != nil && *f.Limit > 0 {
		limit = *f.Limit
		if limit > 1000 { // hard cap to avoid huge responses
			limit = 1000
		}
	}

	// Build WHERE clauses
	query := `
		SELECT
			event_id,
			created_at,
			worker_id,
			worker_name,
			level,
			event_class,
			message,
			details_json
		FROM worker_event
	`
	where := []string{}
	args := []any{}

	if f.WorkerId != nil && *f.WorkerId != 0 {
		where = append(where, "worker_id = $"+itoa(len(args)+1))
		args = append(args, *f.WorkerId)
	}
	if f.Level != nil && *f.Level != "" {
		// expecting single-letter 'D','I','W','E'
		where = append(where, "level = $"+itoa(len(args)+1))
		args = append(args, *f.Level)
	}
	if f.Class != nil && *f.Class != "" {
		// exact match on event_class; switch to ILIKE if you later want case-insensitive
		where = append(where, "event_class = $"+itoa(len(args)+1))
		args = append(args, *f.Class)
	}

	if len(where) > 0 {
		query += " WHERE " + join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"
	query += " LIMIT $" + itoa(len(args)+1)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &pb.WorkerEventList{Events: []*pb.WorkerEventRecord{}}
	for rows.Next() {
		var (
			eventID    int32
			createdAt  time.Time
			workerID   sql.NullInt32
			workerName sql.NullString
			level      string
			class      string
			message    string
			details    sql.NullString
		)

		if err := rows.Scan(
			&eventID,
			&createdAt,
			&workerID,
			&workerName,
			&level,
			&class,
			&message,
			&details,
		); err != nil {
			return nil, err
		}

		rec := &pb.WorkerEventRecord{
			EventId:    eventID,
			CreatedAt:  createdAt.UTC().Format(time.RFC3339),
			WorkerName: workerName.String,
			Level:      level,
			EventClass: class,
			Message:    message,
		}
		if workerID.Valid {
			id := workerID.Int32
			rec.WorkerId = &id
		}
		if details.Valid {
			rec.DetailsJson = details.String
		}

		out.Events = append(out.Events, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// small helpers to avoid importing strings for two tiny ops
func join(parts []string, sep string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	n := len(sep) * (len(parts) - 1)
	for i := 0; i < len(parts); i++ {
		n += len(parts[i])
	}
	b := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, p...)
	}
	return string(b)
}
func itoa(i int) string {
	// tiny int→string to avoid strconv import for a single use
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func (s *taskQueueServer) DeleteWorkerEvent(ctx context.Context, req *pb.WorkerEventId) (*pb.Ack, error) {
	_, err := s.db.ExecContext(ctx, `DELETE FROM worker_event WHERE event_id = $1`, req.EventId)
	if err != nil {
		return nil, err
	}
	return &pb.Ack{Success: true}, nil // adapt to your Ack fields if different
}

func (s *taskQueueServer) PruneWorkerEvents(ctx context.Context, f *pb.WorkerEventPruneFilter) (*pb.WorkerEventPruneResult, error) {
	// Build WHERE
	where := []string{}
	args := []any{}

	// before (RFC3339)
	if f.Before != nil && *f.Before != "" {
		ts, err := time.Parse(time.RFC3339, *f.Before)
		if err != nil {
			return nil, fmt.Errorf("invalid 'before' timestamp: %w", err)
		}
		where = append(where, "created_at < $"+itoa(len(args)+1))
		args = append(args, ts.UTC())
	} else if !f.DryRun {
		// defensive: require an explicit cutoff for destructive prune
		return nil, fmt.Errorf("refusing destructive prune without 'before' cutoff; use dry-run first")
	}

	if f.Level != nil && *f.Level != "" {
		where = append(where, "level = $"+itoa(len(args)+1))
		args = append(args, *f.Level)
	}
	if f.Class != nil && *f.Class != "" {
		where = append(where, "event_class = $"+itoa(len(args)+1))
		args = append(args, *f.Class)
	}
	if f.WorkerId != nil && *f.WorkerId != 0 {
		where = append(where, "worker_id = $"+itoa(len(args)+1))
		args = append(args, *f.WorkerId)
	}

	queryWhere := ""
	if len(where) > 0 {
		queryWhere = " WHERE " + strings.Join(where, " AND ")
	}

	// Dry run → COUNT(*)
	if f.DryRun {
		var cnt int32
		q := "SELECT COUNT(*) FROM worker_event" + queryWhere
		if err := s.db.QueryRowContext(ctx, q, args...).Scan(&cnt); err != nil {
			return nil, err
		}
		return &pb.WorkerEventPruneResult{Matched: cnt, Deleted: 0}, nil
	}

	// Destructive: DELETE and return RowsAffected
	q := "DELETE FROM worker_event" + queryWhere
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	aff, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	return &pb.WorkerEventPruneResult{Matched: int32(aff), Deleted: int32(aff)}, nil
}
