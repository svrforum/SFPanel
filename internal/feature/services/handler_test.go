package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

func TestIsProtectedServiceUnit(t *testing.T) {
	// Panel's own systemd unit is protected against stop/restart/disable.
	if !isProtectedServiceUnit("sfpanel.service") {
		t.Errorf("sfpanel.service should be protected")
	}
	// Case-insensitive — operators sometimes type uppercase.
	if !isProtectedServiceUnit("SFPanel.service") {
		t.Errorf("SFPanel.service (mixed case) should be protected")
	}
	// Anything else is unprotected.
	for _, name := range []string{
		"nginx.service",
		"docker.service",
		"sshd.service",
		"my-app.service",
	} {
		if isProtectedServiceUnit(name) {
			t.Errorf("%s should NOT be protected", name)
		}
	}
}

func TestValidServiceName(t *testing.T) {
	// Regression fence — name regex must accept normal units and reject
	// path traversal / shell metacharacters.
	accepts := []string{
		"nginx.service",
		"getty@tty1.service",
		"system-systemd-cryptsetup.service",
		"my-app_v2.service",
	}
	for _, n := range accepts {
		if !validServiceName.MatchString(n) {
			t.Errorf("validServiceName(%q) = false, want true", n)
		}
	}
	rejects := []string{
		"",
		"../etc/passwd.service",
		"nginx",
		"nginx.timer",
		"foo.service;rm",
		"foo bar.service",
	}
	for _, n := range rejects {
		if validServiceName.MatchString(n) {
			t.Errorf("validServiceName(%q) = true, want false", n)
		}
	}
}

// TestListServices_GracefulEmptyWhenSystemctlAbsent locks in C5: when the
// systemctl binary is missing (minimal containers, follower nodes the
// operator reaches via ?node=) ListServices returns an empty list instead
// of 500. Without this regression test someone could re-tighten the
// handler and unknowingly break remote-node Service tab rendering.
func TestListServices_GracefulEmptyWhenSystemctlAbsent(t *testing.T) {
	mock := commonExec.NewMockCommander()
	// Don't register "exists:systemctl" — MockCommander.Exists returns
	// false for anything not in the Outputs map.
	h := &Handler{Cmd: mock}

	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/system/services", nil)
	c := e.NewContext(req, rec)

	if err := h.ListServices(c); err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Success bool `json:"success"`
		Data    struct {
			Services []interface{} `json:"services"`
			Total    int           `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v — body=%s", err, rec.Body.String())
	}
	if !got.Success {
		t.Errorf("response success=false: %s", rec.Body.String())
	}
	if got.Data.Total != 0 {
		t.Errorf("total=%d, want 0", got.Data.Total)
	}
	if len(got.Data.Services) != 0 {
		t.Errorf("len(services)=%d, want 0", len(got.Data.Services))
	}
}

type actionResult struct {
	status int
	body   string
	code   string
	detail map[string]string
	ran    bool // systemctl <action> was called
}

// runAction calls one of the action handlers for unit, with ?confirm= when
// confirm is not empty, and reports what came back and whether systemctl ran.
func runAction(t *testing.T, action, unit, confirm string) actionResult {
	t.Helper()
	m := commonExec.NewMockCommander()
	h := &Handler{Cmd: m}
	target := "/system/services/" + unit + "/" + action
	if confirm != "" {
		target += "?confirm=" + confirm
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, target, nil), rec)
	c.SetParamNames("name")
	c.SetParamValues(unit)
	handlers := map[string]func(echo.Context) error{
		"stop": h.StopService, "restart": h.RestartService, "disable": h.DisableService,
	}
	if err := handlers[action](c); err != nil {
		t.Fatalf("%s %s: %v", action, unit, err)
	}
	r := actionResult{status: rec.Code, body: rec.Body.String()}
	var got struct {
		Error *struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	// One JSON document: a refusal followed by the success body is two.
	dec := json.NewDecoder(rec.Body)
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode: %v — body=%s", err, r.body)
	}
	if dec.More() {
		t.Errorf("%s %s wrote more than one response: %s", action, unit, r.body)
	}
	if got.Error != nil {
		r.code, r.detail = got.Error.Code, got.Error.Details
	}
	for _, call := range m.Calls {
		if call.Name == "systemctl" && len(call.Args) > 0 && call.Args[0] == action {
			r.ran = true
		}
	}
	return r
}

// The panel's own units were "refused" with a 403 and then stopped anyway:
// the guard returned response.Fail's error, which is nil once the 403 is
// written, so the handler carried on into systemctl and wrote a second body.
func TestPanelUnitIsRefusedAndNotStopped(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	for _, action := range []string{"stop", "restart", "disable"} {
		r := runAction(t, action, "sfpanel.service", "")
		if r.status != http.StatusForbidden || r.code != response.ErrPermissionDenied {
			t.Errorf("%s sfpanel.service: status=%d code=%q, want 403 %s — body=%s", action, r.status, r.code, response.ErrPermissionDenied, r.body)
		}
		if r.ran {
			t.Errorf("%s sfpanel.service: systemctl ran after the refusal", action)
		}
	}
}

// On an ifupdown host, stopping networking.service is ifdown -a: the host
// drops every address, the panel's own included, and on Proxmox the guest
// bridges with them.
func TestNetworkingIsRefusedWhereIfupdownRunsIt(t *testing.T) {
	for _, stack := range []string{platform.StackIfupdown, platform.StackIfupdown2} {
		platform.SetForTest(t, platform.Info{NetworkStack: stack})
		for _, action := range []string{"stop", "restart", "disable"} {
			r := runAction(t, action, "networking.service", "networking.service")
			if r.status != http.StatusForbidden || r.code != response.ErrServiceProtected {
				t.Errorf("%s/%s: status=%d code=%q, want 403 %s — body=%s", stack, action, r.status, r.code, response.ErrServiceProtected, r.body)
			}
			if r.detail["unit"] != "networking.service" || r.detail["hint"] != "ifreload -a" {
				t.Errorf("%s/%s: details=%v, want unit networking.service and hint 'ifreload -a'", stack, action, r.detail)
			}
			if r.ran {
				t.Errorf("%s/%s: systemctl ran", stack, action)
			}
		}
	}
	// On a netplan host networking.service is not what holds the network.
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	if r := runAction(t, "restart", "networking.service", ""); r.status != http.StatusOK || !r.ran {
		t.Errorf("netplan host: restart networking.service status=%d ran=%v, want 200 and run — body=%s", r.status, r.ran, r.body)
	}
}

func TestProxmoxUnitTiers(t *testing.T) {
	platform.SetForTest(t, platform.Info{PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2})

	refused := [][2]string{
		{"stop", "pve-cluster.service"}, {"disable", "corosync.service"},
		{"restart", "watchdog-mux.service"}, {"stop", "proxmox-firewall.service"},
		{"disable", "pve-guests.service"},
	}
	for _, a := range refused {
		r := runAction(t, a[0], a[1], a[1])
		if r.status != http.StatusForbidden || r.code != response.ErrServiceProtected || r.detail["unit"] != a[1] {
			t.Errorf("%s %s: status=%d code=%q details=%v, want 403 %s naming the unit", a[0], a[1], r.status, r.code, r.detail, response.ErrServiceProtected)
		}
		if r.ran {
			t.Errorf("%s %s: systemctl ran", a[0], a[1])
		}
	}

	confirmed := [][2]string{
		{"stop", "pveproxy.service"}, {"restart", "pve-cluster.service"},
		{"disable", "pve-ha-lrm.service"}, {"stop", "ssh.service"},
	}
	for _, a := range confirmed {
		for _, typed := range []string{"", "pveproxy", "something.service"} {
			if typed == a[1] {
				continue
			}
			r := runAction(t, a[0], a[1], typed)
			if r.status != http.StatusConflict || r.code != response.ErrServiceConfirmRequired || r.detail["unit"] != a[1] {
				t.Errorf("%s %s confirm=%q: status=%d code=%q details=%v, want 409 %s naming the unit", a[0], a[1], typed, r.status, r.code, r.detail, response.ErrServiceConfirmRequired)
			}
			if r.ran {
				t.Errorf("%s %s confirm=%q: systemctl ran without the typed name", a[0], a[1], typed)
			}
		}
		if r := runAction(t, a[0], a[1], a[1]); r.status != http.StatusOK || !r.ran {
			t.Errorf("%s %s with the typed name: status=%d ran=%v, want 200 and run — body=%s", a[0], a[1], r.status, r.ran, r.body)
		}
	}

	// A restart of the firewall daemons only reloads the rules.
	if r := runAction(t, "restart", "pve-firewall.service", ""); r.status != http.StatusOK || !r.ran {
		t.Errorf("restart pve-firewall.service: status=%d ran=%v, want 200 and run", r.status, r.ran)
	}
}

// The Proxmox rules hold on Proxmox only: lxc and corosync exist elsewhere.
func TestProxmoxRulesStayOffOtherHosts(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	for _, unit := range []string{"corosync.service", "lxc.service", "watchdog-mux.service"} {
		if r := runAction(t, "stop", unit, ""); r.status != http.StatusOK || !r.ran {
			t.Errorf("stop %s off Proxmox: status=%d ran=%v, want 200 and run — body=%s", unit, r.status, r.ran, r.body)
		}
	}
}

func TestServiceListCarriesProtection(t *testing.T) {
	platform.SetForTest(t, platform.Info{PVE: true, NetworkStack: platform.StackIfupdown2})
	m := commonExec.NewMockCommander()
	m.SetOutput("exists:systemctl", "", nil)
	m.SetOutput("systemctl", "pve-cluster.service loaded active running The Proxmox VE cluster filesystem\n"+
		"ssh.service loaded active running OpenBSD Secure Shell server\n"+
		"nginx.service loaded active running A high performance web server\n", nil)
	h := &Handler{Cmd: m}
	rec := httptest.NewRecorder()
	if err := h.ListServices(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/services", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data struct {
			Services []ServiceInfo `json:"services"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v — %s", err, rec.Body.String())
	}
	want := map[string]map[string]string{
		"pve-cluster.service": {"stop": "refuse", "restart": "confirm", "disable": "refuse"},
		"ssh.service":         {"stop": "confirm", "disable": "confirm"},
		"nginx.service":       nil,
	}
	if len(got.Data.Services) != len(want) {
		t.Fatalf("services = %+v", got.Data.Services)
	}
	for _, s := range got.Data.Services {
		if !reflect.DeepEqual(s.Protection, want[s.Name]) {
			t.Errorf("%s protection = %v, want %v", s.Name, s.Protection, want[s.Name])
		}
	}
	// An ordinary unit carries no protection field at all.
	if strings.Count(rec.Body.String(), `"protection"`) != 2 {
		t.Errorf("protection fields = %d, want 2: %s", strings.Count(rec.Body.String(), `"protection"`), rec.Body.String())
	}
}
