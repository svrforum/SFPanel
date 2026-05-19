package middleware

import (
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// requestLoggerSkip identifies paths whose request log would just be noise
// without adding any operational signal. Heartbeat polls, the system
// metrics tick (called every 2 s by the dashboard WS keepalive fallback)
// and the static SPA shell all qualify. Long-lived WS upgrades (/ws/*)
// don't emit anything useful here either — the handler logs already cover
// the interesting state transitions.
//
// Mutation endpoints, anything authenticated/audited, and errors always
// log; skipping is purely for the high-volume read-only chatter.
func requestLoggerSkip(path string) bool {
	switch path {
	case "/api/v1/health",
		"/api/v1/system/info",
		"/api/v1/monitor/metrics":
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
			if requestLoggerSkip(path) {
				return next(c)
			}
			start := time.Now()
			err := next(c)
			// Errors and non-2xx responses always log, even on otherwise
			// skipped paths — kept above as path-only skip, here for the
			// general case.
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
