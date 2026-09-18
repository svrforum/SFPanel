package appstore

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
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

// newAdvancedHandler returns a handler ready for an advanced install: an admin
// row whose password the re-auth step will accept, and a pre-warmed catalog
// cache so ensureCache never reaches the network. The app declares no ports and
// no env, so the conflict checks before the compose gate are no-ops.
func newAdvancedHandler(t *testing.T, password string) *Handler {
	t.Helper()
	h := newHandler(t)
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := h.DB.Exec("INSERT INTO admin (username, password) VALUES (?, ?)", "admin", hash); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	h.apps = []AppStoreMeta{{ID: "demo", Name: "Demo", Version: "1.0.0"}}
	h.cachedAt = time.Now()
	return h
}

// installAdvanced runs one advanced install and returns the SSE body.
func installAdvanced(t *testing.T, h *Handler, body string) string {
	t.Helper()
	return installAdvancedCtx(t, h, body, context.Background())
}

// installAdvancedCtx is installAdvanced with a caller-chosen request context,
// so a test can hang up the way a client does.
func installAdvancedCtx(t *testing.T, h *Handler, body string, ctx context.Context) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/apps/demo/install", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("demo")
	c.Set("username", "admin")
	if err := h.InstallApp(c); err != nil {
		t.Fatalf("InstallApp returned err: %v", err)
	}
	return rec.Body.String()
}

// TestInstallApp_AdvancedRiskyWithoutAcknowledgement pins the App Store half of
// the contract: a docker.sock bind is refused, the refusal names the finding,
// and the half-created stack directory is cleaned up. The install never reaches
// `docker compose pull`, which is what keeps this test off the real daemon —
// and is why the acknowledged case is asserted in the compose package instead,
// where the handler stops at the filesystem. Everything past this gate in the
// App Store is a live `docker compose pull` + `up -d`.
func TestInstallApp_AdvancedRiskyWithoutAcknowledgement(t *testing.T) {
	h := newAdvancedHandler(t, "correct-horse")

	body := `{"advanced":true,"password":"correct-horse","compose":"services:\n  dozzle:\n    image: amir20/dozzle\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock:ro\n"}`
	out := installAdvanced(t, h, body)

	if !strings.Contains(out, "Refused compose file:") {
		t.Fatalf("stream did not refuse the compose file: %s", out)
	}
	if !strings.Contains(out, "/var/run/docker.sock") {
		t.Errorf("refusal does not name the finding: %s", out)
	}
	// The code is what the client acts on: without it the install dead-ends
	// at a terminal failure event instead of asking the operator once.
	if !strings.Contains(out, `"code":"COMPOSE_RISKY"`) {
		t.Errorf("refusal carries no liftable-tier code: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h.ComposePath, "demo")); !os.IsNotExist(err) {
		t.Errorf("refused install left the stack directory behind: %v", err)
	}
}

// TestInstallApp_AdvancedForbiddenEvenWhenAcknowledged asserts the boundary the
// flag must not reach: a /etc/sfpanel bind — the JWT signing secret and the
// cluster CA key — is refused with the acknowledgement set.
func TestInstallApp_AdvancedForbiddenEvenWhenAcknowledged(t *testing.T) {
	h := newAdvancedHandler(t, "correct-horse")

	body := `{"advanced":true,"password":"correct-horse","acknowledge_risks":true,"compose":"services:\n  thief:\n    image: alpine\n    volumes:\n      - /etc/sfpanel:/loot\n"}`
	out := installAdvanced(t, h, body)

	if !strings.Contains(out, "Refused compose file:") {
		t.Fatalf("forbidden bind was not refused: %s", out)
	}
	if !strings.Contains(out, "/etc/sfpanel") {
		t.Errorf("refusal does not name the forbidden path: %s", out)
	}
	// Tiered, not just refused: a client that saw COMPOSE_RISKY here would
	// offer a confirm for a boundary no answer moves.
	if !strings.Contains(out, `"code":"COMPOSE_FORBIDDEN"`) {
		t.Errorf("forbidden refusal did not carry the forbidden code: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h.ComposePath, "demo")); !os.IsNotExist(err) {
		t.Errorf("forbidden install left the stack directory behind: %v", err)
	}
}

// composeConfigWarning is what `docker compose config` prints on stderr,
// verbatim, when a compose file interpolates a variable the .env does not set.
// It exits 0 and writes the resolved document to stdout regardless.
const composeConfigWarning = `time="2026-09-19T01:17:17+09:00" level=warning msg="The \"TZ\" variable is not set. Defaulting to a blank string."`

// fakeComposeConfig puts a `docker` on PATH that answers any invocation with
// that warning on stderr and `resolved` on stdout — the two-stream shape the
// pre-deploy resolver has to read correctly without a docker daemon.
func fakeComposeConfig(t *testing.T, resolved string) {
	t.Helper()
	bin := t.TempDir()
	docPath := filepath.Join(bin, "resolved.yml")
	warnPath := filepath.Join(bin, "warning.txt")
	if err := os.WriteFile(docPath, []byte(resolved), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warnPath, []byte(composeConfigWarning+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\ncat " + warnPath + " >&2\ncat " + docPath + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stagedStack writes the compose file an install has just staged and returns
// its path. The resolver runs with that directory as its working directory —
// that is how compose finds the stack's .env — so it has to exist.
func stagedStack(t *testing.T, h *Handler) string {
	t.Helper()
	dir := filepath.Join(h.ComposePath, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  app:\n    image: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return composePath
}

// The staged compose says ${UPLOAD_LOCATION}; only the .env beside it knows
// that expands to the directory holding the JWT signing secret and the cluster
// CA key, and `up` resolves that same .env. This also pins the stdout-only
// read: merged with compose's stderr the document does not parse, the guard
// would skip as best-effort, and the bind would deploy.
func TestGuardResolvedInstall_RefusesWhatTheEnvExpandedTo(t *testing.T) {
	h := newHandler(t)
	composePath := stagedStack(t, h)
	fakeComposeConfig(t, "name: demo\nservices:\n  immich:\n    image: x\n    volumes:\n      - type: bind\n        source: /etc/sfpanel\n        target: /usr/src/app/upload\n")

	err := h.guardResolvedInstall(context.Background(), composePath)
	if err == nil {
		t.Fatal("install accepted a resolved compose binding /etc/sfpanel")
	}
	if !strings.Contains(err.Error(), "/etc/sfpanel") {
		t.Errorf("refusal %q does not name the path", err)
	}
}

// The other half: the check must not cost ordinary installs. A stack that
// warns about an unset variable and binds its own data directory passes.
func TestGuardResolvedInstall_AcceptsAnOrdinaryStack(t *testing.T) {
	h := newHandler(t)
	composePath := stagedStack(t, h)
	fakeComposeConfig(t, "name: demo\nservices:\n  app:\n    image: x\n    volumes:\n      - type: bind\n        source: /opt/stacks/demo/data\n        target: /data\n")

	if err := h.guardResolvedInstall(context.Background(), composePath); err != nil {
		t.Errorf("an ordinary stack was refused: %v", err)
	}
}

// Best-effort: a resolver that cannot run says nothing about the tier, and the
// `pull` and `up` that follow report the real problem better than a refusal
// naming no path would.
func TestGuardResolvedInstall_IsBestEffortWhenTheConfigCannotResolve(t *testing.T) {
	h := newHandler(t)
	composePath := stagedStack(t, h)
	t.Setenv("PATH", t.TempDir()) // no docker at all

	if err := h.guardResolvedInstall(context.Background(), composePath); err != nil {
		t.Errorf("a resolver failure became a refusal: %v", err)
	}
}

// End to end through the handler: the interpolation hole the deploy guard was
// created to close, on the App Store's own `docker compose up`. The compose
// text passes the save-time check — `${UPLOAD_LOCATION}` is not a host path —
// and the refusal has to come from the resolved document, reach the client as
// the boundary tier, and leave no stack directory behind. Advanced mode is
// what a test can drive (simple mode fetches the catalog compose over the
// network); the guard sits after both branches, on the one path both share.
func TestInstallApp_RefusesWhatTheResolvedConfigBinds(t *testing.T) {
	h := newAdvancedHandler(t, "correct-horse")
	fakeComposeConfig(t, "name: demo\nservices:\n  app:\n    image: x\n    volumes:\n      - type: bind\n        source: /etc/sfpanel\n        target: /data\n")

	body := `{"advanced":true,"password":"correct-horse","compose":"services:\n  app:\n    image: x\n    volumes:\n      - ${UPLOAD_LOCATION}:/data\n","env_raw":"UPLOAD_LOCATION=/etc/sfpanel\n"}`
	out := installAdvanced(t, h, body)

	if !strings.Contains(out, "Refused compose file:") {
		t.Fatalf("install deployed a stack whose .env pointed at /etc/sfpanel: %s", out)
	}
	if !strings.Contains(out, "/etc/sfpanel") {
		t.Errorf("refusal does not name the path: %s", out)
	}
	// COMPOSE_FORBIDDEN, not COMPOSE_RISKY: a client that saw the liftable
	// tier here would offer a confirm for a boundary no answer moves.
	if !strings.Contains(out, `"code":"COMPOSE_FORBIDDEN"`) {
		t.Errorf("refusal did not carry the forbidden code: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h.ComposePath, "demo")); !os.IsNotExist(err) {
		t.Errorf("refused install left the stack directory behind: %v", err)
	}
}

// Hanging up must not lift the boundary. Everything after the guard runs on a
// detached context so a closed browser tab does not abandon a half-installed
// stack — which means a guard bound to the request would be lifted by the
// cheapest possible move: POST the install, close the connection, and let the
// resolver fail into the best-effort skip while `up` deploys the bind.
func TestInstallApp_RefusesEvenWhenTheClientHangsUp(t *testing.T) {
	h := newAdvancedHandler(t, "correct-horse")
	fakeComposeConfig(t, "name: demo\nservices:\n  app:\n    image: x\n    volumes:\n      - type: bind\n        source: /etc/sfpanel\n        target: /data\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client is already gone when the handler reaches the guard

	body := `{"advanced":true,"password":"correct-horse","compose":"services:\n  app:\n    image: x\n    volumes:\n      - ${UPLOAD_LOCATION}:/data\n","env_raw":"UPLOAD_LOCATION=/etc/sfpanel\n"}`
	out := installAdvancedCtx(t, h, body, ctx)

	if !strings.Contains(out, `"code":"COMPOSE_FORBIDDEN"`) {
		t.Fatalf("a disconnected client got the install past the boundary: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h.ComposePath, "demo")); !os.IsNotExist(err) {
		t.Errorf("refused install left the stack directory behind: %v", err)
	}
}
