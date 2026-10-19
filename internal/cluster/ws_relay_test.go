package cluster

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	c.Request().Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(http.MethodGet, "/ws/terminal?node=remote"))
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
	c.Request().Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(http.MethodGet, "/ws/terminal?node=remote"))

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

// A relay to an agent that cannot be reached over HTTPS, or signed for, is
// refused before the browser's connection is upgraded — after it, the refusal
// is a socket that opens and drops. The requests here carry no WebSocket
// headers, so getting past the checks shows as the upgrader's own 400.
func TestWSRelayRefusesAnAgentBeforeTheUpgrade(t *testing.T) {
	setProxySecret(t, "managers-secret")
	var dialled atomic.Int32
	panel := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dialled.Add(1) }))
	defer panel.Close()
	addr := panel.Listener.Addr().String()
	fsm := fsmWithConfig(t, map[string]string{panelCAKey("a-tls"): makeCert(t, "agent panel CA", true)})
	addTestNode(t, fsm, Node{ID: "a-plain", Kind: KindAgent, APIAddress: addr})
	addTestNode(t, fsm, Node{ID: "a-tls", Kind: KindAgent, APIAddress: addr})
	addTestNode(t, fsm, Node{ID: "m2", Kind: KindManager, APIAddress: addr})
	mgr := &Manager{raft: &RaftNode{fsm: fsm}, heartbeat: NewHeartbeatManager(DefaultHeartbeatInterval, DefaultHeartbeatTimeout)}
	ranLocally := false
	h := WrapEchoWSHandler(func() *Manager { return mgr }, func(echo.Context) error { ranLocally = true; return nil })
	relay := func(node string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/ws/terminal?node="+node, nil), rec)
		c.Set("username", "alice")
		if err := h(c); err != nil {
			t.Fatal(err)
		}
		return rec
	}

	for node, reason := range map[string]error{"a-plain": ErrAgentPanelInsecure, "a-tls": ErrNoAgentKey} {
		rec := relay(node)
		if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), reason.Error()) {
			t.Fatalf("%s: %d %s, want 502 %q", node, rec.Code, rec.Body.String(), reason)
		}
	}
	applyCmd(t, fsm, Command{Type: CmdSetConfig, Key: AgentKeyConfigKey("a-tls"), Value: mustJSON("agent-key")})
	for _, node := range []string{"a-tls", "m2"} {
		if rec := relay(node); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s, want the upgrader's 400", node, rec.Code, rec.Body.String())
		}
	}
	if ranLocally || dialled.Load() != 0 {
		t.Fatalf("ran locally %v, dialled %d", ranLocally, dialled.Load())
	}
}
