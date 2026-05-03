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

func TestGetContainerEvents_NewestFirst_WithCursor(t *testing.T) {
	db := openTestDBObs(t)
	for i := 0; i < 60; i++ {
		db.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('a','x',?,'start')`, int64(1000+i))
	}
	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/?limit=50", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("a")
	h.GetEvents(c)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 50 {
		t.Fatalf("page1: got %d, want 50", len(resp.Data))
	}
	first := int64(resp.Data[0]["ts"].(float64))
	last := int64(resp.Data[len(resp.Data)-1]["ts"].(float64))
	if first <= last {
		t.Errorf("expected newest-first ordering")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/?limit=50&before="+tsString(last), nil)
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)
	c2.SetParamNames("id")
	c2.SetParamValues("a")
	h.GetEvents(c2)
	var resp2 struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec2.Body).Decode(&resp2)
	if len(resp2.Data) != 10 {
		t.Errorf("page2: got %d, want 10", len(resp2.Data))
	}
}

func TestGetRecentEvents_AcrossContainers(t *testing.T) {
	db := openTestDBObs(t)
	db.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('a','x',1,'start'),('b','y',2,'die'),('a','x',3,'restart')`)
	h := &ObservabilityHandler{DB: db, ObservabilityEnabled: true}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?limit=10", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	h.GetRecentEvents(c)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 across 2 containers, got %d", len(resp.Data))
	}
}
