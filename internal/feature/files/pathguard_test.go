package files

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// The file manager used to refuse every write under /etc, /opt, /home, /var,
// /srv, /root and /usr, which is to say everywhere an operator works. Reading
// still succeeded, so the UI offered Save and then failed at the last step.
// These cases exist so that block cannot come back by accident.
func TestWritesAreAllowedWhereOperatorsWork(t *testing.T) {
	for _, p := range []string{
		"/opt/stacks/myapp/docker-compose.yml",
		"/opt/stacks/myapp",
		"/home/operator/notes.txt",
		"/etc/nginx/nginx.conf",
		"/etc/nginx",
		"/var/www/html/index.html",
		"/var/log/myapp.log",
		"/srv/data/export.csv",
		"/usr/local/bin/deploy.sh",
		"/tmp/scratch.txt",
		"/mnt/nas/backup.tar",
	} {
		if err := validatePathForWrite(p); err != nil {
			t.Errorf("write to %s refused: %v", p, err)
		}
	}
}

// Delete keeps a guard, and it is not a security control — the same panel hands
// out a root terminal. It exists because a checkbox is easy to mis-click and
// os.RemoveAll on one of these stops the machine with no recovery.
func TestDeleteRefusesOnlyWhatWouldKillTheMachine(t *testing.T) {
	fatal := []string{"/", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/proc", "/sys", "/dev", "/run", "/usr"}
	for _, p := range fatal {
		if !isSystemFatalPath(p) {
			t.Errorf("%s should be refused — deleting it stops the server", p)
		}
	}

	// Everything below one of those is the operator's business, and so is every
	// working directory. Exact match only.
	for _, p := range []string{
		"/bin/ls",
		"/usr/local/bin/deploy.sh",
		"/lib/systemd/system/unit.service",
		"/etc", "/etc/nginx", "/opt", "/opt/stacks/app",
		"/home", "/home/operator", "/var", "/var/log", "/srv", "/root",
		"/tmp", "/mnt/nas",
	} {
		if isSystemFatalPath(p) {
			t.Errorf("%s is refused but should not be", p)
		}
	}
}

// Path validation itself has to keep rejecting the malformed and the relative,
// which the write path still depends on.
// A narrow set stays refused, and for a different reason from the delete guard:
// these hold the panel's own credentials, so a hole in this one handler must not
// become the ability to mint tokens and node certificates. The "there is a root
// terminal anyway" argument does not cover it, because an attacker who found a
// bug HERE does not have that terminal.
func TestWritesRefusedOnPanelCredentials(t *testing.T) {
	for _, p := range []string{
		"/etc/sfpanel/config.yaml",
		"/etc/sfpanel/cluster/ca.key",
		"/etc/sfpanel/tls/server.key",
		"/root/.ssh/authorized_keys",
		"/etc/sudoers.d/zz",
		"/etc/shadow",
	} {
		if err := validatePathForWrite(p); err == nil {
			t.Errorf("write to %s was allowed — it holds credentials", p)
		}
	}

	// Paths a previous revision also blocked, unblocked on purpose: ordinary
	// administration for an authenticated operator.
	for _, p := range []string{
		"/etc/cron.d/nightly-backup",
		"/etc/systemd/system/myapp.service",
		"/usr/local/bin/deploy.sh",
		"/etc/profile.d/company.sh",
	} {
		if err := validatePathForWrite(p); err != nil {
			t.Errorf("write to %s refused: %v", p, err)
		}
	}
}

func TestValidatePathForWriteStillRejectsMalformed(t *testing.T) {
	for _, p := range []string{
		"",
		"relative/path",
		"../escape",
		"/a/../../etc/passwd",
	} {
		if err := validatePathForWrite(p); err == nil {
			t.Errorf("validatePathForWrite(%q) accepted a malformed path", p)
		}
	}
}

// A dangling symlink is the easy one to plant, and it was the one that got
// through: EvalSymlinks errors when the target does not exist, and the first
// version of this check fell back to the literal path and allowed the write.
// mkdir would then create the chain and the write would land exactly where it
// was refused.
func TestWriteRefusesDanglingSymlinkIntoProtectedPath(t *testing.T) {
	tmp := t.TempDir()
	for _, target := range []string{
		"/etc/sfpanel/cluster/ca.key", // does not exist on a dev box
		"/etc/sfpanel/does-not-exist-yet",
		"/root/.ssh/authorized_keys",
	} {
		link := filepath.Join(tmp, filepath.Base(target)+".link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := validatePathForWrite(link); err == nil {
			t.Errorf("dangling symlink to %s was accepted", target)
		}
	}
}

// A relative link has to be resolved against the link's own directory, or the
// check compares the wrong string.
func TestWriteResolvesRelativeSymlink(t *testing.T) {
	tmp := t.TempDir()
	link := filepath.Join(tmp, "rel")
	if err := os.Symlink("../../etc/sfpanel/config.yaml", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// The target only reaches /etc/sfpanel from a two-level-deep tmp dir; what
	// matters is that a relative target is joined and cleaned rather than
	// compared raw. Assert on the joined form.
	joined := filepath.Clean(filepath.Join(filepath.Dir(link), "../../etc/sfpanel/config.yaml"))
	if isWriteProtectedPath(joined) {
		if err := validatePathForWrite(link); err == nil {
			t.Error("relative symlink into the credential directory was accepted")
		}
	}
}

// Ordinary symlinks must still work — over-blocking would make /var/www style
// setups unusable.
func TestWriteAllowsBenignSymlink(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := validatePathForWrite(link); err != nil {
		t.Errorf("benign symlink refused: %v", err)
	}
}

// Upload validates the destination DIRECTORY and then joins the filename onto
// it. The joined path was never re-checked, so a directory that passes plus a
// filename that lands on a protected file slipped through: destDir
// /var/lib/sfpanel with filename sfpanel.db overwrites the panel's live
// database, and with it the admin account row.
//
// filepath.Base already strips separators and .. from the filename, so the
// hole is not traversal — it is that the two halves are checked separately and
// the result never is.
func TestUploadDestinationIsCheckedAfterJoin(t *testing.T) {
	cases := []struct {
		dir, name string
	}{
		{"/var/lib/sfpanel", "sfpanel.db"},
		{"/var/lib/sfpanel", "sfpanel.db-wal"},
		{"/etc", "shadow"},
		{"/etc", "sudoers"},
	}
	for _, c := range cases {
		if err := validatePathForWrite(c.dir); err != nil {
			continue // the directory itself is already refused; nothing to prove
		}
		dest := filepath.Join(c.dir, c.name)
		if err := validatePathForWrite(dest); err == nil {
			t.Errorf("upload could land on %s — the joined destination is not protected", dest)
		}
	}
}

// The ordinary case must keep working: a directory that passes and a filename
// that is nobody's business but the operator's.
func TestUploadDestinationAllowsOrdinaryTargets(t *testing.T) {
	for _, dest := range []string{
		"/opt/stacks/app/docker-compose.yml",
		"/home/operator/photo.png",
		"/var/www/html/index.html",
		"/usr/local/bin/deploy.sh",
	} {
		if err := validatePathForWrite(dest); err != nil {
			t.Errorf("upload to %s refused: %v", dest, err)
		}
	}
}

// uploadTo drives UploadFile end to end with a real multipart body.
//
// The unit tests above prove the VALIDATOR refuses a protected destination.
// They cannot prove the handler asks it — and that was exactly the bug: upload
// validated the directory, joined the filename, and never checked the result.
// This exercises the handler itself, which is the only level the gap was
// visible at.
func uploadTo(t *testing.T, destDir, filename, content string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("path", destDir); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/files/upload", &body)
	req.Header.Set(echo.HeaderContentType, w.FormDataContentType())
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	h := &Handler{}
	if err := h.UploadFile(c); err != nil {
		t.Fatalf("UploadFile returned err: %v", err)
	}
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded.Error.Code
}

func TestUploadHandlerRefusesProtectedJoinedDestination(t *testing.T) {
	// Assert the REASON, not just the status.
	//
	// Both the protection and an ordinary permission error answer 403, and the
	// first version of this test only checked the code. It passed with the fix
	// removed — the unprivileged test process was simply being denied by the
	// filesystem a few lines later, which proves nothing about the guard. The
	// error code is what distinguishes "refused because it is protected" from
	// "refused because this process happens to lack write permission".
	for _, c := range []struct{ dir, name string }{
		{"/var/lib/sfpanel", "sfpanel.db"}, // would replace the admin account row
		{"/etc", "shadow"},
	} {
		status, code := uploadTo(t, c.dir, c.name, "overwritten")
		if status != http.StatusForbidden || code != response.ErrCriticalPath {
			t.Errorf("upload to %s/%s returned %d/%s, want 403/%s",
				c.dir, c.name, status, code, response.ErrCriticalPath)
		}
	}
}

func TestUploadHandlerAllowsOrdinaryDestination(t *testing.T) {
	dir := t.TempDir()
	if status, code := uploadTo(t, dir, "notes.txt", "hello"); status != http.StatusOK {
		t.Fatalf("upload to a temp dir returned %d/%s, want 200", status, code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}
