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
	for _, base := range [][]string{
		{"PATH=/usr/bin", "USER=root"},
		{"PATH=/usr/bin", "HOME=/root"},
	} {
		env := installEnv(base, "/home/alice")
		if got := lastEnv(env, "HOME"); got != "/home/alice" {
			t.Errorf("base %v: HOME = %q, want /home/alice", base, got)
		}
		if got := lastEnv(env, "DEBIAN_FRONTEND"); got != "noninteractive" {
			t.Errorf("base %v: DEBIAN_FRONTEND = %q, want noninteractive", base, got)
		}
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
