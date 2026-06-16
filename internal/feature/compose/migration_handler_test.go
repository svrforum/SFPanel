package compose

import (
	"context"
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

func TestMigrateFinalizeRejectsUnknownDisposition(t *testing.T) {
	// An unknown disposition errors before touching docker, so a zero-value
	// Handler is enough — retain/delete/clone are covered by the 2-node e2e.
	h := &Handler{}
	if err := h.migrateFinalize(context.Background(), "demo", Disposition("bogus")); err == nil {
		t.Fatal("expected error for unknown disposition")
	}
	// retain is a no-op (source already stopped) and must not error.
	if err := h.migrateFinalize(context.Background(), "demo", DispositionRetain); err != nil {
		t.Fatalf("retain should be a no-op, got %v", err)
	}
}

func TestMigrateRejectsTraversalProject(t *testing.T) {
	h := &Handler{ComposePath: t.TempDir()}
	for _, bad := range []string{"..", "../etc", "a/b", ".hidden", "a..b"} {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/x/migrate", strings.NewReader(`{"targetNodeId":"n","disposition":"retain"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("project")
		c.SetParamValues(bad)
		_ = h.Migrate(c)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("project %q: want 400, got %d", bad, rec.Code)
		}
	}
}

func TestMigratePreflightRejectsTraversalProject(t *testing.T) {
	h := &Handler{ComposePath: t.TempDir()}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/x/migrate/preflight", strings.NewReader(`{"targetNodeId":"n"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("../escape")
	_ = h.MigratePreflight(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for traversal project, got %d", rec.Code)
	}
}
