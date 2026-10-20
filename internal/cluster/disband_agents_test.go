package cluster

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// startQuietLink runs a link that pings once and then not for an hour: what
// it learns after that, it learns without pinging.
func startQuietLink(t *testing.T, mgr *Manager, addr string, res *RegisterAgentResult, tm *TLSManager) (removed <-chan struct{}) {
	t.Helper()
	link, err := NewAgentLink(AgentLinkConfig{
		NodeID: res.NodeID, TLS: tm, StatePath: filepath.Join(t.TempDir(), "agent-state.json"),
		Interval: time.Hour,
	}, []config.ManagerRef{{ID: mgr.LocalNodeID(), GRPCAddress: addr}})
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{})
	link.SetOnRemoved(func() { close(ch) })
	goRun(t, link)
	return ch
}

// An agent pings every thirty seconds, so it learned of its removal up to
// thirty seconds later — and a disband, whose managers are gone a moment
// after, could not wait for that. The leader now ends a removed node's open
// stream itself.
func TestARemovedAgentHearsItWithoutPinging(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	removed := startQuietLink(t, mgr, addr, res, tm)
	waitFor(t, "the agent's one ping", func() bool { return mgr.GetHeartbeat().GetMetrics(res.NodeID) != nil })
	if err := mgr.RemoveNode(res.NodeID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-removed:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not learn of its removal without pinging")
	}
}

// A manager's stream gets the same check. Removed, its open stream ends
// without a ping, and it cannot come back in by opening another: the door
// refuses it before any ping of its could promote it.
func TestARemovedManagersStreamEndsWithoutAPing(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Kind: KindManager, Role: RoleNonVoter})
	s := &GRPCServer{manager: mgr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &scriptedHeartbeatStream{ctx: ctxWithPeerCN(ctx, "m2"), in: make(chan *pb.HeartbeatPing, 1)}
	done := make(chan error, 1)
	go func() { done <- s.Heartbeat(stream) }()

	applyCmd(t, mgr.raft.GetFSM(), Command{Type: CmdRemoveNode, Key: "m2"})
	select {
	case err := <-done:
		if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), NotMemberRemovedMarker) {
			t.Fatalf("the removed manager's stream ended with: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the removed manager's stream stayed open")
	}
	if err := s.authorizeCaller(ctxWithPeerCert("m2", CertRoleManager), methodHeartbeat); status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), NotMemberRemovedMarker) {
		t.Fatalf("a removed manager opening a new stream: %v", err)
	}
}

// A disband reaches the managers only: Raft knows no agents. The leader takes
// every agent out first, the one word an agent understands, and says which
// ones it could not tell — they need 'sfpanel agent reset' by hand.
func TestRemoveAgentsForDisband(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	removed := startQuietLink(t, mgr, addr, res, tm)
	waitFor(t, "the agent's one ping", func() bool { return mgr.GetHeartbeat().GetMetrics(res.NodeID) != nil })
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "a-off", Name: "offline-agent", Kind: KindAgent})

	notTold, err := mgr.RemoveAgentsForDisband(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(notTold, []string{"offline-agent"}) {
		t.Fatalf("not told: %v", notTold)
	}
	// The leader counts an agent told as it sends the refusal; the agent acts
	// on it a fraction of a millisecond later.
	select {
	case <-removed:
	case <-time.After(time.Second):
		t.Fatal("the online agent was not told")
	}
	for _, id := range []string{res.NodeID, "a-off"} {
		if mgr.raft.GetFSM().GetNode(id) != nil {
			t.Fatalf("%s is still a member", id)
		}
	}
}
