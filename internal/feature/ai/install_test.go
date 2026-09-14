package ai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

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

// `npm install -g` is system-wide: it changes what every account resolves, so
// the memo for every account has to go. Dropping only the selected account's
// entry left the chips for every other account showing "설치" for a tool that
// is now installed for all of them.
func TestInstallStream_NpmInstallInvalidatesEveryAccountsMemo(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{}) // no "exists:npm": the stream stops early, the defer still runs
	for _, key := range []string{"root\x00codex", "alice\x00codex", "root\x00claude"} {
		h.toolMemo[key] = toolMemoEntry{installed: true, at: testNow}
	}
	_ = stream(t, h, h.InstallStream, "codex", "?user=alice")

	for _, key := range []string{"root\x00codex", "alice\x00codex"} {
		if _, ok := h.toolMemo[key]; ok {
			t.Errorf("%q survived a system-wide npm install", key)
		}
	}
	if _, ok := h.toolMemo["root\x00claude"]; !ok {
		t.Error("a codex install must not touch the claude memo")
	}
}

func TestInstallStream_NpmMissingIsReportedInTheStream(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{}) // no "exists:npm"
	rec := stream(t, h, h.InstallStream, "codex", "?user=alice")
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(body, "ERROR: npm is not installed") || !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Errorf("stream = %q", body)
	}
}
