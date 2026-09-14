package ai

import (
	"encoding/json"
	"net/http"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

func TestVersionHelpers(t *testing.T) {
	if versionNumber("2.1.92 (Claude Code)") != "2.1.92" || versionNumber("codex-cli 0.154.0") != "0.154.0" || versionNumber("") != "" {
		t.Error("versionNumber")
	}
	cases := []struct {
		a, b string
		want int
	}{{"2.1.92", "2.1.270", -1}, {"0.154.0", "0.154.0", 0}, {"1.0.0", "0.9.9", 1}, {"", "1.0.0", 0}, {"1.0.0", "", 0}}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseProbe(t *testing.T) {
	out := "Welcome banner from .bashrc\nSFP\t/home/alice/.local/bin/claude\t2.1.92 (Claude Code)\n"
	p, v, ok := parseProbe(out)
	if !ok || p != "/home/alice/.local/bin/claude" || v != "2.1.92 (Claude Code)" {
		t.Errorf("got %q %q %v", p, v, ok)
	}
	if _, _, ok := parseProbe("bash: claude: not found\n"); ok {
		t.Error("no SFP line must mean not installed")
	}
}

// The version shown must be the one the account's own login shell resolves
// — the old resolver picked the newest binary across every home and showed
// a user's 2.1.265 for a root terminal that ran 2.1.92.
//
// And it must be an *interactive* login shell. `~/.local/bin` joins PATH in
// the account's .bashrc, which the Debian/Ubuntu skeleton opens with
// `[ -z "$PS1" ] && return` — root's own .bashrc is that skeleton — so a
// non-interactive `-lc` probe returns before the line that extends PATH and
// reports /root/.local/bin/claude as not installed while the session runs it.
func TestToolStatus_ProbesInTheAccountsLoginShell(t *testing.T) {
	stubLatest(t, ToolClaude, "2.1.270", http.StatusOK) // every toolStatus ends in a latest lookup; keep it local
	m := exec.NewMockCommander()
	m.SetOutput("env", "SFP\t/home/alice/.local/bin/claude\t2.1.92 (Claude Code)\n", nil)
	h := newTestHandler(t, m)
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: t.TempDir(), Shell: "/bin/bash"}

	st := h.toolStatus(alice, ToolClaude)
	if !st.Installed || st.Path != "/home/alice/.local/bin/claude" || st.Version != "2.1.92 (Claude Code)" {
		t.Errorf("status = %+v", st)
	}
	c := m.Calls[0]
	want := append(h.envArgv(alice)[1:], "runuser", "-u", "alice", "--", findShell(), "-lic", probeScript, "claude")
	if c.Name != "env" || strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("probe = %s %q\nwant env %q\n(-lic, not -lc: only an interactive login shell reads .bashrc, where ~/.local/bin joins PATH)", c.Name, c.Args, want)
	}

	// The panel's own account probes in a plain login shell — but not with the
	// panel's environment: a systemd system unit has no HOME, so without an
	// explicit one the profile's $HOME/.local/bin becomes /.local/bin and
	// root's own Claude reads as "not installed".
	m2 := exec.NewMockCommander()
	m2.SetOutput("env", "SFP\t/root/.local/bin/claude\t2.1.92 (Claude Code)\n", nil)
	h2 := newTestHandler(t, m2)
	_ = h2.toolStatus(h2.panel, ToolClaude)
	want2 := append(h2.envArgv(h2.panel)[1:], findShell(), "-lic", probeScript, "claude")
	if m2.Calls[0].Name != "env" || strings.Join(m2.Calls[0].Args, "\x00") != strings.Join(want2, "\x00") {
		t.Errorf("panel probe = %s %q\nwant env %q\n(-lic, not -lc: root's .bashrc is where ~/.local/bin joins PATH, and only an interactive shell reads it)", m2.Calls[0].Name, m2.Calls[0].Args, want2)
	}
	if lastEnv(h2.envArgv(h2.panel), "HOME") != "/root" {
		t.Errorf("the panel probe must name its HOME: %q", h2.envArgv(h2.panel))
	}
}

func TestToolStatus_MemoisedUntilInstall(t *testing.T) {
	stubLatest(t, ToolCodex, "", http.StatusOK) // no assertion reads Latest; the stub only keeps the test off the network
	m := exec.NewMockCommander()
	m.SetOutput("env", "", errTest) // exit 3: not installed
	h := newTestHandler(t, m)
	_ = h.toolStatus(h.panel, ToolCodex)
	st := h.toolStatus(h.panel, ToolCodex)
	if st.Installed || len(m.Calls) != 1 {
		t.Errorf("installed=%v after %d probes, want false after 1", st.Installed, len(m.Calls))
	}
	// A tool that is not there cannot have an update: the badge would offer
	// "update" for something the account has never installed.
	if st.UpdateAvailable {
		t.Error("a tool that is not installed must never report an update")
	}
	h.invalidateTool(h.panel.Name, ToolCodex)
	_ = h.toolStatus(h.panel, ToolCodex)
	if len(m.Calls) != 2 {
		t.Errorf("an install must drop the memo; probes = %d", len(m.Calls))
	}
}

func TestToolStatus_UpdateAvailableAndLogin(t *testing.T) {
	stubLatest(t, ToolClaude, "2.1.270", http.StatusOK)
	m := exec.NewMockCommander()
	m.SetOutput("env", "SFP\t/root/.local/bin/claude\t2.1.92 (Claude Code)\n", nil)
	h := newTestHandler(t, m)
	h.panel.Home = t.TempDir()

	st := h.toolStatus(h.panel, ToolClaude)
	if st.Latest != "2.1.270" || !st.UpdateAvailable || st.LoggedIn {
		t.Errorf("status = %+v, want latest 2.1.270, update available, not logged in", st)
	}
	_ = os.MkdirAll(filepath.Join(h.panel.Home, ".claude"), 0o700)
	_ = os.WriteFile(filepath.Join(h.panel.Home, ".claude", ".credentials.json"), []byte("{}"), 0o600)
	if st := h.toolStatus(h.panel, ToolClaude); !st.LoggedIn {
		t.Error("credentials file must read as logged in")
	}
}

func TestTools_BundleAndAccountGuard(t *testing.T) {
	for _, tool := range cliTools { // the bundle looks all three up; none of them upstream
		stubLatest(t, tool, "", http.StatusOK)
	}
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:tmux": {}, "exists:systemd-run": {}, "tmux": {Output: "tmux 3.6a\n"},
	}}
	m.SetOutput("env", "", errTest)
	h := newTestHandler(t, m)

	rec := call(t, h.Tools, http.MethodGet, "", "", "?user=alice")
	var env struct {
		Data struct {
			Tmux struct {
				Installed  bool
				Version    string
				Supported  bool
				MinVersion string `json:"min_version"`
			} `json:"tmux"`
			SystemdRun bool                  `json:"systemd_run"`
			Accounts   []string              `json:"accounts"`
			Account    string                `json:"account"`
			Tools      map[string]ToolStatus `json:"tools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	d := env.Data
	if !d.Tmux.Installed || d.Tmux.Version != "3.6a" || !d.SystemdRun || d.Account != "alice" || strings.Join(d.Accounts, " ") != "root alice dave" || len(d.Tools) != 3 {
		t.Errorf("bundle = %+v", d)
	}
	// The page needs both halves to tell "install it" from "upgrade it".
	if !d.Tmux.Supported || d.Tmux.MinVersion != tmuxMinVersion {
		t.Errorf("tmux support = %+v, want supported with min_version %s", d.Tmux, tmuxMinVersion)
	}

	old := &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:tmux": {}, "exists:systemd-run": {}, "tmux": {Output: "tmux 3.0a\n"},
	}}
	old.SetOutput("env", "", errTest)
	ho := newTestHandler(t, old)
	rec = call(t, ho.Tools, http.MethodGet, "", "", "?user=alice")
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.Tmux.Installed || env.Data.Tmux.Supported {
		t.Errorf("3.0a = %+v, want installed but not supported — the banner says upgrade, not install", env.Data.Tmux)
	}

	rec = call(t, h.Tools, http.MethodGet, "", "", "?user=bob")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("bob: %s, want INVALID_ACCOUNT", code)
	}
}

// systemd_run is "the service form is available", not "the binary is on the
// host". A non-root panel cannot ask PID 1 to run a unit as anyone, so every
// tab it creates carries the "process" marker — and while the bundle reported
// the binary alone, the page suppressed the one line that explains the marker
// and the operator was left with an unexplained warning on every tab.
func TestTools_SystemdRunMeansTheServiceFormIsAvailable(t *testing.T) {
	for _, tool := range cliTools {
		stubLatest(t, tool, "", http.StatusOK)
	}
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:tmux": {}, "exists:systemd-run": {}, "tmux": {Output: "tmux 3.6a\n"},
	}}
	m.SetOutput("env", "", errTest)
	h := newTestHandler(t, m)
	h.isRoot = func() bool { return false }

	rec := call(t, h.Tools, http.MethodGet, "", "", "?user=root")
	var env struct {
		Data struct {
			SystemdRun bool `json:"systemd_run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	// The fixture has systemd-run, so only the root half can produce false.
	if !h.haveSystemdRun() {
		t.Fatal("fixture: systemd-run must be present, or this test proves nothing")
	}
	if env.Data.SystemdRun {
		t.Error("systemd_run must be false for a non-root panel: the page reads it to explain the per-tab process marker, which persistence() already sets")
	}
	if got := h.persistence(); got != "process" {
		t.Errorf("persistence = %q, want process — systemd_run and the marker must come from one predicate", got)
	}
}

// The probe must answer with a path, never an alias. The shell is interactive
// (that is what reads .bashrc, and .bashrc is where ~/.local/bin joins PATH),
// so the account's aliases are in scope and `command -v claude` answers with
// the alias definition — which the tool chip then shows the operator as the
// tool's "path". This runs the real probeScript through a real bash against a
// fixture HOME that both defines such an alias and extends PATH.
func TestProbeScript_ResolvesAPathNotAnAlias(t *testing.T) {
	shell := findShell()
	if filepath.Base(shell) != "bash" {
		t.Skip("bash not available; the probe's resolution semantics are bash's")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(bin, "sfpfaketool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho '9.9.9 (fake)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The Debian/Ubuntu skeleton: a login shell reads .profile, and .profile
	// sources .bashrc — which is where both the PATH line and the alias live.
	if err := os.WriteFile(filepath.Join(home, ".profile"),
		[]byte("if [ -n \"$BASH_VERSION\" ] && [ -f \"$HOME/.bashrc\" ]; then . \"$HOME/.bashrc\"; fi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"),
		[]byte("PATH=\"$HOME/bin:$PATH\"\nalias sfpfaketool='sfpfaketool --verbose'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	acct := Account{Name: "probe", Home: home, Shell: shell}
	probe := func(arg string) (string, error) {
		cmd := osExec.Command(shell, "-lic", probeScript, arg)
		cmd.Env = accountEnv(acct) // same environment shellAs builds
		// CombinedOutput like RunWithTimeout: with no tty bash writes its
		// job-control warnings to stderr, and parseProbe picks the SFP line
		// out of the noise — exactly as it does in production.
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := probe("sfpfaketool")
	if err != nil {
		t.Fatalf("probe failed: %v (output: %q)", err, out)
	}
	p, v, ok := parseProbe(out)
	if !ok {
		t.Fatalf("probe produced no SFP line: %q", out)
	}
	if p != tool {
		t.Errorf("probe resolved %q, want the executable's path %q\n(an alias is in scope in the interactive shell; the resolved value is shown to the operator as the tool's path)", p, tool)
	}
	if !strings.Contains(v, "9.9.9") {
		t.Errorf("version = %q, want the version of the resolved binary (9.9.9)", v)
	}

	// exit 3 stays the not-installed signal: toolStatus reads any error as
	// "not installed", so a name that is nowhere on PATH must not print SFP.
	out, err = probe("sfpnosuchtool")
	if err == nil {
		t.Errorf("a missing tool must fail the probe; got %q", out)
	}
	if _, _, ok := parseProbe(out); ok {
		t.Errorf("a missing tool must not print an SFP line: %q", out)
	}
}
