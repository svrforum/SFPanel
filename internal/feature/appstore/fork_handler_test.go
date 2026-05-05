package appstore

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestCreateFork_RejectsMissingStackName(t *testing.T) {
	body := bytes.NewBufferString(`{"name": "x"}`) // stack_name missing
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/forks", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	h := &Handler{}
	_ = h.CreateFork(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

func TestCreateFork_RejectsBadName(t *testing.T) {
	body := bytes.NewBufferString(`{"stack_name": "s", "name": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/forks", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)

	h := &Handler{}
	_ = h.CreateFork(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
