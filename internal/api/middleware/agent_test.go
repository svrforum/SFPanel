package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

func TestInternalProxyOnly(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("agent-key", []string{"agent-key"})
	h := InternalProxyOnly()(func(c echo.Context) error { return c.String(200, c.Get("username").(string)) })
	run := func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		_ = h(echo.New().NewContext(r, rec))
		return rec
	}
	// A bearer token or nothing at all: 401 — the update watchdog counts 401
	// as alive, so this must not become 403 or 404. The code says why: no
	// credential this node takes, which is MISSING_TOKEN whatever was sent.
	refused := map[string]func(*http.Request){
		"bearer":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer anything") },
		"nothing": func(r *http.Request) {},
		// The managers' own secret is not this agent's key.
		"another key": func(r *http.Request) {
			r.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2With("managers-secret", http.MethodGet, "/api/v1/system/info"))
		},
	}
	for name, set := range refused {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
		set(r)
		if rec := run(r); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"MISSING_TOKEN"`) {
			t.Fatalf("%s: %d %s, want 401 MISSING_TOKEN", name, rec.Code, rec.Body)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	r.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2With("agent-key", http.MethodGet, "/api/v1/system/info"))
	r.Header.Set("X-SFPanel-Original-User", "alice")
	if rec := run(r); rec.Code != 200 || rec.Body.String() != "alice" {
		t.Fatalf("signed: %d %q", rec.Code, rec.Body.String())
	}
	// JWTMiddleware's rule for a proxied request with no name: "admin".
	r = httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	r.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2With("agent-key", http.MethodGet, "/api/v1/system/info"))
	if rec := run(r); rec.Code != 200 || rec.Body.String() != "admin" {
		t.Fatalf("signed, no user: %d %q", rec.Code, rec.Body.String())
	}
}

func TestAgentSourceCheck(t *testing.T) {
	_, mgrNet, _ := net.ParseCIDR("192.0.2.0/24")
	h := AgentSourceCheck(func(ip net.IP) bool { return mgrNet.Contains(ip) })(func(c echo.Context) error { return c.NoContent(204) })
	for addr, want := range map[string]int{"127.0.0.1:5000": 204, "[::1]:5000": 204, "192.0.2.7:5000": 204, "198.51.100.9:5000": 403} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		r.RemoteAddr = addr
		r.Header.Set("X-Forwarded-For", "192.0.2.7") // never trusted for this check
		rec := httptest.NewRecorder()
		_ = h(echo.New().NewContext(r, rec))
		if rec.Code != want {
			t.Fatalf("%s: %d, want %d", addr, rec.Code, want)
		}
		if want == 403 && !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
			t.Fatalf("%s: body %s, want PERMISSION_DENIED", addr, rec.Body)
		}
	}
	// Before Task 17 hands the router the managers' addresses it passes nil:
	// loopback only, never everyone.
	loopbackOnly := AgentSourceCheck(nil)(func(c echo.Context) error { return c.NoContent(204) })
	for addr, want := range map[string]int{"127.0.0.1:5000": 204, "192.0.2.7:5000": 403} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		_ = loopbackOnly(echo.New().NewContext(r, rec))
		if rec.Code != want {
			t.Fatalf("nil allowed, %s: %d, want %d", addr, rec.Code, want)
		}
	}
}
