package monitor

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDBFull(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, _ := sql.Open("sqlite", dbPath)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE container_metrics_history (
		container_id TEXT NOT NULL, container_name TEXT NOT NULL, ts INTEGER NOT NULL,
		cpu_percent REAL NOT NULL, mem_percent REAL NOT NULL, mem_bytes INTEGER NOT NULL,
		PRIMARY KEY (container_id, ts))`)
	db.Exec(`CREATE TABLE container_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		container_id TEXT NOT NULL, container_name TEXT NOT NULL,
		ts INTEGER NOT NULL, event_type TEXT NOT NULL,
		exit_code INTEGER, detail TEXT)`)
	return db
}

func TestPruneMetrics_DropsOlderThanRetention(t *testing.T) {
	db := openTestDBFull(t)
	now := time.Now().UnixMilli()
	old := now - (25 * time.Hour).Milliseconds()
	db.Exec(`INSERT INTO container_metrics_history VALUES ('a','x',?,1,1,1),('a','x',?,2,2,2)`, old, now)

	pruneMetrics(db, 24*time.Hour)

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM container_metrics_history`).Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 row remaining (older row pruned), got %d", n)
	}
}

func TestPruneEvents_AgeAndRowCap(t *testing.T) {
	db := openTestDBFull(t)
	now := time.Now().UnixMilli()
	old := now - (31 * 24 * time.Hour).Milliseconds()
	db.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('a','x',?,'start'),('a','x',?,'start')`, old, now)
	tx, _ := db.Begin()
	for i := 0; i < 5050; i++ {
		tx.Exec(`INSERT INTO container_events (container_id,container_name,ts,event_type) VALUES ('b','y',?,'start')`, now-int64(i*1000))
	}
	tx.Commit()

	pruneEvents(db, 30*24*time.Hour, 5000)

	var nA, nB int
	db.QueryRow(`SELECT COUNT(*) FROM container_events WHERE container_id='a'`).Scan(&nA)
	db.QueryRow(`SELECT COUNT(*) FROM container_events WHERE container_id='b'`).Scan(&nB)
	if nA != 1 {
		t.Errorf("container a: expected 1 row, got %d", nA)
	}
	if nB != 5000 {
		t.Errorf("container b: expected 5000 rows (cap), got %d", nB)
	}
}
