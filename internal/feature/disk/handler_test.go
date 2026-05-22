package disk

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// cancelObservingCommander is a Commander whose RunCtx delegates to a
// caller-supplied closure so the test can observe ctx propagation. The
// non-ctx Run/RunWithTimeout variants intentionally return an error so a
// regression that drops back to a non-cancellable call surfaces as a
// failed assertion rather than a hung handler.
type cancelObservingCommander struct {
	mu         sync.Mutex
	runCtxImpl func(ctx context.Context, name string, args ...string) (string, error)
	runCtxHits int
	runHits    int
}

func (m *cancelObservingCommander) Run(name string, args ...string) (string, error) {
	m.mu.Lock()
	m.runHits++
	m.mu.Unlock()
	return "", fmt.Errorf("handler must use RunCtx, not Run (cmd=%s)", name)
}
func (m *cancelObservingCommander) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	m.mu.Lock()
	m.runHits++
	m.mu.Unlock()
	return "", fmt.Errorf("handler must use RunCtx, not RunWithTimeout (cmd=%s)", name)
}
func (m *cancelObservingCommander) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return "", nil
}
func (m *cancelObservingCommander) RunWithInput(_ string, name string, args ...string) (string, error) {
	return "", nil
}
func (m *cancelObservingCommander) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	m.mu.Lock()
	m.runCtxHits++
	m.mu.Unlock()
	if m.runCtxImpl != nil {
		return m.runCtxImpl(ctx, name, args...)
	}
	return "", nil
}
func (m *cancelObservingCommander) Exists(name string) bool { return true }

// TestGetSmartInfo_HonorsRequestContext is the regression test for Task
// 3.1.B: when a client disconnects mid-request the handler must propagate
// that cancellation to smartctl rather than leaking the subprocess for
// the full 5-minute default timeout. Pre-migration GetSmartInfo called
// h.Cmd.Run which derives its own context.Background — the mock's Run
// returns an error immediately, so the assertion below ("ctx was used")
// fails until the handler is migrated to RunCtx.
func TestGetSmartInfo_HonorsRequestContext(t *testing.T) {
	mock := &cancelObservingCommander{
		runCtxImpl: func(ctx context.Context, name string, args ...string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	h := &Handler{Cmd: mock}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/disk/smart/sda", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("device")
	c.SetParamValues("sda")

	done := make(chan struct{})
	go func() {
		_ = h.GetSmartInfo(c)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// OK — handler returned promptly after the request context was
		// cancelled.
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after request context cancelled")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.runHits != 0 {
		t.Errorf("handler bypassed RunCtx (Run/RunWithTimeout hits=%d) — request context cannot be propagated through those entry points", mock.runHits)
	}
	if mock.runCtxHits == 0 {
		t.Errorf("handler never called RunCtx — cancellation propagation cannot be verified")
	}
}

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

// TestListDisks_GracefulWhenLsblkMissing is the regression test for Task
// 3.3: on minimal containers (and any remote node reached via ?node= that
// lacks lsblk), ListDisks must return an empty array rather than a 500.
// The MockCommander's Exists returns true only for binaries pre-seeded
// with SetOutput("exists:<name>", …); not seeding "exists:lsblk" therefore
// simulates a missing binary. Pre-task the handler always called RunCtx,
// which on the mock returns "" — JSON unmarshal of "" then fails and the
// handler emits ErrDiskError 500.
func TestListDisks_GracefulWhenLsblkMissing(t *testing.T) {
	// Reset the package-level cache so a previously-cached fixture from
	// another test cannot leak in and mask the missing-binary path.
	diskCache.Lock()
	diskCache.devices = nil
	diskCache.iostats = nil
	diskCache.updatedAt = time.Time{}
	diskCache.Unlock()

	h := &Handler{Cmd: exec.NewMockCommander()}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/disk/disks", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.ListDisks(c); err != nil {
		t.Fatalf("ListDisks returned err=%v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("expected empty array data, got %s", rec.Body.String())
	}
}

// TestListFilesystems_GracefulWhenDfMissing mirrors the ListDisks case but
// for df. The mock is primed with a df-failed error to simulate what the
// real SystemCommander returns when exec.LookPath fails for a missing
// binary; without the Exists guard the handler would surface that as a
// 500 FS_ERROR. Note: the empty-output happy path also parses to []
// because parseDfOutput returns an empty slice for short input — the
// SetOutput("df", …, err) below is what distinguishes the "missing-binary"
// regression from that incidental empty-success case.
func TestListFilesystems_GracefulWhenDfMissing(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("df", "", fmt.Errorf("exec: \"df\": executable file not found in $PATH"))
	h := &Handler{Cmd: mock}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/disk/filesystems", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.ListFilesystems(c); err != nil {
		t.Fatalf("ListFilesystems returned err=%v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("expected empty array data, got %s", rec.Body.String())
	}
}

// TestMountFilesystem_SanitizesStderrInResponse is the regression test for
// Task 3.2: when mount(8) fails with ANSI-coloured stderr, the message
// surfaced in response.Fail must be routed through response.SanitizeOutput
// so the JSON body delivered to the operator never contains raw terminal
// control bytes. Pre-task disk handlers passed strings.TrimSpace(out)
// directly into fmt.Sprintf — leaving any \x1b[…m sequences intact.
func TestMountFilesystem_SanitizesStderrInResponse(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("mount", "\x1b[31mmount: /mnt/data: special device /dev/sdb1 does not exist.\x1b[0m\n", fmt.Errorf("mount failed"))

	h := &Handler{Cmd: mock}
	body := `{"device":"sdb1","mount_point":"/mnt/data"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/disk/mount", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	_ = h.MountFilesystem(c)

	if strings.Contains(rec.Body.String(), "\x1b[") {
		t.Errorf("response leaks ANSI sequences: %q", rec.Body.String())
	}
}
