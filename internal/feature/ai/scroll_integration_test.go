package ai

import (
	"context"
	"io"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Exercise an actual tmux attach, including upgrading an existing mouse-off
// session. A wheel byte assertion alone misses the alternate-buffer trap:
// tmux must move its history position, not send arrow keys into the CLI prompt.
// mobile-tmux.spec.ts additionally verifies xterm's visible rows change.
func TestTmuxWheelScrollsExistingSessionHistory(t *testing.T) {
	if _, err := osExec.LookPath("tmux"); err != nil {
		t.Skip("tmux required for real PTY scroll regression")
	}
	h := newTestHandler(t, nil)
	h.socketRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Dir(h.socketPath(h.panel)), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"-f", "/dev/null", "-S", h.socketPath(h.panel)}
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := osExec.CommandContext(ctx, "tmux", append(append([]string{}, base...), args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	id := "aaaaaaaaaaaa"
	run("new-session", "-d", "-s", id, "-x", "80", "-y", "24", "sh -c 'seq 1 300; exec sleep 60'")
	t.Cleanup(func() { _ = osExec.Command("tmux", append(base, "kill-server")...).Run() })
	run("set", "-g", "mouse", "off")
	run("set", "-g", "status", "off")
	name, argv := h.attachArgv(h.panel, id)
	cmd := osExec.Command(name, argv...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	wait := func(check func() bool, message string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(message)
	}
	wait(func() bool { return run("list-clients", "-t", id, "-F", "#{client_tty}") != "" }, "client did not attach")
	// A drag emits several wheel events. The first enters copy mode; subsequent
	// events move its viewport. Keep them separate, as real touch moves are.
	wait(func() bool {
		if _, err := ptmx.Write([]byte("\x1b[<64;10;10M")); err != nil {
			t.Fatal(err)
		}
		position, _ := strconv.Atoi(run("display-message", "-p", "-t", id, "#{scroll_position}"))
		return position > 0
	}, "wheel input did not scroll tmux history")
	wait(func() bool {
		if _, err := ptmx.Write([]byte("\x1b[<65;10;10M")); err != nil {
			t.Fatal(err)
		}
		return run("display-message", "-p", "-t", id, "#{pane_in_mode}") == "0"
	}, "scrolling down did not return to the live CLI")
}
