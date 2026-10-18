package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	osExec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// Every test in the package starts on a host with no npm on the panel's PATH
// and nvm's installer out of reach: an npm install that reached the real ones
// would run `npm install -g` against the test machine, and did once. A test
// that needs either sets it, and puts it back.
func init() {
	lookPath = func(string) (string, error) { return "", osExec.ErrNotFound }
	downloadNVM = func(context.Context) (string, string, error) { return "", "", errors.New("offline") }
}

func stream(t *testing.T, h *Handler, fn echo.HandlerFunc, tool, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ai/tools/"+tool+"/install-stream"+query, nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("tool")
	c.SetParamValues(tool)
	if err := fn(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

// The guards answer as JSON before any SSE header goes out, so the client
// can show the code instead of a broken stream.
func TestInstallStream_GuardsAnswerJSON(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{})
	rec := stream(t, h, h.InstallStream, "shell", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidTool {
		t.Errorf("shell: %s, want INVALID_TOOL", code)
	}
	rec = stream(t, h, h.UpdateStream, "vim", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidTool {
		t.Errorf("vim: %s, want INVALID_TOOL", code)
	}
	rec = stream(t, h, h.InstallStream, "codex", "?user=bob")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("bob: %s, want INVALID_ACCOUNT", code)
	}
}

// lastEnv reads a key the way os/exec resolves Cmd.Env: the last entry wins.
func lastEnv(env []string, key string) string {
	val := ""
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			val = strings.TrimPrefix(e, key+"=")
		}
	}
	return val
}

// Design §3: the installer runs "with the account's HOME". The panel process
// has none to inherit (systemd system unit) and claude.ai/install.sh uses
// $HOME with no fallback, so installEnv must name the account's home whether
// the panel environment carries a different HOME or none at all.
func TestInstallEnv_CarriesTheAccountHome(t *testing.T) {
	panel := Account{Name: "root", Home: "/root", Shell: "/bin/bash"}
	for _, base := range [][]string{
		{"PATH=/usr/bin", "USER=root"},
		{"PATH=/usr/bin", "HOME=/srv"},
	} {
		for _, acct := range []Account{
			{Name: "alice", UID: 1000, Home: "/home/alice", Shell: "/bin/bash"},
			{Name: "root", Home: "/home/alice", Shell: "/bin/bash"}, // the panel branch
		} {
			env := installEnv(base, acct, panel)
			if got := lastEnv(env, "HOME"); got != "/home/alice" {
				t.Errorf("base %v, %s: HOME = %q, want /home/alice", base, acct.Name, got)
			}
			if got := lastEnv(env, "DEBIAN_FRONTEND"); got != "noninteractive" {
				t.Errorf("base %v, %s: DEBIAN_FRONTEND = %q, want noninteractive", base, acct.Name, got)
			}
		}
	}
}

// The installer runs as the account under runuser, so it can read its own
// /proc/self/environ. The panel's environment carries SFPANEL_JWT_SECRET when
// the operator uses that supported override — the ability to mint admin tokens
// — so for any other account the environment must be built, not inherited.
// The panel's own account is not a boundary and keeps what it had.
func TestInstallEnv_ANonPanelAccountInheritsNothing(t *testing.T) {
	panel := Account{Name: "root", Home: "/root", Shell: "/bin/bash"}
	base := []string{"PATH=/usr/sbin:/usr/bin", "SFPANEL_JWT_SECRET=signing-key", "NOTIFY_SOCKET=/run/systemd/notify"}
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}

	env := installEnv(base, alice, panel)
	for _, key := range []string{"SFPANEL_JWT_SECRET", "NOTIFY_SOCKET"} {
		if got := lastEnv(env, key); got != "" {
			t.Errorf("alice's installer carries %s=%q; env = %q", key, got, env)
		}
	}
	if lastEnv(env, "PATH") == "" || lastEnv(env, "USER") != "alice" || lastEnv(env, "LOGNAME") != "alice" {
		t.Errorf("alice's installer needs a PATH and its own identity; env = %q", env)
	}
	if env := installEnv(base, panel, panel); lastEnv(env, "SFPANEL_JWT_SECRET") != "signing-key" {
		t.Errorf("the panel account's installer = %q, want the inherited environment", env)
	}
}

// An npm install changes what every account resolves when the npm is the
// system's, so the memo for every account has to go. Dropping only the
// selected account's entry left the chips for every other account showing
// "설치" for a tool that is now installed for all of them.
func TestInstallStream_NpmInstallInvalidatesEveryAccountsMemo(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{}) // no npm and nvm's installer offline: the stream stops early, the defer still runs
	for _, key := range []string{"root\x00codex", "alice\x00codex", "root\x00claude"} {
		h.toolMemo[key] = toolMemoEntry{installed: true, at: testNow}
	}
	_ = stream(t, h, h.InstallStream, "codex", "?user=alice")

	for _, key := range []string{"root\x00codex", "alice\x00codex"} {
		if _, ok := h.toolMemo[key]; ok {
			t.Errorf("%q survived an npm install", key)
		}
	}
	if _, ok := h.toolMemo["root\x00claude"]; !ok {
		t.Error("a codex install must not touch the claude memo")
	}
}

// With no npm anywhere the install puts Node.js in place first instead of
// telling the operator to go and do it ("ERROR: npm is not installed"), and a
// failure on the way is reported in the stream, which still ends in [DONE].
func TestInstallStream_NodeComesFirstAndItsFailureIsReported(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{}) // alice's login shell finds no npm either
	rec := stream(t, h, h.InstallStream, "codex", "?user=alice")
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Fatalf("stream = %q", body)
	}
	for _, want := range []string{"alice has no Node.js: installing nvm and Node.js LTS first", "ERROR: Failed to download nvm install script: offline"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream lacks %q: %q", want, body)
		}
	}
}

// An npm the panel runs for every account has to install for every account.
// The packages page's Node.js button links /usr/local/bin/npm into root's nvm,
// and `npm install -g` through that link writes under /root; it reads as
// "on PATH" all the same, so where it resolves is what decides.
func TestSystemNpm_AnNpmInsideAHomeIsThatAccounts(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home", "alice")
	sys := filepath.Join(root, "usr", "bin", "npm")
	nvmNpm := filepath.Join(home, ".nvm", "versions", "node", "v24.16.0", "bin", "npm")
	for _, f := range []string{sys, nvmNpm} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	intoHome := filepath.Join(root, "usr", "local", "bin", "npm")
	toSystem := filepath.Join(root, "opt", "bin", "npm")
	for link, target := range map[string]string{intoHome: nvmNpm, toSystem: sys} {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	h := newTestHandler(t, &exec.MockCommander{})
	pw := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(pw, []byte("root:x:0:0:root:/root:/bin/bash\nalice:x:1000:1000::"+home+":/bin/bash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.passwdPath = pw

	prev := lookPath
	t.Cleanup(func() { lookPath = prev })
	for found, want := range map[string]string{
		sys:      sys,
		toSystem: toSystem,
		nvmNpm:   "",
		intoHome: "",
		"/root/.nvm/versions/node/v24.16.0/bin/npm": "", // root's, whether or not this machine lets the test look
	} {
		lookPath = func(string) (string, error) { return found, nil }
		if got := h.systemNpm(); got != want {
			t.Errorf("npm at %s: systemNpm = %q, want %q", found, got, want)
		}
	}
	lookPath = func(string) (string, error) { return "", osExec.ErrNotFound }
	if got := h.systemNpm(); got != "" {
		t.Errorf("no npm: systemNpm = %q", got)
	}
}

// Without a system npm, the install runs as the account on its own Node.js:
// its nvm when it has one, else the npm its login shell finds, else nvm's
// installer first. The route is the argv; the account is runuser's.
func TestAccountNpmCommand_RunsOnTheAccountsOwnNode(t *testing.T) {
	t.Setenv("SFPANEL_JWT_SECRET", "signing-key")
	const pkg = "@openai/codex@latest"
	var lines []string
	sendLine := func(l string) { lines = append(lines, l) }
	newAlice := func() Account {
		return Account{Name: "alice", UID: 1000, GID: 1000, Home: t.TempDir(), Shell: "/bin/bash"}
	}
	assertAccountEnv := func(t *testing.T, cmd *osExec.Cmd, acct Account) {
		t.Helper()
		if lastEnv(cmd.Env, "HOME") != acct.Home || lastEnv(cmd.Env, "SHELL") != acct.Shell || lastEnv(cmd.Env, "SFPANEL_JWT_SECRET") != "" {
			t.Errorf("env = %q, want alice's HOME and SHELL and nothing of the panel's", cmd.Env)
		}
	}

	t.Run("nvm", func(t *testing.T) {
		alice := newAlice()
		nvmDir := filepath.Join(alice.Home, ".nvm")
		if err := os.MkdirAll(nvmDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nvmDir, "nvm.sh"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		h := newTestHandler(t, &exec.MockCommander{})
		cmd, cleanup := h.accountNpmCommand(context.Background(), alice, pkg, sendLine)
		if cmd == nil {
			t.Fatalf("no command; stream = %q", lines)
		}
		defer cleanup()
		if want := []string{"runuser", "-u", "alice", "--", "bash", "-c", nvmNpmScript, pkg, ""}; !slices.Equal(cmd.Args, want) {
			t.Errorf("args = %q, want %q", cmd.Args, want)
		}
		if lastEnv(cmd.Env, "NVM_DIR") != nvmDir {
			t.Errorf("NVM_DIR = %q, want %q", lastEnv(cmd.Env, "NVM_DIR"), nvmDir)
		}
		assertAccountEnv(t, cmd, alice)
	})

	t.Run("the login shell's npm", func(t *testing.T) {
		alice := newAlice()
		m := &exec.MockCommander{Outputs: map[string]exec.MockResult{"env": {Output: "SFP\t" + alice.Home + "/.volta/bin/npm\t10.9.2\n"}}}
		h := newTestHandler(t, m)
		cmd, cleanup := h.accountNpmCommand(context.Background(), alice, pkg, sendLine)
		if cmd == nil {
			t.Fatalf("no command; stream = %q", lines)
		}
		defer cleanup()
		if want := []string{"runuser", "-u", "alice", "--", findShell(), "-lic", `npm install -g "$0"`, pkg}; !slices.Equal(cmd.Args, want) {
			t.Errorf("args = %q, want %q", cmd.Args, want)
		}
		assertAccountEnv(t, cmd, alice)
	})

	t.Run("no Node.js at all", func(t *testing.T) {
		alice := newAlice()
		installer := filepath.Join(t.TempDir(), "install.sh")
		if err := os.WriteFile(installer, []byte("#!/bin/sh\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		prev := downloadNVM
		t.Cleanup(func() { downloadNVM = prev })
		downloadNVM = func(context.Context) (string, string, error) { return installer, "", nil }
		h := newTestHandler(t, &exec.MockCommander{})
		var chowned []int
		h.chown = func(path string, uid, gid int) error {
			if path == installer {
				chowned = append(chowned, uid, gid)
			}
			return nil
		}
		cmd, cleanup := h.accountNpmCommand(context.Background(), alice, pkg, sendLine)
		if cmd == nil {
			t.Fatalf("no command; stream = %q", lines)
		}
		if want := []string{"runuser", "-u", "alice", "--", "bash", "-c", nvmNpmScript, pkg, installer}; !slices.Equal(cmd.Args, want) {
			t.Errorf("args = %q, want %q", cmd.Args, want)
		}
		if !slices.Equal(chowned, []int{1000, 1000}) {
			t.Errorf("installer handed to %v, want alice's 1000:1000", chowned)
		}
		cleanup()
		if _, err := os.Stat(installer); !os.IsNotExist(err) {
			t.Errorf("cleanup left the installer behind: %v", err)
		}
	})

	t.Run("the panel's own account", func(t *testing.T) {
		h := newTestHandler(t, &exec.MockCommander{})
		panel := h.panel
		panel.Home = t.TempDir()
		h.panel = panel
		if err := os.MkdirAll(filepath.Join(panel.Home, ".nvm"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(panel.Home, ".nvm", "nvm.sh"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd, cleanup := h.accountNpmCommand(context.Background(), panel, pkg, sendLine)
		if cmd == nil {
			t.Fatalf("no command; stream = %q", lines)
		}
		defer cleanup()
		if cmd.Args[0] != "bash" {
			t.Errorf("args = %q: the panel's own account needs no runuser", cmd.Args)
		}
	})
}
