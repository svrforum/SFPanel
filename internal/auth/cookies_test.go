package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSetRefreshCookie_FlagsHardened(t *testing.T) {
	rec := httptest.NewRecorder()
	SetRefreshCookie(rec, "secret-value", 7*24*time.Hour, true)

	got := rec.Result().Cookies()
	if len(got) != 1 {
		t.Fatalf("cookies = %d, want 1", len(got))
	}
	c := got[0]
	if c.Name != RefreshCookieName {
		t.Errorf("name = %q, want %q", c.Name, RefreshCookieName)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be true — JS would otherwise steal the cookie via XSS")
	}
	if !c.Secure {
		t.Error("Secure must be true when caller asked for it")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("SameSite must be Strict to block cross-site refresh attempts")
	}
	if c.Path != "/api/v1/auth" {
		t.Errorf("Path = %q, want /api/v1/auth (scope cookie to auth endpoints only)", c.Path)
	}
}

// TestSetRefreshCookie_InsecureWhenNoTLS — Secure=true with plain HTTP makes
// browsers silently drop the cookie, bricking login. Verify the flag tracks
// the caller's decision.
func TestSetRefreshCookie_InsecureWhenNoTLS(t *testing.T) {
	rec := httptest.NewRecorder()
	SetRefreshCookie(rec, "secret", time.Hour, false)
	if rec.Result().Cookies()[0].Secure {
		t.Error("Secure should be false when caller knows the request was plain HTTP")
	}
}

func TestSetCSRFCookie_JSReadable(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCSRFCookie(rec, "abcdef", time.Hour, true)
	c := rec.Result().Cookies()[0]
	if c.HttpOnly {
		t.Error("CSRF cookie must be JS-readable (not HttpOnly) — the SPA echoes it via X-CSRF-Token")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("CSRF cookie SameSite must be Strict")
	}
}

func TestClearAuthCookies_EmitsBothExpiry(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearAuthCookies(rec, true)
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("clear cookies count = %d, want 2", len(cookies))
	}
	for _, c := range cookies {
		if c.MaxAge != -1 {
			t.Errorf("%s MaxAge = %d, want -1 (immediate deletion)", c.Name, c.MaxAge)
		}
	}
}

func TestIsSecureRequest(t *testing.T) {
	cases := []struct {
		name string
		tls  bool
		xff  string
		want bool
	}{
		{"direct TLS", true, "", true},
		{"plain HTTP", false, "", false},
		{"reverse proxy https", false, "https", true},
		{"reverse proxy http", false, "http", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.com/x", nil)
			if c.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if c.xff != "" {
				r.Header.Set("X-Forwarded-Proto", c.xff)
			}
			if got := IsSecureRequest(r); got != c.want {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestGenerateCSRFToken_LengthAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		tok := GenerateCSRFToken()
		if len(tok) != 64 {
			t.Errorf("token length = %d, want 64 hex chars (256 bits)", len(tok))
		}
		if seen[tok] {
			t.Errorf("duplicate token %q — entropy collision", tok)
		}
		seen[tok] = true
	}
}
