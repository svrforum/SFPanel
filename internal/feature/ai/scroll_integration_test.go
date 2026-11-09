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
	// The scrollback has to exist before the first wheel event, and a client
	// attaches within ~30 ms whether or not tmux has read the pane's 300 lines
	// yet. tmux fixes copy mode's scrollable range at the history size it finds
	// when the mode opens: opened on a pane tmux has not filled, the viewport
	// sits at 0 however many wheel events follow, because only leaving and
	// re-entering the mode picks up the lines that arrived since. Waiting for
	// the client alone lost that race about one run in ten, and the loop below
	// cannot retry its way out of it.
	wait(func() bool {
		size, _ := strconv.Atoi(run("display-message", "-p", "-t", id, "#{history_size}"))
		return size >= 100 // 300 lines less a 24-row screen; far past one wheel step
	}, "pane never filled its scrollback")
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

// touchAttach attaches to a fresh session of cmd on a private tmux server the
// way the panel does (attachArgv, through a PTY), and returns a tmux runner
// for queries and a writer standing in for the page's input.
func touchAttach(t *testing.T, cmd string) (run func(args ...string) string, write func(string)) {
	t.Helper()
	if _, err := osExec.LookPath("tmux"); err != nil {
		t.Skip("tmux required for real PTY scroll regression")
	}
	h := newTestHandler(t, nil)
	h.socketRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Dir(h.socketPath(h.panel)), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"-f", "/dev/null", "-S", h.socketPath(h.panel)}
	run = func(args ...string) string {
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
	run("new-session", "-d", "-s", id, "-x", "80", "-y", "24", cmd)
	t.Cleanup(func() { _ = osExec.Command("tmux", append(base, "kill-server")...).Run() })
	run("set", "-g", "status", "off")
	name, argv := h.attachArgv(h.panel, id)
	c := osExec.Command(name, argv...)
	c.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = c.Process.Kill(); _ = c.Wait() })
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	waitFor(t, func() bool { return run("list-clients", "-t", id, "-F", "#{client_tty}") != "" }, "client did not attach")
	return run, func(s string) {
		t.Helper()
		if _, err := ptmx.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
}

func waitFor(t *testing.T, check func() bool, message string) {
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

// settled waits for got to read want and to still read it a moment later: a
// count that overshoots passes through the right value on its way. A timeout
// reports the last value read, not the first.
func settled(t *testing.T, got func() string, want, message string) {
	t.Helper()
	last := ""
	deadline := time.Now().Add(5 * time.Second)
	for last = got(); last != want && time.Now().Before(deadline); last = got() {
		time.Sleep(20 * time.Millisecond)
	}
	if last != want {
		t.Fatalf("%s: got %s, want %s", message, last, want)
	}
	time.Sleep(150 * time.Millisecond)
	if g := got(); g != want {
		t.Fatalf("%s: moved on to %s after reading %s", message, g, want)
	}
}

// One touch line as the page sends it (touchScrollCommands): a wheel report
// with Shift held, then a plain one, at the cell under the finger.
const (
	wheelUp   = "\x1b[<64;10;10M"
	wheelDown = "\x1b[<65;10;10M"
	touchUp   = "\x1b[<68;10;10M" + wheelUp
	touchDown = "\x1b[<69;10;10M" + wheelDown
)

// A finger's line is one line of tmux history, from the first. Plain wheel
// reports must keep tmux's notch — the first enters copy mode without moving,
// each next moves five — or a desktop mouse changes under the operator.
func TestTouchScrollMovesHistoryOneLinePerLine(t *testing.T) {
	run, write := touchAttach(t, "sh -c 'seq 1 300; exec sleep 60'")
	id := "aaaaaaaaaaaa"
	// The history must exist before copy mode opens (see the test above).
	waitFor(t, func() bool {
		size, _ := strconv.Atoi(run("display-message", "-p", "-t", id, "#{history_size}"))
		return size >= 100
	}, "pane never filled its scrollback")
	position := func() string { return run("display-message", "-p", "-t", id, "#{pane_in_mode}:#{scroll_position}") }

	write(touchUp)
	settled(t, position, "1:1", "the first touch line must enter copy mode and move one line")
	// A drag writes several lines at once.
	write(strings.Repeat(touchUp, 6))
	settled(t, position, "1:7", "each touch line must move one line")
	write(wheelUp)
	settled(t, position, "1:12", "a mouse's plain wheel report must still move tmux's five lines")
	write(strings.Repeat(touchDown, 11))
	settled(t, position, "1:1", "each touch line back down must move one line")
	write(touchDown)
	settled(t, position, "0:", "the last line back down must leave copy mode")
}

// A program that takes the wheel itself (Codex: alternate screen, SGR mouse)
// gets one plain report per touch line, exactly what a mouse would send, and
// never the Shift report.
func TestTouchScrollReachesTheProgramAsOneWheelReportPerLine(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "input")
	run, write := touchAttach(t, `sh -c 'printf "\033[?1049h\033[?1000h\033[?1006h"; stty raw -echo; exec cat > `+logPath+`'`)
	id := "aaaaaaaaaaaa"
	waitFor(t, func() bool {
		return run("display-message", "-p", "-t", id, "#{alternate_on}#{mouse_any_flag}") == "11"
	}, "the program never asked for the mouse")

	write(strings.Repeat(touchUp, 3) + wheelUp + touchDown)
	want := strings.Repeat(wheelUp, 4) + wheelDown
	settled(t, func() string { b, _ := os.ReadFile(logPath); return string(b) }, want, "what the program received")
	if run("display-message", "-p", "-t", id, "#{pane_in_mode}") != "0" {
		t.Error("a program that takes the wheel must not be put in copy mode")
	}
}

// A paste into a pane scrolled back into copy mode must not reach the shell.
// tmux takes a quick run of keys for a paste and keeps it from copy mode's
// bindings; with that detection off (assume-paste-time 0 — an earlier way to
// keep touch markers bound) Space paged down and left copy mode at the bottom,
// and the rest of this paste ran: `rm -rf *` in the shell's directory.
func TestTouchScrollKeepsAPasteOutOfCopyModesBindings(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "input")
	run, write := touchAttach(t, "sh -c 'seq 1 300; stty raw -echo; exec cat > "+logPath+"'")
	id := "aaaaaaaaaaaa"
	waitFor(t, func() bool {
		size, _ := strconv.Atoi(run("display-message", "-p", "-t", id, "#{history_size}"))
		return size >= 100
	}, "pane never filled its scrollback")
	position := func() string { return run("display-message", "-p", "-t", id, "#{pane_in_mode}:#{scroll_position}") }
	write(strings.Repeat(touchUp, 3))
	settled(t, position, "1:3", "three touch lines back")

	write("cd /tmp/build; rm -rf *\r")
	time.Sleep(300 * time.Millisecond)
	if b, _ := os.ReadFile(logPath); len(b) != 0 {
		t.Errorf("a paste into copy mode reached the shell: %q", b)
	}
	if got := position(); got != "1:3" {
		t.Errorf("a paste into copy mode moved it to %s", got)
	}
}
