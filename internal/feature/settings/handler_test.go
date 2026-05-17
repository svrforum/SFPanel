package settings

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return &Handler{DB: db}
}

func postSettings(t *testing.T, h *Handler, body string) (int, string) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	_ = h.UpdateSettings(c)
	return rec.Code, rec.Body.String()
}

func TestUpdateSettings_AllowsKnownKeys(t *testing.T) {
	h := newTestHandler(t)
	for _, key := range []string{"terminal_timeout", "max_upload_size"} {
		body := `{"settings":{"` + key + `":"42"}}`
		code, resp := postSettings(t, h, body)
		if code != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d: %s", key, code, resp)
		}
	}
}

func TestUpdateSettings_RejectsUnknownKeys(t *testing.T) {
	h := newTestHandler(t)
	// Real-world poisoning surfaces an attacker would target.
	for _, key := range []string{
		"appstore_cache",             // written by appstore handler — not for user override
		"jwt_secret",                 // not in DB but operator might try
		"cluster_proxy_secret",       // ditto
		"../../etc/passwd",           // path-y key
		"<script>alert(1)</script>",  // XSS payload as key
		"",                           // empty
		"completely_unknown_setting", // unrecognized
	} {
		body, err := json.Marshal(map[string]any{
			"settings": map[string]string{key: "x"},
		})
		if err != nil {
			t.Fatal(err)
		}
		code, resp := postSettings(t, h, string(body))
		if code == http.StatusOK {
			t.Errorf("expected non-200 for key %q, got OK: %s", key, resp)
		}
	}
}

func TestUpdateSettings_AllOrNothing(t *testing.T) {
	h := newTestHandler(t)
	// Mixed batch with one bad key: nothing should be persisted.
	body := `{"settings":{"terminal_timeout":"100","appstore_cache":"poisoned"}}`
	code, _ := postSettings(t, h, body)
	if code == http.StatusOK {
		t.Fatal("mixed valid+invalid batch should be rejected")
	}
	// terminal_timeout must NOT have been written (atomic batch).
	if got := GetSetting(h.DB, "terminal_timeout"); got != "" && got != "30" {
		t.Errorf("terminal_timeout = %q, expected default (no write)", got)
	}
}
