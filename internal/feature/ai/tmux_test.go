package ai

import (
	"os"
	"path/filepath"
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

// A non-panel account is entered with runuser, inside the systemd scope,
// behind `env -i`, and tmux always gets -f /dev/null -S <socket> — the
// operator's ~/.tmux.conf auto-restores sessions and must never load.
func TestSpawnArgv_NonPanelAccountUnderScope(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	name, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", ToolClaude, alice, true)
	if name != "systemd-run" {
		t.Fatalf("name = %q, want systemd-run", name)
	}
	prefix := []string{"--scope", "--quiet", "--collect", "--unit=sfpanel-ai-0123456789ab", "--",
		"env", "-i", "PATH=" + accountPath, "HOME=/home/alice", "USER=alice", "LOGNAME=alice", "SHELL=/bin/bash", "LANG=C.UTF-8", "COLORTERM=truecolor",
		"runuser", "-u", "alice", "--", "tmux", "-f", "/dev/null", "-S", h.socketPath(alice)}
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
	if argv[5] != "env" || slices.Contains(argv, "-i") {
		t.Errorf("the panel's own account is not a boundary: it keeps its environment with the account's variables pinned on top, got %q", argv)
	}
	if i := indexOf(argv, "tmux"); i < 0 || argv[i-1] != "COLORTERM=truecolor" {
		t.Errorf("tmux must follow the env prefix directly: %q", argv)
	}
}

// The socket may not live in /tmp: the shipped unit sets PrivateTmp=true, so
// the service's /tmp is deleted on every stop — including the stop half of a
// restart — and the tmux servers that survive in their scopes would be left
// with sockets in a destroyed mount namespace, unreachable by the restarted
// panel and by an SSH shell alike. Assert the reason: an absolute -S under
// /run, no -L anywhere, one directory per uid.
func TestSocketPath_IsAnAbsoluteRunPathPerAccount(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	h.socketRoot = defaultSocketRoot
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	if got := h.socketPath(alice); got != "/run/sfpanel/ai/1000/sfpanel" {
		t.Errorf("alice's socket = %q, want /run/sfpanel/ai/1000/sfpanel", got)
	}
	if got := h.socketPath(h.panel); got != "/run/sfpanel/ai/0/sfpanel" {
		t.Errorf("the panel's socket = %q, want /run/sfpanel/ai/0/sfpanel", got)
	}
	for _, acct := range []Account{alice, h.panel} {
		_, argv := h.spawnArgv("0123456789ab", "/tmp", ToolShell, acct, true)
		if slices.Contains(argv, "-L") {
			t.Errorf("%s: -L derives the socket from /tmp: %q", acct.Name, argv)
		}
		i := indexOf(argv, "-S")
		if i < 0 || argv[i+1] != h.socketPath(acct) {
			t.Fatalf("%s: no -S %s in %q", acct.Name, h.socketPath(acct), argv)
		}
		if strings.HasPrefix(argv[i+1], "/tmp") || strings.HasPrefix(argv[i+1], "/var/tmp") {
			t.Errorf("%s: socket %q is in a private-tmp directory", acct.Name, argv[i+1])
		}
	}
}

// The directory has to exist before the spawn that binds the socket, be the
// account's own (0700, chowned to it — tmux binds the socket and its lock file
// while running as them) and sit under levels every account can traverse but
// none can write.
func TestEnsureSocketDir_OwnedByTheAccountUnderATraversableRoot(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{})
	var chowned [][3]any
	h.chown = func(p string, uid, gid int) error { chowned = append(chowned, [3]any{p, uid, gid}); return nil }
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	if err := h.ensureSocketDir(alice); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(h.socketPath(alice))
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("%s mode = %v, want 0700", dir, fi.Mode().Perm())
	}
	for _, parent := range []string{h.socketRoot, filepath.Dir(h.socketRoot)} {
		pfi, err := os.Stat(parent)
		if err != nil {
			t.Fatal(err)
		}
		if pfi.Mode().Perm() != 0o711 {
			t.Errorf("%s mode = %v, want 0711 (traversable, not writable or listable)", parent, pfi.Mode().Perm())
		}
	}
	if len(chowned) != 1 || chowned[0] != [3]any{any(dir), any(1000), any(1000)} {
		t.Errorf("chown calls = %v, want one to %s for 1000:1000", chowned, dir)
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
	if name != "setsid" || argv[0] != "env" || !slices.Contains(argv, "HOME=/root") {
		t.Errorf("got %s %q, want setsid env HOME=/root … — the fallback needs the environment too", name, argv[:3])
	}
	if i := indexOf(argv, "tmux"); i < 0 {
		t.Errorf("no tmux in the setsid fallback: %q", argv)
	}
}

// Every tmux invocation, not just the spawn, runs behind the env prefix: a
// short-lived list-windows client runs as the account too, and its
// /proc/self/environ is theirs to read while it lives.
func TestTmuxCmd_CarriesTheAccountEnvironment(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{})
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	name, argv := h.tmuxCmd(alice)
	if name != "env" || argv[0] != "-i" {
		t.Errorf("alice: %s %q, want env -i … (nothing of the panel's environment may cross runuser)", name, argv)
	}
	if !slices.Contains(argv, "HOME=/home/alice") || indexOf(argv, "-i") > indexOf(argv, "runuser") {
		t.Errorf("alice: %q — the env prefix belongs before runuser", argv)
	}
	name, argv = h.tmuxCmd(h.panel)
	if name != "env" || slices.Contains(argv, "-i") || !slices.Contains(argv, "HOME=/root") {
		t.Errorf("the panel account: %s %q, want env HOME=/root … with no -i", name, argv)
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
	m.SetOutput("env", "abc\t1\tclaude\t1\t1789348400\t0\n", errTest)
	h := newTestHandler(t, m)
	if got := h.liveWindows(h.panel); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if !strings.Contains(strings.Join(m.Calls[0].Args, " "), "list-windows -a -F") {
		t.Errorf("expected a list-windows call, got %q", m.Calls[0].Args)
	}
}
