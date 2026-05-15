package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

// Cookie names. Stable across releases — changing these would invalidate
// every existing session.
const (
	// RefreshCookieName holds the opaque refresh token. httpOnly so JS can't
	// read it (XSS-resistant); SameSite=Strict so it doesn't ride along on
	// cross-site requests. Lifetime matches refresh_tokens.expires_at.
	RefreshCookieName = "sfpanel_refresh"
	// CSRFCookieName holds the CSRF token. JS-readable (not httpOnly) so the
	// client can echo it via X-CSRF-Token header — double-submit defense
	// against state-changing CSRF.
	CSRFCookieName = "sfpanel_csrf"
)

// CSRFHeaderName is what state-changing requests carry to prove same-origin
// intent. Matched against CSRFCookieName.
const CSRFHeaderName = "X-CSRF-Token"

// GenerateCSRFToken returns 64 hex chars (256 bits of entropy) suitable for
// the double-submit cookie. Falls back to empty on RNG failure — callers
// should treat that as a 500.
func GenerateCSRFToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// SetRefreshCookie writes the refresh token cookie with the strictest viable
// flags. Path is /api/v1/auth so it's only sent on auth endpoints, not on
// every request — limits exposure if a downstream proxy mis-handles cookies.
// `secure` should be true when the request arrived over TLS (either direct
// HTTPS or X-Forwarded-Proto=https behind a reverse proxy). Setting Secure
// over plain HTTP causes browsers to silently drop the cookie, which would
// brick login on the default `:3628` HTTP listener.
func SetRefreshCookie(w http.ResponseWriter, refreshToken string, maxAge time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    refreshToken,
		Path:     "/api/v1/auth",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// SetCSRFCookie writes the CSRF cookie. NOT httpOnly — the client reads it
// from JS and echoes via X-CSRF-Token. Same MaxAge as refresh so they
// expire together.
func SetCSRFCookie(w http.ResponseWriter, csrfToken string, maxAge time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    csrfToken,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearAuthCookies removes the refresh + csrf cookies on logout. The browser
// honors MaxAge=-1 as "delete now". Path values must match the original Set
// call or the browser keeps the old cookie alongside an expired one.
func ClearAuthCookies(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// IsSecureRequest reports whether the request reached the server over TLS,
// either directly (TLS != nil) or via a trusted reverse proxy that set
// X-Forwarded-Proto=https. Used to decide cookie Secure flag.
func IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
