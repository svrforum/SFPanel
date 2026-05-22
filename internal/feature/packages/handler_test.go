package packages

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateSearchQuery_AllowsMultiWord(t *testing.T) {
	ok := []string{
		"nginx",
		"redis server",
		"libssl-dev",
		"node.js",
		"python3.11",
		"foo bar baz",
		"a+b",
	}
	for _, q := range ok {
		if !validateSearchQuery(q) {
			t.Errorf("validateSearchQuery(%q) should be accepted", q)
		}
	}
}

func TestValidateSearchQuery_RejectsShellMetacharsAndControl(t *testing.T) {
	bad := []string{
		"",          // empty
		"redis; ls", // semicolon
		"a|b",       // pipe
		"a&b",       // ampersand
		"a`b`",      // backtick
		"$(whoami)", // command substitution
		"a\nb",      // newline
		"a\tb",      // tab (we keep apt-cache happy by disallowing whitespace other than space)
		"a/b",       // slash — apt-cache doesn't search paths
		"a*",        // glob
		"a?",        // glob
		"a<b",       // redirection
		"a>b",       // redirection
		"a\"b",      // quote (operator probably typed accidentally; cleaner to reject)
		"a'b",       // quote
	}
	for _, q := range bad {
		if validateSearchQuery(q) {
			t.Errorf("validateSearchQuery(%q) should be rejected", q)
		}
	}
}

func TestValidatePackageName_StillStrict(t *testing.T) {
	// Search query rule must NOT replace the package-name rule. Package
	// names are passed to apt-get install and must remain conservative.
	if validatePackageName("redis server") {
		t.Error("validatePackageName should still reject spaces — package names cannot contain whitespace")
	}
	if !validatePackageName("redis-server") {
		t.Error("validatePackageName(redis-server) should accept")
	}
}

func TestValidatePackageName_RejectsFlagShape(t *testing.T) {
	// Regression: a leading hyphen turns the "package name" into an apt-get
	// flag (e.g. `--reinstall`, `-y`). Even though every install/remove path
	// now passes `--` before the package list, the validator is the first
	// guard and must not accept the flag shape on its own.
	flagShape := []string{
		"--reinstall",
		"-y",
		"--allow-downgrades",
		"-",
		".hidden",  // dpkg names start with [a-z0-9]
		"+plus",    // + is allowed mid-name but not leading
	}
	for _, name := range flagShape {
		if validatePackageName(name) {
			t.Errorf("validatePackageName(%q) should be rejected — leading punctuation is flag-shaped", name)
		}
	}

	// Confirm well-formed names still pass.
	good := []string{
		"nginx",
		"redis-server",
		"libc6",
		"python3.11",
		"g++",
		"lib32stdc++6",
	}
	for _, name := range good {
		if !validatePackageName(name) {
			t.Errorf("validatePackageName(%q) should be accepted", name)
		}
	}
}

// TestDpkgLockHeld_FalseWhenUnlocked confirms an empty, un-flocked lock
// file is reported as available — this is the happy path before any apt
// run starts.
func TestDpkgLockHeld_FalseWhenUnlocked(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock-frontend")
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if dpkgLockHeldAt(lockPath) {
		t.Error("dpkgLockHeldAt = true, want false for unlocked file")
	}
}

// TestDpkgLockHeld_FalseWhenFileMissing confirms that on non-Debian systems
// (or anywhere the path doesn't exist), we report "not held" so apt can
// produce the real error rather than us preempting it with a misleading 409.
func TestDpkgLockHeld_FalseWhenFileMissing(t *testing.T) {
	if dpkgLockHeldAt("/nonexistent/sfpanel-test/path") {
		t.Error("dpkgLockHeldAt = true for missing file, want false (let apt report)")
	}
}

// TestDpkgLockHeld_TrueWhenAnotherProcessHoldsLock spawns a child `flock`
// process that takes an exclusive lock on a tempfile and verifies
// dpkgLockHeldAt observes EWOULDBLOCK. Skipped if the `flock` binary is
// unavailable (Linux-only and not always installed in minimal CI images).
//
// Same-process Linux flock is effectively reentrant for the same fd, so an
// in-process test wouldn't reliably trigger EWOULDBLOCK; a separate process
// is the only way to exercise the held-by-another-process path honestly.
func TestDpkgLockHeld_TrueWhenAnotherProcessHoldsLock(t *testing.T) {
	flockBin, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("flock binary not available; skipping held-lock test")
	}

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock-frontend")
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	// `flock -x <path> -c "sleep 5"` takes an exclusive lock for 5 s.
	cmd := exec.Command(flockBin, "-x", lockPath, "-c", "sleep 5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start flock child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Give the child a moment to actually acquire the lock before we probe.
	// flock(2) is fast but the process needs to exec; poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if dpkgLockHeldAt(lockPath) {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("dpkgLockHeldAt = false, want true while child holds LOCK_EX")
}
