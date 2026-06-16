package compose

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

func newImportCtx(e *echo.Echo, body string, headers map[string]string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/migrate-import", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestMigrateImportRejectsNonInternalProxy(t *testing.T) {
	auth.SetClusterProxySecret("") // ensure no internal-proxy is recognized
	h := &Handler{ComposePath: t.TempDir()}
	c, rec := newImportCtx(echo.New(), "", nil)
	_ = h.MigrateImport(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-internal-proxy, got %d", rec.Code)
	}
}

func TestMigrateImportRejectsMissingSha(t *testing.T) {
	auth.SetClusterProxySecret("test-secret")
	defer auth.SetClusterProxySecret("")
	h := &Handler{ComposePath: t.TempDir()}
	c, rec := newImportCtx(echo.New(), "", map[string]string{"X-SFPanel-Internal-Proxy": "test-secret"})
	_ = h.MigrateImport(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing checksum header, got %d", rec.Code)
	}
}

func TestMigrateImportRejectsBadBundle(t *testing.T) {
	auth.SetClusterProxySecret("test-secret")
	defer auth.SetClusterProxySecret("")
	h := &Handler{ComposePath: t.TempDir()}
	c, rec := newImportCtx(echo.New(), "not a tar bundle", map[string]string{
		"X-SFPanel-Internal-Proxy":   "test-secret",
		"X-SFPanel-Migration-Sha256": "deadbeef",
	})
	_ = h.MigrateImport(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad bundle, got %d", rec.Code)
	}
}
