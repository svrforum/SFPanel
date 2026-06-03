package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// WebhookPayload is the JSON body posted to a generic webhook channel. It
// carries a Slack/Mattermost-compatible `text` field AND the structured alert
// fields, so an off-the-shelf chat incoming-webhook (which reads `text`) and a
// custom receiver (which reads the structured fields) both work without a
// per-target adapter.
type WebhookPayload struct {
	Text      string `json:"text"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Severity  string `json:"severity"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
}

// SendWebhook POSTs a JSON alert payload to an operator-configured URL. Unlike
// the Discord/Telegram channels (fixed hosts), this targets an arbitrary URL by
// design — homelab operators routinely point it at a self-hosted, LAN-internal
// Slack-compatible receiver, so we intentionally do NOT block private/loopback
// addresses (that would break the primary use case). We do require a
// well-formed http(s) URL and reuse the shared, timeout-bounded HTTP client.
func SendWebhook(webhookURL, title, message, severity string) error {
	u, err := url.Parse(webhookURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: webhook URL must be a valid http(s) URL", ErrChannelDelivery)
	}

	payload := WebhookPayload{
		Text:      fmt.Sprintf("[%s] %s\n%s", severity, title, message),
		Title:     title,
		Message:   message,
		Severity:  severity,
		Source:    "SFPanel",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: marshal payload", ErrChannelDelivery)
	}

	resp, err := alertHTTPClient.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: transport error", ErrChannelDelivery)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrChannelDelivery, resp.StatusCode)
	}
	return nil
}
