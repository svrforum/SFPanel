package cluster

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// scriptedHeartbeatStream yields the pings pushed on in, and keeps the pongs.
type scriptedHeartbeatStream struct {
	grpc.ServerStream
	ctx   context.Context
	in    chan *pb.HeartbeatPing
	mu    sync.Mutex
	pongs []*pb.HeartbeatPong
}

func (s *scriptedHeartbeatStream) Context() context.Context { return s.ctx }
func (s *scriptedHeartbeatStream) Send(p *pb.HeartbeatPong) error {
	s.mu.Lock()
	s.pongs = append(s.pongs, p)
	s.mu.Unlock()
	return nil
}
func (s *scriptedHeartbeatStream) Recv() (*pb.HeartbeatPing, error) {
	select {
	case p := <-s.in:
		return p, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}
func (s *scriptedHeartbeatStream) pongCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pongs)
}

// A node removed while its stream is open must be refused on its next ping,
// by the leader, with the marker an agent acts on.
func TestHeartbeatRefusesARemovedNodeOnItsNextPing(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "agent-1", Kind: KindAgent})
	s := &GRPCServer{manager: mgr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &scriptedHeartbeatStream{ctx: ctxWithPeerCN(ctx, "agent-1"), in: make(chan *pb.HeartbeatPing, 1)}
	done := make(chan error, 1)
	go func() { done <- s.AgentHeartbeat(stream) }()

	stream.in <- &pb.HeartbeatPing{NodeId: "agent-1", Timestamp: time.Now().Unix()}
	for i := 0; i < 100 && stream.pongCount() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if stream.pongCount() == 0 {
		t.Fatal("no pong for a member")
	}
	p := stream.pongs[0]
	if !p.FromLeader || p.LeaderGrpcAddress == "" || len(p.Managers) != 1 || p.Managers[0].Id != mgr.LocalNodeID() || p.Managers[0].Kind != string(KindManager) {
		t.Fatalf("pong does not say where the leader is and who the managers are: %+v", p)
	}

	applyCmd(t, mgr.raft.GetFSM(), Command{Type: CmdRemoveNode, Key: "agent-1"})
	stream.in <- &pb.HeartbeatPing{NodeId: "agent-1", Timestamp: time.Now().Unix()}
	select {
	case err := <-done:
		st, _ := status.FromError(err)
		if st.Code() != codes.PermissionDenied || !strings.Contains(st.Message(), NotMemberRemovedMarker) {
			t.Fatalf("removed node's ping: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the removed node's stream was still accepted")
	}
	// Reconnecting does not get it back in: a new stream is refused at the
	// door, with the same marker.
	if err := s.authorizeCaller(ctxWithPeerCert("agent-1", CertRoleAgent), methodAgentBeat); status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), NotMemberRemovedMarker) {
		t.Fatalf("a removed node opening a new stream: %v", err)
	}
}

// A follower refuses an unknown caller with the other marker.
func TestNotMemberErrorNamesWhoSaidIt(t *testing.T) {
	follower := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if err := notMemberError(follower, "x"); !strings.Contains(err.Error(), NotMemberYetMarker) || strings.Contains(err.Error(), NotMemberRemovedMarker) {
		t.Fatalf("follower refusal: %v", err)
	}
}

// gatedFSM holds every Apply until gate is closed: a leader replaying a long
// log, made to last.
type gatedFSM struct {
	*FSM
	gate chan struct{}
}

func (g *gatedFSM) Apply(l *raft.Log) interface{} {
	<-g.gate
	return g.FSM.Apply(l)
}

// A leader applies what was committed before its term only once it commits
// an entry of its own, so after a restart it leads for a moment with an FSM
// that lacks its members (the FSM then holds just the last snapshot). Told
// "removed" in that moment, an agent stops for good. Once caught up, the
// leader's answer is final again — and costs no further Raft write.
func TestALeaderReplayingItsLogDoesNotCallAMemberRemoved(t *testing.T) {
	conf := raft.DefaultConfig()
	conf.LocalID = "m1"
	conf.HeartbeatTimeout = 50 * time.Millisecond
	conf.ElectionTimeout = 50 * time.Millisecond
	conf.LeaderLeaseTimeout = 50 * time.Millisecond
	conf.CommitTimeout = 5 * time.Millisecond
	conf.LogLevel = "ERROR"
	logs, snaps := raft.NewInmemStore(), raft.NewInmemSnapshotStore()
	addr, trans := raft.NewInmemTransport("m1")
	if err := raft.BootstrapCluster(conf, logs, logs, snaps, trans, raft.Configuration{Servers: []raft.Server{{ID: "m1", Address: addr}}}); err != nil {
		t.Fatal(err)
	}
	// What the previous run committed: this manager, then an agent.
	for i, n := range []Node{{ID: "m1", Kind: KindManager, Role: RoleVoter}, {ID: "agent-1", Kind: KindAgent}} {
		node, _ := json.Marshal(n)
		cmd, _ := json.Marshal(Command{Type: CmdAddNode, Value: node})
		if err := logs.StoreLog(&raft.Log{Index: uint64(i + 2), Term: 1, Type: raft.LogCommand, Data: cmd}); err != nil {
			t.Fatal(err)
		}
	}
	fsm := &gatedFSM{FSM: NewFSM(), gate: make(chan struct{})}
	var openGate sync.Once
	r, err := raft.NewRaft(conf, fsm, logs, logs, snaps, trans)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		openGate.Do(func() { close(fsm.gate) })
		_ = r.Shutdown().Error()
	})
	m := &Manager{raft: &RaftNode{raft: r, fsm: fsm.FSM}}
	for deadline := time.Now().Add(5 * time.Second); r.State() != raft.Leader; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no leader")
		}
	}
	if fsm.GetNode("agent-1") != nil {
		t.Fatal("the agent is already applied: the test would prove nothing")
	}

	got := make(chan error, 1)
	go func() { got <- notMemberError(m, "agent-1") }()
	var refusal error
	select {
	case refusal = <-got:
	case <-time.After(200 * time.Millisecond): // still waiting for the replay
	}
	openGate.Do(func() { close(fsm.gate) })
	if refusal == nil {
		select {
		case refusal = <-got:
		case <-time.After(5 * time.Second):
			t.Fatal("no answer once the log was replayed")
		}
	}
	if strings.Contains(refusal.Error(), NotMemberRemovedMarker) || !strings.Contains(refusal.Error(), NotMemberYetMarker) {
		t.Fatalf("a leader still replaying its log refused a member as: %v", refusal)
	}
	if fsm.GetNode("agent-1") == nil {
		t.Fatal("the replay never applied the agent")
	}

	before := r.LastIndex()
	if err := notMemberError(m, "stranger"); !strings.Contains(err.Error(), NotMemberRemovedMarker) {
		t.Fatalf("a caught-up leader refused a stranger as: %v", err)
	}
	if after := r.LastIndex(); after != before {
		t.Fatalf("a refusal in a term already caught up wrote to the Raft log (index %d → %d): a removed node could make every voter write per call", before, after)
	}
}
