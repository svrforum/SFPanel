package ai

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// versionNumber pulls the x.y.z out of "2.1.92 (Claude Code)" or "codex-cli 0.154.0".
func versionNumber(s string) string { return versionRe.FindString(s) }

// compareVersions is -1/0/1 on three numeric parts; 0 when either side is
// empty or malformed, so an unknown latest never claims an update.
func compareVersions(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) != 3 || len(pb) != 3 {
		return 0
	}
	for i := 0; i < 3; i++ {
		x, err1 := strconv.Atoi(pa[i])
		y, err2 := strconv.Atoi(pb[i])
		if err1 != nil || err2 != nil {
			return 0
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

type ToolStatus struct {
	Installed       bool   `json:"installed"`
	Version         string `json:"version"`
	Path            string `json:"path"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	LoggedIn        bool   `json:"logged_in"`
}

type toolMemoEntry struct {
	installed     bool
	version, path string
	at            time.Time
}

const toolTTL = 10 * time.Minute

// loginFiles are the credential files each CLI writes after its OAuth flow —
// a hint for the UI ("첫 실행 시 로그인 필요"), never a gate.
var loginFiles = map[string]string{
	ToolClaude: ".claude/.credentials.json",
	ToolCodex:  ".codex/auth.json",
	ToolGemini: ".gemini/oauth_creds.json",
}

// probeScript runs inside the account's login shell so PATH is theirs
// (~/.local/bin, nvm). $0 is the tool name. Exit 3 = not installed. The
// SFP-prefixed line survives whatever the account's profile prints.
const probeScript = `p=$(command -v "$0") || exit 3; v=$("$0" --version 2>/dev/null | head -n1); printf "SFP\t%s\t%s\n" "$p" "$v"`

func parseProbe(out string) (string, string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "SFP\t") {
			f := strings.Split(line, "\t")
			if len(f) >= 3 {
				return f[1], strings.TrimSpace(f[2]), true
			}
		}
	}
	return "", "", false
}

// shellAs runs script in a login shell as the account; runuser only when
// the account is not the panel's own.
func (h *Handler) shellAs(acct Account, script, arg string) (string, error) {
	if acct.Name != h.panel.Name {
		return h.Cmd.RunWithTimeout(20*time.Second, "runuser", "-u", acct.Name, "--", findShell(), "-lc", script, arg)
	}
	return h.Cmd.RunWithTimeout(20*time.Second, findShell(), "-lc", script, arg)
}

func (h *Handler) toolStatus(acct Account, tool string) ToolStatus {
	key := acct.Name + "\x00" + tool
	h.memoMu.Lock()
	e, ok := h.toolMemo[key]
	h.memoMu.Unlock()
	if !ok || h.now().Sub(e.at) >= toolTTL {
		out, err := h.shellAs(acct, probeScript, tool)
		e = toolMemoEntry{at: h.now()}
		if err == nil {
			if p, v, ok := parseProbe(out); ok {
				e.installed, e.path, e.version = true, p, v
			}
		}
		h.memoMu.Lock()
		h.toolMemo[key] = e
		h.memoMu.Unlock()
	}
	st := ToolStatus{Installed: e.installed, Version: e.version, Path: e.path}
	if rel, ok := loginFiles[tool]; ok && acct.Home != "" {
		if _, err := os.Stat(filepath.Join(acct.Home, rel)); err == nil {
			st.LoggedIn = true
		}
	}
	st.Latest = h.latestVersion(tool)
	st.UpdateAvailable = st.Installed && compareVersions(versionNumber(st.Version), st.Latest) < 0
	return st
}

func (h *Handler) invalidateTool(account, tool string) {
	h.memoMu.Lock()
	delete(h.toolMemo, account+"\x00"+tool)
	h.memoMu.Unlock()
}

type toolsResponse struct {
	Tmux struct {
		Installed bool   `json:"installed"`
		Version   string `json:"version"`
	} `json:"tmux"`
	SystemdRun   bool                  `json:"systemd_run"`
	Accounts     []string              `json:"accounts"`
	PanelAccount string                `json:"panel_account"`
	Account      string                `json:"account"`
	Tools        map[string]ToolStatus `json:"tools"`
}

// Tools — GET /ai/tools?user=<account>. The three probes run concurrently
// so a cold, offline host answers in one 10 s lookup timeout, not three.
func (h *Handler) Tools(c echo.Context) error {
	acct, ok := h.resolveAccount(c.QueryParam("user"))
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "user is not a login account on this node")
	}
	var resp toolsResponse
	resp.Tmux.Installed = h.Cmd.Exists("tmux")
	if resp.Tmux.Installed {
		if out, err := h.Cmd.Run("tmux", "-V"); err == nil {
			resp.Tmux.Version = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "tmux "))
		}
	}
	resp.SystemdRun = h.haveSystemdRun()
	resp.Accounts = []string{}
	for _, a := range h.accounts() {
		resp.Accounts = append(resp.Accounts, a.Name)
	}
	resp.PanelAccount = h.panel.Name
	resp.Account = acct.Name
	resp.Tools = map[string]ToolStatus{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, tool := range cliTools {
		wg.Add(1)
		go func(tool string) {
			defer wg.Done()
			st := h.toolStatus(acct, tool)
			mu.Lock()
			resp.Tools[tool] = st
			mu.Unlock()
		}(tool)
	}
	wg.Wait()
	return response.OK(c, resp)
}
