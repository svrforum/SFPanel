package firewall

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// pveHost is a Proxmox VE 9 node as the panel detects it.
var pveHost = platform.Info{
	OSID: "debian", OSVersionID: "13", PVE: true, PVEVersion: "9.2.3",
	NetworkStack: platform.StackIfupdown2, JournalOnly: true, Resolver: platform.ResolverFile,
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("response is not one JSON envelope: %v — %q", err, rec.Body.String())
	}
	return e
}

func call(t *testing.T, handler func(echo.Context) error, method, target, body string, params ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if len(params) == 2 {
		c.SetParamNames(params[0])
		c.SetParamValues(params[1])
	}
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

func statusOf(t *testing.T, cmd exec.Commander) UFWStatus {
	t.Helper()
	rec := call(t, (&Handler{Cmd: cmd, PanelPort: 3628}).GetUFWStatus, http.MethodGet, "/firewall/status", "")
	e := decode(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var st UFWStatus
	if err := json.Unmarshal(e.Data, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// A Proxmox host has no ufw; its firewall is pve-firewall. The page said
// "Inactive" and offered to enable ufw.
func TestStatusOnProxmoxNamesPVEFirewall(t *testing.T) {
	platform.SetForTest(t, pveHost)
	m := newUFWArgsCommander()
	m.set("pve-firewall status", "Status: disabled/running\n", nil)

	got := statusOf(t, m)
	want := UFWStatus{Manager: "pve-firewall", PVE: &PVEFirewallStatus{Enabled: false, Running: true, Raw: "Status: disabled/running"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v (pve %+v)\nwant %+v (pve %+v)", got, got.PVE, want, want.PVE)
	}
}

// Where the operator already runs ufw on a Proxmox host, ufw is what the page
// manages, with pve-firewall's status beside it.
func TestStatusOnProxmoxWithUfwRunning(t *testing.T) {
	platform.SetForTest(t, pveHost)
	m := newUFWArgsCommander()
	m.setExists("ufw", true)
	m.set("ufw status verbose", "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n", nil)
	m.set("pve-firewall status", "Status: enabled/running\n", nil)

	got := statusOf(t, m)
	if got.Manager != "ufw" || !got.Installed || !got.Active || got.PVE == nil || !got.PVE.Enabled {
		t.Errorf("got %+v (pve %+v)", got, got.PVE)
	}
}

func TestStatusWithoutUfwSaysSo(t *testing.T) {
	platform.SetForTest(t, platform.Info{OSID: "debian", OSVersionID: "12"})
	got := statusOf(t, newUFWArgsCommander())
	if !reflect.DeepEqual(got, UFWStatus{Manager: "none"}) {
		t.Errorf("no ufw: got %+v", got)
	}

	m := newUFWArgsCommander()
	m.setExists("ufw", true)
	m.set("ufw status verbose", "Status: inactive\n", nil)
	got = statusOf(t, m)
	if got.Manager != "ufw" || !got.Installed || got.Active || got.PVE != nil {
		t.Errorf("ufw inactive: got %+v", got)
	}
}

func TestParsePVEFirewallStatus(t *testing.T) {
	for out, want := range map[string]PVEFirewallStatus{
		"Status: disabled/running\n":                  {Running: true, Raw: "Status: disabled/running"},
		"Status: enabled/running\n":                   {Enabled: true, Running: true, Raw: "Status: enabled/running"},
		"Status: enabled/stopped (pending changes)\n": {Enabled: true, Raw: "Status: enabled/stopped (pending changes)"},
		"ipcc_send_rec[1] failed: Connection refused": {Raw: "ipcc_send_rec[1] failed: Connection refused"},
	} {
		if got := parsePVEFirewallStatus(out); got != want {
			t.Errorf("%q: got %+v, want %+v", out, got, want)
		}
	}
}

// ufw's default deny next to pve-firewall cuts the web UI on 8006, the
// cluster and the guest consoles. On a Proxmox host not already running ufw,
// every ufw change is refused with its own code, force or not — even where
// the lockout guard would have let it through (22 is allowed).
func TestUfwChangesAreRefusedOnProxmox(t *testing.T) {
	type tc struct {
		name, method, target, body string
		params                     []string
		handler                    func(*Handler) func(echo.Context) error
		write                      string
	}
	for _, c := range []tc{
		{"enable", http.MethodPost, "/firewall/enable", "", nil, func(h *Handler) func(echo.Context) error { return h.EnableUFW }, "ufw --force enable"},
		{"enable forced", http.MethodPost, "/firewall/enable?force=true", "", nil, func(h *Handler) func(echo.Context) error { return h.EnableUFW }, "ufw --force enable"},
		{"add", http.MethodPost, "/firewall/rules", `{"action":"allow","port":"80","protocol":"tcp"}`, nil, func(h *Handler) func(echo.Context) error { return h.AddRule }, "ufw allow"},
		{"delete", http.MethodDelete, "/firewall/rules/1", "", []string{"number", "1"}, func(h *Handler) func(echo.Context) error { return h.DeleteRule }, "ufw --force delete"},
	} {
		t.Run(c.name, func(t *testing.T) {
			platform.SetForTest(t, pveHost)
			m := newUFWArgsCommander()
			m.setExists("ufw", true)
			m.set("ufw status", "Status: inactive\n", nil)
			m.set("ufw status numbered", "Status: inactive\n", nil)
			m.set("ufw show added", "Added user rules (see 'ufw status' for running firewall):\nufw allow 22/tcp\nufw allow 8006/tcp\n", nil)

			rec := call(t, c.handler(&Handler{Cmd: m, PanelPort: 3628}), c.method, c.target, c.body, c.params...)
			if e := decode(t, rec); rec.Code != http.StatusConflict || e.Error.Code != "FIREWALL_MANAGED_BY_PVE" {
				t.Errorf("got %d %s, want 409 FIREWALL_MANAGED_BY_PVE", rec.Code, rec.Body.String())
			}
			for _, call := range m.calls {
				if strings.HasPrefix(call, c.write) {
					t.Errorf("ran %q", call)
				}
			}
		})
	}
}

// Where ufw already runs on a Proxmox host the panel keeps managing it, and
// the lockout guard keeps 8006 as it keeps SSH and the panel. The refusal
// names the port, so the page can say it is the Proxmox UI at stake.
func TestUfwOnProxmoxKeepsTheWebUI(t *testing.T) {
	const numbered = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 8006/tcp                   ALLOW IN    192.0.2.0/24
`
	newCmd := func() *ufwArgsCommander {
		m := newUFWArgsCommander()
		m.setExists("ufw", true)
		m.set("ufw status", "Status: active\n", nil)
		m.set("ufw status numbered", numbered, nil)
		return m
	}
	wantLockout := func(t *testing.T, m *ufwArgsCommander, rec *httptest.ResponseRecorder, write string) {
		t.Helper()
		e := decode(t, rec)
		if rec.Code != http.StatusConflict || e.Error.Code != "UFW_LOCKOUT" || e.Error.Details["port"] != "8006" {
			t.Errorf("got %d %s, want 409 UFW_LOCKOUT naming port 8006", rec.Code, rec.Body.String())
		}
		for _, call := range m.calls {
			if strings.HasPrefix(call, write) {
				t.Errorf("ran %q", call)
			}
		}
	}

	t.Run("deny 8006", func(t *testing.T) {
		platform.SetForTest(t, pveHost)
		m := newCmd()
		h := &Handler{Cmd: m, PanelPort: 3628}
		rec := call(t, h.AddRule, http.MethodPost, "/firewall/rules", `{"action":"deny","port":"8000:8010","protocol":"tcp"}`)
		wantLockout(t, m, rec, "ufw deny")
	})
	t.Run("delete the last allow for 8006", func(t *testing.T) {
		platform.SetForTest(t, pveHost)
		m := newCmd()
		h := &Handler{Cmd: m, PanelPort: 3628}
		rec := call(t, h.DeleteRule, http.MethodDelete, "/firewall/rules/2", "", "number", "2")
		wantLockout(t, m, rec, "ufw --force delete")
	})
	t.Run("enable with no allow for 8006", func(t *testing.T) {
		platform.SetForTest(t, pveHost)
		m := newCmd()
		m.set("ufw status numbered", "Status: active\n\n[ 1] 22/tcp                     ALLOW IN    Anywhere\n", nil)
		h := &Handler{Cmd: m, PanelPort: 3628}
		rec := call(t, h.EnableUFW, http.MethodPost, "/firewall/enable", "")
		wantLockout(t, m, rec, "ufw --force enable")
	})
	t.Run("other changes go through", func(t *testing.T) {
		platform.SetForTest(t, pveHost)
		m := newCmd()
		h := &Handler{Cmd: m, PanelPort: 3628}
		rec := call(t, h.AddRule, http.MethodPost, "/firewall/rules", `{"action":"allow","port":"80","protocol":"tcp"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d %s", rec.Code, rec.Body.String())
		}
		if m.calls[len(m.calls)-1] != "ufw allow proto tcp to any port 80" {
			t.Errorf("calls %q", m.calls)
		}
	})
}

// requireTool answered "not installed" and the handler carried on: it ran ufw
// all the same and wrote a second body after the first.
func TestAMissingToolIsTheWholeAnswer(t *testing.T) {
	platform.SetForTest(t, platform.Info{})
	m := newUFWArgsCommander()
	rec := call(t, (&Handler{Cmd: m, PanelPort: 3628}).EnableUFW, http.MethodPost, "/firewall/enable?force=true", "")
	if e := decode(t, rec); rec.Code != http.StatusNotImplemented || e.Error.Code != "TOOL_NOT_INSTALLED" {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
	if len(m.calls) != 0 {
		t.Errorf("ran %q", m.calls)
	}
}
