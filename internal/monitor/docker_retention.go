package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
)

const containerEventsPerContainerCap = 5000

// StartDockerMetricsRetention runs an hourly pruner. Caller passes
// `retention` from the parsed config (6h/24h/72h → time.Duration).
func StartDockerMetricsRetention(ctx context.Context, db *sql.DB, retention time.Duration) {
	safe.Go("monitor-docker-metrics-retention", func() {
		pruneMetrics(db, retention)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneMetrics(db, retention)
			}
		}
	})
}

// StartDockerEventsRetention runs an hourly pruner. Enforces both an age
// cap (from config) and a per-container row cap (hardcoded).
func StartDockerEventsRetention(ctx context.Context, db *sql.DB, retention time.Duration) {
	safe.Go("monitor-docker-events-retention", func() {
		pruneEvents(db, retention, containerEventsPerContainerCap)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneEvents(db, retention, containerEventsPerContainerCap)
			}
		}
	})
}

func pruneMetrics(db *sql.DB, retention time.Duration) {
	cutoff := time.Now().Add(-retention).UnixMilli()
	if _, err := db.Exec(`DELETE FROM container_metrics_history WHERE ts < ?`, cutoff); err != nil {
		slog.Warn("container_metrics_history retention prune failed", "error", err)
	}
}

func pruneEvents(db *sql.DB, retention time.Duration, perContainerCap int) {
	cutoff := time.Now().Add(-retention).UnixMilli()
	if _, err := db.Exec(`DELETE FROM container_events WHERE ts < ?`, cutoff); err != nil {
		slog.Warn("container_events age prune failed", "error", err)
	}
	if _, err := db.Exec(`
		DELETE FROM container_events
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY container_id ORDER BY ts DESC) AS rn
				FROM container_events
			) WHERE rn > ?
		)`, perContainerCap); err != nil {
		slog.Warn("container_events row-cap prune failed", "error", err)
	}
}
