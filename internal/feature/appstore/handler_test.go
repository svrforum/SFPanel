package appstore

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

// newHandler returns a Handler backed by a migrated temp SQLite DB, a
// MockCommander, and a temp ComposePath. Mirrors the auth package's
// openTestDB pattern. The mock's Cmd lets UninstallApp's `docker compose
// down` resolve without touching a real docker daemon.
func newHandler(t *testing.T) *Handler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := sfdb.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return &Handler{
		DB:          db,
		ComposePath: t.TempDir(),
		Cmd:         exec.NewMockCommander(),
	}
}

// TestUninstallApp_NotInstalled asserts that uninstalling an app whose
// staging directory (and docker-compose.yml) does not exist returns 404
// with the NOT_FOUND code, and never shells out to docker compose.
func TestUninstallApp_NotInstalled(t *testing.T) {
	h := newHandler(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/appstore/apps/ghost", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("ghost")

	if err := h.UninstallApp(c); err != nil {
		t.Fatalf("UninstallApp returned err: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), response.ErrNotFound) {
		t.Errorf("body lacks %s code: %s", response.ErrNotFound, rec.Body.String())
	}

	mock := h.Cmd.(*exec.MockCommander)
	for _, call := range mock.Calls {
		if call.Name == "docker" {
			t.Errorf("docker compose was invoked for a non-installed app: %+v", call)
		}
	}
}

// TestInstallApp_RejectsNewlineInEnvValue pins task 2: a simple-mode install
// with an env VALUE containing a newline is rejected with INVALID_BODY and
// the offending key named, before any stream/write begins. Advanced=false so
// the simple-mode path runs; no app needs to exist in cache because the
// newline check fires before the cache/app lookup writes anything.
func TestInstallApp_RejectsNewlineInEnvValue(t *testing.T) {
	h := newHandler(t)

	body := strings.NewReader(`{"env":{"PUID":"1000\nEXTRA=injected"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/apps/demo/install", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("demo")

	if err := h.InstallApp(c); err != nil {
		t.Fatalf("InstallApp returned err: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), response.ErrInvalidBody) {
		t.Errorf("body lacks %s code: %s", response.ErrInvalidBody, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "PUID") {
		t.Errorf("body does not name the offending key PUID: %s", rec.Body.String())
	}
}

// TestWriteFileAtomic_ModeAndContents asserts the helper honours the
// requested file mode and writes the exact bytes. This is the regression
// guard for the 0o644 -> 0o600 tightening: compose YAML carries inline
// secrets through `environment:` blocks, and any future caller that
// re-introduces a wider mode flips this test.
func TestWriteFileAtomic_ModeAndContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	payload := []byte("services:\n  app:\n    image: example/app\n")
	if err := writeFileAtomic(path, payload, 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 0600", got)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("contents mismatch: got %q want %q", got, payload)
	}
}

// TestWriteFileAtomic_NoTempLeftover walks the directory after a successful
// write and refuses to find any *.sfpanel.tmp residue. A crash between
// WriteFile and Rename is the only way one survives; in the success path,
// the rename must move the temp into place atomically.
func TestWriteFileAtomic_NoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	if err := writeFileAtomic(path, []byte("x: 1\n"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sfpanel.tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestWriteFileAtomic_OverwriteExisting verifies that a second write to the
// same path replaces the contents (rename-over-existing) and preserves the
// requested mode. This is the normal "re-install over a prior partial" path.
func TestWriteFileAtomic_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")

	// Pre-seed with a wider mode + different content to confirm both are
	// overwritten by the atomic write.
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode after overwrite = %o, want 0600", got)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("contents after overwrite = %q, want \"new\"", got)
	}
}
