package notifications

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/scitq/scitq/server/config"
)

const webhookTimeout = 10 * time.Second

// Webhook is a generic HTTP-POST notifier with a templated body.
// The template runs on the Message plus a small "toJSON" helper, so
// most JSON-eating endpoints (Slack, Discord, ntfy, custom) fit with
// one config line.
//
// Configuration:
//   url:      required, the endpoint URL
//   method:   optional, defaults to POST
//   template: optional Go text/template. Default: JSON with a
//             conventional shape ({event, severity, title, body, meta}).
//   content_type: optional, defaults to application/json
//   headers:  optional map, added to every request (e.g. Authorization)
//
// The template has access to:
//   {{.Event}} {{.Severity}} {{.Title}} {{.Body}} {{.Meta.foo}} …
//   {{toJSON .Body}}    -- JSON-quote a string
//   {{toJSON .Meta}}    -- JSON-serialise the meta map
type Webhook struct {
	name        string
	url         string
	method      string
	contentType string
	headers     map[string]string
	tmpl        *template.Template
	client      *http.Client
}

// defaultWebhookTemplate is the fallback body when the config doesn't
// override it. Emits a compact JSON object that any generic hook
// consumer can parse without extra config on their side.
const defaultWebhookTemplate = `{"event":{{toJSON .Event}},"severity":{{toJSON .Severity}},"title":{{toJSON .Title}},"body":{{toJSON .Body}},"meta":{{toJSON .Meta}}}`

func NewWebhook(cfg config.NotificationChannel) (*Webhook, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("url is required")
	}

	tmplText := cfg.Options["template"]
	if tmplText == "" {
		tmplText = defaultWebhookTemplate
	}
	tmpl, err := template.New(cfg.Name).Funcs(template.FuncMap{"toJSON": toJSON}).Parse(tmplText)
	if err != nil {
		return nil, fmt.Errorf("body template: %w", err)
	}

	method := strings.ToUpper(cfg.Options["method"])
	if method == "" {
		method = http.MethodPost
	}
	contentType := cfg.Options["content_type"]
	if contentType == "" {
		contentType = "application/json"
	}
	return &Webhook{
		name:        cfg.Name,
		url:         cfg.URL,
		method:      method,
		contentType: contentType,
		headers:     cfg.Headers,
		tmpl:        tmpl,
		client:      &http.Client{Timeout: webhookTimeout},
	}, nil
}

func (w *Webhook) Name() string { return w.name }

func (w *Webhook) Send(ctx context.Context, msg Message) error {
	var buf bytes.Buffer
	if err := w.tmpl.Execute(&buf, msg); err != nil {
		return fmt.Errorf("render body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, w.method, w.url, &buf)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", w.contentType)
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// toJSON renders any value as JSON. Used by the default body
// template to produce safely-quoted strings and dicts. On error
// (unlikely with the small set of Go types we hand it) it returns
// null so the surrounding JSON stays valid.
func toJSON(v any) string {
	b, err := jsonMarshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
