package middleware

import (
	"database/sql"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	auditMaxRows     = 50000
	auditCleanupRows = 10000 // delete oldest N rows when max exceeded
)

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
					log.Printf("Audit log write failed: %v", dbErr)
				}

				// Periodic cleanup: keep at most auditMaxRows (check every 5 minutes)
				now := time.Now().Unix()
				if now-lastCleanup.Load() > 300 {
					lastCleanup.Store(now)
					var count int
					if err := db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&count); err == nil && count > auditMaxRows {
						db.Exec("DELETE FROM audit_logs WHERE id IN (SELECT id FROM audit_logs ORDER BY id ASC LIMIT ?)", auditCleanupRows)
					}
				}
			}()

			return err
		}
	}
}
