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
// reads it: a start's body carries the secrets, and so does a saved
// login's, and each is read whole.
func TestAgentInstallRoutesHaveABodyLimit(t *testing.T) {
	router := buildRouter(t, "")
	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path string, size int) *httptest.ResponseRecorder {
		body := `{"servers":[],"account":{"key":"` + strings.Repeat("x", size) + `"}}`
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		return serve(router, "127.0.0.1:5000", r)
	}
	for _, route := range []struct {
		method, path string
		limit        int
	}{
		{http.MethodPost, "/api/v1/cluster/agent-installs", 1 << 20},
		{http.MethodPost, "/api/v1/cluster/agent-installs/r1/servers/s1/host-key", 1 << 20},
		{http.MethodPost, "/api/v1/cluster/agent-installs/r1/servers/s1/standalone", 1 << 20},
		{http.MethodPost, "/api/v1/cluster/agent-installs/r1/proceed", 1 << 20},
		{http.MethodPost, "/api/v1/cluster/agent-installs/r1/servers/s1/recheck", 1 << 20},
		{http.MethodPost, "/api/v1/cluster/agent-installs/r1/servers/s1/revert", 1 << 20},
		// A saved login is replicated and kept: 64 KiB.
		{http.MethodPost, "/api/v1/cluster/agent-installs/logins", 64 << 10},
		{http.MethodPut, "/api/v1/cluster/agent-installs/logins/l1", 64 << 10},
	} {
		if rec := send(route.method, route.path, route.limit); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s with a body over %d KiB = %d %s, want 413", route.method, route.path, route.limit>>10, rec.Code, rec.Body)
		}
		// Outside a cluster the handler answers 400 Cluster not configured.
		if rec := send(route.method, route.path, 1<<10); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s with a 1 KiB body = %d %s, want its handler's 400", route.method, route.path, rec.Code, rec.Body)
		}
	}
}

// The server agents' connection values refuse a body over 64 KiB before
// their handler reads it: they are replicated to every manager.
func TestServerAgentSettingsHaveABodyLimit(t *testing.T) {
	router := buildRouter(t, "")
	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	send := func(size int) *httptest.ResponseRecorder {
		body := `{"zabbix":{"host_metadata":"` + strings.Repeat("x", size) + `"}}`
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/settings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		return serve(router, "127.0.0.1:5000", r)
	}
	if rec := send(65 << 10); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("PUT /api/v1/agents/settings with a 65 KiB body = %d %s, want 413", rec.Code, rec.Body)
	}
	// Outside a cluster the handler answers 400 Cluster not configured.
	if rec := send(1 << 10); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Cluster not configured") {
		t.Errorf("PUT /api/v1/agents/settings with a 1 KiB body = %d %s, want its handler's 400", rec.Code, rec.Body)
	}
}
