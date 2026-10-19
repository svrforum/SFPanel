package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// tlsWithRole gives a node of the cluster whose CA is ca its own cert dir.
func tlsWithRole(t *testing.T, ca *TLSManager, nodeID, role string) *TLSManager {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	tm := NewTLSManager(dir)
	caPEM, err := ca.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveCACert(caPEM); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.IssueNodeCert(nodeID, []string{"127.0.0.1"}, role)
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	return tm
}

type panelAt struct{ base string }

func (p panelAt) URL(path string) string                         { return p.base + path }
func (p panelAt) HTTPClient(time.Duration) (*http.Client, error) { return &http.Client{}, nil }

func startAgentServer(t *testing.T, ca *TLSManager, managers []string, panel LocalPanel) *AgentGRPCServer {
	t.Helper()
	return startAgentServerWith(t, ca, panel, func() []string { return managers })
}

// startAgentServerWith starts agent-1's server with managerIDs as its list.
func startAgentServerWith(t *testing.T, ca *TLSManager, panel LocalPanel, managerIDs func() []string) *AgentGRPCServer {
	t.Helper()
	srv, err := NewAgentGRPCServer(tlsWithRole(t, ca, "agent-1", CertRoleAgent), panel, managerIDs)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

// The agent takes ProxyRequest from its managers, and nothing else from anyone.
func TestAgentServerAcceptsOnlyItsManagers(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("agent-key", []string{"agent-key"})
	var gotHeader string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(auth.InternalProxyHeaderV2)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer panel.Close()
	_, ca := setupCertDir(t)
	srv := startAgentServer(t, ca, []string{"m1"}, panelAt{panel.URL})

	call := func(clientID, role string) (*pb.APIResponse, error) {
		client, err := DialNode(srv.Addr(), tlsWithRole(t, ca, clientID, role))
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.ProxyRequest(context.Background(), &pb.APIRequest{Method: http.MethodGet, Path: "/api/v1/system/info"})
	}

	resp, err := call("m1", CertRoleManager)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("listed manager: %v %+v", err, resp)
	}
	if !accepts("agent-key", gotHeader, "/api/v1/system/info") {
		t.Fatal("the loopback hop was not signed with the agent's key")
	}
	for _, tc := range []struct{ id, role, want string }{
		{"m9", CertRoleManager, "not one of this agent's managers"},
		{"m1", CertRoleAgent, "not a manager"},
		{"agent-2", CertRoleAgent, "not a manager"},
	} {
		_, err := call(tc.id, tc.role)
		if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s/%s: %v", tc.id, tc.role, err)
		}
	}
	// A listed manager may not call anything else on an agent.
	client, _ := DialNode(srv.Addr(), tlsWithRole(t, ca, "m1", CertRoleManager))
	defer client.Close()
	if _, err := client.PreFlight(context.Background(), ""); status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "serves only ProxyRequest") {
		t.Fatalf("PreFlight on an agent: %v", err)
	}
	// Streams too: without the stream interceptor this one answers
	// Unimplemented, and a stream an agent did serve would be open to any
	// cluster certificate.
	hb, err := client.OpenHeartbeat(context.Background())
	if err == nil {
		_, err = hb.Recv()
	}
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "serves only ProxyRequest") {
		t.Fatalf("Heartbeat on an agent: %v", err)
	}
}

// Nothing on an agent's port is open without a certificate — an agent takes
// no joins — so such a caller is refused in the handshake, before any method.
func TestAgentServerRefusesACallerWithoutACertificate(t *testing.T) {
	var reached atomic.Bool
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer panel.Close()
	_, ca := setupCertDir(t)
	srv := startAgentServer(t, ca, []string{"m1"}, panelAt{panel.URL})
	client, err := DialNodeInsecure(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.ProxyRequest(context.Background(), &pb.APIRequest{Method: http.MethodGet, Path: "/api/v1/system/info"})
	// Unavailable is the transport failing: the server ends the connection in
	// the handshake. Past it, the interceptor would answer Unauthenticated.
	// The text is not asserted — whether the client reads the TLS alert or
	// fails its next write first is a race.
	if status.Code(err) != codes.Unavailable || reached.Load() {
		t.Fatalf("no certificate: %v (panel reached: %v)", err, reached.Load())
	}
}

// The list is asked on every call: a manager the agent stops listing is
// refused on the connection it already holds.
func TestAgentServerFollowsItsManagerList(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer panel.Close()
	_, ca := setupCertDir(t)
	var mu sync.Mutex
	managers := []string{"m1"}
	srv := startAgentServerWith(t, ca, panelAt{panel.URL}, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return managers
	})
	client, err := DialNode(srv.Addr(), tlsWithRole(t, ca, "m1", CertRoleManager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	req := &pb.APIRequest{Method: http.MethodGet, Path: "/api/v1/system/info"}
	if resp, err := client.ProxyRequest(context.Background(), req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("listed manager: %v %+v", err, resp)
	}
	mu.Lock()
	managers = []string{"m2"}
	mu.Unlock()
	if _, err := client.ProxyRequest(context.Background(), req); status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "not one of this agent's managers") {
		t.Fatalf("a manager no longer listed: %v", err)
	}
}

// An agent dialing a manager checks who answers: an agent certificate from
// the same CA, or a manager it does not know, is not its manager.
func TestDialManagerChecksTheServer(t *testing.T) {
	_, ca := setupCertDir(t)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer panel.Close()
	// An AgentGRPCServer is a convenient TLS server whose certificate we pick.
	server := func(id, role string) string {
		srv, err := NewAgentGRPCServer(tlsWithRole(t, ca, id, role), panelAt{panel.URL}, func() []string { return []string{"agent-1"} })
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Start("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(srv.Stop)
		return srv.Addr()
	}
	me := tlsWithRole(t, ca, "agent-1", CertRoleAgent)
	for _, tc := range []struct {
		id, role string
		ok       bool
	}{
		{"m1", CertRoleManager, true}, {"m9", CertRoleManager, false}, {"agent-2", CertRoleAgent, false},
		// An agent's certificate under a listed manager's name: only the role
		// check refuses this one.
		{"m1", CertRoleAgent, false},
	} {
		c, err := DialManager(server(tc.id, tc.role), me, func() []string { return []string{"m1"} })
		if err == nil {
			_, err = c.PreFlight(context.Background(), "") // forces the handshake
			c.Close()
		}
		handshakeFailed := err != nil && (strings.Contains(err.Error(), "not a manager") || strings.Contains(err.Error(), "not one of"))
		if tc.ok == handshakeFailed {
			t.Fatalf("%s/%s: %v", tc.id, tc.role, err)
		}
	}
}
