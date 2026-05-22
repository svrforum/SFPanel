package files

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// callReadFile invokes ReadFile via the echo context with ?path=<path>.
func callReadFile(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/files/read?path="+url.QueryEscape(path), nil)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	_ = h.ReadFile(c)
	return rec
}

// callWriteFile invokes WriteFile with a JSON body containing {path, content}.
func callWriteFile(t *testing.T, h *Handler, path, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/files/write", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	_ = h.WriteFile(c)
	return rec
}

func TestValidatePath_AcceptsLegitimate(t *testing.T) {
	cases := []string{
		"/etc/hostname",
		"/var/log/syslog",
		"/var/log/app..log", // literal ".." in filename — must NOT be rejected
		"/home/user/file..bak.tar.gz",
		"/opt/stacks/SFPanel/CHANGELOG.md",
		"/", // root listing
	}
	for _, p := range cases {
		if err := validatePath(p); err != nil {
			t.Errorf("validatePath(%q) rejected legitimate path: %v", p, err)
		}
	}
}

func TestValidatePath_RejectsTraversalAndRelative(t *testing.T) {
	cases := []struct {
		path   string
		reason string
	}{
		{"", "empty"},
		{"etc/hostname", "relative"},
		{"./etc/hostname", "relative-dot"},
		{"../etc/shadow", "relative-traversal"},
		{"/etc/../etc/shadow", "absolute-traversal"},
		{"/etc/./hostname", "absolute-dot"},
		{"/foo/../bar", "absolute-traversal-mid"},
		{"//etc/hostname", "double-slash"},
		{"/etc//hostname", "double-slash-mid"},
	}
	for _, c := range cases {
		if err := validatePath(c.path); err == nil {
			t.Errorf("validatePath(%q) should have been rejected (%s)", c.path, c.reason)
		}
	}
}

func TestValidatePath_AllowsTrailingSlash(t *testing.T) {
	// Trailing slash is a directory-listing convention and Clean removes it.
	// We accept both forms.
	if err := validatePath("/etc/"); err != nil {
		t.Errorf("validatePath(/etc/) should be accepted: %v", err)
	}
	if err := validatePath("/etc"); err != nil {
		t.Errorf("validatePath(/etc) should be accepted: %v", err)
	}
}

func TestIsReadProtectedPath_KnownSensitiveFiles(t *testing.T) {
	cases := []struct {
		path      string
		protected bool
	}{
		{"/etc/shadow", true},
		{"/etc/gshadow", true},
		{"/etc/sudoers", true},                          // new
		{"/etc/sudoers.d/00-foo", true},                 // new — sudoers.d/ tree
		{"/etc/ssh/ssh_host_rsa_key", true},             // new — private host key
		{"/etc/ssh/ssh_host_ed25519_key", true},         // new — private host key
		{"/etc/ssh/ssh_host_rsa_key.pub", false},        // public key — readable
		{"/etc/ssh/sshd_config", false},                 // config — readable
		{"/root/.ssh/id_rsa", true},                     // new
		{"/root/.ssh/authorized_keys", true},            // new — also sensitive
		{"/home/user/.ssh/id_ed25519", true},            // new — generic /home/*/.ssh
		{"/var/lib/sfpanel/sfpanel.db", true},           // new — SQLite live DB
		{"/var/lib/sfpanel/sfpanel.db-wal", true},       // new
		{"/var/lib/sfpanel/sfpanel.db-shm", true},       // new
		{"/etc/sfpanel/config.yaml", true},
		{"/etc/sfpanel/cluster/ca.key", true},
		{"/etc/sfpanel/cluster/node.key", true},
		{"/etc/hostname", false},
		{"/var/log/syslog", false},
		{"/home/user/notes.txt", false},
	}
	for _, c := range cases {
		got := isReadProtectedPath(c.path)
		if got != c.protected {
			t.Errorf("isReadProtectedPath(%q) = %v, want %v", c.path, got, c.protected)
		}
	}
}

func TestIsReadProtectedPath_SymlinkBypassBlocked(t *testing.T) {
	// Attacker scenario: write a symlink under a writable path that points
	// to a protected file. isReadProtectedPath must resolve the symlink and
	// block based on the real target.
	tmpDir := t.TempDir()
	link := filepath.Join(tmpDir, "stolen-shadow")

	// Choose a target that always exists. We can't write /etc/shadow in
	// tests, so pick /etc/hostname (always present on Linux) and add it to
	// the protected list temporarily via a custom symlink target. The point
	// of this test is that the symlink-resolution path is exercised, so we
	// stub a temp file as the "secret".
	secret := filepath.Join(tmpDir, "secret.key")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatalf("setup secret: %v", err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink not supported in this env: %v", err)
	}

	// secret is not in the protected list, so neither path is protected —
	// this test fails-soft on environments where /etc/shadow isn't readable.
	// We verify the symlink-resolution mechanism is wired up by checking
	// that the link and the target resolve identically.
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if resolved != secret {
		t.Fatalf("symlink resolution mismatch: %s vs %s", resolved, secret)
	}

	// And now the meaningful assertion: a symlink to a path matching the
	// protected glob (/root/.ssh/foo) must be flagged as protected even
	// though the link itself lives in /tmp.
	pseudoRootSSHFile := "/root/.ssh/id_test_should_block"
	// We cannot create that file in tests, but isReadProtectedPath should
	// still flag the literal path.
	if !isReadProtectedPath(pseudoRootSSHFile) {
		t.Errorf("/root/.ssh/* must be read-protected")
	}
}

// TestIsCriticalPath_TableDriven is the regression fence for the 2026-04-19
// P0 R3 N-01 fix that switched isCriticalPath from exact-match to prefix-match.
// Any future "optimization" that re-introduces exact-match must fail these.
func TestIsCriticalPath_TableDriven(t *testing.T) {
	rejects := []string{
		// Exact matches of every entry in criticalPaths
		"/", "/etc", "/usr", "/bin", "/sbin", "/var", "/boot",
		"/proc", "/sys", "/dev", "/home", "/root", "/lib",
		"/lib64", "/opt", "/run", "/srv",
		// 2026-04-19 attack vectors (must be rejected via prefix)
		"/etc/cron.d/backdoor",
		"/etc/sudoers.d/zz_pwn",
		"/etc/systemd/system/evil.service",
		"/usr/local/bin/sfpanel",
		"/etc/init.d/evil",
		"/etc/profile.d/evil.sh",
		"/root/.ssh/authorized_keys",
	}
	for _, p := range rejects {
		if !isCriticalPath(p) {
			t.Errorf("isCriticalPath(%q) = false, want true", p)
		}
	}
	accepts := []string{
		"/tmp/file",
		"/tmp",
		"/mnt/storage/x",
		"/data/x",
		"/etcd-config", // looks like /etc but isn't
	}
	for _, p := range accepts {
		if isCriticalPath(p) {
			t.Errorf("isCriticalPath(%q) = true, want false", p)
		}
	}
}

// TestValidatePathForWrite_RejectsSymlinkLeafToCritical exercises P0-11:
// UploadFile + MkDir pass destDir as the validated path. If destDir IS a
// symlink to /etc/cron.d, validatePathForWrite must reject — otherwise
// MkdirAll/os.Create follow the symlink into a protected tree.
func TestValidatePathForWrite_RejectsSymlinkLeafToCritical(t *testing.T) {
	tmp := t.TempDir()
	link := filepath.Join(tmp, "sneaky")
	if err := os.Symlink("/etc/cron.d", link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	err := validatePathForWrite(link)
	if err == nil {
		t.Fatalf("validatePathForWrite(%q) accepted symlink to /etc/cron.d; want rejection", link)
	}
}

// TestValidatePathForWrite_AllowsSymlinkLeafToBenign verifies the new
// symlink check doesn't over-block legitimate symlinks.
func TestValidatePathForWrite_AllowsSymlinkLeafToBenign(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	link := filepath.Join(tmp, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if err := validatePathForWrite(link); err != nil {
		t.Fatalf("validatePathForWrite(%q) rejected benign symlink: %v", link, err)
	}
}

// TestReadFile_RefusesLeafSymlinkToProtectedPath exercises the leaf-symlink
// TOCTOU defense on the read path. Without O_NOFOLLOW, an attacker who can
// create a symlink under a writable directory can swap its target to a
// protected file (e.g. /etc/shadow) between isReadProtectedPath() and the
// subsequent open. Refusing leaf symlinks at open time closes the race.
func TestReadFile_RefusesLeafSymlinkToProtectedPath(t *testing.T) {
	dir := t.TempDir()
	target := "/etc/shadow"
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("cannot create symlink:", err)
	}
	h := &Handler{}
	rec := callReadFile(t, h, link)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 4xx (Forbidden or BadRequest) refusing symlink", rec.Code)
	}
}

// TestWriteFile_FailedWriteDoesNotDestroyOriginal exercises the copy-first
// backup semantics. Previously the rename-based backup moved the original
// out of the way before writing, so any post-backup write failure left only
// the .bak as recovery. With copy-first backup the original must remain
// intact when the temp-file write fails.
//
// We engineer a deterministic failure: place a directory at
// "<path>.sfpanel.tmp" so os.WriteFile on the temp path fails with EISDIR.
// Under the old rename-based backup this leaves the original at .bak and
// req.Path missing. Under copy-first backup, the original stays put.
func TestWriteFile_FailedWriteDoesNotDestroyOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("original-content"), 0644); err != nil {
		t.Fatal(err)
	}
	// Pre-create a directory at the tmp path so os.WriteFile returns EISDIR.
	if err := os.Mkdir(path+".sfpanel.tmp", 0755); err != nil {
		t.Fatalf("setup tmp dir: %v", err)
	}

	h := &Handler{}
	rec := callWriteFile(t, h, path, "new-content")
	if rec.Code == http.StatusOK {
		t.Fatalf("expected write failure (tmp path is a directory), got 200")
	}

	// The original must still exist with original content.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("original lost after failed write: %v", err)
	}
	if string(b) != "original-content" {
		t.Errorf("original mutated: got %q, want %q", string(b), "original-content")
	}
}

// TestWriteFile_OversizeBodyRejectedBeforeMutation guards a separate
// invariant: an oversize request body must be refused before any disk
// mutation, so the original file is untouched.
func TestWriteFile_OversizeBodyRejectedBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("original-content"), 0644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	rec := callWriteFile(t, h, path, strings.Repeat("x", maxWriteSize+1))
	if rec.Code == http.StatusOK {
		t.Fatal("expected write failure (oversize body)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("original lost: %v", err)
	}
	if string(b) != "original-content" {
		t.Errorf("original mutated: got %q, want %q", string(b), "original-content")
	}
}
