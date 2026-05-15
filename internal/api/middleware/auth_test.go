package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

// TestJWTMiddleware_AcceptsV2ProxyHeader verifies the middleware uses
// IsInternalProxyRequest (preferring v2) instead of the old v1-only inline
// check — without this, the v2 replay-resistant header would never validate
// on HTTP routes, leaving v1 captured tokens replayable forever.
func TestJWTMiddleware_AcceptsV2ProxyHeader(t *testing.T) {
	auth.SetClusterProxySecret("test-secret-32-bytes-long-enough!!")
	defer auth.SetClusterProxySecret("")

	mw := JWTMiddleware("jwt-secret")
	called := false
	handler := mw(func(c echo.Context) error {
		called = true
		if u, _ := c.Get("username").(string); u != "alice" {
			t.Errorf("username = %q, want alice", u)
		}
		return c.String(200, "ok")
	})

	v2 := auth.SignProxyRequestV2("GET", "/api/v1/test")
	if v2 == "" {
		t.Fatal("SignProxyRequestV2 returned empty")
	}

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set(auth.InternalProxyHeaderV2, v2)
	req.Header.Set("X-SFPanel-Original-User", "alice")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v1/test")

	if err := handler(c); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !called {
		t.Fatal("downstream handler was not invoked — v2 proxy header rejected")
	}
}

// TestJWTMiddleware_RejectsReplayedV2 confirms the nonce cache fires through
// the middleware (not just the bare validator) — second use of the same
// header must fall through to JWT auth and 401.
func TestJWTMiddleware_RejectsReplayedV2(t *testing.T) {
	auth.SetClusterProxySecret("test-secret-32-bytes-long-enough!!")
	defer auth.SetClusterProxySecret("")

	mw := JWTMiddleware("jwt-secret")
	handler := mw(func(c echo.Context) error { return c.String(200, "ok") })

	v2 := auth.SignProxyRequestV2("GET", "/api/v1/test")

	// First call accepted.
	req1 := httptest.NewRequest("GET", "/api/v1/test", nil)
	req1.Header.Set(auth.InternalProxyHeaderV2, v2)
	rec1 := httptest.NewRecorder()
	c1 := echo.New().NewContext(req1, rec1)
	_ = handler(c1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: status = %d, want 200", rec1.Code)
	}

	// Replay rejected by nonce cache → falls through to JWT auth → 401
	req2 := httptest.NewRequest("GET", "/api/v1/test", nil)
	req2.Header.Set(auth.InternalProxyHeaderV2, v2)
	rec2 := httptest.NewRecorder()
	c2 := echo.New().NewContext(req2, rec2)
	_ = handler(c2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("replayed v2 header: status = %d, want 401 (nonce should be rejected and fall through to JWT)", rec2.Code)
	}
}

// TestJWTMiddleware_V1StillAccepted guarantees we didn't break compat with
// peers that haven't been upgraded yet.
func TestJWTMiddleware_V1StillAccepted(t *testing.T) {
	auth.SetClusterProxySecret("test-secret-32-bytes-long-enough!!")
	defer auth.SetClusterProxySecret("")

	mw := JWTMiddleware("jwt-secret")
	called := false
	handler := mw(func(c echo.Context) error {
		called = true
		return c.String(200, "ok")
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(auth.InternalProxyHeader, "test-secret-32-bytes-long-enough!!")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	_ = handler(c)
	if !called {
		t.Fatal("v1 path should still be accepted (backward compat)")
	}
}
