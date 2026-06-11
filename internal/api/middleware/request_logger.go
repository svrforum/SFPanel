package middleware

import (
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// requestLoggerSkip identifies paths whose request log would just be noise
// without adding any operational signal. Heartbeat polls and the
// dashboard's system-info refresh qualify. Long-lived WS upgrades (/ws/*)
// don't emit anything useful here either — the handler logs already cover
// the interesting state transitions.
//
// Skipping suppresses only the success case: RequestLogger still logs
// skip-path requests that return an error or end with status >= 400.
func requestLoggerSkip(path string) bool {
	switch path {
	case "/api/v1/health",
		"/api/v1/system/info":
		return true
	}
	// WS upgrades land here as plain GETs that the handler doesn't return
	// from until the socket closes; the slog line then arrives after the
	// session ends with a noisy "duration_ms=864000" entry that no
	// dashboard ever wants. The handler logs WS lifecycle separately.
	return strings.HasPrefix(path, "/ws/")
}

func RequestLogger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Path()
			skip := requestLoggerSkip(path)
			start := time.Now()
			err := next(c)
			// Skip paths suppress only the success line — a failing
			// health check or refused WS upgrade still logs.
			if skip && err == nil && c.Response().Status < 400 {
				return err
			}
			slog.Info("request",
				"method", c.Request().Method,
				"path", path,
				"status", c.Response().Status,
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", c.RealIP(),
			)
			return err
		}
	}
}
