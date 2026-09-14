package ai

import (
	"encoding/json"
	"net/http"
	"os"
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
func TestToolStatus_ProbesInTheAccountsLoginShell(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("runuser", "SFP\t/home/alice/.local/bin/claude\t2.1.92 (Claude Code)\n", nil)
	h := newTestHandler(t, m)
	alice := Account{Name: "alice", UID: 1000, Home: t.TempDir(), Shell: "/bin/bash"}

	st := h.toolStatus(alice, ToolClaude)
	if !st.Installed || st.Path != "/home/alice/.local/bin/claude" || st.Version != "2.1.92 (Claude Code)" {
		t.Errorf("status = %+v", st)
	}
	c := m.Calls[0]
	want := []string{"-u", "alice", "--", findShell(), "-lc", probeScript, "claude"}
	if c.Name != "runuser" || strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("probe = %s %q\nwant runuser %q", c.Name, c.Args, want)
	}

	m2 := exec.NewMockCommander()
	m2.SetOutput(findShell(), "SFP\t/root/.local/bin/claude\t2.1.92 (Claude Code)\n", nil)
	h2 := newTestHandler(t, m2)
	_ = h2.toolStatus(h2.panel, ToolClaude)
	if m2.Calls[0].Name != findShell() || m2.Calls[0].Args[0] != "-lc" {
		t.Errorf("the panel account probes in a plain login shell, got %s %q", m2.Calls[0].Name, m2.Calls[0].Args)
	}
}

func TestToolStatus_MemoisedUntilInstall(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput(findShell(), "", errTest) // exit 3: not installed
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
	m.SetOutput(findShell(), "SFP\t/root/.local/bin/claude\t2.1.92 (Claude Code)\n", nil)
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
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:tmux": {}, "exists:systemd-run": {}, "tmux": {Output: "tmux 3.6a\n"},
	}}
	m.SetOutput(findShell(), "", errTest)
	h := newTestHandler(t, m)

	rec := call(t, h.Tools, http.MethodGet, "", "", "?user=alice")
	var env struct {
		Data struct {
			Tmux struct {
				Installed bool
				Version   string
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
	rec = call(t, h.Tools, http.MethodGet, "", "", "?user=bob")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("bob: %s, want INVALID_ACCOUNT", code)
	}
}
