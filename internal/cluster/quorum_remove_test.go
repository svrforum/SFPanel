package cluster

import (
	"strings"
	"testing"
	"time"
)

func TestWouldDropBelowQuorumOnRemove(t *testing.T) {
	cases := []struct {
		voters     int
		wantBlocks bool
		label      string
	}{
		{1, true, "1-voter cluster: removing the only voter destroys the cluster"},
		{2, true, "2-voter cluster: dropping to 1 voter loses any fault tolerance and only the next click bricks the cluster"},
		{3, false, "3-voter cluster: 2 remaining still has quorum (2 of 3 is N/2+1) — no fault tolerance left, but not below quorum"},
		{4, false, "4-voter cluster: 3 remaining still has quorum (3 of 4)"},
		{5, false, "5-voter cluster: 4 remaining still has quorum (3 of 5)"},
		{0, false, "no voters at all — nothing to enforce, fail open"},
	}
	for _, c := range cases {
		msg, blocks := wouldDropBelowQuorumOnRemove("test-node", c.voters)
		if blocks != c.wantBlocks {
			t.Errorf("%s: got blocks=%v want %v (msg=%q)", c.label, blocks, c.wantBlocks, msg)
		}
		if blocks && msg == "" {
			t.Errorf("%s: blocked without a message", c.label)
		}
	}
}

// Of two voters, removing the other leaves a lone voter: refused, whatever
// this leader's heartbeat map says of it. A stream that ended — a network
// blip, a reconnect — or a map a restarted leader has not filled yet does not
// make a voter that is up removable without --force; the node list shows it
// online all the while. One that is really down cannot be removed at all:
// of two, the leader steps down without it.
func TestARemoveRefusesTheOtherOfTwoVotersWhateverItsHeartbeat(t *testing.T) {
	leader, followers := healthyManagers(t, 2)
	other := followers[0].nodeID
	const want = "would drop the cluster below quorum (1/2 voters; quorum 2)"

	if msg, refused := leader.WouldDropBelowQuorumOnRemove(other); !refused || !strings.Contains(msg, want) {
		t.Fatalf("its stream open: refused=%v %q", refused, msg)
	}
	leader.heartbeat.StreamEnded(other)
	if msg, refused := leader.WouldDropBelowQuorumOnRemove(other); !refused || !strings.Contains(msg, want) {
		t.Fatalf("its stream ended: refused=%v %q", refused, msg)
	}
	leader.heartbeat.RemoveNode(other)
	if msg, refused := leader.WouldDropBelowQuorumOnRemove(other); !refused || !strings.Contains(msg, want) {
		t.Fatalf("never heard from: refused=%v %q", refused, msg)
	}
}

// What a peers.json recovery leaves once a voter is lost: the survivors in
// the Raft configuration, and in the FSM the lost voter's record beside
// theirs. Counted from the records, the lost voter's remove was refused as if
// it still took a vote when one voter survived, and with two, a survivor's
// was let through as one of three — leaving a lone voter. The configuration
// is what a quorum is counted from: the record is no voter there, whatever
// its heartbeat, and a survivor is one of two.
func TestARemoveCountsTheVotersOfTheRaftConfiguration(t *testing.T) {
	for _, survivors := range []int{1, 2} {
		leader, followers := healthyManagers(t, survivors)
		lost := Node{ID: "lost", Name: "lost", Kind: KindManager, Role: RoleVoter, Status: StatusOnline}
		if err := leader.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(lost)}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		leader.heartbeat.RecordHeartbeat(&NodeMetrics{NodeID: lost.ID})

		if msg, refused := leader.WouldDropBelowQuorumOnRemove(lost.ID); refused {
			t.Fatalf("%d surviving: removing the lost voter's record: refused %q", survivors, msg)
		}
		for _, f := range followers {
			if msg, refused := leader.WouldDropBelowQuorumOnRemove(f.nodeID); !refused || !strings.Contains(msg, "(1/2 voters; quorum 2)") {
				t.Fatalf("%d surviving: removing a survivor: refused=%v %q", survivors, refused, msg)
			}
		}
	}
}
