package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type TelegramPayload struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

func SendTelegram(botToken, chatID, title, message, severity string) error {
	emoji := "ℹ️"
	switch severity {
	case "warning":
		emoji = "⚠️"
	case "critical":
		emoji = "🚨"
	}

	text := fmt.Sprintf("%s *%s*\n\n%s", emoji, title, message)

	payload := TelegramPayload{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "Markdown",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram api returned %d", resp.StatusCode)
	}
	return nil
}
