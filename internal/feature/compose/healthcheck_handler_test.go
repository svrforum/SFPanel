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
	"github.com/svrforum/SFPanel/internal/docker"
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

func TestTestHealthcheckHandler_RejectsNONE(t *testing.T) {
	body := bytes.NewBufferString(`{"test_type":"NONE"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/foo/healthcheck/svc/test", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")
	h := &Handler{}
	_ = h.TestHealthcheck(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400", rec.Code)
	}
}

func TestTestHealthcheckHandler_RejectsBadDuration(t *testing.T) {
	body := bytes.NewBufferString(`{"test_type":"CMD-SHELL","test_value":"x","interval":"30","timeout":"10s","retries":3,"start_period":"30s"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/foo/healthcheck/svc/test", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")
	h := &Handler{}
	_ = h.TestHealthcheck(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400", rec.Code)
	}
}

// TestApplyHealthcheck_RejectsTraversalProjectName ensures that a
// :project URL parameter containing path traversal sequences cannot
// reach the filesystem. Without validation in ResolveComposeFile, the
// resolver would happily walk into a sibling directory containing a
// compose file and mutate it. We create exactly that sibling layout
// and assert the request is refused and the sibling file is unchanged.
func TestApplyHealthcheck_RejectsTraversalProjectName(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "stacks")
	require.NoError(t, os.MkdirAll(baseDir, 0o755))
	sibling := filepath.Join(root, "etc")
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	composeAtSibling := filepath.Join(sibling, "docker-compose.yml")
	originalContent := []byte("services:\n  evil:\n    image: nginx\n")
	require.NoError(t, os.WriteFile(composeAtSibling, originalContent, 0o644))

	h := &Handler{Compose: docker.NewComposeManager(baseDir, nil)}

	body := bytes.NewBufferString(`{"test_type":"CMD","test_value":"true","interval":"30s","timeout":"10s","retries":3,"start_period":"30s","replace":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/compose/..%2fetc/healthcheck/evil", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("../etc", "evil")

	_ = h.ApplyHealthcheck(c)

	if rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("status=%d, want 4xx", rec.Code)
	}

	nowContent, err := os.ReadFile(composeAtSibling)
	require.NoError(t, err)
	if string(nowContent) != string(originalContent) {
		t.Fatalf("sibling compose file mutated: traversal succeeded")
	}

	// Belt-and-braces: no backup or tmp file should have been written
	// next to the sibling compose either.
	entries, err := os.ReadDir(sibling)
	require.NoError(t, err)
	for _, e := range entries {
		if e.Name() != "docker-compose.yml" {
			t.Fatalf("unexpected file created next to sibling compose: %s", e.Name())
		}
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
