package cluster

import "fmt"

// wouldDropBelowQuorumOnLeave is the pure quorum-on-leave decision, split out
// so the boundary cases are unit-testable without standing up a live Manager
// with Raft + heartbeat state (there is no harness for that today).
//
// Unlike checkQuorumAfterRemoval in the cluster feature handler — which is a
// pure voter-count check used by RemoveNode — this guard is driven by *live
// heartbeat health*: it counts how many of the surviving voters are actually
// reachable right now, not just how many exist in the FSM configuration. That
// distinction is what catches the orphan case: a 2-voter cluster whose peer is
// already offline still has a quorum-of-1 configuration, but leaving would
// strand a lone voter that can neither elect nor commit.
//
// voters maps every voter ID (self included) to its live status. selfID is the
// departing node. Only StatusOnline counts as healthy; suspect/offline/joining
// (and self's own status) are treated as not contributing to the surviving
// reachable set. Returns an operator-useful reason and true when the leave must
// be refused.
func wouldDropBelowQuorumOnLeave(selfID string, voters map[string]NodeStatus) (string, bool) {
	remaining := 0
	healthyRemaining := 0
	for id, status := range voters {
		if id == selfID {
			continue
		}
		remaining++
		if status == StatusOnline {
			healthyRemaining++
		}
	}

	if remaining == 0 {
		return "no remaining voters; the last voter cannot leave (use disband instead)", true
	}

	quorum := remaining/2 + 1
	if healthyRemaining < quorum {
		return fmt.Sprintf(
			"only %d of %d remaining voters reachable (need %d for quorum)",
			healthyRemaining, remaining, quorum), true
	}
	return "", false
}

// WouldDropBelowQuorumOnLeave reports whether this node leaving the cluster
// would leave the surviving voters unable to form quorum. It returns an
// operator-useful reason and true when the leave should be refused.
//
// With no Raft attached there is nothing to protect, so it fails open
// (returns "", false). It mirrors pickTransferTarget's enumeration: read the
// FSM node set and keep only voters. The leader resolves each one's status
// from its heartbeat streams (LiveHealth; a voter absent from it — one that
// never reported — is offline). A follower cannot: managers ping only the
// leader, so its own map holds itself alone, and every follower's leave was
// refused as if the others were down. It reads the status the leader keeps
// in each record instead, which the leader's heartbeat monitor writes on
// every change: as fresh as the node list, so a manager stopped a moment ago
// still counts for up to a few minutes, as it did for the leader before its
// streams were counted. A record with no status is offline.
func (m *Manager) WouldDropBelowQuorumOnLeave() (string, bool) {
	if m.raft == nil {
		return "", false
	}
	state := m.raft.GetFSM().GetState()
	var health map[string]NodeStatus
	if m.raft.IsLeader() {
		health = m.heartbeat.LiveHealth()
	}

	voters := make(map[string]NodeStatus, len(state.Nodes))
	for id, node := range state.Nodes {
		if node.Role != RoleVoter {
			continue
		}
		status, ok := node.Status, node.Status != ""
		if health != nil {
			status, ok = health[id]
		}
		if !ok {
			status = StatusOffline
		}
		voters[id] = status
	}
	return wouldDropBelowQuorumOnLeave(m.nodeID, voters)
}
