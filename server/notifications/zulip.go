package notifications

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/scitq/scitq/server/config"
)

// zulipTimeout: Zulip's API rarely takes more than a second, but we
// don't want a stalled webhook holding a goroutine indefinitely.
const zulipTimeout = 10 * time.Second

// Zulip is the incoming-webhook Notifier. Configure with the URL
// Zulip's "Incoming webhook (generic)" integration hands out — it
// looks like:
//
//   https://<subdomain>.zulipchat.com/api/v1/external/generic?api_key=...&stream=alerts&topic=scitq
//
// The URL carries stream + topic + api_key as query params. We only
// need to POST `content` (the message body) and optionally `topic`
// (overrides the one baked into the URL). See:
// https://zulip.com/integrations/doc/generic
type Zulip struct {
	name   string
	url    string
	client *http.Client
	// topicOverride: when set, sent as the `topic` form field on every
	// message (overrides the URL's default topic). Useful when one
	// integration URL covers many events and each event wants its own
	// thread.
	topicOverride string
}

// NewZulip builds a Zulip notifier from a channel config entry.
// Required: cfg.URL. Optional: cfg.Options["topic"] for a per-channel
// topic override.
func NewZulip(cfg config.NotificationChannel) (*Zulip, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	return &Zulip{
		name:          cfg.Name,
		url:           cfg.URL,
		client:        &http.Client{Timeout: zulipTimeout},
		topicOverride: cfg.Options["topic"],
	}, nil
}

func (z *Zulip) Name() string { return z.name }

func (z *Zulip) Send(ctx context.Context, msg Message) error {
	// Body format: **Title**\n\nBody. Zulip renders **bold** in
	// message content — makes the title stand out in the stream
	// without needing separate widgets.
	content := msg.Body
	if msg.Title != "" {
		if content == "" {
			content = "**" + msg.Title + "**"
		} else {
			content = "**" + msg.Title + "**\n\n" + content
		}
	}

	form := url.Values{}
	form.Set("content", content)
	if z.topicOverride != "" {
		form.Set("topic", z.topicOverride)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, z.url, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// Read enough of the body to make the error actionable but
		// don't slurp arbitrary size — capped to 1 KiB.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("zulip returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
