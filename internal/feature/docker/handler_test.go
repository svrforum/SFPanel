package featuredocker

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	_ "modernc.org/sqlite"
)

// openTestDBContainers creates a temp SQLite DB with the
// container_metrics_history schema used by ListContainers averaging.
func openTestDBContainers(t *testing.T) *sql.DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "containers.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE container_metrics_history (
        container_id TEXT, container_name TEXT, ts INTEGER,
        cpu_percent REAL, mem_percent REAL, mem_bytes INTEGER,
        PRIMARY KEY(container_id, ts)
    )`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

// TestListContainers_AveragingQuery directly exercises the SQL used
// by ListContainers to compute 1-hour averages. We assert that:
//  1. Samples older than 1 hour are excluded.
//  2. AVG returns NULL (sql.NullFloat64{Valid:false}) when there are
//     no samples in the window.
//  3. AVG is correct over the in-window samples.
//
// We cannot exercise the full handler without a real Docker client
// (Handler.Docker is a concrete *docker.Client, not an interface), so
// this test guards the part that can regress silently — the WHERE
// clause and the NULL-vs-value semantics that drive the *float64
// pointers in the response.
func TestListContainers_AveragingQuery(t *testing.T) {
	db := openTestDBContainers(t)
	now := time.Now().UnixMilli()
	old := now - (2 * 3600 * 1000) // 2h ago — should be excluded
	mid := now - (30 * 60 * 1000)  // 30m ago — included
	cur := now - (60 * 1000)       // 1m ago — included

	// Container "a": one stale + two in-window (cpu 4, 6 → avg 5; mem 10, 20 → avg 15).
	if _, err := db.Exec(`INSERT INTO container_metrics_history VALUES
        ('a','x',?, 99, 99, 1),
        ('a','x',?, 4,  10, 1),
        ('a','x',?, 6,  20, 1)`, old, mid, cur); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	// Container "b": only stale samples — averages should be NULL.
	if _, err := db.Exec(`INSERT INTO container_metrics_history VALUES
        ('b','y',?, 1, 1, 1)`, old); err != nil {
		t.Fatalf("insert b: %v", err)
	}

	cutoff := time.Now().Add(-1 * time.Hour).UnixMilli()
	q := `SELECT AVG(cpu_percent), AVG(mem_percent)
            FROM container_metrics_history
           WHERE container_id = ? AND ts >= ?`

	// Container "a": both averages valid.
	var aCPU, aMem sql.NullFloat64
	if err := db.QueryRowContext(context.Background(), q, "a", cutoff).Scan(&aCPU, &aMem); err != nil {
		t.Fatalf("scan a: %v", err)
	}
	if !aCPU.Valid || aCPU.Float64 != 5 {
		t.Errorf("a cpu: got valid=%v value=%v, want 5", aCPU.Valid, aCPU.Float64)
	}
	if !aMem.Valid || aMem.Float64 != 15 {
		t.Errorf("a mem: got valid=%v value=%v, want 15", aMem.Valid, aMem.Float64)
	}

	// Container "b": no in-window rows → NULL averages.
	var bCPU, bMem sql.NullFloat64
	if err := db.QueryRowContext(context.Background(), q, "b", cutoff).Scan(&bCPU, &bMem); err != nil {
		t.Fatalf("scan b: %v", err)
	}
	if bCPU.Valid {
		t.Errorf("b cpu: expected NULL, got %v", bCPU.Float64)
	}
	if bMem.Valid {
		t.Errorf("b mem: expected NULL, got %v", bMem.Float64)
	}

	// Container "c": no rows at all → also NULL.
	var cCPU, cMem sql.NullFloat64
	if err := db.QueryRowContext(context.Background(), q, "c", cutoff).Scan(&cCPU, &cMem); err != nil {
		t.Fatalf("scan c: %v", err)
	}
	if cCPU.Valid || cMem.Valid {
		t.Errorf("c: expected both NULL, got cpu=%v mem=%v", cCPU, cMem)
	}
}

// TestContainerWithMetrics_JSONShape verifies that embedding
// container.Summary preserves its JSON tags (frontend reads Pascal-case
// `Id`, `Names`, `Image`, ...), and that the two new fields appear as
// snake_case `cpu_avg_1h` / `mem_avg_1h` — null when nil, numeric when
// set. This is the contract that lets us add fields without breaking
// the existing client.
func TestContainerWithMetrics_JSONShape(t *testing.T) {
	cpu := 12.5
	row := containerWithMetrics{
		Summary: container.Summary{
			ID:    "abc123",
			Names: []string{"/web"},
			Image: "nginx:latest",
			State: "running",
		},
		CPUAvg1h: &cpu,
		MemAvg1h: nil,
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Embedded Summary JSON tags must be preserved exactly.
	if got["Id"] != "abc123" {
		t.Errorf("Id field missing or wrong: %v", got["Id"])
	}
	if got["Image"] != "nginx:latest" {
		t.Errorf("Image field missing or wrong: %v", got["Image"])
	}
	if got["State"] != "running" {
		t.Errorf("State field missing or wrong: %v", got["State"])
	}

	// New fields appear with snake_case names.
	if v, ok := got["cpu_avg_1h"].(float64); !ok || v != 12.5 {
		t.Errorf("cpu_avg_1h: got %v, want 12.5", got["cpu_avg_1h"])
	}
	// nil pointer must marshal to null (present in JSON, value null).
	if _, present := got["mem_avg_1h"]; !present {
		t.Errorf("mem_avg_1h key missing; expected explicit null")
	}
	if got["mem_avg_1h"] != nil {
		t.Errorf("mem_avg_1h: got %v, want null", got["mem_avg_1h"])
	}
}
