package notifications

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/scitq/scitq/server/config"
)

const zulipDMTimeout = 10 * time.Second

// ZulipDM is the Notifier that sends private (direct) messages via
// Zulip's `messages` REST API. Distinct from the `zulip` backend
// (which POSTs to /api/v1/external/generic for stream messages) —
// incoming-webhook bots CANNOT hit /api/v1/messages, so this backend
// requires a Zulip bot of type "Generic bot" whose bot_email +
// api_key are given in the channel config.
//
// The recipient email is not baked into the channel — it's read from
// each Message's `Meta["run_by_email"]` at Send time. That's what
// makes DM routing per-workflow-owner work without a static
// channel-per-user setup: one `zulip-dm` channel serves every user
// whose scitq account has an email address set.
//
// Config:
//
//   channels:
//     - name: gmt-zulip-dm
//       kind: zulip-dm
//       url: "https://gmt.zulipchat.com"       # Zulip realm, no path
//       options:
//         bot_email: "scitq-bot@gmt.zulipchat.com"
//         api_key: "${ZULIP_API_KEY}"
//         # recipient_meta_key defaults to "run_by_email"; override if
//         # a future event carries the recipient under a different key.
//         recipient_meta_key: "run_by_email"
//
// If the message's Meta lacks the recipient key (or has it blank),
// Send logs a one-liner and returns nil — a user with no email set
// is not an error, just a "no target for this notification". No
// retries; the caller doesn't wait on us anyway.
type ZulipDM struct {
	name             string
	realm            string
	botEmail         string
	apiKey           string
	recipientMetaKey string
	client           *http.Client
}

func NewZulipDM(cfg config.NotificationChannel) (*ZulipDM, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("url (Zulip realm) is required")
	}
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	botEmail := cfg.Options["bot_email"]
	apiKey := cfg.Options["api_key"]
	if botEmail == "" || apiKey == "" {
		return nil, fmt.Errorf("options.bot_email and options.api_key are both required")
	}
	recipientKey := cfg.Options["recipient_meta_key"]
	if recipientKey == "" {
		recipientKey = "run_by_email"
	}
	// Trim trailing slash on realm so the POST URL is well-formed
	// whether the operator wrote https://foo or https://foo/.
	realm := strings.TrimRight(cfg.URL, "/")

	return &ZulipDM{
		name:             cfg.Name,
		realm:            realm,
		botEmail:         botEmail,
		apiKey:           apiKey,
		recipientMetaKey: recipientKey,
		client:           &http.Client{Timeout: zulipDMTimeout},
	}, nil
}

func (z *ZulipDM) Name() string { return z.name }

func (z *ZulipDM) Send(ctx context.Context, msg Message) error {
	recipient := ""
	if msg.Meta != nil {
		recipient = msg.Meta[z.recipientMetaKey]
	}
	if recipient == "" {
		// Not an error — just no address for this event. Common when
		// a user hasn't filled in their scitq_user.email. Log so the
		// admin sees it (once); return nil to keep the dispatcher
		// silent from the caller's perspective.
		log.Printf("🔔 notifications: channel %q dropped %s (no %s in message meta)", z.name, msg.Event, z.recipientMetaKey)
		return nil
	}

	// Same content shape as the stream Zulip backend: **Title**
	// on the first line, blank line, then Body. Zulip renders **bold**
	// so the subject line stands out at the top of the DM view.
	content := msg.Body
	if msg.Title != "" {
		if content == "" {
			content = "**" + msg.Title + "**"
		} else {
			content = "**" + msg.Title + "**\n\n" + content
		}
	}

	// Zulip's messages API expects "to" as a JSON array literal for
	// private (direct) messages when addressing by email. See
	// https://zulip.com/api/send-message — the form field is a JSON
	// string. Multiple recipients would be a comma-separated list
	// inside the array; we only ever DM one owner here.
	form := url.Values{}
	form.Set("type", "private")
	form.Set("to", fmt.Sprintf(`["%s"]`, recipient))
	form.Set("content", content)

	endpoint := z.realm + "/api/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(z.botEmail, z.apiKey)

	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("zulip returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
