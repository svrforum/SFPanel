package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
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
