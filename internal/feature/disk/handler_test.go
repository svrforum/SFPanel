package disk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// TestMountFilesystem_RefusesProtectedPath verifies that an authenticated
// operator cannot point a mount over a system path such as /etc. Before the
// mountguard was wired in, the handler would call os/MkdirAll on the target
// and shell out to mount(8), relying on the kernel to refuse — which it
// does not for paths like /etc.
func TestMountFilesystem_RefusesProtectedPath(t *testing.T) {
	h := &Handler{Cmd: exec.NewMockCommander()}
	body := `{"device":"sdb1","mount_point":"/etc"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/disk/mount", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	_ = h.MountFilesystem(c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), response.ErrInvalidMountpoint) {
		t.Errorf("response did not signal invalid mountpoint: %s", rec.Body.String())
	}
}

// TestUnmountFilesystem_RefusesProtectedPath verifies that unmounting "/"
// (or any other deny-listed path) is rejected at the handler boundary
// rather than handed to umount(8).
func TestUnmountFilesystem_RefusesProtectedPath(t *testing.T) {
	h := &Handler{Cmd: exec.NewMockCommander()}
	body := `{"mount_point":"/"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/disk/umount", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	_ = h.UnmountFilesystem(c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), response.ErrInvalidMountpoint) {
		t.Errorf("response did not signal invalid mountpoint: %s", rec.Body.String())
	}
}

// TestFormatPartition_RefusesDeviceMountedAtProtectedPath exercises the
// FormatPartition guard composition (findDeviceMountpoint + isProtectedMountpoint).
// The mount lookup is overridden so the test does not depend on /proc/mounts.
func TestFormatPartition_RefusesDeviceMountedAtProtectedPath(t *testing.T) {
	orig := findDeviceMountpoint
	findDeviceMountpoint = func(devPath string) (string, error) {
		if devPath == "/dev/sdb1" {
			return "/etc", nil
		}
		return "", nil
	}
	t.Cleanup(func() { findDeviceMountpoint = orig })

	h := &Handler{Cmd: exec.NewMockCommander()}
	body := `{"device":"sdb1","fs_type":"ext4","label":"data"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/disk/format", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	_ = h.FormatPartition(c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), response.ErrInvalidDevice) {
		t.Errorf("response did not signal invalid device: %s", rec.Body.String())
	}
	// Asserting on the guard-specific message distinguishes this branch from
	// the os.Stat "device does not exist" failure that also returns
	// ErrInvalidDevice on hosts without /dev/sdb1.
	if !strings.Contains(rec.Body.String(), "protected system path") {
		t.Errorf("response was not the protected-path rejection: %s", rec.Body.String())
	}
}
