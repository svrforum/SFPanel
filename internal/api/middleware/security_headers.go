package middleware

import "github.com/labstack/echo/v4"

// SecurityHeaders emits a baseline set of HTTP security response headers on
// every served response. The panel ships its own bundled assets (no inline
// scripts in production), proxies to itself only, and serves a single-origin
// SPA — so a tight CSP is practical without breaking features.
//
// What this catches:
//   - XSS impact reduced (CSP forbids inline event handlers and external
//     scripts; only same-origin + the bundled font CDN are reachable).
//   - clickjacking blocked (frame-ancestors 'none' / X-Frame-Options DENY).
//   - MIME sniffing disabled so a misclassified asset can't execute as JS.
//   - Referer leakage on outbound clicks limited to origin.
//
// What this does NOT do:
//   - Force HTTPS via HSTS. The panel binds plain HTTP by design (operator
//     fronts with a reverse proxy + TLS), and emitting HSTS over plain HTTP
//     either has no effect (browsers ignore it) or pins the wrong origin if
//     the reverse proxy ever terminates differently. Add HSTS at the
//     reverse-proxy layer instead.
func SecurityHeaders() echo.MiddlewareFunc {
	// CSP: 'self' covers the bundled SPA + WS + API. cdn.jsdelivr.net is
	// allowed for the Pretendard font CSS (also pinned via SRI in
	// index.html). data: is allowed for img-src so xterm.js and chart
	// canvases can render. connect-src includes wss: for WebSocket
	// upgrades on either http/https origins behind a reverse proxy.
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; " +
		"font-src 'self' data: https://cdn.jsdelivr.net; " +
		"img-src 'self' data: blob: https:; " +
		"connect-src 'self' ws: wss: https:; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Response().Header()
			if h.Get("Content-Security-Policy") == "" {
				h.Set("Content-Security-Policy", csp)
			}
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")
			return next(c)
		}
	}
}
