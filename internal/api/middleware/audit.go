package middleware

import (
	"database/sql"
	"strings"

	"github.com/labstack/echo/v4"
)

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

			go func() {
				_, _ = db.Exec(
					"INSERT INTO audit_logs (username, method, path, status, ip) VALUES (?, ?, ?, ?, ?)",
					username, method, path, status, ip,
				)
			}()

			return err
		}
	}
}
