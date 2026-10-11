package packages

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func runs(m *exec.MockCommander, name string, first string) int {
	n := 0
	for _, c := range m.Calls {
		if c.Name == name && (first == "" || (len(c.Args) > 0 && c.Args[0] == first)) {
			n++
		}
	}
	return n
}

func get(t *testing.T, fn echo.HandlerFunc) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	if err := fn(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
}

// The page asks five CLIs for their versions on every open; the answers
// change only when this handler installs something, so a second open within
// the TTL must not start a single process.
func TestStatusIsMemoisedUntilAMutation(t *testing.T) {
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:node": {}, "node": {Output: "v24.0.0"}, "npm": {Output: "11.0.0"},
	}}
	h := &Handler{Cmd: m}

	get(t, h.GetNodeStatus)
	get(t, h.GetNodeStatus)
	get(t, h.GetNodeStatus)
	if got := runs(m, "node", "--version"); got != 1 {
		t.Errorf("node --version ran %d times across three opens, want 1", got)
	}

	h.invalidateStatus() // what every Install*/Switch* handler defers
	get(t, h.GetNodeStatus)
	if got := runs(m, "node", "--version"); got != 2 {
		t.Errorf("node --version ran %d times after an install, want 2 — the memo must be dropped", got)
	}
}

// Docker's version is memoised but whether the daemon is running is not: an
// operator can stop it under the panel at any time, and the query is cheap.
func TestDockerRunningStateIsAlwaysLive(t *testing.T) {
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:docker": {}, "docker": {Output: "Docker version 29.0.0"}, "systemctl": {Output: "active"},
	}}
	h := &Handler{Cmd: m}

	get(t, h.GetDockerStatus)
	get(t, h.GetDockerStatus)
	if got := runs(m, "docker", "--version"); got != 1 {
		t.Errorf("docker --version ran %d times, want 1 (memoised)", got)
	}
	if got := runs(m, "systemctl", "is-active"); got != 2 {
		t.Errorf("systemctl is-active ran %d times, want 2 (never memoised)", got)
	}
}

// Installing or removing a package from the list is a mutation like any
// other: the memo said "no Node.js" for ten minutes after nodejs was
// installed from the package list, against what the memo's own comment says.
func TestAPackageInstallDropsTheMemo(t *testing.T) {
	_, f, _ := txSetup(t, false)
	h := &Handler{Cmd: f}
	f.set("apt-get", "Setting up nodejs (20.19.2) ...", nil)

	get(t, h.GetNodeStatus) // not installed, memoised
	f.set("exists:node", "", nil)
	f.set("node", "v20.19.2", nil)

	for name, mutate := range map[string]func(){
		"install": func() { call(t, h.InstallPackage, http.MethodPost, "/", `{"name":"nodejs"}`) },
		"remove":  func() { call(t, h.RemovePackage, http.MethodPost, "/", `{"name":"nodejs"}`) },
	} {
		before := len(f.calls("node"))
		mutate()
		get(t, h.GetNodeStatus)
		if len(f.calls("node")) == before {
			t.Errorf("after %s the node status still came from the memo", name)
		}
	}
}
