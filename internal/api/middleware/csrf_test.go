package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

func newCSRFHandler() echo.HandlerFunc {
	return CSRFProtect()(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
}

func TestCSRFProtect_GETIsExempt(t *testing.T) {
	handler := newCSRFHandler()
	req := httptest.NewRequest("GET", "/api/v1/anything", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	_ = handler(c)
	if rec.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200 (CSRF check should skip safe methods)", rec.Code)
	}
}

func TestCSRFProtect_BootstrapPathsExempt(t *testing.T) {
	for _, p := range []string{"/api/v1/auth/login", "/api/v1/auth/setup", "/api/v1/auth/refresh"} {
		t.Run(p, func(t *testing.T) {
			handler := newCSRFHandler()
			req := httptest.NewRequest("POST", p, strings.NewReader(""))
			rec := httptest.NewRecorder()
			e := echo.New()
			c := e.NewContext(req, rec)
			c.SetPath(p)
			_ = handler(c)
			if rec.Code != http.StatusOK {
				t.Errorf("%s status = %d, want 200 (bootstrap path should bypass CSRF)", p, rec.Code)
			}
		})
	}
}

func TestCSRFProtect_StateChangeWithoutCookieRejected(t *testing.T) {
	handler := newCSRFHandler()
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/api/v1/settings")
	_ = handler(c)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without CSRF cookie: status = %d, want 403", rec.Code)
	}
}

func TestCSRFProtect_HeaderMismatchRejected(t *testing.T) {
	handler := newCSRFHandler()
	req := httptest.NewRequest("DELETE", "/api/v1/settings", nil)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "good"})
	req.Header.Set(auth.CSRFHeaderName, "bad")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/api/v1/settings")
	_ = handler(c)
	if rec.Code != http.StatusForbidden {
		t.Errorf("mismatched CSRF: status = %d, want 403", rec.Code)
	}
}

func TestCSRFProtect_HeaderMatchAccepted(t *testing.T) {
	handler := newCSRFHandler()
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "secret-token"})
	req.Header.Set(auth.CSRFHeaderName, "secret-token")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/api/v1/settings")
	_ = handler(c)
	if rec.Code != http.StatusOK {
		t.Errorf("matched CSRF: status = %d, want 200", rec.Code)
	}
}

func TestCSRFProtect_BearerWithoutCookiesExempt(t *testing.T) {
	// Zero ambient authority: Authorization header can't be forged
	// cross-site and no sfpanel cookie rides along — must pass without an
	// X-CSRF-Token header (the Tauri desktop wrapper path).
	handler := newCSRFHandler()
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer some.jwt.token")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/api/v1/settings")
	_ = handler(c)
	if rec.Code != http.StatusOK {
		t.Errorf("bearer without sfpanel cookies: status = %d, want 200", rec.Code)
	}
}

func TestCSRFProtect_BearerWithCookieStillChecked(t *testing.T) {
	// Any sfpanel cookie present → full double-submit check applies; a
	// Bearer header alone must not downgrade a browser session.
	cookies := []*http.Cookie{
		{Name: auth.CSRFCookieName, Value: "token"},
		{Name: auth.RefreshCookieName, Value: "refresh"},
	}
	for _, ck := range cookies {
		t.Run(ck.Name, func(t *testing.T) {
			handler := newCSRFHandler()
			req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer some.jwt.token")
			req.AddCookie(ck)
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(req, rec)
			c.SetPath("/api/v1/settings")
			_ = handler(c)
			if rec.Code != http.StatusForbidden {
				t.Errorf("bearer with %s cookie but no CSRF header: status = %d, want 403", ck.Name, rec.Code)
			}
		})
	}
}

func TestCSRFProtect_CookielessNonBearerRejected(t *testing.T) {
	// No cookies AND no Bearer header → the exemption must not fire.
	handler := newCSRFHandler()
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/api/v1/settings")
	_ = handler(c)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookieless non-bearer POST: status = %d, want 403", rec.Code)
	}
}

func TestCSRFProtect_InternalProxyBypass(t *testing.T) {
	auth.SetClusterProxySecret("test-secret-32-bytes-long-enough!!")
	defer auth.SetClusterProxySecret("")

	handler := newCSRFHandler()
	req := httptest.NewRequest("POST", "/api/v1/settings", strings.NewReader("{}"))
	req.Header.Set(auth.InternalProxyHeader, "test-secret-32-bytes-long-enough!!")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/api/v1/settings")
	_ = handler(c)
	if rec.Code != http.StatusOK {
		t.Errorf("internal proxy request: status = %d, want 200 (mTLS auth should bypass CSRF)", rec.Code)
	}
}
