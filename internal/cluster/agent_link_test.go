package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// registeredAgent registers an agent with mgr and gives it a cert dir, as
// `sfpanel agent join` would.
func registeredAgent(t *testing.T, mgr *Manager) (*RegisterAgentResult, *TLSManager) {
	t.Helper()
	settleRotation(t, mgr)
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	csr, key := agentCSR(t)
	in := registerInput(t, tok.Token)
	in.CSR = csr
	res, err := mgr.HandleRegisterAgent(in)
	if err != nil {
		t.Fatal(err)
	}
	tm := NewTLSManager(filepath.Join(t.TempDir(), "certs"))
	if err := tm.SaveCACert(res.CACert); err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	if err := tm.SaveNodeCert(res.NodeCert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		t.Fatal(err)
	}
	return res, tm
}

func runLink(t *testing.T, mgr *Manager, addr string, res *RegisterAgentResult, tm *TLSManager, panelCA func() ([]byte, error), onRemoved func()) (*AgentLink, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	link, err := NewAgentLink(AgentLinkConfig{
		NodeID: res.NodeID, TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"), Version: "test",
		Interval: 100 * time.Millisecond, Collect: func() (float64, float64, float64, int) { return 1, 2, 3, 4 },
		PanelCA: panelCA,
	}, []config.ManagerRef{{ID: mgr.LocalNodeID(), GRPCAddress: addr}})
	if err != nil {
		t.Fatal(err)
	}
	if onRemoved != nil {
		link.SetOnRemoved(onRemoved)
	}
	cancel, done := goRun(t, link)
	return link, cancel, done
}

// goRun runs link until the test ends.
func goRun(t *testing.T, link *AgentLink) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { link.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel, done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAgentLinkHeartbeats(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	link, _, _ := runLink(t, mgr, addr, res, tm, nil, nil)
	waitFor(t, "the leader to record the agent", func() bool { return mgr.GetHeartbeat().GetMetrics(res.NodeID) != nil })
	if m := mgr.GetHeartbeat().GetMetrics(res.NodeID); m.Version != "test" || m.ContainerCount != 4 {
		t.Fatalf("metrics %+v", m)
	}
	waitFor(t, "the pong to name the managers", func() bool {
		data, _ := os.ReadFile(link.StatePath)
		return len(data) > 0 && slices.Contains(link.ManagerIDs(), mgr.LocalNodeID())
	})
	if hosts := link.ManagerHosts(); !slices.Equal(hosts, []string{"127.0.0.1"}) {
		t.Fatalf("manager hosts %v", hosts)
	}
}

// The leader removes the agent: on its next ping the link learns it, saves
// it, calls OnRemoved and stops — and a restarted link stays stopped.
func TestAgentLinkStopsWhenRemoved(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	removed := make(chan struct{})
	link, _, done := runLink(t, mgr, addr, res, tm, nil, func() { close(removed) })
	waitFor(t, "a first heartbeat", func() bool { return mgr.GetHeartbeat().GetMetrics(res.NodeID) != nil })
	if err := mgr.RemoveNode(res.NodeID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-removed:
	case <-time.After(5 * time.Second):
		t.Fatal("the link did not learn of its removal")
	}
	<-done
	st, err := LoadAgentState(link.StatePath, nil)
	if err != nil || !st.Removed {
		t.Fatalf("removal not saved: %+v %v", st, err)
	}
	again, err := NewAgentLink(AgentLinkConfig{NodeID: res.NodeID, TLS: tm, StatePath: link.StatePath, Interval: time.Hour}, nil)
	if err != nil || !again.Removed() {
		t.Fatal("a restarted link forgot the removal")
	}
}

func TestAgentLinkReportsItsPanelCA(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	fresh := panelCAPEM(t) // a different CA than the one sent at registration
	runLink(t, mgr, addr, res, tm, func() ([]byte, error) { return fresh, nil }, nil)
	waitFor(t, "the leader to store the reported CA", func() bool {
		v, _ := mgr.raft.GetFSM().GetConfigValue(panelCAKey(res.NodeID))
		return v == string(fresh)
	})
}

func TestNextTargets(t *testing.T) {
	st := AgentState{Leader: "192.0.2.2:3629", Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}, {ID: "m2", GRPCAddress: "192.0.2.2:3629"}}}
	if got := nextTargets(st); !slices.Equal(got, []string{"192.0.2.2:3629", "192.0.2.1:3629"}) {
		t.Fatalf("targets %v", got)
	}
}

// fakeManager is a manager's gRPC listener whose answers to an agent's
// heartbeat come from pong: a pong to send, or an error that ends the stream.
// report answers ReportPanelCA (nil: Unimplemented). self is its address.
type fakeManager struct {
	pb.UnimplementedClusterServiceServer
	self    string
	pong    func(self string) (*pb.HeartbeatPong, error)
	report  func() (*pb.ReportPanelCAResponse, error)
	streams atomic.Int32
	reports atomic.Int32
}

func (f *fakeManager) AgentHeartbeat(stream pb.ClusterService_AgentHeartbeatServer) error {
	f.streams.Add(1)
	for {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		pong, err := f.pong(f.self)
		if err != nil {
			return err
		}
		if err := stream.Send(pong); err != nil {
			return err
		}
	}
}

func (f *fakeManager) ReportPanelCA(ctx context.Context, req *pb.ReportPanelCARequest) (*pb.ReportPanelCAResponse, error) {
	f.reports.Add(1)
	if f.report == nil {
		return f.UnimplementedClusterServiceServer.ReportPanelCA(ctx, req)
	}
	return f.report()
}

// startFakeManager serves f under the certificate in tm.
func startFakeManager(t *testing.T, tm *TLSManager, f *fakeManager) *fakeManager {
	t.Helper()
	cfg, err := tm.ServerTLSConfig()
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

// startLink runs a link for the agent whose certificate is in tm.
func startLink(t *testing.T, tm *TLSManager, nodeID string, seed []config.ManagerRef) *AgentLink {
	t.Helper()
	link, err := NewAgentLink(AgentLinkConfig{
		NodeID: nodeID, TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"), Version: "test",
		Interval: 100 * time.Millisecond, Collect: func() (float64, float64, float64, int) { return 0, 0, 0, 0 },
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	goRun(t, link)
	return link
}

// A manager that does not list the agent yet refuses it, as a follower does
// right after a registration. Only the leader's word is a removal: the link
// tries again.
func TestAgentLinkIsNotStoppedByAFollower(t *testing.T) {
	_, ca := setupCertDir(t)
	follower := startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{pong: func(string) (*pb.HeartbeatPong, error) {
		return nil, notMemberError(nil, "agent-1")
	}})
	link := startLink(t, tlsWithRole(t, ca, "agent-1", CertRoleAgent), "agent-1", []config.ManagerRef{{ID: "m1", GRPCAddress: follower.self}})
	waitFor(t, "the link to try again", func() bool { return follower.streams.Load() >= 3 })
	if link.Removed() {
		t.Fatal("a follower's refusal stopped the link")
	}
}

// A follower's pong names the leader elsewhere: the link moves there, where
// its heartbeats count.
func TestAgentLinkFollowsTheLeader(t *testing.T) {
	mgr, leaderAddr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	follower := startFakeManager(t, tlsWithRole(t, mgr.GetTLS(), "f1", CertRoleManager), &fakeManager{pong: func(string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{LeaderGrpcAddress: leaderAddr}, nil
	}})
	// No leader known yet, so the follower, listed first, is tried first.
	startLink(t, tm, res.NodeID, []config.ManagerRef{{ID: "f1", GRPCAddress: follower.self}, {ID: mgr.LocalNodeID(), GRPCAddress: leaderAddr}})
	waitFor(t, "the leader to record the agent", func() bool { return mgr.GetHeartbeat().GetMetrics(res.NodeID) != nil })
	if follower.streams.Load() == 0 {
		t.Fatal("the follower was never tried: the test did not cover the move")
	}
}

// A follower answers the panel-CA report with the leader's address, and the
// report goes on there. This follower's pongs name no leader, so the
// heartbeat stays with it: only that hop can bring the CA to the leader.
func TestAgentLinkReportsThroughAFollower(t *testing.T) {
	mgr, leaderAddr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	follower := startFakeManager(t, tlsWithRole(t, mgr.GetTLS(), "f1", CertRoleManager), &fakeManager{
		pong: func(string) (*pb.HeartbeatPong, error) { return &pb.HeartbeatPong{}, nil },
		report: func() (*pb.ReportPanelCAResponse, error) {
			return &pb.ReportPanelCAResponse{Error: ErrNotLeader.Error(), LeaderGrpcAddress: leaderAddr}, nil
		},
	})
	fresh := panelCAPEM(t)
	link, err := NewAgentLink(AgentLinkConfig{
		NodeID: res.NodeID, TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"),
		Interval: 100 * time.Millisecond, PanelCA: func() ([]byte, error) { return fresh, nil },
	}, []config.ManagerRef{{ID: "f1", GRPCAddress: follower.self}, {ID: mgr.LocalNodeID(), GRPCAddress: leaderAddr}})
	if err != nil {
		t.Fatal(err)
	}
	goRun(t, link)
	waitFor(t, "the leader to store the reported CA", func() bool {
		v, _ := mgr.raft.GetFSM().GetConfigValue(panelCAKey(res.NodeID))
		return v == string(fresh)
	})
	if follower.reports.Load() == 0 {
		t.Fatal("the report never went to the follower: the test did not cover the hop")
	}
}

// A pong that names no managers — a manager older than the field sends none —
// leaves the list as it was. Taken as empty, it would cut the agent off from
// every manager, and the saved state would keep it cut off.
func TestAgentLinkKeepsItsListWhenAPongNamesNone(t *testing.T) {
	_, ca := setupCertDir(t)
	leader := startFakeManager(t, tlsWithRole(t, ca, "m1", CertRoleManager), &fakeManager{pong: func(self string) (*pb.HeartbeatPong, error) {
		return &pb.HeartbeatPong{FromLeader: true, LeaderGrpcAddress: self}, nil
	}})
	link := startLink(t, tlsWithRole(t, ca, "agent-1", CertRoleAgent), "agent-1", []config.ManagerRef{{ID: "m1", GRPCAddress: leader.self}})
	// The leader address is new to the agent, so the first pong is saved —
	// together with the list the agent kept for it.
	waitFor(t, "the first pong to be saved", func() bool {
		_, err := os.Stat(link.StatePath)
		return err == nil
	})
	if ids := link.ManagerIDs(); !slices.Equal(ids, []string{"m1"}) {
		t.Fatalf("managers after a pong naming none: %v", ids)
	}
	if st, err := LoadAgentState(link.StatePath, nil); err != nil || len(st.Managers) != 1 {
		t.Fatalf("saved state: %+v %v", st, err)
	}
}

// The agent key file is 0600 and reads back as written, less surrounding
// whitespace. An empty key is refused both ways: an agent that came up with
// one would refuse every request its managers send.
func TestAgentKeyFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	tm := NewTLSManager(dir)
	if _, err := tm.LoadAgentKey(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no key file: %v", err)
	}
	if err := tm.SaveAgentKey(" \n"); !errors.Is(err, errEmptyAgentKey) {
		t.Fatalf("saved an empty key: %v", err)
	}
	if err := tm.SaveAgentKey("k1\n"); err != nil {
		t.Fatal(err)
	}
	if k, err := tm.LoadAgentKey(); err != nil || k != "k1" {
		t.Fatalf("loaded %q, %v", k, err)
	}
	st, err := os.Stat(filepath.Join(dir, "agent.key"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("agent.key: %v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.key"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.LoadAgentKey(); !errors.Is(err, errEmptyAgentKey) {
		t.Fatalf("loaded an empty key: %v", err)
	}
}
