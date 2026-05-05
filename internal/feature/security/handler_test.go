package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// TestPutPolicy_RejectsBadMode — invalid mode returns 400 INVALID_POLICY
// before any FSM apply is attempted.
func TestPutPolicy_RejectsBadMode(t *testing.T) {
	body := bytes.NewBufferString(`{"mode": "kapow", "rules": []}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/policy", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	h := &Handler{}
	_ = h.PutPolicy(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

// TestPutPolicy_RejectsRuleWithoutPattern — empty pattern rejected by
// Policy.Validate before reaching the FSM.
func TestPutPolicy_RejectsRuleWithoutPattern(t *testing.T) {
	body := bytes.NewBufferString(`{"mode":"warn","rules":[{"pattern":"","identity":{"subject_prefix":"x","issuer":"y"}}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/policy", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	h := &Handler{}
	_ = h.PutPolicy(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestVerifyImage_RejectsMissingRef — empty ref returns 400.
func TestVerifyImage_RejectsMissingRef(t *testing.T) {
	body := bytes.NewBufferString(`{"ref": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/security/verify-image", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	h := &Handler{}
	_ = h.VerifyImage(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
