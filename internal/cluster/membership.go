package cluster

import (
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// A refusal carrying this marker came from the leader: the caller is not
// in the cluster's replicated state and will not be.
const NotMemberRemovedMarker = "removed from the cluster"

// A follower that does not know the caller yet says this instead; the
// caller should try the leader before concluding anything.
const NotMemberYetMarker = "not a member of this cluster yet"

// notMemberError is the refusal for a caller this node's FSM does not list.
// Only a leader whose FSM has caught up may call it removed: a follower, or a
// leader still applying what its predecessors committed, may just not have
// the record yet, and an agent told it was removed stops for good. The FSM is
// read again after the catch-up, which may have brought the record in. The
// leader notes whom it told, for a disband waiting on its agents.
func notMemberError(m *Manager, cn string) error {
	if m != nil && m.raft.caughtUpLeader(2*time.Second) && m.raft.GetFSM().GetNode(cn) == nil {
		m.noteToldRemoved(cn)
		return status.Errorf(codes.PermissionDenied, "%s is not a member of this cluster: %s", cn, NotMemberRemovedMarker)
	}
	return status.Errorf(codes.PermissionDenied, "%s is %s (a node that has just joined learns its peers within seconds; try the leader)", cn, NotMemberYetMarker)
}

// managerInfos lists the managers for a heartbeat pong: an agent learns from
// it whom to accept requests from and where to send its heartbeats once the
// leader moves. Sorted, so an unchanged list compares equal from one pong to
// the next.
func (m *Manager) managerInfos() []*pb.NodeInfo {
	if m == nil || m.raft == nil {
		return nil
	}
	var out []*pb.NodeInfo
	for _, n := range m.raft.GetFSM().GetState().Nodes {
		if !n.IsManager() {
			continue
		}
		out = append(out, &pb.NodeInfo{Id: n.ID, Name: n.Name, ApiAddress: n.APIAddress, GrpcAddress: n.GRPCAddress, Role: string(n.Role), Status: string(n.Status), Kind: string(KindManager)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}
