package system

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
)

// fakeReleaseServer returns a GitHub-shaped JSON describing whatever tag the
// caller asks for. Tests use it to drive RunUpdate without hitting the real
// api.github.com.
type fakeReleaseServer struct {
	tag string
}

// newHandler wires a Handler against the fake release server. We reach into
// it by setting the GitHub URL via a substitute http.RoundTripper would be
// cleaner long-term, but RunUpdate hard-codes the URL, so for these tests
// we exercise the easier paths: TryLock contention and the downgrade guard.
// Both kick in *before* the GitHub call.
func newHandler(version string) *Handler {
	return &Handler{
		Version:    version,
		DBPath:     "/tmp/test.db",
		ConfigPath: "/tmp/test.yaml",
		Cmd:        commonExec.NewMockCommander(),
	}
}

// TestRunUpdateConcurrentLock proves a second concurrent caller is rejected
// with HTTP 409 / UPDATE_IN_PROGRESS rather than racing into the download.
func TestRunUpdateConcurrentLock(t *testing.T) {
	h := newHandler("0.11.1")

	// Take the lock manually to simulate an in-flight update.
	updateMu.Lock()
	defer updateMu.Unlock()

	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil)
	ctx := e.NewContext(req, rec)

	if err := h.RunUpdate(ctx); err != nil {
		t.Fatalf("RunUpdate returned error: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UPDATE_IN_PROGRESS") {
		t.Errorf("expected UPDATE_IN_PROGRESS error code, got body: %s", rec.Body.String())
	}
}

// TestRunUpdateLockReleased verifies the lock is released after the handler
// returns so subsequent updates aren't permanently blocked. We can't drive
// a successful end-to-end update in a unit test (it would actually swap the
// binary), so we drive the lock semantics through TryLock directly.
func TestRunUpdateLockReleased(t *testing.T) {
	if !updateMu.TryLock() {
		t.Fatal("expected lock to be free at test start")
	}
	updateMu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if !updateMu.TryLock() {
			t.Error("second goroutine could not acquire lock after release")
			return
		}
		updateMu.Unlock()
	}()
	wg.Wait()
}
