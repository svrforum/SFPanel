package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v4"
)

func newSPATestHandler() echo.HandlerFunc {
	fsys := fstest.MapFS{
		"web/dist/index.html":        {Data: []byte("<!doctype html><div id=root></div>")},
		"web/dist/assets/app-abc.js": {Data: []byte("console.log(1)")},
	}
	return spaHandler(fsys)
}

func doSPAReq(h echo.HandlerFunc, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	_ = h(echo.New().NewContext(req, rec))
	return rec
}

// TestSPAHandler_MissingAssetReturns404 is the core regression for the
// post-upgrade breakage: a missing /assets/*.js must 404, not fall back to
// index.html (which served text/html for a JS request and tripped strict MIME
// checking, bricking the whole SPA after an upgrade).
func TestSPAHandler_MissingAssetReturns404(t *testing.T) {
	rec := doSPAReq(newSPATestHandler(), "/assets/Dashboard-DEAD.js")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing asset: code=%d, want 404 (must not fall back to index.html)", rec.Code)
	}
}

// TestSPAHandler_ClientRouteServesIndexNoCache: extensionless client routes
// still fall back to index.html, served no-cache so upgrades take effect.
func TestSPAHandler_ClientRouteServesIndexNoCache(t *testing.T) {
	for _, p := range []string{"/", "/docker/stacks", "/disk"} {
		rec := doSPAReq(newSPATestHandler(), p)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: code=%d, want 200", p, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control=%q, want no-cache", p, cc)
		}
	}
}

// TestSPAHandler_HashedAssetImmutable: existing hashed assets get a long
// immutable cache so the browser doesn't refetch them every navigation.
func TestSPAHandler_HashedAssetImmutable(t *testing.T) {
	rec := doSPAReq(newSPATestHandler(), "/assets/app-abc.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset: code=%d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control=%q, want immutable", cc)
	}
}

func TestIsStaticAssetPath(t *testing.T) {
	cases := map[string]bool{
		"assets/x.js":      true,
		"favicon.ico":      true,
		"manifest.webmanifest": true,
		"docker":           false,
		"disk/overview":    false,
		"":                 false,
	}
	for p, want := range cases {
		if got := isStaticAssetPath(p); got != want {
			t.Errorf("isStaticAssetPath(%q)=%v, want %v", p, got, want)
		}
	}
}
