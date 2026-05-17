package alert

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestListChannels_MasksSecrets verifies the GET /alerts/channels HTTP
// response strips/masks Discord webhook URLs and Telegram bot tokens before
// returning. The audit that prompted this test found the prior handler
// returned the raw `config` column, so a captured browser session or a
// stale log file could leak credentials that operators would then have to
// rotate.
func TestListChannels_MasksSecrets(t *testing.T) {
	db := openAlertTestDB(t)
	db.Exec(`CREATE TABLE IF NOT EXISTS alert_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT, name TEXT,
		config TEXT, enabled INTEGER, created_at TEXT)`)
	const webhook = "https://discord.com/api/webhooks/9999/AAAABBBBCCCCDDDD"
	const botToken = "987654:TOKEN-tail-secret-9999"
	db.Exec(`INSERT INTO alert_channels (type,name,config,enabled,created_at) VALUES
		('discord','prod', ?, 1, '2026-05-17'),
		('telegram','ops', ?, 1, '2026-05-17')`,
		`{"webhook_url":"`+webhook+`"}`,
		`{"bot_token":"`+botToken+`","chat_id":"-100123"}`,
	)

	h := &Handler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/alerts/channels", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.ListChannels(c); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}

	body := rec.Body.String()
	// Raw secrets must NOT appear anywhere in the response.
	for _, leak := range []string{webhook, "AAAABBBBCCCC", botToken, "TOKEN-tail-secret"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaked secret %q: %s", leak, body)
		}
	}
	// Channel identifying info MUST still be present so the UI can render
	// a meaningful list (name + type + masked-tail).
	for _, keep := range []string{"prod", "ops", "discord", "telegram"} {
		if !strings.Contains(body, keep) {
			t.Errorf("response missing %q: %s", keep, body)
		}
	}

	// Parse the response shape to confirm the masked config is still valid
	// JSON the frontend can render.
	var env struct {
		Success bool           `json:"success"`
		Data    []AlertChannel `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data) != 2 {
		t.Fatalf("got %d channels, want 2", len(env.Data))
	}
	// Each channel's config must still parse as JSON.
	for _, ch := range env.Data {
		var probe map[string]any
		if err := json.Unmarshal([]byte(ch.Config), &probe); err != nil {
			t.Errorf("channel %q config not JSON after masking: %v (%s)", ch.Name, err, ch.Config)
		}
	}
}
