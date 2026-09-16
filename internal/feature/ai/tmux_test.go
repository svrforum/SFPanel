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

// The account's tmux server is started by PID 1 as a transient *service*, not
// as a scope. A scope forks the caller, so the server would inherit the
// panel's environment (SFPANEL_JWT_SECRET included) and the panel's
// PrivateTmp mount namespace — which systemd deletes on every restart,
// exactly the event the surviving sessions exist to outlive. Assert the
// reason: --uid/--gid and Type=forking rather than --scope, and no runuser or
// env prefix, because systemd sets the account's environment itself.
func TestSpawnArgv_ServerFormIsATransientService(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	name, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", ToolClaude, "", alice, spawnService)
	if name != "systemd-run" {
		t.Fatalf("name = %q, want systemd-run", name)
	}
	prefix := []string{"--unit=sfpanel-ai-1000", "--collect", "--uid=alice", "--gid=1000",
		"-p", "Type=forking", "--setenv=LANG=C.UTF-8", "--setenv=COLORTERM=truecolor",
		"--", "tmux", "-f", "/dev/null", "-S", h.socketPath(alice)}
	if !slices.Equal(argv[:len(prefix)], prefix) {
		t.Errorf("argv prefix = %q\nwant %q", argv[:len(prefix)], prefix)
	}
	if slices.Contains(argv, "--scope") {
		t.Errorf("a scope inherits the panel's mount namespace and environment: %q", argv)
	}
	for _, bad := range []string{"runuser", "env", "setsid"} {
		if slices.Contains(argv, bad) {
			t.Errorf("systemd --uid already enters the account; %s must not appear: %q", bad, argv)
		}
	}
	ns := indexOf(argv, "new-session")
	if ns < 0 || indexOf(argv, "prefix") > ns || indexOf(argv, "history-limit") > ns {
		t.Errorf("options must be applied before new-session so they govern the first window: %q", argv)
	}
	tail := argv[ns:]
	want := []string{"new-session", "-d", "-s", "0123456789ab", "-c", "/opt/stacks/app", "--", "/bin/bash", "-lic", `command "$0" "$@"; exec /bin/bash -l`, "claude"}
	if !slices.Equal(tail, want) {
		t.Errorf("new-session tail = %q\nwant %q", tail, want)
	}
}

// Once the server is up, a session is created by an ordinary client: the
// env-prefixed, runuser-entered form, and never a second systemd unit — the
// unit name is per account, so a second one would fail as already claimed.
func TestSpawnArgv_ClientFormTalksToTheRunningServer(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	name, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", ToolClaude, "", alice, spawnClient)
	if name != "env" {
		t.Fatalf("name = %q, want env", name)
	}
	prefix := []string{"-i", "PATH=" + accountPath, "HOME=/home/alice", "USER=alice", "LOGNAME=alice", "SHELL=/bin/bash", "LANG=C.UTF-8", "COLORTERM=truecolor",
		"runuser", "-u", "alice", "--", "tmux", "-f", "/dev/null", "-S", h.socketPath(alice)}
	if !slices.Equal(argv[:len(prefix)], prefix) {
		t.Errorf("argv prefix = %q\nwant %q", argv[:len(prefix)], prefix)
	}
	for _, bad := range []string{"systemd-run", "--unit=sfpanel-ai-1000", "setsid"} {
		if slices.Contains(argv, bad) {
			t.Errorf("a server that is already up needs no unit; %s must not appear: %q", bad, argv)
		}
	}
}

// new-session carries no -e in either form: the session environment is the
// server's, and the server got it from systemd (--setenv) or from the env prefix
// that started it. A -e pair here would be the only copy that disagreed.
func TestSpawnArgv_NoPerSessionEnvironmentPairs(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}
	for _, form := range []spawnForm{spawnService, spawnClient, spawnSetsid} {
		_, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", ToolClaude, "", alice, form)
		ns := indexOf(argv, "new-session")
		if ns < 0 {
			t.Fatalf("form %d: no new-session in %q", form, argv)
		}
		if slices.Contains(argv[ns:], "-e") {
			t.Errorf("form %d: new-session must not set its own environment: %q", form, argv[ns:])
		}
	}
}

func TestSpawnArgv_PanelAccountHasNoRunuser(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	name, argv := h.spawnArgv("0123456789ab", "/root", ToolCodex, "", h.panel, spawnClient)
	if slices.Contains(argv, "runuser") {
		t.Errorf("the panel's own account must not go through runuser: %q", argv)
	}
	if name != "env" || slices.Contains(argv, "-i") {
		t.Errorf("the panel's own account is not a boundary: it keeps its environment with the account's variables pinned on top, got %s %q", name, argv)
	}
	if i := indexOf(argv, "tmux"); i < 0 || argv[i-1] != "COLORTERM=truecolor" {
		t.Errorf("tmux must follow the env prefix directly: %q", argv)
	}
}

// The form is chosen from three facts, and each one alone decides it: a
// server already on the socket means client, no server plus root plus
// systemd-run means service, and anything less means the setsid fallback.
func TestSpawnFormFor(t *testing.T) {
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}
	cases := []struct {
		name       string
		listErr    error
		haveRun    bool
		root       bool
		want       spawnForm
		wantPersis string
	}{
		{"server already running", nil, true, true, spawnClient, "service"},
		{"no server, root, systemd-run", errTest, true, true, spawnService, "service"},
		{"no server, no systemd-run", errTest, false, true, spawnSetsid, "process"},
		{"no server, panel is not root", errTest, true, false, spawnSetsid, "process"},
	}
	for _, tc := range cases {
		m := exec.NewMockCommander()
		m.SetOutput("env", "", tc.listErr)
		if tc.haveRun {
			m.SetOutput("exists:systemd-run", "", nil)
		}
		h := newTestHandler(t, m)
		h.isRoot = func() bool { return tc.root }
		if got := h.spawnFormFor(alice); got != tc.want {
			t.Errorf("%s: form = %d, want %d", tc.name, got, tc.want)
		}
		if got := h.persistence(); got != tc.wantPersis {
			t.Errorf("%s: persistence = %q, want %q", tc.name, got, tc.wantPersis)
		}
	}
}

// serverRunning must not be liveWindows: that one folds a failed client into
// an empty map, so "no windows" and "no server" would be the same answer and
// every spawn after the first would try to claim the unit again. The fixture
// is a non-zero exit with output that *would* parse, so only honouring the
// error can produce false.
func TestServerRunning_HonoursTheExitStatus(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("env", "sfpanel: 1 windows\n", errTest)
	h := newTestHandler(t, m)
	if h.serverRunning(h.panel) {
		t.Error("a non-zero list-sessions means no server")
	}
	if last := m.Calls[len(m.Calls)-1]; !slices.Contains(last.Args, "list-sessions") {
		t.Errorf("expected a list-sessions probe, got %q", last.Args)
	}
	m2 := exec.NewMockCommander()
	m2.SetOutput("env", "", nil)
	if !newTestHandler(t, m2).serverRunning(h.panel) {
		t.Error("exit 0 means the server is there, even with no sessions listed")
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
		_, argv := h.spawnArgv("0123456789ab", "/tmp", ToolShell, "", acct, spawnClient)
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

// /run/sfpanel is root's to create: MkdirAll("/run/sfpanel/ai") fails outright
// for a panel running as an ordinary account, which took the whole module down
// on such an install. It falls back to the panel state directory — and must not
// pay for that with the state directory's own 0700, which guards the database,
// nor attempt a chown it cannot perform and does not need (its only account is
// itself).
func TestSocketRoot_NonRootPanelUsesTheStateDirectory(t *testing.T) {
	state := t.TempDir()
	if got := socketRoot(state, true); got != defaultSocketRoot {
		t.Errorf("root panel socket root = %q, want %q", got, defaultSocketRoot)
	}
	if got := socketRoot(state, false); got != filepath.Join(state, "ai") {
		t.Errorf("non-root panel socket root = %q, want %q", got, filepath.Join(state, "ai"))
	}

	// t.TempDir() is 0755&~umask; the real /var/lib/sfpanel is 0700.
	if err := os.Chmod(state, 0o700); err != nil {
		t.Fatal(err)
	}
	h := newTestHandler(t, &exec.MockCommander{})
	h.isRoot = func() bool { return false }
	h.socketRoot = socketRoot(state, false)
	chowns := 0
	h.chown = func(string, int, int) error { chowns++; return nil }
	if err := h.ensureSocketDir(h.panel); err != nil {
		t.Fatalf("a non-root panel must be able to prepare its own socket directory: %v", err)
	}
	if chowns != 0 {
		t.Errorf("chown ran %d times; a non-root panel has nobody to give the directory to", chowns)
	}
	leaf := filepath.Dir(h.socketPath(h.panel))
	fi, err := os.Stat(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("%s mode = %v, want 0700", leaf, fi.Mode().Perm())
	}
	sfi, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if sfi.Mode().Perm() != 0o700 {
		t.Errorf("state directory mode = %v, want 0700 — it holds the panel database and must not be widened for accounts that cannot exist", sfi.Mode().Perm())
	}
}

func TestSpawnArgv_ShellToolIsAPlainLoginShell(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	_, argv := h.spawnArgv("0123456789ab", "/root", ToolShell, "", h.panel, spawnClient)
	if cmd := command(argv); !slices.Equal(cmd, []string{"/bin/bash", "-l"}) {
		t.Errorf("shell session must run `/bin/bash -l` and carry no -c wrapper: %q", cmd)
	}
}

// The tool wrapper runs in an *interactive* login shell. `~/.local/bin` — the
// Claude native installer's directory — joins PATH in the account's .bashrc,
// and the Debian/Ubuntu skeleton opens that file with `[ -z "$PS1" ] && return`,
// so a non-interactive `bash -l -c` never reads it: the wrapper exited 127 with
// "claude: command not found" for root and dropped the pane to the fallback
// shell. Assert the reason on both sides — the flag is on the wrapper, and the
// `shell` tool must not gain it: that one has tmux's tty and `bash -l` is
// already interactive, so -i would only add job-control noise.
func TestSessionCommands_ToolWrapperIsAnInteractiveLoginShell(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	for _, form := range []spawnForm{spawnService, spawnClient, spawnSetsid} {
		for _, tool := range []string{ToolClaude, ToolCodex, ToolGemini} {
			_, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", tool, "", alice, form)
			want := []string{"/bin/bash", "-lic", `command "$0" "$@"; exec /bin/bash -l`, tool}
			if cmd := command(argv); !slices.Equal(cmd, want) {
				t.Errorf("form %d, %s: wrapper = %q\nwant %q\n(only an interactive login shell reads .bashrc, where ~/.local/bin joins PATH)", form, tool, cmd, want)
			}
		}
		_, argv := h.spawnArgv("0123456789ab", "/opt/stacks/app", ToolShell, "", alice, form)
		cmd := command(argv)
		if !slices.Equal(cmd, []string{"/bin/bash", "-l"}) {
			t.Errorf("form %d, shell: = %q, want [/bin/bash -l] — tmux gives the pane a tty, so it is interactive without a flag", form, cmd)
		}
		for _, bad := range []string{"-lic", "-i", "-li"} {
			if slices.Contains(cmd, bad) {
				t.Errorf("form %d, shell: %s adds job-control noise to a shell that is already interactive: %q", form, bad, cmd)
			}
		}
	}
}

func TestSpawnArgv_FallsBackToSetsidWithoutSystemdRun(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{Outputs: map[string]exec.MockResult{"infocmp": {}}})
	name, argv := h.spawnArgv("0123456789ab", "/root", ToolClaude, "", h.panel, spawnSetsid)
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

// The floor exists because tmuxOptions does not parse on an older tmux; an
// unreadable version is not a reason to refuse, so it counts as supported.
func TestTmuxVersionSupported(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"3.2a", true},
		{"3.2", true},
		{"3.6a", true},
		{"4.0", true},
		{"10.0", true}, // two-digit major, not string order
		{"3.0a", false},
		{"3.1c", false},
		{"2.9a", false},
		{"next-3.5", true}, // no leading d.d — never block on what we cannot read
		{"", true},
		{"unknown", true},
	}
	for _, tc := range cases {
		if got := tmuxVersionSupported(tc.v); got != tc.want {
			t.Errorf("tmuxVersionSupported(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// The probe costs a fork and the page asks for the version on every poll, so
// it is memoised — but only for the TTL. Pinned for the panel's lifetime, the
// refusal kept naming the old version after the operator did exactly what the
// banner told them to do, and the upgrade only took effect on a restart.
func TestTmuxVersion_MemoisedForTheTTLThenReprobed(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("tmux", "tmux 3.0a\n", nil)
	h := newTestHandler(t, m)
	now := testNow
	h.now = func() time.Time { return now }
	probes := func() int {
		n := 0
		for _, c := range m.Calls {
			if c.Name == "tmux" {
				n++
			}
		}
		return n
	}

	for i := 0; i < 3; i++ {
		if got := h.tmuxVersion(); got != "3.0a" {
			t.Fatalf("tmuxVersion = %q, want 3.0a", got)
		}
	}
	if n := probes(); n != 1 {
		t.Errorf("tmux -V ran %d times inside the window, want 1", n)
	}
	if h.tmuxTooOld() == "" {
		t.Error("3.0a is below the floor and must be refused")
	}

	// The operator upgrades tmux from the Packages page. Nothing restarts the
	// panel, so only the TTL can let the new version through.
	m.SetOutput("tmux", "tmux 3.6a\n", nil)
	now = now.Add(tmuxVersionTTL - time.Second)
	if got := h.tmuxVersion(); got != "3.0a" || probes() != 1 {
		t.Errorf("inside the window: version %q after %d probes, want the memo untouched", got, probes())
	}
	now = now.Add(2 * time.Second)
	if got := h.tmuxVersion(); got != "3.6a" {
		t.Errorf("past the TTL: version = %q, want the upgraded 3.6a", got)
	}
	if n := probes(); n != 2 {
		t.Errorf("tmux -V ran %d times, want a second probe past the TTL", n)
	}
	if msg := h.tmuxTooOld(); msg != "" {
		t.Errorf("the upgrade must lift the refusal without a restart, got %q", msg)
	}

	// A tmux that has gone away mid-flight must not clear a version we know is
	// below the floor: "" reads as supported and would let a create through to
	// the usage error the floor exists to replace.
	m.SetOutput("tmux", "", errTest)
	now = now.Add(2 * tmuxVersionTTL)
	if got := h.tmuxVersion(); got != "3.6a" {
		t.Errorf("a failed probe = %q, want the last known answer kept", got)
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

// The profile is one more entry in the same strict allowlist, and it reaches
// the tool through the variable that tool reads — nothing else changes.
func TestProfileVar(t *testing.T) {
	acct := Account{Name: "alice", Home: "/home/alice"}
	if v, ok := profileVar(acct, ToolCodex, "work"); !ok || v != "CODEX_HOME=/home/alice/.sfpanel-ai/codex/work" {
		t.Errorf("codex: got (%q, %v)", v, ok)
	}
	if v, ok := profileVar(acct, ToolClaude, "b"); !ok || v != "CLAUDE_CONFIG_DIR=/home/alice/.sfpanel-ai/claude/b" {
		t.Errorf("claude: got (%q, %v)", v, ok)
	}
	for _, c := range []struct{ tool, profile string }{{ToolCodex, ""}, {ToolShell, "work"}, {ToolGemini, "work"}, {ToolCodex, "../x"}} {
		if v, ok := profileVar(acct, c.tool, c.profile); ok {
			t.Errorf("%+v must produce no variable, got %q", c, v)
		}
	}
}

// The profile is a PER-SESSION variable and must ride on new-session -e.
// Measured on this host: a client's environment never reaches the session it
// creates (tmux copies only update-environment), and a variable given at
// server start becomes the server's global environment — so the next session,
// the one the operator picked 기본 for, would silently run on that profile.
func TestSpawnArgv_ProfileRidesOnNewSessionE(t *testing.T) {
	h := newTestHandler(t, tmuxMock(""))
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}
	want := "CODEX_HOME=/home/alice/.sfpanel-ai/codex/work"

	for _, form := range []spawnForm{spawnService, spawnClient, spawnSetsid} {
		_, argv := h.spawnArgv("0123456789ab", "/tmp", ToolCodex, "work", alice, form)
		i := slices.Index(argv, "-e")
		if i < 0 || i+1 >= len(argv) || argv[i+1] != want {
			t.Errorf("form %v: want `-e %s` on new-session, got %q", form, want, argv)
		}
		if ns := slices.Index(argv, "new-session"); ns < 0 || i < ns {
			t.Errorf("form %v: the -e pair must sit on new-session, got %q", form, argv)
		}
		// It must NOT reach the server's global environment.
		for _, a := range argv {
			if strings.HasPrefix(a, "--setenv=CODEX_HOME=") {
				t.Errorf("form %v: a profile in the server environment leaks into later sessions: %q", form, argv)
			}
		}
	}

	// A default-profile session sets nothing, so it inherits nothing.
	for _, form := range []spawnForm{spawnService, spawnClient, spawnSetsid} {
		_, argv := h.spawnArgv("0123456789ab", "/tmp", ToolCodex, "", alice, form)
		for _, a := range argv {
			if strings.Contains(a, "CODEX_HOME") {
				t.Errorf("form %v: the default profile must name no config home: %q", form, argv)
			}
		}
	}
}
