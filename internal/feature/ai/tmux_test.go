package ai

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func indexOf(argv []string, s string) int { return slices.Index(argv, s) }

// command returns what tmux is told to run: everything after new-session's
// terminating --. argv itself always carries a -c (new-session's working
// directory), so the wrapper check has to look here.
func command(argv []string) []string {
	for i := len(argv) - 1; i >= 0; i-- {
		if argv[i] == "--" {
			return argv[i+1:]
		}
	}
	return nil
}

// A non-panel account is entered with runuser, inside the systemd scope, and
// tmux always gets -f /dev/null -L sfpanel — the operator's ~/.tmux.conf
// auto-restores sessions and must never load.
func TestSpawnArgv_NonPanelAccountUnderScope(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	alice := Account{Name: "alice", UID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	name, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", ToolClaude, alice, true)
	if name != "systemd-run" {
		t.Fatalf("name = %q, want systemd-run", name)
	}
	prefix := []string{"--scope", "--quiet", "--collect", "--unit=sfpanel-ai-0123456789ab", "--", "runuser", "-u", "alice", "--", "tmux", "-f", "/dev/null", "-L", "sfpanel"}
	if !slices.Equal(argv[:len(prefix)], prefix) {
		t.Errorf("argv prefix = %q\nwant %q", argv[:len(prefix)], prefix)
	}
	ns := indexOf(argv, "new-session")
	if ns < 0 || indexOf(argv, "prefix") > ns || indexOf(argv, "history-limit") > ns {
		t.Errorf("options must be applied before new-session so they govern the first window: %q", argv)
	}
	tail := argv[ns:]
	want := []string{"new-session", "-d", "-s", "0123456789ab", "-c", "/opt/stacks/app", "-e", "LANG=C.UTF-8", "-e", "COLORTERM=truecolor", "--", "/bin/bash", "-l", "-c", `command "$0" "$@"; exec /bin/bash -l`, "claude"}
	if !slices.Equal(tail, want) {
		t.Errorf("new-session tail = %q\nwant %q", tail, want)
	}
}

func TestSpawnArgv_PanelAccountHasNoRunuser(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	_, argv := h.spawnArgv("0123456789ab", "/root", ToolCodex, h.panel, true)
	if slices.Contains(argv, "runuser") {
		t.Errorf("the panel's own account must not go through runuser: %q", argv)
	}
	if argv[5] != "tmux" {
		t.Errorf("argv[5] = %q, want tmux right after the scope's --", argv[5])
	}
}

func TestSpawnArgv_ShellToolIsAPlainLoginShell(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	_, argv := h.spawnArgv("0123456789ab", "/root", ToolShell, h.panel, true)
	if cmd := command(argv); !slices.Equal(cmd, []string{"/bin/bash", "-l"}) {
		t.Errorf("shell session must run `/bin/bash -l` and carry no -c wrapper: %q", cmd)
	}
}

func TestSpawnArgv_FallsBackToSetsidWithoutSystemdRun(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	name, argv := h.spawnArgv("0123456789ab", "/root", ToolClaude, h.panel, false)
	if name != "setsid" || argv[0] != "tmux" {
		t.Errorf("got %s %q, want setsid tmux …", name, argv[:2])
	}
}

// Without the tmux-256color terminfo the server must advertise
// screen-256color, or every CLI inside starts with a broken TERM.
func TestDefaultTerminal_FallsBackWhenTerminfoMissing(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("infocmp", "", errTest)
	h := newTestHandler(t, m)
	if got := h.defaultTerminal(); got != "screen-256color" {
		t.Errorf("defaultTerminal = %q, want screen-256color", got)
	}
}

func TestParseListWindows_KeepsOnlyTheCurrentWindow(t *testing.T) {
	out := "abc\t0\tvim\t0\t1789348000\t0\nabc\t1\tclaude\t1\t1789348400\t0\nxyz\t1\tbash\t0\t1789348300\t1\ngarbage line\n"
	live := parseListWindows(out)
	if len(live) != 2 {
		t.Fatalf("parsed %d sessions, want 2", len(live))
	}
	if w := live["abc"]; w.Command != "claude" || !w.Attached || w.Activity.Unix() != 1789348400 || w.Bell {
		t.Errorf("abc = %+v", w)
	}
	if w := live["xyz"]; w.Command != "bash" || w.Attached || !w.Bell {
		t.Errorf("xyz = %+v", w)
	}
}

func TestDeriveState(t *testing.T) {
	now := testNow
	cases := []struct {
		name string
		w    liveWindow
		want string
	}{
		{"tool with output 4s ago is working", liveWindow{Command: "claude", Activity: now.Add(-4 * time.Second)}, StateWorking},
		{"tool silent for 6s is waiting", liveWindow{Command: "claude", Activity: now.Add(-6 * time.Second)}, StateWaiting},
		{"bell wins over fresh activity", liveWindow{Command: "codex", Activity: now, Bell: true}, StateWaiting},
		{"npm wrapper counts as a tool", liveWindow{Command: "node", Activity: now}, StateWorking},
		{"a shell is never waiting, even with a bell", liveWindow{Command: "bash", Activity: now.Add(-time.Hour), Bell: true}, StateShell},
		{"zsh is a shell too", liveWindow{Command: "zsh", Activity: now}, StateShell},
	}
	for _, tc := range cases {
		if got := deriveState(tc.w, now); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// "no server running" is the normal answer when every session has ended;
// it must read as an empty map, not an error. The fixture is a line that
// *would* parse into one session if the error path were not taken, so an
// empty result can only mean the error was honoured.
func TestLiveWindows_NoServerIsEmpty(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("tmux", "abc\t1\tclaude\t1\t1789348400\t0\n", errTest)
	h := newTestHandler(t, m)
	if got := h.liveWindows(h.panel); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if !strings.Contains(strings.Join(m.Calls[0].Args, " "), "list-windows -a -F") {
		t.Errorf("expected a list-windows call, got %q", m.Calls[0].Args)
	}
}
