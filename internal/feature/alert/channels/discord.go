package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

func SendDiscord(webhookURL, title, message, severity string) error {
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
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	resp, err := alertHTTPClient.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord webhook returned %d", resp.StatusCode)
	}
	return nil
}
