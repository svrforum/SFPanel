package compose

import (
	"context"
	"encoding/json"
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
	c, rec := newImportCtx(echo.New(), "", map[string]string{auth.InternalProxyHeaderV2: auth.SignProxyRequestV2(http.MethodPost, "/api/v1/docker/compose/migrate-import")})
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
		auth.InternalProxyHeaderV2:   auth.SignProxyRequestV2(http.MethodPost, "/api/v1/docker/compose/migrate-import"),
		"X-SFPanel-Migration-Sha256": "deadbeef",
	})
	_ = h.MigrateImport(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad bundle, got %d", rec.Code)
	}
}

func TestMigrateImportRejectsOversizeBundle(t *testing.T) {
	auth.SetClusterProxySecret("test-secret")
	defer auth.SetClusterProxySecret("")
	prev := maxMigrationBundleBytes
	maxMigrationBundleBytes = 16
	defer func() { maxMigrationBundleBytes = prev }()

	h := &Handler{ComposePath: t.TempDir()}
	big := strings.Repeat("A", 1024)
	c, rec := newImportCtx(echo.New(), big, map[string]string{
		auth.InternalProxyHeaderV2:   auth.SignProxyRequestV2(http.MethodPost, "/api/v1/docker/compose/migrate-import"),
		"X-SFPanel-Migration-Sha256": "deadbeef",
	})
	_ = h.MigrateImport(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for oversize bundle, got %d", rec.Code)
	}
}

// The target says it takes an import's policy from the sending manager: a
// migration from an agent may go only to a target that does.
func TestMigrateTargetInfoStatesTheImportPolicy(t *testing.T) {
	withFakeDocker(t) // target-info asks docker where it stores images
	auth.SetClusterProxySecret("test-secret")
	defer auth.SetClusterProxySecret("")
	h := &Handler{ComposePath: t.TempDir()}
	const path = "/api/v1/docker/compose/migrate/target-info"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(http.MethodGet, path))
	rec := httptest.NewRecorder()
	if err := h.MigrateTargetInfo(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Data["import_policy"] != float64(1) {
		t.Fatalf("import_policy = %v (%v): %d %s", got.Data["import_policy"], err, rec.Code, rec.Body)
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

func TestTryAcquireVolumesSerializesSharedVolume(t *testing.T) {
	h := &Handler{}
	// First import grabs its volumes (one of them shared).
	got, contended := h.tryAcquireVolumes([]string{"a", "shared", "b"})
	if contended != "" {
		t.Fatalf("first acquire should succeed, got contended %q", contended)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 acquired, got %d", len(got))
	}
	// A second import touching the shared volume is refused and grabs NOTHING.
	got2, contended2 := h.tryAcquireVolumes([]string{"c", "shared", "d"})
	if contended2 != "shared" {
		t.Fatalf("expected contention on shared, got %q", contended2)
	}
	if got2 != nil {
		t.Fatalf("contended acquire must return nil set, got %v", got2)
	}
	// All-or-nothing: the refused import must not have left c/d locked.
	got3, c3 := h.tryAcquireVolumes([]string{"c", "d"})
	if c3 != "" || len(got3) != 2 {
		t.Fatalf("partial lock leaked: c/d should be free, contended=%q got=%v", c3, got3)
	}
	h.releaseVolumes(got3)
	// After the first import releases, the shared volume is free again.
	h.releaseVolumes(got)
	if got4, c4 := h.tryAcquireVolumes([]string{"shared"}); c4 != "" || len(got4) != 1 {
		t.Fatalf("shared not released, contended=%q", c4)
	}
}
