package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// The proxy-check helpers live in internal/auth so feature modules can consult
// them without importing api/middleware (which would be a feature → middleware
// reverse dependency). Only the wiring entry point is still re-exported here;
// the InternalProxyHeader constant and IsInternalProxyRequest aliases that used
// to sit alongside it had no callers left and were removed.

// SetClusterProxySecret forwards to internal/auth; kept here because main.go
// originally wired the startup call through middleware.
func SetClusterProxySecret(secret string) { auth.SetClusterProxySecret(secret) }

// allowsQueryToken returns true for the few endpoints that legitimately need
// to pass a JWT through a URL (plain file download via <a>, backup download,
// image thumbnails via <img>). Everything else must use the Authorization
// header.
//
// The thumbnail route is here because an <img> element cannot carry a header,
// and the alternative — fetching every tile with a header and holding a blob
// URL per file — costs native lazy loading, HTTP caching, and the lifecycle
// management of two hundred object URLs on one screen.
//
// The trade is that the token appears in the request line. This panel's own
// access log records the route pattern rather than the URI (see
// request_logger.go), so nothing leaks there, and an <img> src creates no
// browser history entry. A reverse proxy in front WILL log the full URI,
// which is the same exposure the download route above already carries — worth
// knowing before adding a third caller.
func allowsQueryToken(path string) bool {
	return path == "/api/v1/files/download" ||
		path == "/api/v1/files/thumbnail" ||
		path == "/api/v1/system/backup"
}

func JWTMiddleware(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Trust cluster-internal proxy requests (authenticated via mTLS).
			// Delegates to auth.IsInternalProxyRequest so v2 (HMAC + timestamp
			// + nonce, replay-resistant) is preferred. v1 fallback handled
			// inside. The previous inline check accepted v1 only, leaving
			// captured headers replayable forever.
			if auth.IsInternalProxyRequest(c.Request()) {
				username := c.Request().Header.Get("X-SFPanel-Original-User")
				if username == "" {
					username = "admin"
				}
				c.Set("username", username)
				return next(c)
			}

			header := c.Request().Header.Get("Authorization")
			if header == "" {
				// Fallback: accept token from query parameter ONLY on endpoints
				// that can't send a custom Authorization header (plain <a>
				// downloads). Leaving ?token= allowed on every route would
				// otherwise leak JWTs into access logs, Referer, and browser
				// history for any protected GET.
				if qToken := c.QueryParam("token"); qToken != "" && allowsQueryToken(c.Request().URL.Path) {
					header = "Bearer " + qToken
				} else {
					return response.Fail(c, http.StatusUnauthorized, response.ErrMissingToken, "Authorization header is required")
				}
			}

			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "Invalid authorization header format")
			}

			claims, err := auth.ParseToken(parts[1], secret)
			if err != nil {
				return response.Fail(c, http.StatusUnauthorized, response.ErrInvalidToken, "Invalid or expired token")
			}

			c.Set("username", claims.Username)
			return next(c)
		}
	}
}
