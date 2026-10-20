package middleware

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// When the key for an agent cannot be had, the relay stops before anything
// reaches the agent — no unsigned request, no manager-signed fallback.
func TestRelayStopsWhenTheAgentCannotBeSignedFor(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/files/download?path=/x", nil), httptest.NewRecorder())
	err := executeHTTPRelay(c, target.URL+"/api/v1/files/download?path=/x", target.Client(), func(*http.Request) error { return errors.New("no key") }, time.Minute, true)
	_ = err
	if hits.Load() != 0 {
		t.Fatal("the agent was contacted without a signature")
	}
	if rec := c.Response(); rec.Status != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Status)
	}
}

func TestClusterRoutesAreNotSentToAgents(t *testing.T) {
	for _, p := range []string{"/api/v1/cluster/leave", "/api/v1/cluster/status"} {
		if !agentRouteRefused(p) {
			t.Fatalf("%s would be sent to an agent", p)
		}
	}
	if agentRouteRefused("/api/v1/system/info") {
		t.Fatal("a node route refused")
	}
}

// setAuthHeaders signs or refuses. It used to fall back to the operator's
// bearer token when this node had no proxy secret — a login every manager
// accepts for a day, which an agent must never be handed.
func TestSetAuthHeadersNeverForwardsTheBearer(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("", nil)
	dir := t.TempDir()
	noCA := cluster.NewManager(&config.ClusterConfig{CertDir: dir, DataDir: dir}) // ProxySecret() is ""
	orig := httptest.NewRequest(http.MethodGet, "/api/v1/files/download?path=/x", nil)
	orig.Header.Set("Authorization", "Bearer user-jwt")
	for _, target := range []*cluster.Node{{ID: "a1", Kind: cluster.KindAgent}, {ID: "m2", Kind: cluster.KindManager}} {
		out, _ := http.NewRequest(http.MethodGet, "https://peer:3628/api/v1/files/download?path=/x", nil)
		err := setAuthHeaders(out, orig, noCA, target)
		if err == nil || out.Header.Get("Authorization") != "" || out.Header.Get(auth.InternalProxyHeaderV2) != "" {
			t.Fatalf("%s: err %v, headers %v", target.ID, err, out.Header)
		}
		if target.Kind == cluster.KindAgent && !errors.Is(err, cluster.ErrNoAgentKey) {
			t.Fatalf("agent: %v", err)
		}
	}
}

// loopbackAt is the panel a gRPC server under test hops to.
type loopbackAt string

func (l loopbackAt) URL(path string) string { return string(l) + path }
func (l loopbackAt) HTTPClient(timeout time.Duration) (*http.Client, error) {
	return &http.Client{Timeout: timeout}, nil
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// agentPanel is an agent's panel port: it records what reached it and answers
// with status.
type agentPanel struct {
	*httptest.Server
	mu     sync.Mutex
	hits   int
	last   *http.Request
	status int
}

func newAgentPanel(tls bool) *agentPanel {
	p := &agentPanel{status: http.StatusOK}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits++
		p.last = r.Clone(r.Context())
		status := p.status
		p.mu.Unlock()
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"MISSING_TOKEN","message":"no"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ok\n\n"))
	})
	if tls {
		p.Server = httptest.NewTLSServer(h)
	} else {
		p.Server = httptest.NewServer(h)
	}
	return p
}

func (p *agentPanel) seen() (int, *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits, p.last
}

// Every relay a ?node= request can take to an agent — SSE, binary, gRPC —
// against a real one-manager cluster, where the agent's kind, panel CA and key
// come from replicated state. Nothing reaches an agent in clear or unsigned,
// what does reach it is signed with its own key and is no manager's
// credential, its 401 does not reach the browser as one, and cluster routes
// are neither relayed nor run here.
func TestRelaysToAnAgent(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	raftPort := freePort(t)
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1,
	})
	if err := mgr.Init("relays"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	auth.SetProxySecrets("managers-secret", []string{"managers-secret"})

	plain, secure := newAgentPanel(false), newAgentPanel(true)
	defer plain.Close()
	defer secure.Close()
	// The agent's gRPC side, played by this manager's own gRPC server: its
	// loopback hop answers 401.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer refusing.Close()
	srv, err := cluster.NewGRPCServer(mgr, 0, loopbackAt(refusing.URL))
	if err != nil {
		t.Fatal(err)
	}
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	if err := srv.Start(grpcAddr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	for id, panel := range map[string]*agentPanel{"a-plain": plain, "a-tls": secure} {
		v, _ := json.Marshal(cluster.Node{ID: id, Name: id, Kind: cluster.KindAgent, APIAddress: panel.Listener.Addr().String(), GRPCAddress: grpcAddr})
		if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secure.Certificate().Raw})
	if err := mgr.SetPanelCA("a-tls", string(ca)); err != nil {
		t.Fatal(err)
	}
	// a-plain has a key, so only its missing panel CA can stop a relay to it.
	if err := mgr.SetConfig(cluster.AgentKeyConfigKey("a-plain"), "plain-agent-key"); err != nil {
		t.Fatal(err)
	}

	ranHere := false
	h := ClusterProxyMiddleware(func() *cluster.Manager { return mgr }, nil)(func(c echo.Context) error {
		ranHere = true
		return c.NoContent(http.StatusTeapot)
	})
	relay := func(method, target string) (int, string) {
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("Authorization", "Bearer user-jwt")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set("username", "alice")
		if err := h(c); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Error.Code
	}
	const (
		binary = "/api/v1/files/download?path=/x&node="
		sse    = "/api/v1/docker/images/pull?node="
	)
	expect := func(what string, gotStatus int, gotCode string, status int, code string) {
		t.Helper()
		if gotStatus != status || gotCode != code {
			t.Fatalf("%s: %d %q, want %d %q", what, gotStatus, gotCode, status, code)
		}
	}

	status, code := relay(http.MethodPost, "/api/v1/cluster/leave?node=a-plain")
	expect("cluster route", status, code, http.StatusConflict, response.ErrClusterRouteNotOnAgent)
	status, code = relay(http.MethodGet, binary+"a-plain")
	expect("binary, no panel CA", status, code, http.StatusBadGateway, response.ErrClusterAgentInsecure)
	status, code = relay(http.MethodPost, sse+"a-plain")
	expect("SSE, no panel CA", status, code, http.StatusBadGateway, response.ErrClusterAgentInsecure)
	status, code = relay(http.MethodGet, binary+"a-tls")
	expect("binary, no key", status, code, http.StatusBadGateway, response.ErrClusterAgentAuthFailed)
	status, code = relay(http.MethodPost, sse+"a-tls")
	expect("SSE, no key", status, code, http.StatusBadGateway, response.ErrClusterAgentAuthFailed)
	if hits, _ := plain.seen(); hits != 0 {
		t.Fatalf("the agent without a panel CA was contacted %d times", hits)
	}
	if hits, _ := secure.seen(); hits != 0 {
		t.Fatalf("the agent without a key was contacted %d times", hits)
	}

	if err := mgr.SetConfig(cluster.AgentKeyConfigKey("a-tls"), "agent-key"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, target string }{{http.MethodGet, binary + "a-tls"}, {http.MethodPost, sse + "a-tls"}} {
		if status, _ := relay(tc.method, tc.target); status != http.StatusOK {
			t.Fatalf("%s %s: %d", tc.method, tc.target, status)
		}
		_, got := secure.seen()
		if got.Header.Get("Authorization") != "" {
			t.Fatalf("%s: the operator's bearer token reached the agent", tc.target)
		}
		// The manager check first: a MAC that does not match records no nonce.
		if verifies("managers-secret", got) {
			t.Fatalf("%s: a manager accepts the header the agent was sent", tc.target)
		}
		if !verifies("agent-key", got) {
			t.Fatalf("%s: not signed with the agent's key", tc.target)
		}
	}

	secure.mu.Lock()
	secure.status = http.StatusUnauthorized
	secure.mu.Unlock()
	status, code = relay(http.MethodGet, binary+"a-tls")
	expect("binary, the agent answers 401", status, code, http.StatusBadGateway, response.ErrClusterAgentAuthFailed)
	status, code = relay(http.MethodPost, sse+"a-tls")
	expect("SSE, the agent answers 401", status, code, http.StatusBadGateway, response.ErrClusterAgentAuthFailed)
	status, code = relay(http.MethodGet, "/api/v1/system/info?node=a-tls")
	expect("gRPC, the agent answers 401", status, code, http.StatusBadGateway, response.ErrClusterAgentAuthFailed)
	if ranHere {
		t.Fatal("a request for an agent ran on the manager")
	}
}

// verifies reports whether a node accepting only key takes r's header.
func verifies(key string, r *http.Request) bool {
	auth.SetProxySecrets(key, []string{key})
	check, _ := http.NewRequest(r.Method, r.URL.RequestURI(), nil)
	check.Header.Set(auth.InternalProxyHeaderV2, r.Header.Get(auth.InternalProxyHeaderV2))
	return auth.IsInternalProxyRequest(check)
}
