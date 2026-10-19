package middleware

import (
	"net"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// InternalProxyOnly is an agent's whole authentication: a request signed
// with its key by one of its managers. The username is the operator the
// manager authenticated. Anything else is 401 — not 403 or 404 — because
// the update watchdog treats 200 and 401 as "the new binary is up".
func InternalProxyOnly() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !auth.IsInternalProxyRequest(c.Request()) {
				return response.Fail(c, http.StatusUnauthorized, response.ErrMissingToken,
					"This node accepts only requests its managers sign")
			}
			// JWTMiddleware's rule for a proxied request: no name is "admin".
			username := c.Request().Header.Get("X-SFPanel-Original-User")
			if username == "" {
				username = "admin"
			}
			c.Set("username", username)
			return next(c)
		}
	}
}

// AgentSourceCheck lets an agent's panel answer loopback and its managers
// only. The signature is the real check; this keeps the rest of the network
// from reaching even the 401. RemoteAddr, never X-Forwarded-For. A nil
// allowed admits loopback alone.
func AgentSourceCheck(allowed func(net.IP) bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if ip := peerIP(c.Request().RemoteAddr); ip != nil && (ip.IsLoopback() || allowed != nil && allowed(ip)) {
				return next(c)
			}
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied,
				"This node answers only its managers")
		}
	}
}

// peerIP is the address of the connection a request came on; nil when
// RemoteAddr is not host:port, which net/http always sets for TCP.
func peerIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}
