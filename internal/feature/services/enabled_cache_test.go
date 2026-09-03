package services

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func countCalls(m *exec.MockCommander, arg string) int {
	n := 0
	for _, c := range m.Calls {
		if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == arg {
			n++
		}
	}
	return n
}

func listOnce(t *testing.T, h *Handler) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/system/services", nil)
	rec := httptest.NewRecorder()
	if err := h.ListServices(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
}

// list-unit-files costs PID 1 the better part of a second and answers a
// question that only enable/disable changes, so it must not be paid on every
// cache miss of the 3-second unit list — which the services page's 15-second
// poll misses every time.
func TestEnabledStatesSurviveTheUnitListCache(t *testing.T) {
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{"exists:systemctl": {}}}
	h := &Handler{Cmd: m}

	listOnce(t, h)
	h.invalidateServiceCache() // the 3s TTL expiring
	listOnce(t, h)
	h.invalidateServiceCache()
	listOnce(t, h)

	if got := countCalls(m, "list-units"); got != 3 {
		t.Errorf("list-units ran %d times, want 3 (once per miss)", got)
	}
	if got := countCalls(m, "list-unit-files"); got != 1 {
		t.Errorf("list-unit-files ran %d times, want 1 — it is the expensive one and nothing changed it", got)
	}
}

// And the one thing that does change it drops the cache, or a unit the
// operator just enabled would show as disabled for five minutes.
func TestEnableDropsTheEnabledCache(t *testing.T) {
	m := &exec.MockCommander{Outputs: map[string]exec.MockResult{"exists:systemctl": {}}}
	h := &Handler{Cmd: m}

	listOnce(t, h)
	h.invalidateEnabledCache() // what EnableService / DisableService call
	h.invalidateServiceCache()
	listOnce(t, h)

	if got := countCalls(m, "list-unit-files"); got != 2 {
		t.Errorf("list-unit-files ran %d times, want 2 after an enable", got)
	}
}
