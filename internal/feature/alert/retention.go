package alert

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// alertHistoryMaxAge bounds how long alert_history rows are kept by the
// background pruner. Manual ClearHistory is still available for operators
// who want a hard reset.
const alertHistoryMaxAge = 30 * 24 * time.Hour

// alertHistoryMaxRows is the absolute cap regardless of age — protects
// against runaway alert storms filling the DB.
const alertHistoryMaxRows = 50000

// StartHistoryRetention runs a background goroutine that prunes
// alert_history every hour. Same shape as middleware.StartAuditRetention.
func StartHistoryRetention(ctx context.Context, db *sql.DB) {
	go func() {
		// Run once at startup so a returning-from-downtime panel catches up
		// before waiting another hour.
		pruneAlertHistory(db)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneAlertHistory(db)
			}
		}
	}()
}

func pruneAlertHistory(db *sql.DB) {
	cutoff := time.Now().Add(-alertHistoryMaxAge)
	if _, err := db.Exec(
		`DELETE FROM alert_history WHERE created_at < ?`,
		cutoff.Format("2006-01-02 15:04:05"),
	); err != nil {
		slog.Warn("alert history age prune failed", "error", err)
	}
	if _, err := db.Exec(
		`DELETE FROM alert_history WHERE id IN (
			SELECT id FROM alert_history ORDER BY id DESC LIMIT -1 OFFSET ?
		)`,
		alertHistoryMaxRows,
	); err != nil {
		slog.Warn("alert history row-cap prune failed", "error", err)
	}
}
