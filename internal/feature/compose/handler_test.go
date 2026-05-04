package compose

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
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
