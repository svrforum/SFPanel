package middleware

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// SecurityHeaders emits a baseline set of HTTP security response headers on
// every served response. The panel ships its own bundled assets, proxies to
// itself only, and serves a single-origin SPA — so a tight CSP is practical
// without breaking features.
//
// scriptHashes allows index.html's inline pre-paint theme script by hash; see
// InlineScriptHashes. Pass none and script-src stays 'self' only.
//
// What this catches:
//   - XSS impact reduced (CSP forbids inline event handlers and external
//     scripts; only same-origin + the bundled font CDN are reachable).
//   - clickjacking blocked (frame-ancestors 'none' / X-Frame-Options DENY).
//   - MIME sniffing disabled so a misclassified asset can't execute as JS.
//   - Referer leakage on outbound clicks limited to origin.
//
// What this does NOT do:
//
//   - Force HTTPS via HSTS. This is deliberate in BOTH modes, and the reason
//     changed when server.tls arrived.
//
//     Plain-HTTP mode (the default): browsers ignore HSTS over plain HTTP, so
//     it would do nothing — or pin the wrong origin if a reverse proxy in
//     front ever terminates differently. Add HSTS at the proxy instead.
//
//     TLS mode: emitting HSTS would be actively harmful. The panel's
//     certificate comes from a local CA the operator installs by hand, and
//     HSTS removes the browser's "proceed anyway" affordance for certificate
//     errors. Anyone who has not installed the CA yet — a new phone, a guest
//     laptop, the operator themselves on first visit — would be locked out
//     with no way through, on the very page that offers the CA download.
func SecurityHeaders(scriptHashes ...string) echo.MiddlewareFunc {
	// CSP: 'self' covers the bundled SPA + WS + API. cdn.jsdelivr.net is
	// allowed for the Pretendard font CSS (also pinned via SRI in
	// index.html). data: is allowed for img-src so xterm.js and chart
	// canvases can render. connect-src includes wss: for WebSocket
	// upgrades on either http/https origins behind a reverse proxy.
	scriptSrc := "'self'"
	if len(scriptHashes) > 0 {
		scriptSrc += " " + strings.Join(scriptHashes, " ")
	}
	csp := "default-src 'self'; " +
		"script-src " + scriptSrc + "; " +
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
