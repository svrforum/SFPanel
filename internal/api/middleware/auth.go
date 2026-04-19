package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// InternalProxyHeader / IsInternalProxyRequest / SetClusterProxySecret were
// moved into internal/auth so feature modules can consult the proxy check
// without importing api/middleware (which would be a feature → middleware
// reverse dependency). The names below are kept as thin aliases so existing
// callers inside this package continue to compile.
const InternalProxyHeader = auth.InternalProxyHeader

// SetClusterProxySecret forwards to internal/auth; kept here because main.go
// originally wired the startup call through middleware.
func SetClusterProxySecret(secret string) { auth.SetClusterProxySecret(secret) }

// IsInternalProxyRequest forwards to internal/auth; kept for external callers
// that imported it from middleware in the past.
func IsInternalProxyRequest(r *http.Request) bool { return auth.IsInternalProxyRequest(r) }

// allowsQueryToken returns true for the few endpoints that legitimately need
// to pass a JWT through a URL (plain file download via <a>, backup download).
// Everything else must use the Authorization header.
func allowsQueryToken(path string) bool {
	return path == "/api/v1/files/download" ||
		path == "/api/v1/system/backup"
}

func JWTMiddleware(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Trust cluster-internal proxy requests (authenticated via mTLS)
			proxySecret := auth.ClusterProxySecret()
			if proxyToken := c.Request().Header.Get(InternalProxyHeader); proxyToken != "" && proxySecret != "" {
				if subtle.ConstantTimeCompare([]byte(proxyToken), []byte(proxySecret)) == 1 {
					username := c.Request().Header.Get("X-SFPanel-Original-User")
					if username == "" {
						username = "admin"
					}
					c.Set("username", username)
					return next(c)
				}
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
