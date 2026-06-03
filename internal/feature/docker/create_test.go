package featuredocker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestCreateContainer_Validation locks the input guards that run before any
// Docker call, so they can be exercised with a nil Docker client: a missing or
// malformed image, a bad container name, an unknown restart policy, and an
// out-of-range host port all yield 400 without touching the daemon.
func TestCreateContainer_Validation(t *testing.T) {
	h := &Handler{} // nil Docker — valid input would panic, so only 400 paths are safe here
	e := echo.New()

	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/docker/containers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if err := h.CreateContainer(c); err != nil {
			t.Fatalf("CreateContainer err: %v", err)
		}
		return rec
	}

	cases := []struct {
		name string
		body string
	}{
		{"missing image", `{"name":"x"}`},
		{"image with shell metachars", `{"image":"nginx; rm -rf /"}`},
		{"bad container name", `{"image":"nginx","name":"bad name!"}`},
		{"unknown restart policy", `{"image":"nginx","restart_policy":"sometimes"}`},
		{"host port out of range", `{"image":"nginx","ports":[{"host_port":"99999","container_port":"80"}]}`},
		{"non-numeric host port", `{"image":"nginx","ports":[{"host_port":"abc","container_port":"80"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := call(tc.body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 — body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	// A valid restart policy + valid name should pass validation and reach the
	// Docker call. With a nil client that panics, so we only assert the
	// validators accept the shapes above by their 400s; the happy path is
	// covered by integration against a real daemon.
}
