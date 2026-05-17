package system

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/release"
)

// TestMain neutralises the supervisor-less restart's os.Exit call for the
// duration of all tests in this package. Without this, every RestoreBackup
// happy-path test (and every RunUpdate that falls into the no-systemd
// branch) would queue a 2-second-delayed os.Exit(0) and the entire test
// binary would terminate mid-suite once the timer fired. The override lets
// us still assert the exit *was scheduled* via an atomic counter.
//
// We also pin isSystemdActive to false by default so tests are deterministic
// regardless of whether the host has /run/systemd/system (a dev workstation
// running `go test` against a systemd Linux box would otherwise take the
// systemctl branch and skip the supervisor-less code paths these tests
// exist to exercise). Tests that need the systemd-active branch override it
// per-test via withSystemdActive(true).
var exitCount atomic.Int32

func TestMain(m *testing.M) {
	prevExit := exitProcess
	exitProcess = func() { exitCount.Add(1) }
	defer func() { exitProcess = prevExit }()

	prevSystemd := isSystemdActive
	isSystemdActive = func() bool { return false }
	defer func() { isSystemdActive = prevSystemd }()

	os.Exit(m.Run())
}

// withSystemdActive flips isSystemdActive for the duration of a single test
// (cleaned up via t.Cleanup). Tests using this must not call t.Parallel —
// the override is a package-level var and would race across goroutines.
func withSystemdActive(t *testing.T, v bool) {
	t.Helper()
	prev := isSystemdActive
	isSystemdActive = func() bool { return v }
	t.Cleanup(func() { isSystemdActive = prev })
}

// newHandler wires a Handler against the fake release server. We reach into
// it by setting the GitHub URL via a substitute http.RoundTripper would be
// cleaner long-term, but RunUpdate hard-codes the URL, so for these tests
// we exercise the easier paths: TryLock contention and the downgrade guard.
// Both kick in *before* the GitHub call.
func newHandler(version string) *Handler {
	return &Handler{
		Version:    version,
		DBPath:     "/tmp/test.db",
		ConfigPath: "/tmp/test.yaml",
		Cmd:        commonExec.NewMockCommander(),
	}
}

// TestRunUpdateConcurrentLock proves a second concurrent caller is rejected
// with HTTP 409 / UPDATE_IN_PROGRESS rather than racing into the download.
func TestRunUpdateConcurrentLock(t *testing.T) {
	h := newHandler("0.11.1")

	// Take the lock manually to simulate an in-flight update.
	updateMu.Lock()
	defer updateMu.Unlock()

	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil)
	ctx := e.NewContext(req, rec)

	if err := h.RunUpdate(ctx); err != nil {
		t.Fatalf("RunUpdate returned error: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UPDATE_IN_PROGRESS") {
		t.Errorf("expected UPDATE_IN_PROGRESS error code, got body: %s", rec.Body.String())
	}
}

// TestRunUpdateLockReleased verifies the lock is released after the handler
// returns so subsequent updates aren't permanently blocked. We can't drive
// a successful end-to-end update in a unit test (it would actually swap the
// binary), so we drive the lock semantics through TryLock directly.
func TestRunUpdateLockReleased(t *testing.T) {
	if !updateMu.TryLock() {
		t.Fatal("expected lock to be free at test start")
	}
	updateMu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if !updateMu.TryLock() {
			t.Error("second goroutine could not acquire lock after release")
			return
		}
		updateMu.Unlock()
	}()
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Update flow: downgrade + up-to-date guards (hits the GitHub call, served
// from a local httptest server via the releaseAPIURL test seam).
// ---------------------------------------------------------------------------

// withReleaseAPI temporarily points the package-level releaseAPIURL at u for
// the duration of a single test.
func withReleaseAPI(t *testing.T, u string) {
	t.Helper()
	prev := releaseAPIURL
	releaseAPIURL = u
	t.Cleanup(func() { releaseAPIURL = prev })
}

// fakeReleaseServer returns an httptest.Server that responds with a minimal
// GitHub Releases JSON document — enough for CheckUpdate / RunUpdate to
// decode and run version comparison.
func fakeReleaseServer(t *testing.T, tag string, assets []release.Asset) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubRelease{
			TagName:     tag,
			Body:        "test release",
			PublishedAt: "2026-05-17T00:00:00Z",
			Assets:      assets,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunUpdateDowngradeBlocked confirms that when the upstream advertises a
// version older than what we're running, RunUpdate refuses with the
// UPDATE_DOWNGRADE_BLOCKED error code. This is a defence-in-depth check
// against a compromised release page that points to a known-vulnerable older
// tag — without this guard the binary would silently roll backwards.
func TestRunUpdateDowngradeBlocked(t *testing.T) {
	srv := fakeReleaseServer(t, "v0.10.0", nil)
	withReleaseAPI(t, srv.URL)

	h := newHandler("0.13.10")
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil)
	ctx := e.NewContext(req, rec)

	if err := h.RunUpdate(ctx); err != nil {
		t.Fatalf("RunUpdate returned error: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "UPDATE_DOWNGRADE_BLOCKED") {
		t.Errorf("expected UPDATE_DOWNGRADE_BLOCKED error code, got body: %s", rec.Body.String())
	}
}

// TestRunUpdateUpToDateShortCircuits confirms the handler returns the
// up_to_date status without falling through to the downgrade/SSE branches
// when current == latest.
func TestRunUpdateUpToDateShortCircuits(t *testing.T) {
	srv := fakeReleaseServer(t, "v0.13.10", nil)
	withReleaseAPI(t, srv.URL)

	h := newHandler("0.13.10")
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil)
	ctx := e.NewContext(req, rec)

	if err := h.RunUpdate(ctx); err != nil {
		t.Fatalf("RunUpdate returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "up_to_date") {
		t.Errorf("expected up_to_date status, got body: %s", rec.Body.String())
	}
}

// TestCheckUpdateForwardAvailable proves that when upstream is newer,
// CheckUpdate flags UpdateAvailable=true. Belt-and-braces for the
// API-side regression that would mask available updates from the UI.
func TestCheckUpdateForwardAvailable(t *testing.T) {
	srv := fakeReleaseServer(t, "v0.14.0", nil)
	withReleaseAPI(t, srv.URL)

	h := newHandler("0.13.10")
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/update/check", nil)
	ctx := e.NewContext(req, rec)

	if err := h.CheckUpdate(ctx); err != nil {
		t.Fatalf("CheckUpdate returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool                `json:"success"`
		Data    UpdateCheckResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if !env.Data.UpdateAvailable {
		t.Errorf("expected UpdateAvailable=true for 0.13.10 -> 0.14.0")
	}
	if env.Data.LatestVersion != "0.14.0" || env.Data.CurrentVersion != "0.13.10" {
		t.Errorf("version fields wrong: %+v", env.Data)
	}
}

// ---------------------------------------------------------------------------
// Update flow: SHA-256 mismatch rejection. Drives the full RunUpdate path
// against a local server that serves a binary whose hash does not match the
// checksums.txt — the handler should emit an SSE 'error' event and never
// touch the binary on disk.
// ---------------------------------------------------------------------------

// TestRunUpdateRejectsChecksumMismatch builds a fake release server that
// returns a tarball whose SHA-256 doesn't match the published checksum.
// The handler should emit an SSE 'error' event and bail before any rename.
func TestRunUpdateRejectsChecksumMismatch(t *testing.T) {
	// Build a tiny valid-looking tar.gz with a "sfpanel" entry. The actual
	// bytes don't matter — we'll lie about its checksum.
	var archiveBuf bytes.Buffer
	gw := gzip.NewWriter(&archiveBuf)
	tw := tar.NewWriter(gw)
	body := []byte("fake binary payload")
	_ = tw.WriteHeader(&tar.Header{Name: "sfpanel", Size: int64(len(body)), Mode: 0755, Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gw.Close()

	// Choose an obviously-wrong expected hash so the mismatch is unambiguous.
	wrongHash := strings.Repeat("0", 64)
	// Real hash, for the parallel sanity check at the end.
	realHash := sha256.Sum256(archiveBuf.Bytes())

	const (
		archiveName   = "sfpanel_0.14.0_linux_amd64.tar.gz"
		checksumsName = "checksums.txt"
	)
	checksumsBody := fmt.Sprintf("%s  %s\n", wrongHash, archiveName)

	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		// /api is bound below as the rewrite of releaseAPIURL.
	})

	var srv *httptest.Server
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBuf.Bytes())
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, checksumsBody)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve the GitHub release JSON at root.
		_ = json.NewEncoder(w).Encode(GitHubRelease{
			TagName: "v0.14.0",
			Assets: []release.Asset{
				{Name: archiveName, BrowserDownloadURL: srv.URL + "/archive"},
				{Name: checksumsName, BrowserDownloadURL: srv.URL + "/checksums"},
			},
		})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	withReleaseAPI(t, srv.URL+"/")

	// Skip on architectures we don't have an asset fixture for. RunUpdate
	// asks for sfpanel_<ver>_linux_<runtime.GOARCH>.tar.gz; if the host
	// arch doesn't match the asset name we generated above, the handler
	// would correctly bail with "Release asset not found" — which is a
	// real bug to test elsewhere, just not the path this test exercises.
	if !strings.Contains(archiveName, "_"+runtime.GOARCH+".") {
		t.Skipf("test fixture assumes amd64 asset naming, host is %s", runtime.GOARCH)
	}

	h := newHandler("0.13.10")
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil)
	ctx := e.NewContext(req, rec)

	if err := h.RunUpdate(ctx); err != nil {
		t.Fatalf("RunUpdate returned error: %v", err)
	}

	// SSE 'error' event must mention the checksum failure. RunUpdate writes
	// a literal "Checksum verification failed" string when hashes diverge.
	body2 := rec.Body.String()
	if !strings.Contains(body2, "Checksum verification failed") {
		t.Errorf("expected SSE error event for checksum mismatch, got body:\n%s", body2)
	}
	// Sanity: the bytes we actually served hashed to realHash; this is just
	// to keep the test honest if someone refactors the fixture builder.
	if hex.EncodeToString(realHash[:]) == wrongHash {
		t.Fatalf("fixture programming error: real hash collides with wrongHash")
	}
}

// ---------------------------------------------------------------------------
// Backup restore: tar validation paths. The handler accepts a tar.gz upload
// and must:
//   - reject symlink / hardlink / non-regular entries (no exotic typeflags)
//   - reject path-traversal entries (".." or absolute paths)
//   - silently drop entries outside the {sfpanel.db, config.yaml, compose/*}
//     allowlist
//   - require sfpanel.db to be present
//   - atomically rename .new → final on success
// ---------------------------------------------------------------------------

// tarEntry is a single archive member used by buildTarGz fixtures.
type tarEntry struct {
	name     string
	typeflag byte
	body     []byte
	linkname string // for symlink/hardlink entries
	mode     int64
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0644
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     mode,
			Size:     int64(len(e.body)),
			Linkname: e.linkname,
		}
		if e.typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar.WriteHeader(%s): %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("tar.Write(%s): %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	return buf.Bytes()
}

// newRestoreRequest packages the supplied tar.gz bytes as a multipart upload
// against the RestoreBackup handler.
func newRestoreRequest(t *testing.T, archive []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, err := mw.CreateFormFile("backup", "backup.tar.gz")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := w.Write(archive); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/restore", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// restoreHandler wires a Handler against per-test temp paths so each
// RestoreBackup invocation operates in its own sandbox. The Cmd mock has no
// outputs configured, so it returns the zero MockResult (empty string, nil
// err); that means `systemctl is-active` *succeeds* by default. The reason
// the restart side-effect doesn't fire in most tests is that TestMain pins
// isSystemdActive to false — the handler short-circuits before reaching the
// systemctl call. Tests that flip isSystemdActive to true and want the
// supervisor-less branch must explicitly SetOutput an error for systemctl.
func restoreHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	composeDir := filepath.Join(dir, "compose")
	if err := os.MkdirAll(composeDir, 0755); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		DBPath:      filepath.Join(dir, "sfpanel.db"),
		ConfigPath:  filepath.Join(dir, "config.yaml"),
		ComposePath: composeDir,
		Cmd:         commonExec.NewMockCommander(),
	}
	// Seed plausible existing files so the backup-of-current path runs.
	_ = os.WriteFile(h.DBPath, []byte("old-db"), 0600)
	_ = os.WriteFile(h.ConfigPath, []byte("old: config\n"), 0600)
	return h, dir
}

func runRestore(t *testing.T, h *Handler, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	rec := httptest.NewRecorder()
	ctx := e.NewContext(newRestoreRequest(t, archive), rec)
	if err := h.RestoreBackup(ctx); err != nil {
		t.Fatalf("RestoreBackup returned error: %v", err)
	}
	return rec
}

// TestRestoreBackup_HappyPath confirms a valid archive containing
// sfpanel.db + config.yaml + a compose stack file is restored atomically:
// destination files contain the new contents and no .new temp file is left
// behind.
func TestRestoreBackup_HappyPath(t *testing.T) {
	h, _ := restoreHandler(t)
	const newDB = "fresh-db-contents"
	const newCfg = "server:\n  port: 3628\n"
	const composeBody = "services:\n  test:\n    image: nginx\n"
	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte(newDB)},
		{name: "config.yaml", typeflag: tar.TypeReg, body: []byte(newCfg)},
		{name: "compose/app/docker-compose.yml", typeflag: tar.TypeReg, body: []byte(composeBody)},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := os.ReadFile(h.DBPath)
	if string(got) != newDB {
		t.Errorf("DB not replaced: got %q want %q", got, newDB)
	}
	gotCfg, _ := os.ReadFile(h.ConfigPath)
	if string(gotCfg) != newCfg {
		t.Errorf("config not replaced: got %q want %q", gotCfg, newCfg)
	}
	gotCompose, err := os.ReadFile(filepath.Join(h.ComposePath, "app", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("compose file not restored: %v", err)
	}
	if string(gotCompose) != composeBody {
		t.Errorf("compose body wrong: got %q want %q", gotCompose, composeBody)
	}
	// No .new temp files left behind.
	for _, leftover := range []string{h.DBPath + ".new", h.ConfigPath + ".new"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("temp file %q was not renamed away", leftover)
		}
	}
}

// TestRestoreBackup_SymlinkEntryDropped confirms the handler discards
// symlink entries even when their name looks legitimate. A crafted archive
// containing `compose/app/docker-compose.yml -> /etc/cron.d/evil` must not
// reach disk.
func TestRestoreBackup_SymlinkEntryDropped(t *testing.T) {
	h, _ := restoreHandler(t)
	archive := buildTarGz(t, []tarEntry{
		// Required entry so the handler doesn't 400 on missing sfpanel.db.
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte("db")},
		// Hostile symlink entry: name passes the allowlist prefix check.
		{name: "compose/app/docker-compose.yml", typeflag: tar.TypeSymlink, linkname: "/etc/cron.d/evil"},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// The symlink must not have materialised under ComposePath.
	dest := filepath.Join(h.ComposePath, "app", "docker-compose.yml")
	if _, err := os.Stat(dest); err == nil {
		t.Errorf("symlink entry was materialised at %s — guard failed", dest)
	}
}

// TestRestoreBackup_PathTraversalDropped confirms entries with ".." in the
// archive name are dropped, so a crafted archive cannot write outside the
// allowed roots.
func TestRestoreBackup_PathTraversalDropped(t *testing.T) {
	h, _ := restoreHandler(t)
	// Place a sentinel file one directory above ComposePath. If the guard
	// is broken, "compose/../sentinel" would rewrite it.
	parent := filepath.Dir(h.ComposePath)
	sentinel := filepath.Join(parent, "sentinel")
	if err := os.WriteFile(sentinel, []byte("ORIGINAL"), 0644); err != nil {
		t.Fatal(err)
	}

	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte("db")},
		// Path traversal: filepath.Clean("compose/../sentinel") == "sentinel".
		// Since "sentinel" doesn't start with "compose/", the allowlist also
		// drops it — belt-and-braces guard verification.
		{name: "compose/../sentinel", typeflag: tar.TypeReg, body: []byte("PWNED")},
		// Explicit ".." prefix — caught by the strings.HasPrefix(clean, "..") check.
		{name: "../etc/passwd", typeflag: tar.TypeReg, body: []byte("PWNED")},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := os.ReadFile(sentinel)
	if string(got) != "ORIGINAL" {
		t.Errorf("sentinel file was overwritten: got %q — traversal guard failed", got)
	}
}

// TestRestoreBackup_AbsolutePathDropped confirms entries with absolute paths
// (`/etc/passwd`) are dropped — neither the allowlist prefix match nor the
// IsAbs check should let them through.
func TestRestoreBackup_AbsolutePathDropped(t *testing.T) {
	h, _ := restoreHandler(t)
	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte("db")},
		{name: "/tmp/sfpanel-absolute-evil", typeflag: tar.TypeReg, body: []byte("PWNED")},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat("/tmp/sfpanel-absolute-evil"); err == nil {
		_ = os.Remove("/tmp/sfpanel-absolute-evil")
		t.Errorf("absolute-path entry was materialised — IsAbs guard failed")
	}
}

// TestRestoreBackup_UnknownFileSilentlyDropped confirms files outside the
// {sfpanel.db, config.yaml, compose/**} allowlist are silently dropped: they
// don't reach disk and they don't cause an error response either.
func TestRestoreBackup_UnknownFileSilentlyDropped(t *testing.T) {
	h, dir := restoreHandler(t)
	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte("db")},
		{name: "secrets.txt", typeflag: tar.TypeReg, body: []byte("nope")},
		{name: "etc/passwd", typeflag: tar.TypeReg, body: []byte("nope")},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"secrets.txt", "etc/passwd"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			t.Errorf("non-allowlisted entry %q was materialised", name)
		}
	}
}

// TestRestoreBackup_MissingDBRejected confirms an archive that lacks
// sfpanel.db is rejected with 400 / RESTORE_FAILED before any file is
// touched.
func TestRestoreBackup_MissingDBRejected(t *testing.T) {
	h, _ := restoreHandler(t)
	originalDB, _ := os.ReadFile(h.DBPath)

	archive := buildTarGz(t, []tarEntry{
		{name: "config.yaml", typeflag: tar.TypeReg, body: []byte("only: cfg\n")},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "RESTORE_FAILED") {
		t.Errorf("expected RESTORE_FAILED error code, body: %s", rec.Body.String())
	}
	got, _ := os.ReadFile(h.DBPath)
	if !bytes.Equal(got, originalDB) {
		t.Errorf("DB was modified despite missing-sfpanel.db rejection: got %q want %q", got, originalDB)
	}
}

// TestRestoreBackup_InvalidGzipRejected exercises the "uploaded a random
// file" case: the first read of gzip.NewReader fails and the handler
// returns 400 / RESTORE_FAILED with a recognisable message.
func TestRestoreBackup_InvalidGzipRejected(t *testing.T) {
	h, _ := restoreHandler(t)
	rec := runRestore(t, h, []byte("this is not a gzip archive"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid gzip file") {
		t.Errorf("expected gzip-rejection message, body: %s", rec.Body.String())
	}
}

// TestRestoreBackup_NoSystemdEmitsSupervisorMessage verifies the
// no-supervisor branch: when lifecycle.IsSystemdActive() returns false
// (the test environment lacks /run/systemd/system; even if it didn't, the
// mock Commander returns an error from `systemctl is-active`), the response
// message tells the operator that the process is exiting and an external
// supervisor must restart sfpanel. Also confirms exitProcess was scheduled
// (via the TestMain hook) so the panel really does come down rather than
// keeping a stale DB handle open.
func TestRestoreBackup_NoSystemdEmitsSupervisorMessage(t *testing.T) {
	before := exitCount.Load()

	h, _ := restoreHandler(t)
	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte("db")},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"process is exiting", "supervisor"} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing %q in supervisor-less mode: %s", want, body)
		}
	}

	// The exit goroutine sleeps 2 s before calling exitProcess. Wait up to
	// 3 s for it to fire — if it doesn't, the restart side-effect was lost
	// and the panel would keep serving with a stale DB.
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if exitCount.Load() > before {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected exitProcess to be scheduled in no-systemd branch (before=%d, now=%d)", before, exitCount.Load())
		case <-tick.C:
		}
	}
}

// TestRestoreBackup_SystemdPresentButUnitInactive verifies the fall-through
// branch: systemd is up but `systemctl is-active sfpanel` returns non-zero
// (renamed unit, dev `go run` on a systemd host, etc.). The handler must
// emit the supervisor-less message rather than silently claim a restart was
// scheduled — otherwise the operator sees "Service restarting..." and waits
// forever for a service that won't come back.
func TestRestoreBackup_SystemdPresentButUnitInactive(t *testing.T) {
	withSystemdActive(t, true)
	before := exitCount.Load()

	h, _ := restoreHandler(t)
	// Make `systemctl is-active --quiet sfpanel` fail (renamed unit, not
	// installed, etc.) to exercise the systemd-present-but-unit-inactive
	// fall-through into the supervisor-less branch.
	h.Cmd.(*commonExec.MockCommander).SetOutput("systemctl", "", errors.New("inactive"))
	archive := buildTarGz(t, []tarEntry{
		{name: "sfpanel.db", typeflag: tar.TypeReg, body: []byte("db")},
	})

	rec := runRestore(t, h, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"process is exiting", "supervisor"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected supervisor-less message when unit inactive, got: %s", body)
		}
	}

	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if exitCount.Load() > before {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected exitProcess to be scheduled when systemd unit inactive (before=%d, now=%d)", before, exitCount.Load())
		case <-tick.C:
		}
	}
}
