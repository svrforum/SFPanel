package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"
)

func callHealth(t *testing.T, db *sql.DB) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	_ = healthHandler(db, "v9.9.9")(c)
	return rec
}

func TestHealthHandler_OkWhenDBReachable(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rec := callHealth(t, db)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool              `json:"success"`
		Data    map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Data["status"] != "ok" || resp.Data["version"] != "v9.9.9" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestHealthHandler_503WhenDBUnreachable(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close() // a closed pool fails PingContext → readiness must report 503
	rec := callHealth(t, db)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 on unreachable DB, got %d: %s", rec.Code, rec.Body.String())
	}
}
