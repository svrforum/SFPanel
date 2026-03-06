package monitor

import (
	"database/sql"
	"log"
	"sync"
	"time"
)

// MetricsPoint is a single time-series data point for the history buffer.
type MetricsPoint struct {
	Time       int64   `json:"time"`
	CPU        float64 `json:"cpu"`
	MemPercent float64 `json:"mem_percent"`
}

const (
	// Collect a point every 30 seconds, keep 24 hours = 2880 points.
	historyInterval = 30 * time.Second
	historyMaxLen   = 2880
)

var (
	historyMu     sync.RWMutex
	historyPoints []MetricsPoint
	historyDB     *sql.DB
)

// StartHistoryCollector begins collecting metrics at regular intervals
// in a background goroutine. It persists data to SQLite so history
// survives process restarts. Call once at startup after DB is ready.
func StartHistoryCollector(db *sql.DB) {
	historyDB = db

	// Load existing history from DB (up to 24h)
	loadHistoryFromDB()

	go func() {
		ticker := time.NewTicker(historyInterval)
		defer ticker.Stop()

		// Collect first point immediately
		collectPoint()

		for range ticker.C {
			collectPoint()
		}
	}()
}

func loadHistoryFromDB() {
	if historyDB == nil {
		return
	}

	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	rows, err := historyDB.Query(
		"SELECT time, cpu, mem_percent FROM metrics_history WHERE time > ? ORDER BY time ASC",
		cutoff,
	)
	if err != nil {
		log.Printf("Failed to load metrics history: %v", err)
		return
	}
	defer rows.Close()

	var points []MetricsPoint
	for rows.Next() {
		var pt MetricsPoint
		if err := rows.Scan(&pt.Time, &pt.CPU, &pt.MemPercent); err != nil {
			continue
		}
		points = append(points, pt)
	}

	// Trim to max length
	if len(points) > historyMaxLen {
		points = points[len(points)-historyMaxLen:]
	}

	historyMu.Lock()
	historyPoints = points
	historyMu.Unlock()

	log.Printf("Loaded %d metrics history points from database", len(points))

	// Clean up old entries beyond 24h
	go func() {
		_, _ = historyDB.Exec("DELETE FROM metrics_history WHERE time <= ?", cutoff)
	}()
}

func collectPoint() {
	m, err := GetMetrics()
	if err != nil {
		return
	}

	pt := MetricsPoint{
		Time:       time.Now().UnixMilli(),
		CPU:        m.CPU,
		MemPercent: m.MemPercent,
	}

	historyMu.Lock()
	historyPoints = append(historyPoints, pt)
	if len(historyPoints) > historyMaxLen {
		historyPoints = historyPoints[len(historyPoints)-historyMaxLen:]
	}
	historyMu.Unlock()

	// Persist to DB (single write per 30s — negligible for SQLite)
	saveToDB(pt)
}

func saveToDB(pt MetricsPoint) {
	if historyDB == nil {
		return
	}
	_, _ = historyDB.Exec(
		"INSERT OR REPLACE INTO metrics_history (time, cpu, mem_percent) VALUES (?, ?, ?)",
		pt.Time, pt.CPU, pt.MemPercent,
	)
}

// FlushPending is a no-op now since we write every point immediately.
// Kept for API compatibility.
func FlushPending() {}

// GetHistory returns a copy of the collected metrics history.
func GetHistory() []MetricsPoint {
	historyMu.RLock()
	defer historyMu.RUnlock()

	result := make([]MetricsPoint, len(historyPoints))
	copy(result, historyPoints)
	return result
}
