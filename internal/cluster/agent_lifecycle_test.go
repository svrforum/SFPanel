package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// An agent reports its own panel CA; the node is its certificate's CN.
func TestReportPanelCAStoresForTheCallerOnly(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "agent-1", Kind: KindAgent, APIAddress: "192.0.2.10:3628"})
	s := &GRPCServer{manager: mgr}
	resp, err := s.ReportPanelCA(ctxWithPeerCN(context.Background(), "agent-1"), &pb.ReportPanelCARequest{CaCert: panelCAPEM(t)})
	if err != nil || !resp.Success {
		t.Fatalf("report: %v %+v", err, resp)
	}
	if ep, err := mgr.PanelEndpointChecked(mgr.raft.GetFSM().GetNode("agent-1")); err != nil || ep.TLSConfig == nil {
		t.Fatalf("not stored: %v", err)
	}
	_, err = s.ReportPanelCA(ctxWithPeerCN(context.Background(), "stranger"), &pb.ReportPanelCARequest{CaCert: panelCAPEM(t)})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a non-member's report: %v", err)
	}
	if mgr.NodeServesTLS("stranger") {
		t.Fatal("a non-member's panel CA was stored")
	}
	if resp, _ := s.ReportPanelCA(ctxWithPeerCN(context.Background(), "agent-1"), &pb.ReportPanelCARequest{CaCert: []byte("not a certificate")}); resp == nil || resp.Success {
		t.Fatal("garbage stored as a panel CA")
	}
}

// An agent may report its panel CA whenever it likes, and every change is a
// Raft entry on every manager: a change is taken at most once a minute per
// node. Reporting the stored CA again is no change and always answered.
func TestAReportedPanelCAChangesAtMostOncePerMinute(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "agent-1", Kind: KindAgent, APIAddress: "192.0.2.10:3628"})
	s := &GRPCServer{manager: mgr}
	report := func(ca []byte) *pb.ReportPanelCAResponse {
		t.Helper()
		resp, err := s.ReportPanelCA(ctxWithPeerCN(context.Background(), "agent-1"), &pb.ReportPanelCARequest{CaCert: ca})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first, second := panelCAPEM(t), panelCAPEM(t)
	if r := report(first); !r.Success {
		t.Fatalf("first report: %+v", r)
	}
	before := mgr.raft.raft.LastIndex()
	if r := report(second); r.Success || !strings.Contains(r.Error, "less than a minute ago") {
		t.Fatalf("a second change at once: %+v", r)
	}
	if after := mgr.raft.raft.LastIndex(); after != before {
		t.Fatalf("a refused change wrote to the Raft log: index %d → %d", before, after)
	}
	if r := report(first); !r.Success {
		t.Fatalf("the stored CA reported again: %+v", r)
	}
	if err := mgr.SetReportedPanelCA("agent-1", string(second), time.Now().Add(2*time.Minute)); err != nil {
		t.Fatalf("a change a minute later: %v", err)
	}
	if v, _ := mgr.raft.GetFSM().GetConfigValue(panelCAKey("agent-1")); v != string(second) {
		t.Fatal("the later change was not stored")
	}
}

// A follower cannot store a panel CA. It answers with the leader's address,
// where the agent reports instead — SetPanelCA's own refusal carries none.
func TestReportPanelCAToAFollowerNamesTheLeader(t *testing.T) {
	nodes, _ := inmemRaft(t, "m1", "m2")
	leader, follower := nodes[0], nodes[1]
	for _, n := range []Node{{ID: "m1", Kind: KindManager, Role: RoleVoter, GRPCAddress: "198.51.100.1:3629"}, {ID: "agent-1", Kind: KindAgent}} {
		if err := leader.Apply(Command{Type: CmdAddNode, Value: mustJSON(n)}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); follower.GetFSM().GetNode("agent-1") == nil || follower.LeaderID() != "m1"; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the follower did not catch up")
		}
	}
	s := &GRPCServer{manager: &Manager{raft: follower}}
	resp, err := s.ReportPanelCA(ctxWithPeerCN(context.Background(), "agent-1"), &pb.ReportPanelCARequest{CaCert: panelCAPEM(t)})
	if err != nil || resp.Success || resp.Error != ErrNotLeader.Error() || resp.LeaderGrpcAddress != "198.51.100.1:3629" {
		t.Fatalf("a follower's answer: %v %+v", err, resp)
	}
}

// Removing an agent drops its key first (managers can no longer sign for it),
// then its record and panel CA, and never touches Raft — it was never there.
func TestRemoveAgent(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := mgr.HandleRegisterAgent(registerInput(t, tok.Token))
	if err != nil {
		t.Fatal(err)
	}
	before := len(mgr.raft.raft.GetConfiguration().Configuration().Servers)
	if err := mgr.RemoveNode(res.NodeID); err != nil {
		t.Fatal(err)
	}
	if mgr.raft.GetFSM().GetNode(res.NodeID) != nil {
		t.Fatal("record kept")
	}
	if _, err := mgr.SignProxyRequestFor(&Node{ID: res.NodeID, Kind: KindAgent}, http.MethodGet, "/x"); !errors.Is(err, ErrNoAgentKey) {
		t.Fatalf("key kept: %v", err)
	}
	if ep := mgr.PanelEndpointFor(&Node{ID: res.NodeID, APIAddress: "192.0.2.10:3628"}); ep.TLSConfig != nil {
		t.Fatal("panel CA kept as a trust anchor")
	}
	if after := len(mgr.raft.raft.GetConfiguration().Configuration().Servers); after != before {
		t.Fatalf("Raft configuration changed: %d → %d", before, after)
	}
}

// What a removal writes, in order. The key goes first: a removal cut short
// leaves an agent no manager can sign for, never a record gone while its key
// is still replicated. And no Raft configuration entry: RemoveServer of an ID
// Raft does not know still writes one, so the server count above cannot tell.
func TestRemovingAnAgentDropsItsKeyFirst(t *testing.T) {
	nodes, logs := inmemRaft(t, "m1")
	mgr := NewManager(&config.ClusterConfig{NodeID: "m1"})
	mgr.raft = nodes[0]
	t.Cleanup(mgr.Shutdown)
	const id = "agent-1"
	if err := mgr.SetConfig(AgentKeyConfigKey(id), "agent-key"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: id, Kind: KindAgent, GRPCAddress: "192.0.2.10:3629"})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetPanelCA(id, string(panelCAPEM(t))); err != nil {
		t.Fatal(err)
	}
	from, _ := logs[0].LastIndex()
	if err := mgr.RemoveNode(id); err != nil {
		t.Fatal(err)
	}
	to, _ := logs[0].LastIndex()
	var written []Command
	for i := from + 1; i <= to; i++ {
		var l raft.Log
		if err := logs[0].GetLog(i, &l); err != nil {
			t.Fatal(err)
		}
		switch l.Type {
		case raft.LogConfiguration:
			t.Fatalf("removing an agent wrote a Raft configuration (entry %d)", i)
		case raft.LogCommand:
			var cmd Command
			if err := json.Unmarshal(l.Data, &cmd); err != nil {
				t.Fatal(err)
			}
			written = append(written, Command{Type: cmd.Type, Key: cmd.Key})
		}
	}
	want := []Command{
		{Type: CmdDeleteConfig, Key: AgentKeyConfigKey(id)},
		{Type: CmdRemoveNode, Key: id},
		{Type: CmdDeleteConfig, Key: panelCAKey(id)},
	}
	if !slices.EqualFunc(written, want, func(a, b Command) bool { return a.Type == b.Type && a.Key == b.Key }) {
		t.Fatalf("removal wrote %+v, want %+v", written, want)
	}
}

// inmemRaft starts an in-memory Raft group. The first ID is its only voter, so
// it leads at once and stays the leader; the rest follow as non-voters. The
// log stores are returned so a test can read what was written.
func inmemRaft(t *testing.T, ids ...string) ([]*RaftNode, []*raft.InmemStore) {
	t.Helper()
	addrs := make([]raft.ServerAddress, len(ids))
	trans := make([]*raft.InmemTransport, len(ids))
	servers := make([]raft.Server, len(ids))
	for i, id := range ids {
		addrs[i], trans[i] = raft.NewInmemTransport(raft.ServerAddress(id))
		servers[i] = raft.Server{Suffrage: raft.Nonvoter, ID: raft.ServerID(id), Address: addrs[i]}
	}
	servers[0].Suffrage = raft.Voter
	for i := range trans {
		for j := range trans {
			if i != j {
				trans[i].Connect(addrs[j], trans[j])
			}
		}
	}
	nodes := make([]*RaftNode, len(ids))
	stores := make([]*raft.InmemStore, len(ids))
	for i, id := range ids {
		conf := raft.DefaultConfig()
		conf.LocalID = raft.ServerID(id)
		conf.HeartbeatTimeout = 50 * time.Millisecond
		conf.ElectionTimeout = 50 * time.Millisecond
		conf.LeaderLeaseTimeout = 50 * time.Millisecond
		conf.CommitTimeout = 5 * time.Millisecond
		conf.LogLevel = "ERROR"
		logs, snaps := raft.NewInmemStore(), raft.NewInmemSnapshotStore()
		if err := raft.BootstrapCluster(conf, logs, logs, snaps, trans[i], raft.Configuration{Servers: servers}); err != nil {
			t.Fatal(err)
		}
		fsm := NewFSM()
		r, err := raft.NewRaft(conf, fsm, logs, logs, snaps, trans[i])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Shutdown().Error() })
		nodes[i], stores[i] = &RaftNode{raft: r, fsm: fsm}, logs
	}
	for deadline := time.Now().Add(5 * time.Second); !nodes[0].IsLeader(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no leader")
		}
	}
	return nodes, stores
}
