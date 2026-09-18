package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/docker"
)

// TestDiffStack_EmptyYAML_Returns400 covers the early-return validation
// path for the diff endpoint: an empty proposed YAML should be rejected
// before the handler ever touches the Compose manager, so we can assert
// the contract without spinning up docker or disk state.
func TestDiffStack_EmptyYAML_Returns400(t *testing.T) {
	body := bytes.NewBufferString(`{"yaml": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/myproj/diff", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("myproj")

	h := &Handler{}
	_ = h.DiffStack(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

// TestImportFromGit_RejectsBadURL covers the validation gate on the
// import endpoint: a non-github HTTPS URL must be rejected with 400
// before the handler attempts any network I/O.
func TestImportFromGit_RejectsBadURL(t *testing.T) {
	body := bytes.NewBufferString(`{"url":"http://example.com/foo.git","name":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/compose/import", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	h := &Handler{}
	_ = h.ImportFromGit(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// riskyCompose binds the docker socket — the pattern that made the App Store's
// own catalog unopenable in the editor. It is acknowledgeable, not forbidden.
const riskyCompose = "services:\n  dozzle:\n    image: amir20/dozzle\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock:ro\n"

// forbiddenCompose binds the panel's own config directory, which holds the JWT
// signing secret and the cluster CA key. No acknowledgement lifts it.
const forbiddenCompose = "services:\n  thief:\n    image: alpine\n    volumes:\n      - /etc/sfpanel:/loot\n"

// benignCompose trips nothing.
const benignCompose = "services:\n  web:\n    image: nginx:alpine\n"

// newComposeHandler returns a handler whose stacks root is a fresh temp dir, so
// a request that gets past validation really writes and can be asserted on.
func newComposeHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	return &Handler{Compose: docker.NewComposeManager(root, nil), ComposePath: root}, root
}

// postJSON runs one handler over a JSON body and returns the recorder.
func postJSON(t *testing.T, fn func(echo.Context) error, method, target, body string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	for k, v := range params {
		c.SetParamNames(k)
		c.SetParamValues(v)
	}
	require.NoError(t, fn(c))
	return rec
}

// errCode pulls error.code out of a response body, failing if it is absent.
func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.False(t, resp.Success)
	return resp.Error.Code
}

// TestCreateProject_RiskyWithoutAcknowledgement asserts the reason, not just the
// refusal: a docker.sock bind is COMPOSE_RISKY, and nothing is written.
func TestCreateProject_RiskyWithoutAcknowledgement(t *testing.T) {
	h, root := newComposeHandler(t)
	body, err := json.Marshal(map[string]any{"name": "dozzle", "yaml": riskyCompose})
	require.NoError(t, err)

	rec := postJSON(t, h.CreateProject, http.MethodPost, "/api/v1/docker/compose", string(body), nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, response.ErrComposeRisky, errCode(t, rec))
	_, statErr := os.Stat(filepath.Join(root, "dozzle"))
	require.True(t, os.IsNotExist(statErr), "refused create must not write the project")
}

// TestCreateProject_RiskyAcknowledged proves the flag is what lifts it: the same
// request with acknowledge_risks succeeds and the YAML lands on disk.
func TestCreateProject_RiskyAcknowledged(t *testing.T) {
	h, root := newComposeHandler(t)
	body, err := json.Marshal(map[string]any{"name": "dozzle", "yaml": riskyCompose, "acknowledge_risks": true})
	require.NoError(t, err)

	rec := postJSON(t, h.CreateProject, http.MethodPost, "/api/v1/docker/compose", string(body), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	written, readErr := os.ReadFile(filepath.Join(root, "dozzle", "docker-compose.yml"))
	require.NoError(t, readErr)
	require.Equal(t, riskyCompose, string(written))
}

// TestCreateProject_ForbiddenEvenWhenAcknowledged asserts the boundary the flag
// must not reach: a /etc/sfpanel bind is COMPOSE_FORBIDDEN with the flag set.
func TestCreateProject_ForbiddenEvenWhenAcknowledged(t *testing.T) {
	h, root := newComposeHandler(t)
	body, err := json.Marshal(map[string]any{"name": "thief", "yaml": forbiddenCompose, "acknowledge_risks": true})
	require.NoError(t, err)

	rec := postJSON(t, h.CreateProject, http.MethodPost, "/api/v1/docker/compose", string(body), nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, response.ErrComposeForbidden, errCode(t, rec))
	_, statErr := os.Stat(filepath.Join(root, "thief"))
	require.True(t, os.IsNotExist(statErr), "forbidden create must not write the project")
}

// seedProject creates a benign stack so UpdateProject has something to update.
func seedProject(t *testing.T, h *Handler, name string) {
	t.Helper()
	_, err := h.Compose.CreateProject(context.Background(), name, benignCompose)
	require.NoError(t, err)
}

// TestUpdateProject_RiskyWithoutAcknowledgement asserts the reason and that the
// deployed YAML is untouched.
func TestUpdateProject_RiskyWithoutAcknowledgement(t *testing.T) {
	h, root := newComposeHandler(t)
	seedProject(t, h, "dozzle")
	body, err := json.Marshal(map[string]any{"yaml": riskyCompose})
	require.NoError(t, err)

	rec := postJSON(t, h.UpdateProject, http.MethodPut, "/api/v1/docker/compose/dozzle", string(body),
		map[string]string{"project": "dozzle"})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, response.ErrComposeRisky, errCode(t, rec))
	kept, readErr := os.ReadFile(filepath.Join(root, "dozzle", "docker-compose.yml"))
	require.NoError(t, readErr)
	require.Equal(t, benignCompose, string(kept), "refused update must not rewrite the stack")
}

// TestUpdateProject_RiskyAcknowledged proves the flag lifts the risky tier.
func TestUpdateProject_RiskyAcknowledged(t *testing.T) {
	h, root := newComposeHandler(t)
	seedProject(t, h, "dozzle")
	body, err := json.Marshal(map[string]any{"yaml": riskyCompose, "acknowledge_risks": true})
	require.NoError(t, err)

	rec := postJSON(t, h.UpdateProject, http.MethodPut, "/api/v1/docker/compose/dozzle", string(body),
		map[string]string{"project": "dozzle"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	written, readErr := os.ReadFile(filepath.Join(root, "dozzle", "docker-compose.yml"))
	require.NoError(t, readErr)
	require.Equal(t, riskyCompose, string(written))
}

// TestUpdateProject_ForbiddenEvenWhenAcknowledged asserts the boundary holds on
// the update path too.
func TestUpdateProject_ForbiddenEvenWhenAcknowledged(t *testing.T) {
	h, root := newComposeHandler(t)
	seedProject(t, h, "dozzle")
	body, err := json.Marshal(map[string]any{"yaml": forbiddenCompose, "acknowledge_risks": true})
	require.NoError(t, err)

	rec := postJSON(t, h.UpdateProject, http.MethodPut, "/api/v1/docker/compose/dozzle", string(body),
		map[string]string{"project": "dozzle"})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, response.ErrComposeForbidden, errCode(t, rec))
	kept, readErr := os.ReadFile(filepath.Join(root, "dozzle", "docker-compose.yml"))
	require.NoError(t, readErr)
	require.Equal(t, benignCompose, string(kept), "forbidden update must not rewrite the stack")
}
