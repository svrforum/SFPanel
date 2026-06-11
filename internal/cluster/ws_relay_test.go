package cluster

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
)

func newRelayContext(t *testing.T, target string) echo.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return echo.New().NewContext(req, httptest.NewRecorder())
}

// setProxySecret swaps the process-global cluster proxy secret for the test
// and restores the previous value afterwards.
func setProxySecret(t *testing.T, secret string) {
	t.Helper()
	old := auth.ClusterProxySecret()
	auth.SetClusterProxySecret(secret)
	t.Cleanup(func() { auth.SetClusterProxySecret(old) })
}

// Username already on the echo context (chained internal-proxy hop routed
// through middleware) takes precedence over everything else.
func TestResolveRelayUsername_ContextUsername(t *testing.T) {
	c := newRelayContext(t, "/ws/terminal?node=remote")
	c.Set("username", "alice")

	if got := resolveRelayUsername(c, ""); got != "alice" {
		t.Fatalf("resolveRelayUsername = %q, want %q", got, "alice")
	}
}

// A validated internal-proxy request yields the X-SFPanel-Original-User the
// upstream node stamped.
func TestResolveRelayUsername_InternalProxyOriginalUser(t *testing.T) {
	setProxySecret(t, "relay-test-secret")

	c := newRelayContext(t, "/ws/terminal?node=remote")
	c.Request().Header.Set(auth.InternalProxyHeader, "relay-test-secret")
	c.Request().Header.Set("X-SFPanel-Original-User", "bob")

	if got := resolveRelayUsername(c, ""); got != "bob" {
		t.Fatalf("resolveRelayUsername = %q, want %q", got, "bob")
	}
}

// A validated internal-proxy request WITHOUT an original-user header resolves
// to "" — WrapEchoWSHandler then rejects the relay with 401 instead of
// dialing the remote with an anonymous identity.
func TestResolveRelayUsername_InternalProxyMissingUser(t *testing.T) {
	setProxySecret(t, "relay-test-secret")

	c := newRelayContext(t, "/ws/terminal?node=remote")
	c.Request().Header.Set(auth.InternalProxyHeader, "relay-test-secret")

	if got := resolveRelayUsername(c, ""); got != "" {
		t.Fatalf("resolveRelayUsername = %q, want empty", got)
	}
}

// A single-use ticket in the query authenticates and is consumed.
func TestResolveRelayUsername_Ticket(t *testing.T) {
	ticket := auth.MintWSTicket("carol")
	c := newRelayContext(t, "/ws/terminal?node=remote&ticket="+ticket)

	if got := resolveRelayUsername(c, ""); got != "carol" {
		t.Fatalf("resolveRelayUsername = %q, want %q", got, "carol")
	}
	// Consumed: a second resolution with the same ticket must fail.
	c2 := newRelayContext(t, "/ws/terminal?node=remote&ticket="+ticket)
	if got := resolveRelayUsername(c2, ""); got != "" {
		t.Fatalf("re-used ticket resolved to %q, want empty", got)
	}
}

// No credential anywhere → "" — the auth-bypass case WrapEchoWSHandler must
// reject before relaying to a remote node (the remote trusts the
// internal-proxy headers and would serve logs/metrics/exec unauthenticated).
func TestResolveRelayUsername_Unauthenticated(t *testing.T) {
	setProxySecret(t, "relay-test-secret")

	c := newRelayContext(t, "/ws/docker/containers/abc/exec?node=remote-uuid")
	if got := resolveRelayUsername(c, "some-jwt-secret"); got != "" {
		t.Fatalf("resolveRelayUsername = %q, want empty for unauthenticated request", got)
	}
}
