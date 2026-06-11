package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

// csrfSafeMethods are exempt from CSRF check — GET/HEAD/OPTIONS don't change
// server state, so a cross-site read is bounded by CORS (already configured).
var csrfSafeMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// CSRFProtect enforces the double-submit-cookie pattern on state-changing
// requests. The login flow plants a JS-readable sfpanel_csrf cookie; the
// client echoes it via the X-CSRF-Token header. An attacker who tricks a
// victim's browser into POSTing to the panel can NOT read the cookie
// (cross-origin) and so can NOT forge the header — the request fails.
//
// Skipped paths:
//   - GET/HEAD/OPTIONS — non-state-changing
//   - Internal cluster proxy requests — already mTLS-authenticated +
//     v2 replay-protected, and the proxy code doesn't carry a CSRF token
//   - Login / setup / refresh — bootstrap flows that mint the cookie
//     themselves; no cookie exists yet to compare against
//   - Bearer-token requests carrying NO sfpanel cookies — zero ambient
//     authority, so cross-site forgery is structurally impossible
//   - File downloads / system backup over ?token= GET — already covered
//     by the safe-method bypass
//
// Returns 403 (CSRFTokenMismatch) on missing or mismatched token.
func CSRFProtect() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			r := c.Request()

			if csrfSafeMethods[r.Method] {
				return next(c)
			}

			// Cluster-internal requests authenticated by mTLS bypass — those
			// are server-to-server, not browser-driven.
			if auth.IsInternalProxyRequest(r) {
				return next(c)
			}

			// Bootstrap endpoints: there's no cookie to compare against yet
			// because these are the flows that mint it.
			switch r.URL.Path {
			case "/api/v1/auth/login",
				"/api/v1/auth/setup",
				"/api/v1/auth/refresh":
				return next(c)
			}

			// Bearer-authenticated request with zero sfpanel cookies: CSRF
			// rides on ambient authority (cookies the browser attaches without
			// the attacker's involvement), and an Authorization header cannot
			// be set by a cross-site page. With neither sfpanel_csrf nor
			// sfpanel_refresh on the request there is nothing for a forged
			// request to ride, so the check is moot. This is the only viable
			// path for the Tauri desktop wrapper, whose webview origin can't
			// read the API origin's cookies. If ANY sfpanel cookie is present
			// the full double-submit check below still applies, so a browser
			// session can't be downgraded by merely adding a Bearer header.
			// (JWTMiddleware runs before this and has already verified the
			// token — we only gate on its presence here.)
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				_, csrfErr := r.Cookie(auth.CSRFCookieName)
				_, refreshErr := r.Cookie(auth.RefreshCookieName)
				if csrfErr != nil && refreshErr != nil {
					return next(c)
				}
			}

			cookie, err := r.Cookie(auth.CSRFCookieName)
			if err != nil || cookie.Value == "" {
				return response.Fail(c, http.StatusForbidden,
					response.ErrCSRFTokenMissing, "CSRF cookie missing")
			}
			header := r.Header.Get(auth.CSRFHeaderName)
			if header == "" {
				return response.Fail(c, http.StatusForbidden,
					response.ErrCSRFTokenMissing, "CSRF header missing")
			}
			if subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) != 1 {
				return response.Fail(c, http.StatusForbidden,
					response.ErrCSRFTokenMismatch, "CSRF token mismatch")
			}
			return next(c)
		}
	}
}
