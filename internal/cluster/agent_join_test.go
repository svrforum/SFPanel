package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/yaml.v3"
)

func agentJoinTarget(t *testing.T) (*AgentJoinEngine, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Database.Path = filepath.Join(dir, "sfpanel.db")
	cfg.Auth.JWTSecret = strings.Repeat("c", 64) // the cluster key of a former member
	cfg.Cluster.GRPCPort = freePortPair(t)
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	cfg.Cluster.DataDir = filepath.Join(dir, "data")
	path := filepath.Join(dir, "config.yaml")
	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return &AgentJoinEngine{
		Config: cfg, ConfigPath: path,
		// The test leader runs on this host and is named after it; a
		// cluster holds each name once.
		hostname: func() (string, error) { return "agent-host", nil },
		// The tests register at documentation addresses no host has.
		listenOn: func(net.IP) error { return nil },
	}, path
}

func TestAgentJoin(t *testing.T) {
	mgr, addr, _, pin := realLeader(t)
	settleRotation(t, mgr)
	tok, _ := mgr.CreateJoinToken(time.Hour, KindAgent)
	e, path := agentJoinTarget(t)
	res, err := e.Execute(addr, tok.Token, "192.0.2.10", pin, panelCAPEM(t))
	if err != nil {
		t.Fatal(err)
	}
	node := mgr.raft.GetFSM().GetNode(res.NodeID)
	if node == nil || node.Kind != KindAgent {
		t.Fatalf("not registered: %+v", node)
	}
	for _, f := range []string{"ca.crt", "node.crt", "node.key", "agent.key"} {
		st, err := os.Stat(filepath.Join(e.Config.Cluster.CertDir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, err, st)
		}
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Cluster.IsAgent() || saved.Cluster.NodeID != res.NodeID || len(saved.Cluster.Managers) != 1 || saved.Cluster.Managers[0].ID != mgr.LocalNodeID() {
		t.Fatalf("config: %+v", saved.Cluster)
	}
	if saved.Auth.JWTSecret == strings.Repeat("c", 64) {
		t.Fatal("kept a former member's cluster JWT key")
	}
	k, _ := NewTLSManager(e.Config.Cluster.CertDir).LoadAgentKey()
	if v, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID)); k == "" || k != v {
		t.Fatal("agent key on disk differs from the managers' copy")
	}
}

// The wrong fingerprint stops the join before the token travels, and leaves
// the server as it was.
func TestAgentJoinWrongPin(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	settleRotation(t, mgr)
	tok, _ := mgr.CreateJoinToken(time.Hour, KindAgent)
	e, path := agentJoinTarget(t)
	before, _ := os.ReadFile(path)
	_, err := e.Execute(addr, tok.Token, "192.0.2.10", "sha256:"+strings.Repeat("00", 32), panelCAPEM(t))
	if err == nil || !strings.Contains(err.Error(), "fingerprint does not match") {
		t.Fatalf("wrong pin: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("config changed")
	}
	if _, err := os.Stat(filepath.Join(e.Config.Cluster.CertDir, "agent.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("files written")
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("token consumed: %v", err)
	}
}

func TestAgentJoinRefusals(t *testing.T) {
	mgr, addr, managerToken, pin := realLeader(t)
	settleRotation(t, mgr)
	e, _ := agentJoinTarget(t)
	if _, err := e.Execute(addr, managerToken, "192.0.2.10", pin, panelCAPEM(t)); err == nil || !strings.Contains(err.Error(), ErrTokenKind.Error()) {
		t.Fatalf("manager token: %v", err)
	}
	e.Config.Cluster.Enabled = true
	if _, err := e.Execute(addr, managerToken, "192.0.2.10", pin, panelCAPEM(t)); !errors.Is(err, ErrAlreadyClustered) {
		t.Fatalf("already a member: %v", err)
	}
}

// fakeRegistrar is a manager as a joining agent meets it: PreFlight hands out
// the cluster CA key, as every manager's does, and RegisterAgent answers with
// answer — nil is Unimplemented, a manager from before agents. self is its
// address; last is the registration it last got.
type fakeRegistrar struct {
	pb.UnimplementedClusterServiceServer
	caKey     []byte
	answer    func(self string, req *pb.RegisterAgentRequest) *pb.RegisterAgentResponse
	self      string
	registers atomic.Int32
	last      atomic.Pointer[pb.RegisterAgentRequest]
}

func (f *fakeRegistrar) PreFlight(context.Context, *pb.PreFlightRequest) (*pb.PreFlightResponse, error) {
	return &pb.PreFlightResponse{Error: ErrTokenNotFound.Error(), CaPublicKey: f.caKey}, nil
}

func (f *fakeRegistrar) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	f.registers.Add(1)
	f.last.Store(req)
	if f.answer == nil {
		return f.UnimplementedClusterServiceServer.RegisterAgent(ctx, req)
	}
	return f.answer(f.self, req), nil
}

// startFakeRegistrar serves f as manager m1 of the cluster whose CA is ca.
func startFakeRegistrar(t *testing.T, ca *TLSManager, f *fakeRegistrar) *fakeRegistrar {
	t.Helper()
	f.caKey = caPublicKey(t, ca)
	cfg, err := tlsWithRole(t, ca, "m1", CertRoleManager).ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.self = lis.Addr().String() // before Serve, which the handlers read it after
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)))
	pb.RegisterClusterServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return f
}

func caPublicKey(t *testing.T, ca *TLSManager) []byte {
	t.Helper()
	caPEM, err := ca.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("ca.crt is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert.RawSubjectPublicKeyInfo
}

// answerAs answers a registration as the leader of ca's cluster would: node
// agent-1, its certificate signed for the request's key.
func answerAs(ca *TLSManager) func(string, *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
	return func(self string, req *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
		cert, err := ca.SignNodeCSR("agent-1", []string{"192.0.2.10"}, CertRoleAgent, req.Csr)
		if err != nil {
			return &pb.RegisterAgentResponse{Error: err.Error()}
		}
		caPEM, err := ca.LoadCACert()
		if err != nil {
			return &pb.RegisterAgentResponse{Error: err.Error()}
		}
		return &pb.RegisterAgentResponse{
			Success: true, NodeId: "agent-1", NodeCert: cert, CaCert: caPEM, ClusterName: "test-cluster",
			AgentKey: strings.Repeat("ab", 32),
			Managers: []*pb.NodeInfo{{Id: "m1", GrpcAddress: self, Kind: string(KindManager)}},
		}
	}
}

var agentFiles = []string{"ca.crt", "node.crt", "node.key", "agent.key"}

// assertUntouched checks a failed join left the server as it was: the config
// file and the engine's config unchanged, no agent file in the cert dir.
func assertUntouched(t *testing.T, e *AgentJoinEngine, path string, before []byte) {
	t.Helper()
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("the config file changed")
	}
	if e.Config.Cluster.Enabled || e.Config.Cluster.IsAgent() || e.Config.Auth.JWTSecret != strings.Repeat("c", 64) {
		t.Errorf("the engine's config changed: %+v", e.Config.Cluster)
	}
	for _, f := range agentFiles {
		if _, err := os.Stat(filepath.Join(e.Config.Cluster.CertDir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind: %v", f, err)
		}
	}
}

// A join aimed at a follower goes on to the leader it names, over a
// connection checked against the same pin, and registers there.
func TestAgentJoinFollowsTheLeader(t *testing.T) {
	mgr, leaderAddr, _, pin := realLeader(t)
	settleRotation(t, mgr)
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	follower := startFakeRegistrar(t, mgr.GetTLS(), &fakeRegistrar{answer: func(string, *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
		return &pb.RegisterAgentResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: leaderAddr}
	}})
	e, _ := agentJoinTarget(t)
	res, err := e.Execute(follower.self, tok.Token, "192.0.2.10", pin, panelCAPEM(t))
	if err != nil {
		t.Fatal(err)
	}
	if follower.registers.Load() != 1 {
		t.Fatal("the follower was not asked: the test did not cover the redirect")
	}
	if n := mgr.raft.GetFSM().GetNode(res.NodeID); n == nil || n.Kind != KindAgent {
		t.Fatalf("not registered with the leader: %+v", n)
	}
}

// A manager that is not the leader and names none stops the join, which says
// why; one that keeps naming a leader that is not one is followed
// maxLeaderRedirects times, then the join stops.
func TestAgentJoinStopsFollowingALeaderThatMoves(t *testing.T) {
	_, ca := setupCertDir(t)
	pin := CAPin(caPublicKey(t, ca))
	for _, tc := range []struct {
		name   string
		leader func(self string) string
		calls  int32
		reason string
	}{
		{"names no leader", func(string) string { return "" }, 1, "knows no leader yet"},
		{"names itself", func(self string) string { return self }, 1 + maxLeaderRedirects, "the leadership moved during the join"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: func(self string, _ *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
				return &pb.RegisterAgentResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: tc.leader(self)}
			}})
			e, path := agentJoinTarget(t)
			before, _ := os.ReadFile(path)
			_, err := e.Execute(f.self, "tok", "192.0.2.10", pin, panelCAPEM(t))
			if err == nil || !strings.Contains(err.Error(), ErrNotLeader.Error()) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("got %v", err)
			}
			if got := f.registers.Load(); got != tc.calls {
				t.Fatalf("%d registrations sent, want %d", got, tc.calls)
			}
			assertUntouched(t, e, path, before)
		})
	}
}

// An answer the agent could not run from is refused before anything is
// written — or, for a file that cannot be saved, with what was written taken
// back. The leader's record stays, and the error says how to remove it.
func TestAgentJoinRefusesABadAnswer(t *testing.T) {
	_, ca := setupCertDir(t)
	_, otherCA := setupCertDir(t)
	otherCAPEM, err := otherCA.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	otherCSR, _ := agentCSR(t)
	pin := CAPin(caPublicKey(t, ca))
	for _, tc := range []struct {
		name   string
		spoil  func(resp *pb.RegisterAgentResponse, req *pb.RegisterAgentRequest)
		reason string
	}{
		{"a CA that is not the pinned one", func(r *pb.RegisterAgentResponse, _ *pb.RegisterAgentRequest) { r.CaCert = otherCAPEM },
			"the CA in the registration response is not the pinned one"},
		{"a certificate from another CA", func(r *pb.RegisterAgentResponse, req *pb.RegisterAgentRequest) {
			r.NodeCert, _ = otherCA.SignNodeCSR("agent-1", []string{"192.0.2.10"}, CertRoleAgent, req.Csr)
		}, "the certificate in the registration response is not this agent's"},
		{"a certificate for another key", func(r *pb.RegisterAgentResponse, _ *pb.RegisterAgentRequest) {
			r.NodeCert, _ = ca.SignNodeCSR("agent-1", []string{"192.0.2.10"}, CertRoleAgent, otherCSR)
		}, "the certificate in the registration response is not this agent's"},
		{"a certificate naming another node", func(r *pb.RegisterAgentResponse, req *pb.RegisterAgentRequest) {
			r.NodeCert, _ = ca.SignNodeCSR("agent-2", []string{"192.0.2.10"}, CertRoleAgent, req.Csr)
		}, "the certificate in the registration response is not this agent's"},
		{"a manager's certificate", func(r *pb.RegisterAgentResponse, req *pb.RegisterAgentRequest) {
			r.NodeCert, _ = ca.SignNodeCSR("agent-1", []string{"192.0.2.10"}, CertRoleManager, req.Csr)
		}, "the certificate in the registration response is not this agent's"},
		{"no managers", func(r *pb.RegisterAgentResponse, _ *pb.RegisterAgentRequest) { r.Managers = nil },
			"cluster.managers must name at least one manager"},
		// The CA and the certificate are written by then; they must go.
		{"no agent key", func(r *pb.RegisterAgentResponse, _ *pb.RegisterAgentRequest) { r.AgentKey = "" },
			"write the agent's files: " + errEmptyAgentKey.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			valid := answerAs(ca)
			f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: func(self string, req *pb.RegisterAgentRequest) *pb.RegisterAgentResponse {
				resp := valid(self, req)
				tc.spoil(resp, req)
				return resp
			}})
			e, path := agentJoinTarget(t)
			before, _ := os.ReadFile(path)
			_, err := e.Execute(f.self, "tok", "192.0.2.10", pin, panelCAPEM(t))
			if err == nil || !strings.Contains(err.Error(), tc.reason) || !strings.Contains(err.Error(), "sfpanel cluster remove agent-1") {
				t.Fatalf("got %v", err)
			}
			assertUntouched(t, e, path, before)
		})
	}
}

// The config is written last. When it cannot be, the files written before it
// are taken back.
func TestAgentJoinTakesBackItsFilesWhenTheConfigIsNotSaved(t *testing.T) {
	_, ca := setupCertDir(t)
	f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: answerAs(ca)})
	e, _ := agentJoinTarget(t)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.ConfigPath = filepath.Join(notADir, "config.yaml") // a directory that cannot be made
	_, err := e.Execute(f.self, "tok", "192.0.2.10", CAPin(caPublicKey(t, ca)), panelCAPEM(t))
	if err == nil || !strings.Contains(err.Error(), "save the agent config") || !strings.Contains(err.Error(), "sfpanel cluster remove agent-1") {
		t.Fatalf("got %v", err)
	}
	assertUntouched(t, e, e.ConfigPath, nil)
}

// What the join can tell is wrong on its own is refused before the token goes
// anywhere.
func TestAgentJoinRefusesBeforeTheTokenTravels(t *testing.T) {
	_, ca := setupCertDir(t)
	f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: answerAs(ca)})
	pin := CAPin(caPublicKey(t, ca))
	for _, tc := range []struct {
		name, advertise, pin, reason string
		noConfigPath                 bool
	}{
		{name: "no config file", advertise: "192.0.2.10", pin: pin, noConfigPath: true, reason: "no config file"},
		{name: "a malformed pin", advertise: "192.0.2.10", pin: "sha256:00", reason: "must be sha256: and 64 hex characters"},
		{name: "a host name to advertise", advertise: "agent.example", pin: pin, reason: "is not an IP address managers can reach"},
		{name: "the unspecified address", advertise: "0.0.0.0", pin: pin, reason: "is not an IP address managers can reach"},
		{name: "the unspecified IPv6 address", advertise: "::", pin: pin, reason: "is not an IP address managers can reach"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, path := agentJoinTarget(t)
			before, _ := os.ReadFile(path)
			if tc.noConfigPath {
				e.ConfigPath = ""
			}
			calls := f.registers.Load()
			_, err := e.Execute(f.self, "tok", tc.advertise, tc.pin, panelCAPEM(t))
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("got %v", err)
			}
			if f.registers.Load() != calls {
				t.Fatal("the registration was sent")
			}
			assertUntouched(t, e, path, before)
		})
	}
}

// The agent's listeners bind the advertise address itself, so one this
// server does not hold — behind NAT, or an address it has lost — is refused
// before the token travels, not found at the next boot.
func TestAgentJoinRefusesAnAddressThisServerDoesNotHave(t *testing.T) {
	_, ca := setupCertDir(t)
	f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: answerAs(ca)})
	pin := CAPin(caPublicKey(t, ca))
	e, path := agentJoinTarget(t)
	e.listenOn = nil // the real check
	before, _ := os.ReadFile(path)
	_, err := e.Execute(f.self, "tok", "192.0.2.10", pin, panelCAPEM(t))
	if err == nil || !strings.Contains(err.Error(), "is not an address of this server") {
		t.Fatalf("got %v", err)
	}
	if f.registers.Load() != 0 {
		t.Fatal("the registration was sent")
	}
	assertUntouched(t, e, path, before)
	if _, err := e.Execute(f.self, "tok", "127.0.0.1", pin, panelCAPEM(t)); err != nil {
		t.Fatalf("an address this server holds: %v", err)
	}
}

// The agent registers at an IP — its certificate names that address — from
// the command, else the config, else the route to the manager, written the
// way the leader parses it.
func TestAgentJoinAdvertiseAddress(t *testing.T) {
	_, ca := setupCertDir(t)
	f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: answerAs(ca)})
	pin := CAPin(caPublicKey(t, ca))
	for _, tc := range []struct{ given, inConfig, want string }{
		{"192.0.2.10", "192.0.2.99", "192.0.2.10"},
		{"", "192.0.2.11", "192.0.2.11"},
		{"", "", "127.0.0.1"}, // the fake manager is reached over loopback
		{"::ffff:192.0.2.12", "", "192.0.2.12"},
		{"2001:db8::10", "", "2001:db8::10"},
	} {
		e, _ := agentJoinTarget(t)
		e.Config.Cluster.AdvertiseAddress = tc.inConfig
		if _, err := e.Execute(f.self, "tok", tc.given, pin, panelCAPEM(t)); err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		req := f.last.Load()
		wantAPI := net.JoinHostPort(tc.want, "3628")
		wantGRPC := net.JoinHostPort(tc.want, strconv.Itoa(e.Config.Cluster.GRPCPort))
		if req.ApiAddress != wantAPI || req.GrpcAddress != wantGRPC || e.Config.Cluster.AdvertiseAddress != tc.want {
			t.Fatalf("%+v: registered %s and %s, saved %q", tc, req.ApiAddress, req.GrpcAddress, e.Config.Cluster.AdvertiseAddress)
		}
	}
}

// With no host name to go by, the agent registers under eight random hex
// digits: the leader refuses a registration with no name.
func TestAgentJoinNamesAServerWithoutAHostName(t *testing.T) {
	_, ca := setupCertDir(t)
	f := startFakeRegistrar(t, ca, &fakeRegistrar{answer: answerAs(ca)})
	for _, hostname := range []func() (string, error){
		func() (string, error) { return "", errors.New("no host name") },
		func() (string, error) { return "", nil },
	} {
		e, _ := agentJoinTarget(t)
		e.hostname = hostname
		if _, err := e.Execute(f.self, "tok", "192.0.2.10", CAPin(caPublicKey(t, ca)), panelCAPEM(t)); err != nil {
			t.Fatal(err)
		}
		name := f.last.Load().NodeName
		if len(name) != 8 || strings.Trim(name, "0123456789abcdef") != "" || e.Config.Cluster.NodeName != name {
			t.Fatalf("registered as %q, saved %q", name, e.Config.Cluster.NodeName)
		}
	}
}

// A manager from before agents has no RegisterAgent: the operator is told to
// upgrade the managers, not shown a bare gRPC code.
func TestAgentJoinToAManagerFromBeforeAgents(t *testing.T) {
	_, ca := setupCertDir(t)
	f := startFakeRegistrar(t, ca, &fakeRegistrar{})
	e, path := agentJoinTarget(t)
	before, _ := os.ReadFile(path)
	_, err := e.Execute(f.self, "tok", "192.0.2.10", CAPin(caPublicKey(t, ca)), panelCAPEM(t))
	if err == nil || !strings.Contains(err.Error(), "every manager must be upgraded") {
		t.Fatalf("got %v", err)
	}
	assertUntouched(t, e, path, before)
}

// The leader's refusals reach the operator with its text, which names the
// refusal, and what to do — for an agent: a manager token is the wrong kind
// here, the opposite of a manager join's advice.
func TestAgentRefusalMessage(t *testing.T) {
	for errStr, advice := range map[string]string{
		ErrTokenNotFound.Error():     "lives only on the leader that created it",
		ErrTokenExpired.Error():      "sfpanel cluster token --agent",
		ErrTokenUsed.Error():         "admits one server",
		ErrTokenKind.Error():         "this is a manager token",
		ErrNotLeader.Error():         "knows no leader yet",
		ErrMaxAgentsReached.Error():  "cluster.max_agents",
		ErrNodeIdentityTaken.Error(): `registers as "agent-host" at 192.0.2.10`,
	} {
		if got := agentRefusalMessage(errStr, "agent-host", "192.0.2.10"); !strings.HasPrefix(got, errStr+" — ") || !strings.Contains(got, advice) {
			t.Errorf("%q: %q", errStr, got)
		}
	}
	if got := agentRefusalMessage(ErrProxyRotationPending.Error(), "agent-host", "192.0.2.10"); got != ErrProxyRotationPending.Error() {
		t.Errorf("a refusal that says what to do already is shown as it is: %q", got)
	}
}
