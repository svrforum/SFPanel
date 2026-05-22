package disk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

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
	if !strings.Contains(rec.Body.String(), "INVALID_MOUNTPOINT") {
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
}
