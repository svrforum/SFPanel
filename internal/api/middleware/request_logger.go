package middleware

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
)

func RequestLogger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Path() == "/api/v1/health" {
				return next(c)
			}
			start := time.Now()
			err := next(c)
			slog.Info("request",
				"method", c.Request().Method,
				"path", c.Path(),
				"status", c.Response().Status,
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", c.RealIP(),
			)
			return err
		}
	}
}
