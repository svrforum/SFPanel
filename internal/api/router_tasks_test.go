package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
)

// The task and task-run routes that take a body refuse one over 1 MiB before
// their handler reads it: a run's inputs are kept in every manager's state.
func TestTaskRoutesLimitTheirBody(t *testing.T) {
	router := buildRouter(t, "")
	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path string, size int) *httptest.ResponseRecorder {
		body := `{"yaml":"` + strings.Repeat("x", size) + `"}`
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		return serve(router, "127.0.0.1:5000", r)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/cluster/tasks"},
		{http.MethodPost, "/api/v1/cluster/tasks/validate"},
		{http.MethodPost, "/api/v1/cluster/tasks/format"},
		{http.MethodPost, "/api/v1/cluster/tasks/import/preview"},
		{http.MethodPost, "/api/v1/cluster/tasks/import"},
		{http.MethodPut, "/api/v1/cluster/tasks/t1"},
		{http.MethodPost, "/api/v1/cluster/task-runs/plan"},
		{http.MethodPost, "/api/v1/cluster/task-runs"},
		{http.MethodPost, "/api/v1/cluster/task-runs/r1/stop"},
		{http.MethodPost, "/api/v1/cluster/task-runs/r1/rerun"},
	} {
		if rec := send(route.method, route.path, 1<<20); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s with a body over 1 MiB = %d %s, want 413", route.method, route.path, rec.Code, rec.Body)
		}
		if rec := send(route.method, route.path, 1<<10); rec.Code == http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s with a 1 KiB body = 413, want its handler's answer", route.method, route.path)
		}
	}
}

// The agent install routes refuse a body over 1 MiB before their handler
// reads it: a start's body carries the secrets, and is read whole.
func TestAgentInstallRoutesHaveABodyLimit(t *testing.T) {
	router := buildRouter(t, "")
	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	send := func(path string, size int) *httptest.ResponseRecorder {
		body := `{"servers":[],"account":{"key":"` + strings.Repeat("x", size) + `"}}`
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		return serve(router, "127.0.0.1:5000", r)
	}
	for _, path := range []string{
		"/api/v1/cluster/agent-installs",
		"/api/v1/cluster/agent-installs/r1/servers/s1/host-key",
		"/api/v1/cluster/agent-installs/r1/servers/s1/standalone",
		"/api/v1/cluster/agent-installs/r1/proceed",
		"/api/v1/cluster/agent-installs/r1/servers/s1/recheck",
		"/api/v1/cluster/agent-installs/r1/servers/s1/revert",
	} {
		if rec := send(path, 1<<20); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("POST %s with a body over 1 MiB = %d %s, want 413", path, rec.Code, rec.Body)
		}
		// Outside a cluster the handler answers 400 Cluster not configured.
		if rec := send(path, 1<<10); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s with a 1 KiB body = %d %s, want its handler's 400", path, rec.Code, rec.Body)
		}
	}
}
