package monitor

import (
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
)

// StartHistoryCollector begins collecting metrics at regular intervals
// in a background goroutine. Call once at startup.
func StartHistoryCollector() {
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
}

// GetHistory returns a copy of the collected metrics history.
func GetHistory() []MetricsPoint {
	historyMu.RLock()
	defer historyMu.RUnlock()

	result := make([]MetricsPoint, len(historyPoints))
	copy(result, historyPoints)
	return result
}
