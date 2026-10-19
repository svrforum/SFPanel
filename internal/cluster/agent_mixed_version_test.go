package cluster

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// serveFake serves srv under the certificate in tm and counts every stream
// any method opens on it.
func serveFake(t *testing.T, tm *TLSManager, srv pb.ClusterServiceServer, opened *atomic.Int32) string {
	t.Helper()
	cfg, err := tm.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)), grpc.StreamInterceptor(
		func(s any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
			if opened != nil {
				opened.Add(1)
			}
			return h(s, ss)
		}))
	pb.RegisterClusterServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

// preAgentManager is a manager from before agents: its Heartbeat makes every
// node that pings it a Raft voter — PromoteOnHeartbeatIfPending knew no
// agents — and its pong carries none of the fields an agent follows.
type preAgentManager struct {
	pb.UnimplementedClusterServiceServer
	pings, opened atomic.Int32
}

func (f *preAgentManager) Heartbeat(stream pb.ClusterService_HeartbeatServer) error {
	for {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		f.pings.Add(1)
		if err := stream.Send(&pb.HeartbeatPong{LeaderId: "m1"}); err != nil {
			return err
		}
	}
}

// Such a manager promotes on the first ping, before any answer the agent
// could judge it by. So the agent's heartbeat is an RPC of its own, which
// that manager does not have: it never sees an agent's ping, and the link
// moves on.
func TestAgentLinkNeverPingsAManagersHeartbeat(t *testing.T) {
	_, ca := setupCertDir(t)
	old := &preAgentManager{}
	addr := serveFake(t, tlsWithRole(t, ca, "m1", CertRoleManager), old, &old.opened)
	link := startLink(t, tlsWithRole(t, ca, "agent-1", CertRoleAgent), "agent-1", []config.ManagerRef{{ID: "m1", GRPCAddress: addr}})
	waitFor(t, "three heartbeat attempts", func() bool { return old.pings.Load() > 0 || old.opened.Load() >= 3 })
	if n := old.pings.Load(); n > 0 {
		t.Fatalf("the agent sent %d pings to a manager's Heartbeat, which would make it a Raft voter there", n)
	}
	if link.Removed() {
		t.Fatal("an older manager stopped the link")
	}
}

// Agents register only once every manager is online and reports CapAgents:
// one that is down may come back running a release from before agents, and
// one that runs such a release forwards operators' credentials to agents.
// The refusal names whom it waits for and leaves the token usable.
func TestRegisterAgentWaitsForEveryManager(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Name: "peer-2", Kind: KindManager, Role: RoleVoter})
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	register := func() error {
		_, err := mgr.HandleRegisterAgent(registerInput(t, tok.Token))
		return err
	}

	if err := register(); !errors.Is(err, ErrManagersNotReadyForAgents) || !strings.Contains(err.Error(), "peer-2 (not heard from)") {
		t.Fatalf("with a manager not heard from: %v", err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "0.79.0", Capabilities: []string{CapProxySecret}})
	if err := register(); !errors.Is(err, ErrManagersNotReadyForAgents) || !strings.Contains(err.Error(), "peer-2 (0.79.0)") {
		t.Fatalf("with a manager from before agents: %v", err)
	}
	if _, err := mgr.tokens.Peek(tok.Token); err != nil {
		t.Fatalf("a refusal consumed the token: %v", err)
	}

	mgr.GetHeartbeat().RecordHeartbeat(&NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{CapProxySecret, CapAgents}})
	if err := register(); err != nil {
		t.Fatalf("with every manager ready: %v", err)
	}
}

// Once an agent exists, a manager join that does not say its code knows
// agents is refused before the token is spent — the same manager forwards
// operators' credentials to agents and makes them Raft voters. Without agents
// it is taken as before, which rolling upgrades of managers rely on.
func TestAClusterWithAgentsTakesNoManagerFromBeforeThem(t *testing.T) {
	mgr, _, token, _ := realLeader(t)
	// The leader's own ID: a join that passes every check about the joiner's
	// code is refused as a duplicate, so nothing joins.
	self := mgr.LocalNodeID()
	if _, _, _, _, err := mgr.HandleJoin(self, "dup", "127.0.0.1:1", "127.0.0.1:2", token, nil); !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("an older manager with no agents in the cluster: %v", err)
	}

	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "agent-1", Kind: KindAgent})
	if _, _, _, _, err := mgr.HandleJoin("new-node", "n", "127.0.0.1:1", "127.0.0.1:2", token, nil); !errors.Is(err, ErrJoinerPredatesAgents) {
		t.Fatalf("an older manager into a cluster with agents: %v", err)
	}
	if _, _, _, _, err := mgr.HandleJoin("new-node", "n", "127.0.0.1:1", "127.0.0.1:2", token, []string{CapProxySecret}); !errors.Is(err, ErrJoinerPredatesAgents) {
		t.Fatalf("a manager naming other capabilities only: %v", err)
	}
	if _, err := mgr.tokens.Peek(token); err != nil {
		t.Fatalf("the refusal consumed the token: %v", err)
	}
	if _, _, _, _, err := mgr.HandleJoin(self, "dup", "127.0.0.1:1", "127.0.0.1:2", token, []string{CapAgents}); !errors.Is(err, ErrNodeAlreadyExists) {
		t.Fatalf("a manager that knows agents: %v", err)
	}
}

// joinRecorder is a leader that keeps the join requests it receives and
// refuses them.
type joinRecorder struct {
	pb.UnimplementedClusterServiceServer
	caKey []byte
	mu    sync.Mutex
	joins []*pb.JoinRequest
}

func (f *joinRecorder) PreFlight(context.Context, *pb.PreFlightRequest) (*pb.PreFlightResponse, error) {
	return &pb.PreFlightResponse{Valid: true, ClusterName: "c", CaPublicKey: f.caKey}, nil
}

func (f *joinRecorder) Join(_ context.Context, req *pb.JoinRequest) (*pb.JoinResponse, error) {
	f.mu.Lock()
	f.joins = append(f.joins, req)
	f.mu.Unlock()
	return &pb.JoinResponse{Error: "recorded"}, nil
}

// The joining side says its code knows agents; a cluster with agents refuses
// a join that does not.
func TestJoinEngineSaysItKnowsAgents(t *testing.T) {
	_, ca := setupCertDir(t)
	caPEM, err := ca.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	f := &joinRecorder{caKey: caCert.RawSubjectPublicKeyInfo}
	addr := serveFake(t, tlsWithRole(t, ca, "leader", CertRoleManager), f, nil)

	e := &JoinEngine{Config: &config.Config{}}
	e.Config.Cluster.GRPCPort = freePortPair(t)
	e.Config.Cluster.CertDir = filepath.Join(t.TempDir(), "certs")
	if _, err := e.Execute(addr, "a-token", "127.0.0.1", CAPin(f.caKey)); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("the join did not reach the leader: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.joins) != 1 || !slices.Contains(f.joins[0].Capabilities, CapAgents) {
		t.Fatalf("join requests %v", f.joins)
	}
}

// Every manager reports CapAgents on its heartbeat, whatever the state of the
// proxy secret: it is a property of the code.
func TestManagersReportThatTheyKnowAgents(t *testing.T) {
	_, tlsMgr := setupCertDir(t)
	m := proxyTestManager(t, tlsMgr, filepath.Join(t.TempDir(), "raft"), true)
	if caps := m.heartbeatCapabilities(); !slices.Contains(caps, CapAgents) {
		t.Fatalf("reports %v", caps)
	}
}
