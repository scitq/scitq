package notifications

import (
	"context"
	"encoding/json"
	"log"

	"github.com/scitq/scitq/server/config"
)

// Log is a Notifier that writes to the standard server log. Always
// available, no config beyond name — useful as a smoke test channel
// or a "notify me on the console" for a lab operator without a
// separate integration.
type Log struct {
	name string
}

func NewLog(cfg config.NotificationChannel) *Log {
	return &Log{name: cfg.Name}
}

func (l *Log) Name() string { return l.name }

func (l *Log) Send(_ context.Context, msg Message) error {
	log.Printf("🔔 [%s] %s [%s] %s — %s", l.name, msg.Event, msg.Severity, msg.Title, msg.Body)
	return nil
}

// jsonMarshal is a tiny wrapper so webhook.go can call it without an
// encoding/json import (keeping the import graph explicit per file).
// Placed here rather than in webhook.go because Log is also the file
// that could legitimately want to render a JSON body one day.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
