package cluster

import "testing"

// A proxied request carrying a body must declare its type. Echo's c.Bind()
// answers 415 without one, which the receiving handler turns into a 400 — the
// failure that broke the panel-CA publish on a live two-node cluster. Every
// caller before it used GET with a nil body, so nothing had exercised it.
func TestProxiedBodyDeclaresContentType(t *testing.T) {
	withBody := proxyToNodeHeaders("", []byte(`{"node_id":"x"}`))
	if withBody["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json — c.Bind() would reject the body",
			withBody["Content-Type"])
	}

	// A bodyless request must not claim to carry JSON.
	if _, present := proxyToNodeHeaders("admin", nil)["Content-Type"]; present {
		t.Error("a request with no body should not declare a Content-Type")
	}

	if got := proxyToNodeHeaders("admin", nil)["X-SFPanel-Original-User"]; got != "admin" {
		t.Errorf("Original-User = %q, want admin — audit attribution depends on it", got)
	}
}
