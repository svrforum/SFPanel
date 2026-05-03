package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

const auditMaxRows = 50000

// AuditMiddleware logs all state-changing API requests (POST, PUT, DELETE)
// to the audit_logs table after the handler has executed successfully.
func AuditMiddleware(db *sql.DB) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)

			method := c.Request().Method
			if method == "GET" || method == "HEAD" || method == "OPTIONS" {
				return err
			}

			path := c.Request().URL.Path
			// Skip auth endpoints to avoid logging login attempts with passwords
			if strings.HasPrefix(path, "/api/v1/auth/login") || strings.HasPrefix(path, "/api/v1/auth/setup") {
				return err
			}

			status := c.Response().Status
			username, _ := c.Get("username").(string)
			ip := c.RealIP()
			nodeID := c.QueryParam("node")

			go func() {
				if _, dbErr := db.Exec(
					"INSERT INTO audit_logs (username, method, path, status, ip, node_id) VALUES (?, ?, ?, ?, ?, ?)",
					username, method, path, status, ip, nodeID,
				); dbErr != nil {
					slog.Error("audit log write failed", "error", dbErr)
				}
			}()

			return err
		}
	}
}

// StartAuditRetention runs a background goroutine that prunes audit_logs
// down to auditMaxRows every 5 minutes. The previous design piggybacked on
// inserts via a CAS — if the panel was idle for hours, nothing pruned and
// the table grew unbounded after a bursty period.
//
// Returns when ctx is cancelled. Caller is expected to wire ctx to the
// shared bgCtx from main.go so retention stops cleanly on shutdown.
func StartAuditRetention(ctx context.Context, db *sql.DB) {
	go func() {
		// Run once on startup so a host that was offline for a long time
		// catches up before waiting another 5 minutes.
		pruneAuditLogs(db)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruneAuditLogs(db)
			}
		}
	}()
}

func pruneAuditLogs(db *sql.DB) {
	if _, err := db.Exec(
		"DELETE FROM audit_logs WHERE id IN (SELECT id FROM audit_logs ORDER BY id DESC LIMIT -1 OFFSET ?)",
		auditMaxRows,
	); err != nil {
		slog.Warn("audit log retention prune failed", "error", err)
	}
}
