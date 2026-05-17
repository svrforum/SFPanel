package logs

import (
	"bufio"
	"os/exec"
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
