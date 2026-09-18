package featuredocker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
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

// decodeFail reads the error code off a response.Fail body. The code is the
// contract the frontend branches on (COMPOSE_RISKY raises the confirm dialog,
// COMPOSE_FORBIDDEN does not), so the tests below assert it rather than the
// 400 they share with every other validator in this handler.
func decodeFail(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code, body.Error.Message
}

// The forbidden tier has to hold on this route the way it holds on the compose
// endpoints: POST /docker/containers hands spec.Volumes to the daemon as
// HostConfig.Binds, so a bind of the panel's own secrets would otherwise be a
// sibling route around the boundary, reached with the same session and no
// acknowledgement.
//
// Nil Docker client: a spec that got past the guard would panic in
// h.Docker.CreateContainer, which is what removing the guard produces here.
func TestCreateContainer_RefusesThePanelsOwnPaths(t *testing.T) {
	h := &Handler{}
	e := echo.New()

	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/docker/containers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		if err := h.CreateContainer(e.NewContext(req, rec)); err != nil {
			t.Fatalf("CreateContainer err: %v", err)
		}
		return rec
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"panel config dir", `{"image":"alpine","volumes":["/etc/sfpanel:/loot"]}`},
		{"panel config dir, acknowledged", `{"image":"alpine","volumes":["/etc/sfpanel:/loot"],"acknowledge_risks":true}`},
		{"panel database", `{"image":"alpine","volumes":["/var/lib/sfpanel/sfpanel.db:/loot/db"],"acknowledge_risks":true}`},
		{"host ssh keys", `{"image":"alpine","volumes":["/root/.ssh:/loot"],"acknowledge_risks":true}`},
		{"sudoers drop-in", `{"image":"alpine","volumes":["/etc/sudoers.d:/loot"],"acknowledge_risks":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 — body=%s", rec.Code, rec.Body.String())
			}
			code, message := decodeFail(t, rec)
			if code != response.ErrComposeForbidden {
				t.Errorf("code = %q, want %q — %s", code, response.ErrComposeForbidden, rec.Body.String())
			}
			if !strings.Contains(message, "binds sensitive host path") {
				t.Errorf("message %q does not name what was refused", message)
			}
		})
	}
}

// The risky tier is the operator's to lift, and this route lifts it with the
// same flag and the same dialog the compose endpoints use: refused without
// acknowledge_risks, and past the guard with it (the nil client panics there,
// which is how the pass is observed without a daemon).
func TestCreateContainer_RiskyBindNeedsTheAcknowledgement(t *testing.T) {
	h := &Handler{}
	e := echo.New()
	body := `{"image":"dozzle","volumes":["/var/run/docker.sock:/var/run/docker.sock:ro"]}`

	req := httptest.NewRequest(http.MethodPost, "/docker/containers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := h.CreateContainer(e.NewContext(req, rec)); err != nil {
		t.Fatalf("CreateContainer err: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — body=%s", rec.Code, rec.Body.String())
	}
	code, message := decodeFail(t, rec)
	if code != response.ErrComposeRisky {
		t.Errorf("code = %q, want %q — %s", code, response.ErrComposeRisky, rec.Body.String())
	}
	if !strings.Contains(message, "docker.sock") {
		t.Errorf("message %q does not name the mount", message)
	}

	// Acknowledged, the same spec reaches the daemon call. With a nil client
	// that panics — recovered here, because the panic *is* the evidence the
	// guard let it through.
	acked := `{"image":"dozzle","volumes":["/var/run/docker.sock:/var/run/docker.sock:ro"],"acknowledge_risks":true}`
	req = httptest.NewRequest(http.MethodPost, "/docker/containers", strings.NewReader(acked))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	reached := func() (reached bool) {
		defer func() {
			if recover() != nil {
				reached = true
			}
		}()
		_ = h.CreateContainer(e.NewContext(req, rec))
		return false
	}()
	if !reached {
		code, _ := decodeFail(t, rec)
		t.Errorf("an acknowledged docker.sock bind was still refused with %q — %s", code, rec.Body.String())
	}
}

// An ordinary container — no bind at all, and a bind of the operator's own data
// directory — must not be refused by the new gate. Same nil-client observation
// as above: reaching the daemon call is the pass.
func TestCreateContainer_OrdinarySpecsAreNotRefused(t *testing.T) {
	h := &Handler{}
	e := echo.New()

	for _, tc := range []struct {
		name string
		body string
	}{
		{"no volumes", `{"image":"nginx:latest","name":"web"}`},
		{"named volume", `{"image":"nginx:latest","volumes":["appdata:/usr/share/nginx/html"]}`},
		{"ordinary data dir", `{"image":"nginx:latest","volumes":["/srv/appdata:/usr/share/nginx/html:ro"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/docker/containers", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			reached := func() (reached bool) {
				defer func() {
					if recover() != nil {
						reached = true
					}
				}()
				_ = h.CreateContainer(e.NewContext(req, rec))
				return false
			}()
			if !reached {
				code, message := decodeFail(t, rec)
				t.Errorf("an ordinary spec was refused with %q: %s", code, message)
			}
		})
	}
}
