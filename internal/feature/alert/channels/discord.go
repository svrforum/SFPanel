package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// alertHTTPClient is shared across all alert channel implementations.
var alertHTTPClient = &http.Client{Timeout: 10 * time.Second}

type DiscordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp"`
}

type DiscordPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []DiscordEmbed `json:"embeds"`
}

// isDiscordWebhook blocks SSRF attempts where an authenticated admin sets a
// webhook URL pointing at internal metadata endpoints or arbitrary hosts.
// The official webhook path is https://discord.com/api/webhooks/...; anything
// else is rejected.
func isDiscordWebhook(u string) bool {
	u = strings.TrimSpace(u)
	return strings.HasPrefix(u, "https://discord.com/api/webhooks/") ||
		strings.HasPrefix(u, "https://discordapp.com/api/webhooks/")
}

func SendDiscord(webhookURL, title, message, severity string) error {
	if !isDiscordWebhook(webhookURL) {
		return fmt.Errorf("invalid webhook URL: must point at discord.com/api/webhooks/")
	}
	color := 0x3182f6 // blue (info)
	switch severity {
	case "warning":
		color = 0xf59e0b // yellow
	case "critical":
		color = 0xef4444 // red
	}

	payload := DiscordPayload{
		Embeds: []DiscordEmbed{{
			Title:       title,
			Description: message,
			Color:       color,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: marshal payload", ErrChannelDelivery)
	}
	return sendDiscordTo(webhookURL, string(body))
}

// sendDiscordTo posts a pre-built JSON payload to the given Discord webhook URL.
// Errors are wrapped with ErrChannelDelivery and a fixed phrase so the
// caller-visible message never includes the webhook secret path.
//
// The second argument is the marshaled JSON payload as a string; the helper
// must not introduce the URL into the error path. For the test helper the
// payload value is irrelevant — the transport fails before the body is parsed.
func sendDiscordTo(webhookURL, jsonBody string) error {
	resp, err := alertHTTPClient.Post(webhookURL, "application/json", bytes.NewReader([]byte(jsonBody)))
	if err != nil {
		return fmt.Errorf("%w: transport error", ErrChannelDelivery)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrChannelDelivery, resp.StatusCode)
	}
	return nil
}
