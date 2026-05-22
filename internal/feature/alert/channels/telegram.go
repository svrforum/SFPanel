package channels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
)

type TelegramPayload struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// SendTelegram uses HTML parse mode because rule names and metric values can
// contain the markdown metacharacters (`_`, `*`, `` ` ``, `[`) that would
// otherwise make the API reject the message. HTML is the one Telegram parse
// mode where we only have to escape `<`, `>`, and `&`.
func SendTelegram(botToken, chatID, title, message, severity string) error {
	emoji := "ℹ️"
	switch severity {
	case "warning":
		emoji = "⚠️"
	case "critical":
		emoji = "🚨"
	}

	text := fmt.Sprintf("%s <b>%s</b>\n\n%s", emoji, html.EscapeString(title), html.EscapeString(message))

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	return sendTelegramTo(url, chatID, text)
}

// sendTelegramTo posts a pre-built text payload to the given Telegram API URL.
// Errors are wrapped with ErrChannelDelivery and a fixed phrase so that the
// caller-visible message never includes the bot token embedded in the URL.
func sendTelegramTo(url, chatID, text string) error {
	payload := TelegramPayload{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: marshal payload", ErrChannelDelivery)
	}

	resp, err := alertHTTPClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: transport error", ErrChannelDelivery)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrChannelDelivery, resp.StatusCode)
	}
	return nil
}
