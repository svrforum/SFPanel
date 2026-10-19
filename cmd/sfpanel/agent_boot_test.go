package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

func TestAgentListenAddrs(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Cluster.AdvertiseAddress = "192.0.2.10"
	if adv, lo := agentListenAddrs(cfg); adv != "192.0.2.10:3628" || lo != "127.0.0.1:3628" {
		t.Fatalf("%q %q", adv, lo)
	}
	cfg.Cluster.AdvertiseAddress = "127.0.0.1"
	if adv, lo := agentListenAddrs(cfg); adv != "" || lo != "127.0.0.1:3628" {
		t.Fatalf("same address twice: %q %q", adv, lo)
	}
}

// Removal closes the cluster-facing listener; loopback keeps answering, so
// the update watchdog and local tools still see the panel.
func TestCloseAdvertiseKeepsLoopback(t *testing.T) {
	free := func() string {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		return ln.Addr().String()
	}
	adv, lo := free(), free()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	ls, err := serveAgent(srv, adv, lo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	get := func(addr string) error {
		c := http.Client{Timeout: time.Second}
		resp, err := c.Get("http://" + addr + "/")
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if get(adv) != nil || get(lo) != nil {
		t.Fatal("both listeners should answer")
	}
	if err := ls.CloseAdvertise(); err != nil {
		t.Fatal(err)
	}
	if get(adv) == nil {
		t.Fatal("the advertise listener still answers")
	}
	if err := get(lo); err != nil {
		t.Fatalf("loopback stopped: %v", err)
	}
}

// Every agent's panel has a certificate — its managers relay to it over HTTPS
// only — and then both listeners serve TLS, the cluster-facing one closing as
// it does over plain HTTP.
func TestServeAgentOverTLS(t *testing.T) {
	m := paneltls.New(t.TempDir())
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	caPEM, err := m.CACertPEM()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	free := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().String()
	}
	adv, lo := free(), free()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	ls, err := serveAgent(srv, adv, lo, m.ServerCertPath(), m.ServerKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	t.Cleanup(client.CloseIdleConnections)
	get := func(addr string) error {
		resp, err := client.Get("https://" + addr + "/")
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	for _, addr := range []string{adv, lo} {
		if err := get(addr); err != nil {
			t.Fatalf("HTTPS on %s: %v", addr, err)
		}
	}
	if err := ls.CloseAdvertise(); err != nil {
		t.Fatal(err)
	}
	if get(adv) == nil {
		t.Fatal("the advertise listener still answers")
	}
	if err := get(lo); err != nil {
		t.Fatalf("loopback stopped: %v", err)
	}
}

func TestAgentAllowsSource(t *testing.T) {
	a := &agentRuntime{allowed: mustCIDRs(t, "198.51.100.0/24")}
	a.managerHosts = func() []string { return []string{"192.0.2.1"} }
	for ip, want := range map[string]bool{"192.0.2.1": true, "198.51.100.20": true, "192.0.2.2": false} {
		if got := a.AllowsSource(net.ParseIP(ip)); got != want {
			t.Fatalf("%s: %v", ip, got)
		}
	}
}

// mustCIDRs parses entries as cluster.agent_allowed_sources are parsed.
func mustCIDRs(t *testing.T, entries ...string) []*net.IPNet {
	t.Helper()
	nets, err := parseAllowedSources(entries)
	if err != nil {
		t.Fatal(err)
	}
	return nets
}

// cluster.agent_allowed_sources takes single addresses as well as CIDRs, of
// either family; an entry that is neither stops the agent rather than
// shrinking the list unseen.
func TestParseAllowedSources(t *testing.T) {
	a := &agentRuntime{allowed: mustCIDRs(t, "198.51.100.7", "2001:db8::1", "203.0.113.0/24")}
	for ip, want := range map[string]bool{
		"198.51.100.7": true, "198.51.100.8": false, "::ffff:198.51.100.7": true,
		"2001:db8::1": true, "2001:db8::2": false, "203.0.113.200": true,
	} {
		if got := a.AllowsSource(net.ParseIP(ip)); got != want {
			t.Fatalf("%s: %v", ip, got)
		}
	}
	if _, err := parseAllowedSources([]string{"198.51.100.0/33"}); err == nil || !strings.Contains(err.Error(), `"198.51.100.0/33"`) {
		t.Fatalf("a bad entry: %v", err)
	}
}

// clusterAddr is the address the agents in these tests register with: a
// second loopback address, so that an agent's cluster-facing listener and its
// loopback one are two listeners, as on a real host.
func clusterAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("127.0.0.2 is not a local address here: %v", err)
	}
	ln.Close()
	return "127.0.0.2"
}

// freePortOn returns a port free on every one of hosts.
func freePortOn(t *testing.T, hosts ...string) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(hosts[0], "0"))
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		free := true
		for _, h := range hosts[1:] {
			other, err := net.Listen("tcp", net.JoinHostPort(h, strconv.Itoa(port)))
			if err != nil {
				free = false
				break
			}
			other.Close()
		}
		if free {
			return port
		}
	}
	t.Fatal("no port free on every host")
	return 0
}

// nodeCerts writes into dir a certificate from ca for nodeID at addr.
func nodeCerts(t *testing.T, ca *cluster.TLSManager, dir, nodeID, addr, role string) *cluster.TLSManager {
	t.Helper()
	tm := cluster.NewTLSManager(dir)
	caPEM, err := ca.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveCACert(caPEM); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.IssueNodeCert(nodeID, []string{addr}, role)
	if err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	return tm
}

// agentSetup is an agent's config and files as `sfpanel agent join` leaves
// them, and the certificate of its manager m1, from the same cluster CA.
type agentSetup struct {
	cfg     *config.Config
	key     string
	manager *cluster.TLSManager
}

func newAgentSetup(t *testing.T, advertise string) *agentSetup {
	t.Helper()
	dir := t.TempDir()
	ca := cluster.NewTLSManager(filepath.Join(dir, "ca"))
	if err := ca.InitCA("test"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Server.Port = freePortOn(t, "127.0.0.1", advertise)
	cfg.Cluster = config.ClusterConfig{
		Enabled: true, Role: config.ClusterRoleAgent, NodeID: "agent-1",
		AdvertiseAddress: advertise, GRPCPort: freePortOn(t, advertise),
		CertDir: filepath.Join(dir, "certs"), DataDir: filepath.Join(dir, "data"),
		// Never dialled: a test that starts the link names a manager of its own.
		Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}},
	}
	tm := nodeCerts(t, ca, cfg.Cluster.CertDir, "agent-1", advertise, cluster.CertRoleAgent)
	key := strings.Repeat("4a", 32)
	if err := tm.SaveAgentKey(key); err != nil {
		t.Fatal(err)
	}
	// startAgent installs the agent's key process-wide.
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	return &agentSetup{
		cfg:     cfg,
		key:     key,
		manager: nodeCerts(t, ca, filepath.Join(dir, "m1"), "m1", "127.0.0.1", cluster.CertRoleManager),
	}
}

func dialFails(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return true
	}
	c.Close()
	return false
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func getStatus(c *http.Client, addr string) error {
	resp, err := c.Get("http://" + addr + "/")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// No key, no agent: its managers would have nothing to sign with that it
// takes, and it must not come up as anything else (main exits).
func TestStartAgentNeedsItsKey(t *testing.T) {
	s := newAgentSetup(t, "127.0.0.1")
	if err := os.Remove(filepath.Join(s.cfg.Cluster.CertDir, "agent.key")); err != nil {
		t.Fatal(err)
	}
	rt, err := startAgent(s.cfg, "test", nil, nil)
	if !errors.Is(err, os.ErrNotExist) {
		if rt != nil {
			rt.Stop()
		}
		t.Fatalf("started without agent.key: %v", err)
	}
}

// A manager's request reaches the agent's panel through the gRPC server on
// the agent's cluster address, signed for the loopback hop with the agent's
// key — the one key its panel takes.
func TestAgentTakesItsManagersRequests(t *testing.T) {
	s := newAgentSetup(t, clusterAddr(t))
	rt, err := startAgent(s.cfg, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Stop)
	if want := net.JoinHostPort(s.cfg.Cluster.AdvertiseAddress, strconv.Itoa(s.cfg.Cluster.GRPCPort)); rt.grpc.Addr() != want {
		t.Fatalf("gRPC server on %s, not on the cluster address %s", rt.grpc.Addr(), want)
	}

	entered, release := make(chan struct{}, 1), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.IsInternalProxyRequest(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/slow" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})}
	t.Cleanup(func() { srv.Close() })
	if err := rt.servePanel(srv, s.cfg, "", ""); err != nil {
		t.Fatal(err)
	}

	if auth.ClusterProxySecret() != s.key {
		t.Fatal("the loopback hop does not sign with the agent's key")
	}
	other := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	other.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2With("the-managers-secret", http.MethodGet, "/api/v1/system/info"))
	if auth.IsInternalProxyRequest(other) {
		t.Fatal("the panel takes a request signed with another key")
	}

	client, err := cluster.DialNode(rt.grpc.Addr(), s.manager)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.ProxyRequest(ctx, &pb.APIRequest{Method: http.MethodGet, Path: "/api/v1/system/info"})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("a manager's request: %v %v", resp, err)
	}

	// A request still running does not hold up Stop, and so exit: a proxied
	// request may run for minutes.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.ProxyRequest(ctx, &pb.APIRequest{Method: http.MethodGet, Path: "/slow"})
	}()
	<-entered
	stopped := make(chan struct{})
	go func() { rt.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Error("Stop waited for a request in flight")
	}
	close(release)
	<-done
}

// removingLeader refuses the agent's heartbeat as a leader that has removed
// it does (notMemberError).
type removingLeader struct {
	pb.UnimplementedClusterServiceServer
}

func (removingLeader) Heartbeat(pb.ClusterService_HeartbeatServer) error {
	return status.Errorf(codes.PermissionDenied, "agent-1 is not a member of this cluster: %s", cluster.NotMemberRemovedMarker)
}

func startRemovingLeader(t *testing.T, tm *cluster.TLSManager) string {
	t.Helper()
	cfg, err := tm.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)))
	pb.RegisterClusterServiceServer(srv, removingLeader{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// Once the leader removes the agent, its cluster address stops answering —
// the connections already open on it too — and its gRPC server stops, while
// loopback keeps answering the update watchdog and `sfpanel agent`.
func TestRemovalClosesTheAgentsClusterSide(t *testing.T) {
	s := newAgentSetup(t, clusterAddr(t))
	s.cfg.Cluster.Managers = []config.ManagerRef{{ID: "m1", GRPCAddress: startRemovingLeader(t, s.manager)}}
	rt, err := startAgent(s.cfg, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Stop)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	t.Cleanup(func() { srv.Close() })
	if err := rt.servePanel(srv, s.cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	adv, lo := agentListenAddrs(s.cfg)
	kept := &http.Client{Timeout: time.Second, Transport: &http.Transport{}} // keeps its connection open
	t.Cleanup(kept.CloseIdleConnections)
	if err := getStatus(kept, adv); err != nil {
		t.Fatalf("the cluster address before the removal: %v", err)
	}
	if dialFails(rt.grpc.Addr()) {
		t.Fatal("the gRPC server is not listening before the removal")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt.StartLink(ctx)
	waitUntil(t, "the cluster address to close", func() bool { return dialFails(adv) })
	waitUntil(t, "the gRPC server to stop", func() bool { return dialFails(rt.grpc.Addr()) })
	if err := getStatus(kept, adv); err == nil {
		t.Fatal("a connection opened before the removal still answers")
	}
	if err := getStatus(&http.Client{Timeout: time.Second}, lo); err != nil {
		t.Fatalf("loopback stopped: %v", err)
	}
	st, err := cluster.LoadAgentState(filepath.Join(s.cfg.Cluster.DataDir, "agent-state.json"), nil)
	if err != nil || !st.Removed {
		t.Fatalf("saved state: %+v %v", st, err)
	}
}

// An agent the leader removed boots to loopback alone — no gRPC server, no
// cluster-facing listener — until `sfpanel agent reset`.
func TestARemovedAgentServesLoopbackOnly(t *testing.T) {
	s := newAgentSetup(t, clusterAddr(t))
	if err := (&cluster.AgentState{Removed: true}).Save(filepath.Join(s.cfg.Cluster.DataDir, "agent-state.json")); err != nil {
		t.Fatal(err)
	}
	rt, err := startAgent(s.cfg, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Stop)
	if !dialFails(net.JoinHostPort(s.cfg.Cluster.AdvertiseAddress, strconv.Itoa(s.cfg.Cluster.GRPCPort))) {
		t.Fatal("a removed agent started its gRPC server")
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	t.Cleanup(func() { srv.Close() })
	if err := rt.servePanel(srv, s.cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	rt.StartLink(context.Background())
	adv, lo := agentListenAddrs(s.cfg)
	if !dialFails(adv) {
		t.Fatal("a removed agent listens on its cluster address")
	}
	if err := getStatus(&http.Client{Timeout: time.Second}, lo); err != nil {
		t.Fatalf("loopback: %v", err)
	}
}

// An agent listens on the address it registered with. An empty or unspecified
// one would bind every interface, and the panel's loopback listener would then
// fail as "address already in use".
func TestStartAgentRefusesAnAddressItCannotListenOn(t *testing.T) {
	s := newAgentSetup(t, "127.0.0.1")
	for _, adv := range []string{"", "0.0.0.0", "::"} {
		s.cfg.Cluster.AdvertiseAddress = adv
		rt, err := startAgent(s.cfg, "test", nil, nil)
		if err == nil {
			rt.Stop()
			t.Fatalf("%q: started", adv)
		}
		if !strings.Contains(err.Error(), "cluster.advertise_address") {
			t.Fatalf("%q: %v", adv, err)
		}
	}
}

// The CA an agent reports and a manager publishes is the one behind the
// panel's certificate: the managed CA, or the operator's ca_file.
func TestPanelCAReader(t *testing.T) {
	dir := t.TempDir()
	if _, err := paneltls.New(dir).Ensure(); err != nil {
		t.Fatal(err)
	}
	caPath := paneltls.New(dir).CACertPath()
	want, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	managed := &config.Config{}
	managed.Server.TLS = config.TLSConfig{Enabled: true, Dir: dir}
	operator := &config.Config{}
	operator.Server.TLS = config.TLSConfig{Enabled: true, Dir: dir, CertFile: "/x.crt", KeyFile: "/x.key", CAFile: caPath}
	for name, cfg := range map[string]*config.Config{"managed": managed, "operator": operator} {
		read := panelCAReader(cfg)
		if read == nil {
			t.Fatalf("%s: no reader", name)
		}
		if got, err := read(); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: not the CA (%v)", name, err)
		}
	}
	operator.Server.TLS.CAFile = ""
	plain := &config.Config{}
	plain.Server.TLS = config.TLSConfig{Dir: dir}
	for name, cfg := range map[string]*config.Config{"operator without ca_file": operator, "plain HTTP": plain} {
		if panelCAReader(cfg) != nil {
			t.Fatalf("%s: a reader", name)
		}
	}
}
