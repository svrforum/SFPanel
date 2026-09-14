package ai

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func stubLatest(t *testing.T, tool, body string, status int) *int32 {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	prev := latestSources[tool]
	latestSources[tool] = srv.URL
	t.Cleanup(func() { latestSources[tool] = prev; srv.Close() })
	return &hits
}

func TestParseLatest(t *testing.T) {
	if got := parseLatest(ToolClaude, "2.1.270\n"); got != "2.1.270" {
		t.Errorf("claude: %q", got)
	}
	if got := parseLatest(ToolCodex, `{"name":"@openai/codex","version":"0.154.0"}`); got != "0.154.0" {
		t.Errorf("codex: %q", got)
	}
	if got := parseLatest(ToolGemini, "<html>rate limited</html>"); got != "" {
		t.Errorf("garbage must yield empty, got %q", got)
	}
}

// A version lookup is a network call on a request path: it is memoised for
// an hour, and a failure is an empty string, never an error.
func TestLatestVersion_MemoisedAndFailureIsEmpty(t *testing.T) {
	hits := stubLatest(t, ToolClaude, "2.1.270", http.StatusOK)
	h := newTestHandler(t, nil)
	if v := h.latestVersion(ToolClaude); v != "2.1.270" {
		t.Fatalf("got %q", v)
	}
	if v := h.latestVersion(ToolClaude); v != "2.1.270" || atomic.LoadInt32(hits) != 1 {
		t.Errorf("second call: %q after %d fetches, want memoised", v, *hits)
	}
	h.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	_ = h.latestVersion(ToolClaude)
	if atomic.LoadInt32(hits) != 2 {
		t.Errorf("after the TTL the source must be asked again; fetches = %d", *hits)
	}

	stubLatest(t, ToolCodex, "", http.StatusInternalServerError)
	if v := h.latestVersion(ToolCodex); v != "" {
		t.Errorf("a failing source must give %q, got %q", "", v)
	}
}

// A failure is memoised too — an offline host must not re-dial on every
// request — but for minutes, not the full hour: a blip while the panel boots
// (network-online.target is not the internet) would otherwise hide the update
// badge for an hour.
func TestLatestVersion_AFailureExpiresSooner(t *testing.T) {
	hits := stubLatest(t, ToolGemini, "", http.StatusInternalServerError)
	h := newTestHandler(t, nil)
	if v := h.latestVersion(ToolGemini); v != "" {
		t.Fatalf("got %q", v)
	}
	h.now = func() time.Time { return testNow.Add(latestFailTTL - time.Minute) }
	_ = h.latestVersion(ToolGemini)
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("inside the failure TTL the source must not be asked again; fetches = %d", *hits)
	}
	h.now = func() time.Time { return testNow.Add(latestFailTTL + time.Minute) }
	_ = h.latestVersion(ToolGemini)
	if atomic.LoadInt32(hits) != 2 {
		t.Errorf("a failure must be retried after %v, not after %v; fetches = %d", latestFailTTL, latestTTL, *hits)
	}
	// An answer keeps the long TTL: the failure path must not shorten it.
	okHits := stubLatest(t, ToolClaude, "2.1.270", http.StatusOK)
	h.now = func() time.Time { return testNow }
	_ = h.latestVersion(ToolClaude)
	h.now = func() time.Time { return testNow.Add(latestFailTTL + time.Minute) }
	if v := h.latestVersion(ToolClaude); v != "2.1.270" || atomic.LoadInt32(okHits) != 1 {
		t.Errorf("an answer must stay memoised for %v: %q after %d fetches", latestTTL, v, *okHits)
	}
}
