package logs

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestProcessKillUnblocksScanner asserts the underlying invariant the WS
// log-tail handler relies on: killing a never-EOFing subprocess closes its
// stdout pipe, which causes bufio.Scanner.Scan() to return false, which
// closes the scanner goroutine. Before the fix in handler.go, the handler
// waited on the scanner channel without first killing the process, so
// `tail -F` (which never EOFs) pinned the goroutine and the subprocess
// forever on every client disconnect.
//
// The test does not exercise the WS handler directly — that requires a
// gorilla/websocket round-trip and a real tail target — but it validates
// the kill-first-then-wait pattern in isolation.
func TestProcessKillUnblocksScanner(t *testing.T) {
	// `yes` writes "y\n" forever; equivalent to `tail -F` for our purposes.
	cmd := exec.Command("yes")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start yes: %v", err)
	}

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 256*1024), 1024*1024)
		// Drain without doing anything; the test cares about when this
		// loop exits, not what it sees.
		for scanner.Scan() {
		}
	}()

	// Without this kill, scanDone would never close — `yes` runs forever
	// and the scanner blocks indefinitely on its pipe.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	select {
	case <-scanDone:
		// Good: pipe close propagated, scanner exited.
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not exit within 2s of process kill — pipe close did not propagate")
	}

	_ = cmd.Wait()
}

// TestValidateCustomSourcePath_RejectsSymlinkToSensitive exercises P0-13:
// a symlink inside an allowlisted dir pointing OUTSIDE the allowlist must
// be rejected — the literal-prefix check alone misses this bypass.
func TestValidateCustomSourcePath_RejectsSymlinkToSensitive(t *testing.T) {
	tmp := t.TempDir()
	sensitive := filepath.Join(tmp, "sensitive.txt")
	if err := os.WriteFile(sensitive, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(tmp, "allowed")
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "alias.log")
	if err := os.Symlink(sensitive, link); err != nil {
		t.Fatal(err)
	}
	err := validateCustomSourcePath(link, []string{allowed + "/"})
	if err == nil {
		t.Fatalf("validateCustomSourcePath accepted symlink leaving allowlisted dir; want rejection")
	}
}

// TestValidateCustomSourcePath_AllowsBenignInAllowlistSymlink: don't over-block.
func TestValidateCustomSourcePath_AllowsBenignInAllowlistSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real.log")
	if err := os.WriteFile(target, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "alias.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomSourcePath(link, []string{tmp + "/"}); err != nil {
		t.Fatalf("validateCustomSourcePath rejected benign in-allowlist symlink: %v", err)
	}
}

// TestValidateCustomSourcePath_RejectsNonAbsolute + traversal.
func TestValidateCustomSourcePath_RejectsBadInputs(t *testing.T) {
	cases := []string{
		"",
		"relative/path",
		"/var/log/../etc/shadow",
		"/etc/shadow", // not in allowlist
	}
	for _, p := range cases {
		if err := validateCustomSourcePath(p, []string{"/var/log/"}); err == nil {
			t.Errorf("validateCustomSourcePath(%q) accepted; want rejection", p)
		}
	}
}
