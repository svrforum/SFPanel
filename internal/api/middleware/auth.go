package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

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

// SetClusterProxySecret configures the secret used to validate internal proxy requests.
func SetClusterProxySecret(secret string) {
	clusterProxySecret = secret
}

// IsInternalProxyRequest checks if the HTTP request carries a valid internal proxy header.
// Used by WebSocket handlers to bypass JWT validation for cluster-relayed connections.
func IsInternalProxyRequest(r *http.Request) bool {
	if clusterProxySecret == "" {
		return false
	}
	proxyToken := r.Header.Get(InternalProxyHeader)
	if proxyToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(proxyToken), []byte(clusterProxySecret)) == 1
}

func JWTMiddleware(secret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Trust cluster-internal proxy requests (authenticated via mTLS)
			if proxyToken := c.Request().Header.Get(InternalProxyHeader); proxyToken != "" && clusterProxySecret != "" {
				if subtle.ConstantTimeCompare([]byte(proxyToken), []byte(clusterProxySecret)) == 1 {
					c.Set("username", "admin")
					return next(c)
				}
			}

			header := c.Request().Header.Get("Authorization")
			if header == "" {
				// Fallback: accept token from query parameter (for file downloads via <a> tags)
				if qToken := c.QueryParam("token"); qToken != "" {
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
