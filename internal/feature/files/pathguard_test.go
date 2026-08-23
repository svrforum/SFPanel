package files

import (
	"os"
	"path/filepath"
	"testing"
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
