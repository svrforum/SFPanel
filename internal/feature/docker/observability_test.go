package featuredocker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"
)

func openTestDBObs(t *testing.T) *sql.DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.db")
	db, _ := sql.Open("sqlite", p)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE container_metrics_history (container_id TEXT, container_name TEXT, ts INTEGER, cpu_percent REAL, mem_percent REAL, mem_bytes INTEGER, PRIMARY KEY(container_id,ts))`)
	db.Exec(`CREATE TABLE container_events (id INTEGER PRIMARY KEY AUTOINCREMENT, container_id TEXT, container_name TEXT, ts INTEGER, event_type TEXT, exit_code INTEGER, detail TEXT)`)
	return db
}

func TestGetContainerMetrics_Range1h(t *testing.T) {
	db := openTestDBObs(t)
	now := time.Now().UnixMilli()
	old := now - (2 * 3600 * 1000)
	mid := now - (30 * 60 * 1000)
	db.Exec(`INSERT INTO container_metrics_history VALUES ('a','x',?,1,1,1),('a','x',?,2,2,2),('a','x',?,3,3,3)`, old, mid, now)

	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?range=1h", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a")

	if err := h.GetMetrics(c); err != nil {
		t.Fatalf("err: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	var resp struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 rows in last 1h, got %d", len(resp.Data))
	}
}

func TestGetContainerMetrics_InvalidRange(t *testing.T) {
	db := openTestDBObs(t)
	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?range=invalid", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a")

	h.GetMetrics(c)
	if rec.Code != 400 {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// Helper to build "before" cursor in event tests (Task 12).
func tsString(n int64) string { return strconv.FormatInt(n, 10) }
