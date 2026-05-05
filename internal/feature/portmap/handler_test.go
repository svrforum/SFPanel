package portmap

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// TestGetPortMap_GracefulDegradation: when ss + ufw are missing AND Docker
// is nil, page still renders 200 OK with empty rows.
func TestGetPortMap_GracefulDegradation(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("ss", "", errors.New("ss not found"))
	mock.SetOutput("ufw", "", errors.New("ufw not installed"))

	h := &Handler{Cmd: mock, Docker: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/portmap", nil)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)

	require.NoError(t, h.GetPortMap(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Success bool         `json:"success"`
		Data    []PortMapRow `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Empty(t, resp.Data) // all sources failed → empty rows
}
