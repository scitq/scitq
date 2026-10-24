// Package audit records decisions made on scitq objects — who or what
// changed a worker, task, workflow, recruiter or template, and through which
// channel — in the audit_event table.
package audit

import (
	"database/sql"
	"encoding/json"
	"log"
)

// Actor identifies who made a decision.
type Actor struct {
	Kind    string // "user" or "scheduler"
	Name    string // username, or scheduler component
	Channel string // "cli", "ui", "mcp" or "scheduler"
	Client  string // MCP client name when known
}

// Scheduler is the actor for decisions scitq takes on its own.
func Scheduler(component string) Actor {
	return Actor{Kind: "scheduler", Name: component, Channel: "scheduler"}
}

// Execer is satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Record writes one audit event. Recording is best-effort: a failure is
// logged and never fails the audited operation.
func Record(db Execer, a Actor, action, objectKind string, objectID int32, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	dj, err := json.Marshal(details)
	if err != nil {
		log.Printf("⚠️ audit: cannot encode details for %s %s %d: %v", action, objectKind, objectID, err)
		dj = []byte("{}")
	}
	var client any
	if a.Client != "" {
		client = a.Client
	}
	if _, err := db.Exec(`
		INSERT INTO audit_event (actor_kind, actor_name, channel, client, action, object_kind, object_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.Kind, a.Name, a.Channel, client, action, objectKind, objectID, dj,
	); err != nil {
		log.Printf("⚠️ audit: failed to record %s on %s %d: %v", action, objectKind, objectID, err)
	}
}
