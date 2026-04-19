package monitor

import (
	"context"
	"database/sql"
	"log/slog"
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
	// Collect a point every 60 seconds; retention is time-based (24h rolling).
	// historyMaxLen caps the in-memory ring buffer at 2× expected size as a
	// belt-and-suspenders limit against clock jumps.
	historyInterval = 60 * time.Second
	historyMaxLen   = 2880
)

var (
	historyMu      sync.RWMutex
	historyPoints  []MetricsPoint
	historyDB      *sql.DB
	historyStarted sync.Once
)

// StartHistoryCollector begins collecting metrics at regular intervals
// in a background goroutine. It persists data to SQLite so history
// survives process restarts. Safe to call multiple times: only the first
// call actually starts a collector. The collector stops when ctx is done.
func StartHistoryCollector(ctx context.Context, db *sql.DB) {
	historyStarted.Do(func() {
		historyDB = db

		// Load existing history from DB (up to 24h)
		loadHistoryFromDB()

		go func() {
			ticker := time.NewTicker(historyInterval)
			defer ticker.Stop()

			// Collect first point immediately
			collectPoint()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					collectPoint()
				}
			}
		}()
	})
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
		slog.Error("failed to load metrics history", "error", err)
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

	slog.Info("loaded metrics history", "points", len(points))

	// Clean up old entries beyond 24h
	go func() {
		_, _ = historyDB.Exec("DELETE FROM metrics_history WHERE time <= ?", cutoff)
	}()
}

func collectPoint() {
	m, err := GetMetrics()
	if err != nil {
		// Even if some subsystems fail, try to get at least CPU and memory
		// which are the only fields stored in MetricsPoint.
		m, err = GetCoreMetrics()
		if err != nil {
			slog.Warn("failed to collect metrics", "error", err)
			return
		}
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
	if _, err := historyDB.Exec(
		"INSERT OR REPLACE INTO metrics_history (time, cpu, mem_percent) VALUES (?, ?, ?)",
		pt.Time, pt.CPU, pt.MemPercent,
	); err != nil {
		slog.Warn("failed to persist metrics point", "error", err)
	}
}

// FlushPending is a no-op now since we write every point immediately.
// Kept for API compatibility.
func FlushPending() {}

// GetHistory returns a copy of the collected metrics history.
func GetHistory() []MetricsPoint {
	return GetHistoryRange("")
}

// GetHistoryRange returns metrics history for the given time range.
// Supported ranges: "1h", "4h", "12h", "24h" (default).
// Longer ranges are downsampled to ~120 points for consistent client performance.
func GetHistoryRange(rangeStr string) []MetricsPoint {
	historyMu.RLock()
	defer historyMu.RUnlock()

	if len(historyPoints) == 0 {
		return []MetricsPoint{}
	}

	// Determine cutoff time
	now := time.Now().UnixMilli()
	var cutoff int64
	switch rangeStr {
	case "1h":
		cutoff = now - 1*60*60*1000
	case "4h":
		cutoff = now - 4*60*60*1000
	case "12h":
		cutoff = now - 12*60*60*1000
	default:
		cutoff = now - 24*60*60*1000
	}

	// Filter by time range (pre-allocate to avoid repeated reallocation)
	filtered := make([]MetricsPoint, 0, len(historyPoints))
	for _, pt := range historyPoints {
		if pt.Time >= cutoff {
			filtered = append(filtered, pt)
		}
	}

	if len(filtered) == 0 {
		return []MetricsPoint{}
	}

	// Downsample to ~120 points if needed
	const maxPoints = 120
	if len(filtered) <= maxPoints {
		result := make([]MetricsPoint, len(filtered))
		copy(result, filtered)
		return result
	}

	step := len(filtered) / maxPoints
	if step < 1 {
		step = 1
	}

	result := make([]MetricsPoint, 0, maxPoints+1)
	for i := 0; i < len(filtered); i += step {
		result = append(result, filtered[i])
	}
	// Always include the last point
	if result[len(result)-1].Time != filtered[len(filtered)-1].Time {
		result = append(result, filtered[len(filtered)-1])
	}
	return result
}
