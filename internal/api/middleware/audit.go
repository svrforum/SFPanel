package middleware

import (
	"database/sql"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
)

const auditMaxRows = 50000

var lastCleanup atomic.Int64

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

				// Periodic cleanup: keep at most auditMaxRows. CompareAndSwap
				// guarantees only one goroutine per 5-minute window runs the
				// DELETE, and the single statement keeps count+prune atomic so
				// two concurrent middlewares can't double-delete.
				now := time.Now().Unix()
				prev := lastCleanup.Load()
				if now-prev > 300 && lastCleanup.CompareAndSwap(prev, now) {
					if _, dbErr := db.Exec(
						"DELETE FROM audit_logs WHERE id IN (SELECT id FROM audit_logs ORDER BY id DESC LIMIT -1 OFFSET ?)",
						auditMaxRows,
					); dbErr != nil {
						slog.Warn("audit log cleanup failed", "error", dbErr)
					}
				}
			}()

			return err
		}
	}
}
