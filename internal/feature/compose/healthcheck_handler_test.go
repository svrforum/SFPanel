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

// TestApplyHealthcheckHandler_RejectsBadDuration — validation runs
// before any disk I/O, so we can drive it with a bare Handler{}.
func TestApplyHealthcheckHandler_RejectsBadDuration(t *testing.T) {
	body := bytes.NewBufferString(`{"test_type":"CMD-SHELL","test_value":"x","interval":"30","timeout":"10s","retries":3,"start_period":"30s","replace":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/compose/foo/healthcheck/jellyfin", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "jellyfin")

	h := &Handler{}
	_ = h.ApplyHealthcheck(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

func TestApplyHealthcheckHandler_RejectsMissingTestType(t *testing.T) {
	body := bytes.NewBufferString(`{"test_value":"x","interval":"30s","timeout":"10s","retries":3,"start_period":"30s"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/compose/foo/healthcheck/svc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")

	h := &Handler{}
	_ = h.ApplyHealthcheck(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
