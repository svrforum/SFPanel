package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// endpointAnswer runs action's endpoint on unit, with ?confirm=<unit> when
// confirmed, and decodes what it answered.
func endpointAnswer(t *testing.T, action, unit string, confirmed bool) (status int, code, message string, details map[string]string, ran bool) {
	t.Helper()
	m := commonExec.NewMockCommander()
	h := &Handler{Cmd: m}
	target := "/system/services/x/" + action
	if confirmed {
		target += "?confirm=" + unit
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, target, nil), rec)
	c.SetParamNames("name")
	c.SetParamValues(unit)
	handlers := map[string]echo.HandlerFunc{
		"start": h.StartService, "stop": h.StopService, "restart": h.RestartService,
		"enable": h.EnableService, "disable": h.DisableService,
	}
	if err := handlers[action](c); err != nil {
		t.Fatalf("%s %s: %v", action, unit, err)
	}
	var body struct {
		Error *struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: %v — %s", action, unit, err, rec.Body.String())
	}
	if body.Error != nil {
		code, message, details = body.Error.Code, body.Error.Message, body.Error.Details
	}
	for _, call := range m.Calls {
		if call.Name == "systemctl" && len(call.Args) > 0 && call.Args[0] == action {
			ran = true
		}
	}
	return rec.Code, code, message, details, ran
}

// A task job's service step and the Services page refuse the same units for
// the same reasons, in the same words: both go through UnitGuard.
func TestUnitGuardRefusesForTheSameReasonsAsTheEndpoint(t *testing.T) {
	pve := platform.Info{PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2}
	netplan := platform.Info{NetworkStack: platform.StackNetplan}
	for _, tc := range []struct {
		label     string
		info      platform.Info
		unit      string
		action    string
		confirmed bool
		status    int // 0: allowed
		code      string
		details   map[string]string
	}{
		{"a name without .service", netplan, "nginx", "restart", true, http.StatusBadRequest, response.ErrInvalidName, nil},
		{"a name shaped like a flag", netplan, "-nginx.service", "start", false, http.StatusBadRequest, response.ErrInvalidName, nil},
		{"the panel's own unit, stopped", netplan, "sfpanel.service", "stop", true, http.StatusForbidden, response.ErrPermissionDenied, nil},
		{"the panel's journal, restarted", pve, "systemd-journald.service", "restart", true, http.StatusForbidden, response.ErrPermissionDenied, nil},
		{"dbus, disabled", netplan, "dbus.service", "disable", true, http.StatusForbidden, response.ErrPermissionDenied, nil},
		{"the Proxmox cluster filesystem, stopped", pve, "pve-cluster.service", "stop", true, http.StatusForbidden, response.ErrServiceProtected, map[string]string{"unit": "pve-cluster.service", "hint": ""}},
		{"ifupdown's networking, restarted", pve, "networking.service", "restart", true, http.StatusForbidden, response.ErrServiceProtected, map[string]string{"unit": "networking.service", "hint": "ifreload -a"}},
		{"pveproxy stopped without the typed name", pve, "pveproxy.service", "stop", false, http.StatusConflict, response.ErrServiceConfirmRequired, map[string]string{"unit": "pveproxy.service"}},
		{"ssh disabled without the typed name", netplan, "ssh.service", "disable", false, http.StatusConflict, response.ErrServiceConfirmRequired, map[string]string{"unit": "ssh.service"}},
		{"pveproxy stopped with the typed name", pve, "pveproxy.service", "stop", true, 0, "", nil},
		{"the Proxmox cluster filesystem, started", pve, "pve-cluster.service", "start", false, 0, "", nil},
		{"the panel's own unit, enabled", netplan, "sfpanel.service", "enable", false, 0, "", nil},
		{"an ordinary unit, stopped", netplan, "nginx.service", "stop", false, 0, "", nil},
		{"corosync away from Proxmox", netplan, "corosync.service", "disable", false, 0, "", nil},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := UnitGuard(tc.info, tc.unit, tc.action, tc.confirmed)
			platform.SetForTest(t, tc.info)
			status, code, message, details, ran := endpointAnswer(t, tc.action, tc.unit, tc.confirmed)

			if tc.status == 0 {
				if r != nil {
					t.Errorf("UnitGuard refused: %+v", r)
				}
				if status != http.StatusOK || !ran {
					t.Errorf("endpoint: %d ran=%v, want 200 and systemctl run", status, ran)
				}
				return
			}
			if r == nil {
				t.Fatalf("UnitGuard let it through, want %d %s", tc.status, tc.code)
			}
			if r.Status != tc.status || r.Code != tc.code || !reflect.DeepEqual(r.Details, tc.details) {
				t.Errorf("UnitGuard = %d %s %v, want %d %s %v", r.Status, r.Code, r.Details, tc.status, tc.code, tc.details)
			}
			if status != r.Status || code != r.Code || message != r.Message || !reflect.DeepEqual(details, r.Details) {
				t.Errorf("endpoint = %d %s %q %v\nUnitGuard = %d %s %q %v", status, code, message, details, r.Status, r.Code, r.Message, r.Details)
			}
			if ran {
				t.Errorf("endpoint ran systemctl %s after refusing", tc.action)
			}
		})
	}
}

// An action UnitGuard does not know is not let through unguarded.
func TestUnitGuardRefusesAnUnknownAction(t *testing.T) {
	r := UnitGuard(platform.Info{NetworkStack: platform.StackNetplan}, "nginx.service", "reload", true)
	if r == nil || r.Status != http.StatusBadRequest || r.Code != response.ErrInvalidRequest {
		t.Errorf("UnitGuard(reload) = %+v, want 400 %s", r, response.ErrInvalidRequest)
	}
}
