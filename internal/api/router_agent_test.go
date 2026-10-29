package api

import (
	"context"
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/docker"
	featureAlert "github.com/svrforum/SFPanel/internal/feature/alert"
)

// testJWTKey is the key both test routers install with auth.SetJWTSecret.
var testJWTKey = strings.Repeat("j", 64)

// managersNet stands in for the managers' addresses on an agent. Not
// 192.0.2.0/24: httptest.NewRequest comes from 192.0.2.1 unless told
// otherwise, and a request that forgot its address must not pass for a
// manager's.
var managersNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("203.0.113.0/24")
	return n
}()

func buildRouter(t *testing.T, role string) *echo.Echo {
	t.Helper()
	dir := t.TempDir()
	database, err := sfdb.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { auth.SetJWTSecret("") })
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Server.StacksPath = filepath.Join(dir, "stacks")
	cfg.Database.Path = filepath.Join(dir, "t.db")
	cfg.Auth.JWTSecret = testJWTKey
	cfg.Cluster.Role = role
	cfg.Cluster.NodeID = "node-1"
	var sources func(net.IP) bool
	if role == config.ClusterRoleAgent {
		sources = func(ip net.IP) bool { return managersNet.Contains(ip) }
	}
	e, cleanup := NewRouter(database, sfdb.NewAsyncWriter(ctx, database, "audit", 16), featureAlert.NewManager(database), cfg, embed.FS{}, "test", nil, filepath.Join(dir, "config.yaml"), nil, docker.NewLazy("unix:///nonexistent.sock"), sources, nil)
	t.Cleanup(cleanup)
	return e
}

// routeSet is every route the router registered, as "METHOD /path". Group.Use
// also registers catch-all not-found routes so a group's middleware runs for
// its unknown paths too; those are not routes and are left out.
func routeSet(e *echo.Echo) map[string]bool {
	set := map[string]bool{}
	for _, r := range e.Routes() {
		if r.Method == echo.RouteNotFound {
			continue
		}
		set[r.Method+" "+r.Path] = true
	}
	return set
}

// expectedManagerOnlyRoutes is what an agent does not serve: login and the
// accounts behind it, cluster management, the source side of a stack
// migration (plan C's manager brokers it), the cluster dashboard push and
// the SPA. Sorted.
var expectedManagerOnlyRoutes = func() []string {
	s := []string{
		"POST /api/v1/auth/login",
		"GET /api/v1/auth/setup-status",
		"POST /api/v1/auth/setup",
		"POST /api/v1/auth/refresh",

		"GET /api/v1/auth/2fa/status",
		"POST /api/v1/auth/2fa/setup",
		"POST /api/v1/auth/2fa/verify",
		"DELETE /api/v1/auth/2fa",
		"POST /api/v1/auth/2fa/recovery",
		"GET /api/v1/auth/2fa/recovery/status",
		"POST /api/v1/auth/change-password",
		"POST /api/v1/auth/ws-ticket",
		"POST /api/v1/auth/logout",

		"GET /api/v1/cluster/status",
		"GET /api/v1/cluster/overview",
		"GET /api/v1/cluster/nodes",
		"POST /api/v1/cluster/token",
		"GET /api/v1/cluster/tokens",
		"DELETE /api/v1/cluster/tokens/:id",
		"DELETE /api/v1/cluster/nodes/:id",
		"PATCH /api/v1/cluster/nodes/:id/labels",
		"PUT /api/v1/cluster/name",
		"PATCH /api/v1/cluster/nodes/:id/address",
		"POST /api/v1/cluster/panel-ca",
		"GET /api/v1/cluster/events",
		"POST /api/v1/cluster/leader-transfer",
		"POST /api/v1/cluster/init",
		"POST /api/v1/cluster/join",
		"POST /api/v1/cluster/leave",
		"POST /api/v1/cluster/disband",
		"GET /api/v1/cluster/interfaces",
		"POST /api/v1/cluster/update",
		"POST /api/v1/cluster/rotate/jwt",
		"POST /api/v1/cluster/rotate/ca",
		"POST /api/v1/cluster/rotate/ca/abort",
		"GET /api/v1/cluster/rotation",
		"POST /api/v1/cluster/nodes/:id/convert",
		"POST /api/v1/cluster/become-agent",
		"POST /api/v1/cluster/nodes/:id/promote",

		"GET /api/v1/docker/compose/cluster-stacks",
		"POST /api/v1/docker/compose/:project/migrate/preflight",
		"POST /api/v1/docker/compose/:project/migrate",

		"GET /ws/cluster/overview",
		"GET /*",
	}
	slices.Sort(s)
	return s
}()

// expectedAgentOnlyRoutes is what an agent serves and a manager does not: its
// half of its promotion to a manager, which the leader calls. Sorted.
var expectedAgentOnlyRoutes = []string{
	"POST /api/v1/system/become-manager",
}

// sourceSessionRoutes are the source side of a migration that a manager
// carries: every node serves them for its own stacks, an agent too.
var sourceSessionRoutes = []string{
	"GET /api/v1/docker/compose/:project/migrate-source/info",
	"POST /api/v1/docker/compose/:project/migrate-source",
	"GET /api/v1/docker/compose/:project/migrate-source/bundle",
	"POST /api/v1/docker/compose/:project/migrate-source/outcome",
}

// serve sends r to e from addr and returns the recorded response.
func serve(e *echo.Echo, addr string, r *http.Request) *httptest.ResponseRecorder {
	r.RemoteAddr = addr
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	return rec
}

// Agent routes are the manager's minus exactly the list in this task, plus
// the agent's own half of a promotion, and an unsigned request to one of
// them gets 401.
func TestAgentRouteSet(t *testing.T) {
	managerRouter := buildRouter(t, "")
	agentRouter := buildRouter(t, config.ClusterRoleAgent)
	manager := routeSet(managerRouter)
	agent := routeSet(agentRouter)
	var agentOnly []string
	for r := range agent {
		if !manager[r] {
			agentOnly = append(agentOnly, r)
		}
	}
	slices.Sort(agentOnly)
	if !slices.Equal(agentOnly, expectedAgentOnlyRoutes) {
		t.Fatalf("agent-only routes differ:\n got %v\nwant %v", agentOnly, expectedAgentOnlyRoutes)
	}
	var missing []string
	for r := range manager {
		if !agent[r] {
			missing = append(missing, r)
		}
	}
	slices.Sort(missing)
	if !slices.Equal(missing, expectedManagerOnlyRoutes) {
		t.Fatalf("manager-only routes differ:\n got %v\nwant %v", missing, expectedManagerOnlyRoutes)
	}
	for _, r := range sourceSessionRoutes {
		if !agent[r] || !manager[r] {
			t.Fatalf("%s: served by the agent %v, by the manager %v; every node serves its stacks' migration sessions", r, agent[r], manager[r])
		}
	}

	// The agent's chain is its managers' signature and nothing else. From
	// loopback, so the source check is not what refuses.
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("agent-key", []string{"agent-key"})
	const local = "127.0.0.1:5000"
	settings := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil) }

	if rec := serve(agentRouter, local, settings()); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"MISSING_TOKEN"`) {
		t.Fatalf("unsigned: %d %s, want 401 MISSING_TOKEN", rec.Code, rec.Body)
	}
	// The agent's half of a promotion as well: only what its managers sign
	// reaches it, the leader's call among them.
	promote := httptest.NewRequest(http.MethodPost, "/api/v1/system/become-manager", strings.NewReader(`{}`))
	promote.Header.Set("Content-Type", "application/json")
	if rec := serve(agentRouter, local, promote); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"MISSING_TOKEN"`) {
		t.Fatalf("unsigned promotion: %d %s, want 401 MISSING_TOKEN", rec.Code, rec.Body)
	}
	// A token for this node's own JWT key gets nowhere on an agent. The
	// manager router takes the same token, so the refusal is the agent's
	// chain and not a bad token.
	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	bearer := func() *http.Request {
		r := settings()
		r.Header.Set("Authorization", "Bearer "+tok)
		return r
	}
	if rec := serve(managerRouter, local, bearer()); rec.Code != http.StatusOK {
		t.Fatalf("bearer on the manager: %d %s, want 200", rec.Code, rec.Body)
	}
	if rec := serve(agentRouter, local, bearer()); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"MISSING_TOKEN"`) {
		t.Fatalf("bearer on the agent: %d %s, want 401 MISSING_TOKEN", rec.Code, rec.Body)
	}
	signed := settings()
	signed.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2With("agent-key", http.MethodGet, "/api/v1/settings"))
	if rec := serve(agentRouter, local, signed); rec.Code != http.StatusOK {
		t.Fatalf("signed: %d %s, want 200", rec.Code, rec.Body)
	}
}

// A write a manager relays is audited on the agent under the operator the
// manager named, stamped with the node ID the agent registered under: there
// is no cluster manager on an agent to ask for it.
func TestAgentAuditsTheOperatorOnItsNode(t *testing.T) {
	agentRouter := buildRouter(t, config.ClusterRoleAgent)
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("agent-key", []string{"agent-key"})
	signed := func(method, uri, body string) *http.Request {
		r := httptest.NewRequest(method, uri, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-SFPanel-Original-User", "alice")
		r.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2With("agent-key", method, uri))
		return r
	}
	if rec := serve(agentRouter, "127.0.0.1:5000", signed(http.MethodPut, "/api/v1/settings", `{"settings":{"terminal_timeout":"45"}}`)); rec.Code != http.StatusOK {
		t.Fatalf("PUT /settings: %d %s", rec.Code, rec.Body)
	}
	// The audit row is written by a background drain.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var resp struct {
			Data struct {
				Logs []struct {
					Username, Method, Path string
					NodeID                 string `json:"node_id"`
				} `json:"logs"`
			} `json:"data"`
		}
		rec := serve(agentRouter, "127.0.0.1:5000", signed(http.MethodGet, "/api/v1/audit/logs", ""))
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("GET /audit/logs: %d %s", rec.Code, rec.Body)
		}
		if logs := resp.Data.Logs; len(logs) > 0 {
			if l := logs[0]; l.Username != "alice" || l.Method != http.MethodPut || l.Path != "/api/v1/settings" || l.NodeID != "node-1" {
				t.Fatalf("audit row %+v, want alice's PUT /api/v1/settings on node-1", l)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no audit row for the relayed write")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An agent answers loopback and its managers on every path — health, the
// WebSockets on the root router, paths no route matches, a CORS preflight —
// and nobody else; a panel that is not an agent keeps answering everyone.
func TestAgentSourceCheckCoversEveryRoute(t *testing.T) {
	agentRouter := buildRouter(t, config.ClusterRoleAgent)
	const stranger = "198.51.100.9:5000"
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/health", nil),
		httptest.NewRequest(http.MethodGet, "/ws/terminal", nil),
		httptest.NewRequest(http.MethodGet, "/no/such/path", nil),
		func() *http.Request {
			r := httptest.NewRequest(http.MethodOptions, "/api/v1/settings", nil)
			r.Header.Set("Origin", "http://localhost:5173")
			r.Header.Set("Access-Control-Request-Method", http.MethodGet)
			return r
		}(),
	} {
		r.Header.Set("X-Forwarded-For", "203.0.113.5") // a manager's address, claimed
		if rec := serve(agentRouter, stranger, r); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
			t.Fatalf("%s %s from %s: %d %s, want 403 PERMISSION_DENIED", r.Method, r.URL.Path, stranger, rec.Code, rec.Body)
		}
	}
	for _, addr := range []string{"127.0.0.1:5000", "[::1]:5000", "203.0.113.5:5000"} {
		if rec := serve(agentRouter, addr, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)); rec.Code != http.StatusOK {
			t.Fatalf("health from %s: %d %s, want 200", addr, rec.Code, rec.Body)
		}
	}
	if rec := serve(buildRouter(t, ""), stranger, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)); rec.Code != http.StatusOK {
		t.Fatalf("health on a manager from %s: %d %s, want 200", stranger, rec.Code, rec.Body)
	}
}
