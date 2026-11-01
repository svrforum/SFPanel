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
