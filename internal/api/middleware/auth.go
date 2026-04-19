package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// InternalProxyHeader is set by the gRPC ProxyRequest handler to bypass JWT
// validation for cluster-internal requests. The value must match the cluster's
// internal proxy secret (derived from the TLS CA cert hash).
const InternalProxyHeader = "X-SFPanel-Internal-Proxy"

// clusterProxySecret is set at startup for validating internal proxy requests.
var clusterProxySecret string
var clusterProxySecretMu sync.RWMutex

// SetClusterProxySecret configures the secret used to validate internal proxy requests.
func SetClusterProxySecret(secret string) {
	clusterProxySecretMu.Lock()
	clusterProxySecret = secret
	clusterProxySecretMu.Unlock()
}

// allowsQueryToken returns true for the few endpoints that legitimately need
// to pass a JWT through a URL (plain file download via <a>, backup download).
// Everything else must use the Authorization header.
func allowsQueryToken(path string) bool {
	return path == "/api/v1/files/download" ||
		path == "/api/v1/system/backup"
}

// IsInternalProxyRequest checks if the HTTP request carries a valid internal proxy header.
// Used by WebSocket handlers to bypass JWT validation for cluster-relayed connections.
func IsInternalProxyRequest(r *http.Request) bool {
	clusterProxySecretMu.RLock()
	secret := clusterProxySecret
	clusterProxySecretMu.RUnlock()
	if secret == "" {
		return false
	}
	proxyToken := r.Header.Get(InternalProxyHeader)
	if proxyToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(proxyToken), []byte(secret)) == 1
}

func JWTMiddleware(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Trust cluster-internal proxy requests (authenticated via mTLS)
			clusterProxySecretMu.RLock()
			proxySecret := clusterProxySecret
			clusterProxySecretMu.RUnlock()
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
