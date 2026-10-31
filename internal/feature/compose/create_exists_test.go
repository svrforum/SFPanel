package compose

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// Creating a stack under a name that is taken is the operator's conflict, not
// the server failing: 409 STACK_ALREADY_EXISTS, as the Git import answers
// it, and the stack that was there is left as it was.
func TestCreatingAnExistingStackIs409(t *testing.T) {
	h, root := newComposeHandler(t)
	seedProject(t, h, "web")
	body, err := json.Marshal(map[string]any{"name": "web", "yaml": riskyCompose, "acknowledge_risks": true})
	require.NoError(t, err)

	rec := postJSON(t, h.CreateProject, http.MethodPost, "/api/v1/docker/compose", string(body), nil)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, response.ErrStackAlreadyExists, errCode(t, rec))
	kept, readErr := os.ReadFile(filepath.Join(root, "web", "docker-compose.yml"))
	require.NoError(t, readErr)
	require.Equal(t, benignCompose, string(kept), "the existing stack must not be rewritten")
}

// The Git import tells a taken name by the same sentinel, and answers it as
// it always did.
func TestImportingOverAnExistingStackIs409(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(riskyCompose)) })
	h, root := newComposeHandler(t)
	seedProject(t, h, "bar")

	rec := postJSON(t, h.ImportFromGit, http.MethodPost, "/api/v1/docker/compose/import",
		`{"url":"https://github.com/foo/bar","name":"bar","acknowledge_risks":true}`, nil)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, response.ErrStackAlreadyExists, errCode(t, rec))
	kept, readErr := os.ReadFile(filepath.Join(root, "bar", "docker-compose.yml"))
	require.NoError(t, readErr)
	require.Equal(t, benignCompose, string(kept), "the existing stack must not be rewritten")
}
