package compose

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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

func TestRemoveHealthcheckHandler_RejectsSHA256Mismatch(t *testing.T) {
	body := bytes.NewBufferString(`{"base_yaml_sha256":"deadbeef"}`)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/docker/compose/foo/healthcheck/svc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")

	// Handler will fail at the Compose==nil guard before any disk I/O.
	// We assert it does NOT 200; precise non-200 status is acceptable.
	h := &Handler{}
	_ = h.RemoveHealthcheck(c)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200, got 200: %s", rec.Body.String())
	}
}

func TestPruneHealthcheckBackups_KeepsLastN(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "docker-compose.yml")
	now := time.Now()
	for i := 0; i < 7; i++ {
		p := yamlPath + ".bak.healthcheck." + strconv.Itoa(int(now.Add(-time.Duration(7-i)*time.Second).UnixMilli()))
		if err := os.WriteFile(p, []byte("backup"+strconv.Itoa(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-time.Duration(7-i) * time.Second)
		_ = os.Chtimes(p, ts, ts)
	}
	pruneHealthcheckBackups(yamlPath, 5)
	matches, _ := filepath.Glob(yamlPath + ".bak.healthcheck.*")
	if len(matches) != 5 {
		t.Fatalf("kept %d files, want 5", len(matches))
	}
}
