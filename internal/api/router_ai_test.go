package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
)

// The terminal page's snippets and key rows refuse a body over 1 MiB before
// their handler decodes it: each is read whole and kept in the node's
// database.
func TestTerminalPrefsRoutesLimitTheirBody(t *testing.T) {
	router := buildRouter(t, "")
	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	send := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		return serve(router, "127.0.0.1:5000", r)
	}
	for path, body := range map[string][2]string{
		"/api/v1/ai/prefs/snippets": {`{"snippets":[{"kind":"shell","body":"ls"}]}`, `{"snippets":[{"kind":"shell","body":"` + strings.Repeat("x", 1<<20) + `"}]}`},
		"/api/v1/ai/prefs/keysets":  {`{"keysets":{"shell":[[{"id":"tab"}]]}}`, `{"keysets":{"shell":[[{"label":"x","text":"` + strings.Repeat("x", 1<<20) + `"}]]}}`},
	} {
		// Without the limit the handler would answer 400: too long.
		if rec := send(path, body[1]); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("PUT %s with a body over 1 MiB = %d %s, want 413", path, rec.Code, rec.Body)
		}
		if rec := send(path, body[0]); rec.Code != http.StatusOK {
			t.Errorf("PUT %s with a small body = %d %s, want 200", path, rec.Code, rec.Body)
		}
	}
	// And what was saved is what the page is given.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ai/prefs", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := serve(router, "127.0.0.1:5000", r)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"body":"ls"`) || !strings.Contains(rec.Body.String(), `"id":"tab"`) {
		t.Errorf("GET /api/v1/ai/prefs = %d %s", rec.Code, rec.Body)
	}
}
