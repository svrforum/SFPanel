package alert

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
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

// insertChannel stores a channel row directly, bypassing the handlers.
func insertChannel(t *testing.T, db *sql.DB, name, typ, config string, enabled int) int64 {
	t.Helper()
	res, err := db.Exec("INSERT INTO alert_channels (type, name, config, enabled) VALUES (?, ?, ?, ?)", typ, name, config, enabled)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// failure decodes an error envelope, so a refusal is checked for its reason
// and not only for its status: a body that fails to bind is a 400 as well,
// and that is the failure #116 was.
func failure(t *testing.T, rec *httptest.ResponseRecorder) response.ErrorBody {
	t.Helper()
	var env response.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error == nil {
		t.Fatalf("status %d, not an error envelope: %s", rec.Code, rec.Body)
	}
	return *env.Error
}

// TestCreateChannel_AcceptsEncodedConfig keeps the form this handler always
// took — config as a string of JSON — working for API callers, and stores
// what the string holds rather than the string: a JSON string in the column
// would pass json.Valid, but the manager cannot read a channel out of one.
func TestCreateChannel_AcceptsEncodedConfig(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	rec := callJSON(t, h.CreateChannel, http.MethodPost,
		`{"name":"api","type":"webhook","config":"{\"webhook_url\":\"https://hooks.example.com/api\"}"}`, 0)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	var id int64
	if err := db.QueryRow("SELECT id FROM alert_channels WHERE name='api'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if got := storedChannelConfig(t, db, id)["webhook_url"]; got != "https://hooks.example.com/api" {
		t.Errorf("stored webhook_url %q", got)
	}
}

// TestUpdateChannel_AcceptsConfigInEitherForm: the PUT body declared config
// as a string too, so an object config failed there exactly as it did on
// create. An absent, null or empty config still leaves the stored one alone.
func TestUpdateChannel_AcceptsConfigInEitherForm(t *testing.T) {
	const old = "https://hooks.example.com/old"
	cases := []struct{ name, body, want string }{
		{"object", `{"config":{"webhook_url":"https://hooks.example.com/a"}}`, "https://hooks.example.com/a"},
		{"encoded string", `{"config":"{\"webhook_url\":\"https://hooks.example.com/b\"}"}`, "https://hooks.example.com/b"},
		{"absent", `{"name":"renamed"}`, old},
		{"null", `{"config":null}`, old},
		{"empty string", `{"config":""}`, old},
	}
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := insertChannel(t, db, "hook", "webhook", `{"webhook_url":"`+old+`"}`, 1)
			rec := callJSON(t, h.UpdateChannel, http.MethodPut, tc.body, int(id))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
			}
			if got := storedChannelConfig(t, db, id)["webhook_url"]; got != tc.want {
				t.Errorf("stored webhook_url %q, want %q", got, tc.want)
			}
		})
	}
}

// TestChannelConfig_RefusesWhatIsNotAnObject: whichever form config arrives
// in, what gets stored has to be a JSON object, because that is the only
// thing the manager and the test-send can read a channel out of.
func TestChannelConfig_RefusesWhatIsNotAnObject(t *testing.T) {
	cases := []struct {
		name, field string
		onUpdate    bool // absent and null mean "keep the stored config" on update
	}{
		{"absent", ``, false},
		{"null", `,"config":null`, false},
		{"empty string", `,"config":""`, false},
		{"array", `,"config":[1]`, true},
		{"number", `,"config":5`, true},
		{"string that is not JSON", `,"config":"not json"`, true},
		{"string holding an array", `,"config":"[1]"`, true},
		{"string holding a string", `,"config":"\"x\""`, true},
	}
	const old = `{"webhook_url":"https://hooks.example.com/old"}`
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	id := insertChannel(t, db, "kept", "webhook", old, 1)
	for _, tc := range cases {
		t.Run("create/"+tc.name, func(t *testing.T) {
			rec := callJSON(t, h.CreateChannel, http.MethodPost, `{"name":"n","type":"webhook"`+tc.field+`}`, 0)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
			}
			if e := failure(t, rec); e.Code != response.ErrInvalidValue || e.Message != "config must be a JSON object" {
				t.Errorf("refused with %s %q, want the config reason", e.Code, e.Message)
			}
		})
		if !tc.onUpdate {
			continue
		}
		t.Run("update/"+tc.name, func(t *testing.T) {
			rec := callJSON(t, h.UpdateChannel, http.MethodPut, `{"enabled":true`+tc.field+`}`, int(id))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
			}
			if e := failure(t, rec); e.Code != response.ErrInvalidValue || e.Message != "config must be a JSON object" {
				t.Errorf("refused with %s %q, want the config reason", e.Code, e.Message)
			}
		})
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM alert_channels").Scan(&n); err != nil || n != 1 {
		t.Errorf("%d channels after refusals (%v), want only the seeded one", n, err)
	}
	var config string
	if err := db.QueryRow("SELECT config FROM alert_channels WHERE id=?", id).Scan(&config); err != nil || config != old {
		t.Errorf("seeded config became %s (%v)", config, err)
	}
}

// TestUpdateChannel_KeepsEnabledWhenOmitted: the update is partial — an
// omitted field keeps its stored value, which the page relies on to leave the
// masked secrets alone — but enabled was set to 1 whenever it was omitted, so
// a PUT that only renamed a disabled channel, or rotated its webhook, also
// switched it back on.
func TestUpdateChannel_KeepsEnabledWhenOmitted(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	id := insertChannel(t, db, "paused", "webhook", `{"webhook_url":"https://hooks.example.com/a"}`, 0)
	rec := callJSON(t, h.UpdateChannel, http.MethodPut, `{"name":"renamed"}`, int(id))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}
	var name string
	var enabled int
	if err := db.QueryRow("SELECT name, enabled FROM alert_channels WHERE id=?", id).Scan(&name, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "renamed" {
		t.Fatalf("name %q: the update did not apply", name)
	}
	if enabled != 0 {
		t.Errorf("enabled %d after a PUT that did not mention it, want 0", enabled)
	}

	// Saying it still works, both ways.
	for _, want := range []int{1, 0} {
		body := `{"enabled":false}`
		if want == 1 {
			body = `{"enabled":true}`
		}
		if rec := callJSON(t, h.UpdateChannel, http.MethodPut, body, int(id)); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", body, rec.Code, rec.Body)
		}
		if err := db.QueryRow("SELECT enabled FROM alert_channels WHERE id=?", id).Scan(&enabled); err != nil || enabled != want {
			t.Errorf("%s: enabled %d (%v), want %d", body, enabled, err, want)
		}
	}
}
