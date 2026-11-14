package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	osExec "os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/exec"
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

// Against a real tmux: the copy is the whole history down to the screen's
// last row, every row of the screen present, colours kept, with the pane's
// size; a program in the alternate screen, or a pane in copy mode, gets no
// copy at all.
func TestSessionScrollbackEndsWithTheScreen(t *testing.T) {
	if _, err := osExec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	h := newTestHandler(t, nil)
	h.Cmd = exec.NewCommander()
	h.DB = openTestDB(t)
	if err := os.MkdirAll(filepath.Dir(h.socketPath(h.panel)), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"-f", "/dev/null", "-S", h.socketPath(h.panel)}
	tmux := func(args ...string) string {
		out, _ := osExec.Command("tmux", append(append([]string{}, base...), args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() { _ = osExec.Command("tmux", append(base, "kill-server")...).Run() })
	shell, program := "aaaaaaaaaaaa", "bbbbbbbbbbbb"
	tmux("new-session", "-d", "-s", shell, "-x", "80", "-y", "24", `sh -c 'seq 1 100; printf "\033[31mred\033[0m\n"; exec sleep 60'`)
	tmux("new-session", "-d", "-s", program, "-x", "80", "-y", "24", `sh -c 'printf "\033[?1049hfull screen"; exec sleep 60'`)
	for _, id := range []string{shell, program} {
		_ = insertSession(h.DB, sessionRow{ID: id, Tool: ToolShell, Title: id, RunAs: "root", CWD: "/"})
	}
	waitFor(t, func() bool { return strings.Contains(tmux("capture-pane", "-p", "-t", shell), "red") }, "the shell never drew")
	waitFor(t, func() bool { return tmux("display-message", "-p", "-t", program, "#{alternate_on}") == "1" }, "the program never took the alternate screen")

	type copyOf struct {
		Alternate bool   `json:"alternate"`
		CopyMode  bool   `json:"copy_mode"`
		Cols      int    `json:"cols"`
		Rows      int    `json:"rows"`
		Text      string `json:"text"`
	}
	scrollback := func(id string) copyOf {
		rec := call(t, h.SessionScrollback, http.MethodGet, "", id, "")
		var env struct {
			Data copyOf `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: %v: %s", id, err, rec.Body.String())
		}
		return env.Data
	}
	got := scrollback(shell)
	alt, text := got.Alternate, got.Text
	if got.Cols != 80 || got.Rows != 24 || got.CopyMode {
		t.Errorf("pane %dx%d copy mode %v, want 80x24 and live", got.Cols, got.Rows, got.CopyMode)
	}
	lines := strings.Split(text, "\n")
	history, _ := strconv.Atoi(tmux("display-message", "-p", "-t", shell, "#{history_size}"))
	if alt || len(lines) != history+24 {
		t.Fatalf("alternate %v, %d lines; want the %d lines of history and the 24 of the screen", alt, len(lines), history)
	}
	screen := strings.Split(tmux("capture-pane", "-p", "-t", shell), "\n")
	if got := stripSGR(lines[len(lines)-24]); got != screen[0] {
		t.Errorf("the copy's screen starts with %q, the screen with %q", got, screen[0])
	}
	if !strings.Contains(text, "\x1b[31mred") {
		t.Error("the copy lost its colours")
	}
	if got := scrollback(program); !got.Alternate || got.Text != "" {
		t.Errorf("a program in the alternate screen: %+v; want alternate and nothing", got)
	}
	tmux("copy-mode", "-t", shell)
	waitFor(t, func() bool { return tmux("display-message", "-p", "-t", shell, "#{pane_in_mode}") == "1" }, "copy mode never opened")
	if got := scrollback(shell); !got.CopyMode || got.Text != "" {
		t.Errorf("a pane in copy mode: %+v; want copy_mode and nothing", got)
	}
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripSGR(s string) string { return sgrRe.ReplaceAllString(s, "") }

// Against a real tmux: "latest" takes a pane that is scrolled back in copy
// mode to its live screen, and is nothing at all to a pane that is not in it —
// no key reaches the program there, which a blind `q` or Escape would be.
func TestLeaveCopyModeReturnsToTheLiveScreen(t *testing.T) {
	if _, err := osExec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	h := newTestHandler(t, nil)
	h.Cmd = exec.NewCommander()
	if err := os.MkdirAll(filepath.Dir(h.socketPath(h.panel)), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"-f", "/dev/null", "-S", h.socketPath(h.panel)}
	tmux := func(args ...string) string {
		out, _ := osExec.Command("tmux", append(append([]string{}, base...), args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() { _ = osExec.Command("tmux", append(base, "kill-server")...).Run() })
	id := "aaaaaaaaaaaa"
	// cat echoes whatever it is sent, so a stray key shows on the screen.
	tmux("new-session", "-d", "-s", id, "-x", "80", "-y", "24", "sh -c 'seq 1 300; exec cat'")
	screen := func() string { return tmux("capture-pane", "-p", "-t", id) }
	mode := func() string { return tmux("display-message", "-p", "-t", id, "#{pane_in_mode}") }
	waitFor(t, func() bool { return strings.HasSuffix(screen(), "300") }, "the pane never drew")
	live := screen()

	h.leaveCopyMode(h.panel, id)
	time.Sleep(150 * time.Millisecond)
	if got := screen(); got != live || mode() != "0" {
		t.Errorf("a pane on its live screen was sent something: mode %s, screen now ends %q", mode(), got[max(0, len(got)-20):])
	}

	tmux("copy-mode", "-t", id)
	tmux("send-keys", "-X", "-t", id, "-N", "50", "scroll-up")
	waitFor(t, func() bool {
		return tmux("display-message", "-p", "-t", id, "#{pane_in_mode} #{scroll_position}") == "1 50"
	}, "copy mode never scrolled back")
	h.leaveCopyMode(h.panel, id)
	waitFor(t, func() bool { return mode() == "0" }, "the pane stayed in copy mode")
	if got := screen(); got != live {
		t.Errorf("leaving copy mode typed into the program: screen now ends %q", got[max(0, len(got)-20):])
	}
}

// The frame itself, through a real attach: the pane leaves copy mode, and the
// frame is not written into the session as the text it would be to a panel
// that does not know it.
func TestAttachWS_LatestFrameLeavesCopyModeAndIsNotInput(t *testing.T) {
	if _, err := osExec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	h := newTestHandler(t, nil)
	h.Cmd = exec.NewCommander()
	h.DB = openTestDB(t)
	if err := os.MkdirAll(filepath.Dir(h.socketPath(h.panel)), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"-f", "/dev/null", "-S", h.socketPath(h.panel)}
	tmux := func(args ...string) string {
		out, _ := osExec.Command("tmux", append(append([]string{}, base...), args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() { _ = osExec.Command("tmux", append(base, "kill-server")...).Run() })
	id := "aaaaaaaaaaaa"
	tmux("new-session", "-d", "-s", id, "-x", "80", "-y", "24", "sh -c 'seq 1 300; exec cat'")
	if err := insertSession(h.DB, sessionRow{ID: id, Tool: ToolShell, Title: id, RunAs: "root", CWD: "/"}); err != nil {
		t.Fatal(err)
	}
	screen := func() string { return tmux("capture-pane", "-p", "-t", id) }
	mode := func() string { return tmux("display-message", "-p", "-t", id, "#{pane_in_mode}") }
	waitFor(t, func() bool { return strings.HasSuffix(screen(), "300") }, "the pane never drew")

	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS(func() string { return "test-secret" }, nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()
	tok, err := auth.GenerateToken("admin", "test-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/ai/attach?session_id="+id+"&token="+tok, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()
	waitFor(t, func() bool { return tmux("display-message", "-p", "-t", id, "#{session_attached}") == "1" }, "the attach never reached tmux")
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.HasSuffix(screen(), "300") }, "the pane did not settle at the attach's size")
	live := screen()

	tmux("copy-mode", "-t", id)
	tmux("send-keys", "-X", "-t", id, "-N", "50", "scroll-up")
	waitFor(t, func() bool { return mode() == "1" }, "copy mode never opened")
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"latest"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return mode() == "0" }, "the latest frame left the pane in copy mode")
	// Sent again on the live screen: still not input.
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"latest"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := screen(); got != live {
		t.Errorf("the frame reached the program as text: screen now ends %q", got[max(0, len(got)-40):])
	}
}
