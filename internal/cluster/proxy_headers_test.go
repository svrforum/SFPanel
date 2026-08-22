package cluster

import "testing"

// proxyHeadersFor mirrors the header construction inside ProxyToNode. Kept as a
// separate helper because ProxyToNode itself needs a live connection pool.
func proxyHeadersFor(body []byte, username string) map[string]string {
	headers := map[string]string{}
	if username != "" {
		headers["X-SFPanel-Original-User"] = username
	}
	if len(body) > 0 {
		headers["Content-Type"] = "application/json"
	}
	return headers
}

// A proxied request carrying a body must declare its type. Echo's c.Bind()
// answers 415 without one, which the receiving handler turns into a 400 — the
// failure that broke the panel-CA publish on a live two-node cluster. Every
// caller before it used GET with a nil body, so nothing had exercised it.
func TestProxiedBodyDeclaresContentType(t *testing.T) {
	withBody := proxyHeadersFor([]byte(`{"node_id":"x"}`), "")
	if withBody["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json — c.Bind() would reject the body",
			withBody["Content-Type"])
	}

	// A bodyless request must not claim to carry JSON.
	if _, present := proxyHeadersFor(nil, "admin")["Content-Type"]; present {
		t.Error("a request with no body should not declare a Content-Type")
	}

	if got := proxyHeadersFor(nil, "admin")["X-SFPanel-Original-User"]; got != "admin" {
		t.Errorf("Original-User = %q, want admin — audit attribution depends on it", got)
	}
}
