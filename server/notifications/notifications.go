// Package notifications sends user-facing convenience notifications
// (workflow completion, ...) to configured channels. It is
// deliberately NOT the admin monitoring path — leaked workers, DB
// pool saturation, quota exhaustion etc. surface through
// server/metrics as Prometheus gauges/counters that Zabbix / Grafana
// consume. Alerting logic (silence, escalate, page on-call) lives in
// the monitoring tool.
//
// This package is best-effort: a failed webhook is logged and the
// caller keeps going. Users who miss a workflow-done ping are mildly
// annoyed; that's the whole failure surface.
//
// Backends:
//
//   - zulip: knows the Zulip incoming-webhook JSON shape natively.
//     Just pass the webhook URL (with stream + topic + api_key as URL
//     params, per Zulip's docs).
//   - webhook: generic HTTP POST with a Go-template body. Fits Slack,
//     Discord, ntfy, or any custom endpoint that accepts JSON or text.
//   - log: writes the notification to the server log. Always
//     available; useful as a smoke test or as a fallback channel.
//
// The Dispatcher reads a route table (event -> channels) and fans
// each Emit() call out to every subscribed channel in parallel. No
// retries, no dedup, no rate limit for now — add those the day we
// need them.
package notifications

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/scitq/scitq/server/config"
)

// Severity ranks the message. Backends can filter on it; the initial
// backends ignore it. Kept in the interface from day one so a future
// "warn+ only" channel doesn't force a signature change.
type Severity string

const (
	SeverityInfo Severity = "info"
	SeverityWarn Severity = "warn"
	SeverityCrit Severity = "crit"
)

// EventType is the routing key. Backends never look at this — the
// Dispatcher does, matching against the configured routes.
type EventType string

const (
	// EventWorkflowTerminal fires when a workflow transitions to S or
	// F. Meta carries workflow_id, name, status, and counts.
	EventWorkflowTerminal EventType = "workflow.terminal"
)

// Message is one notification. Backends turn (Title, Body, Meta)
// into their own on-the-wire format. Keep Body compact — most
// channels line-truncate at some point.
type Message struct {
	Event    EventType
	Severity Severity
	Title    string
	Body     string
	// Meta is arbitrary key/value context. The Zulip backend ignores
	// it (Body carries the human-readable text); the generic webhook
	// backend exposes it to its body template as `.Meta.<key>`.
	Meta map[string]string
}

// Notifier is one delivery target.
type Notifier interface {
	// Name is the channel name from config — used in log lines and
	// as the routing key.
	Name() string
	// Send delivers the message. Errors are logged and swallowed by
	// the Dispatcher — a broken webhook must never bring down the
	// caller's code path.
	Send(ctx context.Context, msg Message) error
}

// Dispatcher owns the channel set + routing table and dispatches
// events to matching channels in parallel.
type Dispatcher struct {
	channels map[string]Notifier
	// routes: event -> list of channel names.
	routes map[EventType][]string
	// alwaysLog controls the built-in log fallback: every message is
	// also written to the server log regardless of channel routing.
	// Handy for a smoke test / audit trail; off by default.
	alwaysLog bool
}

// New builds a Dispatcher from the given config block. Unknown
// backend kinds and malformed URLs are logged and skipped rather than
// failing server startup — a bad notification channel is not a reason
// to refuse to boot.
func New(cfg config.NotificationsConfig) *Dispatcher {
	d := &Dispatcher{
		channels:  make(map[string]Notifier),
		routes:    make(map[EventType][]string),
		alwaysLog: cfg.AlwaysLog,
	}

	for _, ch := range cfg.Channels {
		if ch.Name == "" {
			log.Printf("⚠️ notifications: skipping unnamed channel entry")
			continue
		}
		if _, dupe := d.channels[ch.Name]; dupe {
			log.Printf("⚠️ notifications: duplicate channel name %q — later entry wins", ch.Name)
		}
		n, err := buildBackend(ch)
		if err != nil {
			log.Printf("⚠️ notifications: channel %q (kind=%q) rejected: %v", ch.Name, ch.Kind, err)
			continue
		}
		d.channels[ch.Name] = n
		log.Printf("🔔 notifications: channel %q ready (kind=%s)", ch.Name, ch.Kind)
	}

	for _, r := range cfg.Routes {
		if r.Event == "" {
			log.Printf("⚠️ notifications: skipping route entry with no event")
			continue
		}
		for _, chName := range r.Channels {
			if _, ok := d.channels[chName]; !ok {
				log.Printf("⚠️ notifications: route %q references unknown channel %q — dropped", r.Event, chName)
				continue
			}
			d.routes[EventType(r.Event)] = append(d.routes[EventType(r.Event)], chName)
		}
	}

	return d
}

// buildBackend instantiates the concrete Notifier for a channel spec.
func buildBackend(ch config.NotificationChannel) (Notifier, error) {
	switch strings.ToLower(ch.Kind) {
	case "zulip":
		return NewZulip(ch)
	case "zulip-dm":
		return NewZulipDM(ch)
	case "webhook":
		return NewWebhook(ch)
	case "log":
		return NewLog(ch), nil
	default:
		return nil, fmt.Errorf("unknown backend kind %q (want zulip / zulip-dm / webhook / log)", ch.Kind)
	}
}

// Emit dispatches msg to every channel subscribed to msg.Event.
// Deliveries run in parallel; the call returns as soon as they are
// launched. Errors are logged inside each backend's Send — this
// method never returns one because callers on hot paths (workflow
// completion) shouldn't be blocked by a slow webhook.
//
// The caller's ctx is INTENTIONALLY not passed down to the send
// goroutines. Callers on the workflow-status path build a ctx with a
// short timeout for their DB queries and defer-cancel it on return;
// since Emit doesn't wg.Wait, that cancel fires while the HTTP
// request is still in flight and kills it with "context canceled"
// (observed 2026-09-10 on the very first live workflow.terminal to
// Zulip). Each backend caps request duration via its own
// http.Client.Timeout, so context.Background here is both safe and
// necessary.
func (d *Dispatcher) Emit(ctx context.Context, msg Message) {
	if d == nil {
		return
	}
	if d.alwaysLog {
		log.Printf("🔔 %s [%s] %s — %s", msg.Event, msg.Severity, msg.Title, msg.Body)
	}
	_ = ctx // intentionally not forwarded; see comment above.
	targets := d.routes[msg.Event]
	if len(targets) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, name := range targets {
		n, ok := d.channels[name]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(n Notifier) {
			defer wg.Done()
			if err := n.Send(context.Background(), msg); err != nil {
				log.Printf("⚠️ notifications: channel %q rejected %s: %v", n.Name(), msg.Event, err)
			}
		}(n)
	}
	// Best-effort: we do NOT wg.Wait() on purpose. Fire-and-forget so
	// the caller (workflow status recomputer, etc.) returns immediately.
	// The goroutines outlive this call and finish on their own.
}
