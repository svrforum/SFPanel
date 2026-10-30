package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// A manager that stops closes its heartbeat stream at once, while its last
// ping stays inside the timeout for three minutes: a JWT rotation started two
// minutes after it stopped passed its gate on the rig. What an operator
// starts asks whether the manager is up now.
func TestAManagerWhoseStreamEndedIsDownForARotation(t *testing.T) {
	mgr, _ := jwtLeader(t)
	addTestNode(t, mgr.raft.GetFSM(), Node{ID: "m2", Name: "m2", Kind: KindManager, Role: RoleVoter})
	s := &GRPCServer{manager: mgr}
	// open runs one heartbeat stream from m2 until the returned stop.
	open := func() (stop func()) {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &scriptedHeartbeatStream{ctx: ctxWithPeerCN(ctx, "m2"), in: make(chan *pb.HeartbeatPing, 1)}
		done := make(chan error, 1)
		go func() { done <- s.Heartbeat(stream) }()
		stream.in <- &pb.HeartbeatPing{NodeId: "m2", Version: "test", Capabilities: []string{CapAgents, CapJWTRotation}, Timestamp: time.Now().Unix()}
		waitFor(t, "a pong to m2", func() bool { return stream.pongCount() > 0 })
		return func() {
			cancel()
			<-done
		}
	}

	stop := open()
	if _, err := mgr.RotateJWTKey(); err != nil {
		t.Fatalf("with m2 up: %v", err)
	}
	stop()
	if mgr.GetHeartbeat().CheckHealth()["m2"] != StatusOnline {
		t.Fatal("m2's last ping should still be inside the timeout")
	}
	if _, err := mgr.RotateJWTKey(); !errors.Is(err, ErrRotationRefused) || !strings.Contains(err.Error(), "m2 (offline)") {
		t.Fatalf("with m2 stopped a moment ago: %v", err)
	}
	if v := mgr.jwtRotationStatus(mgr.raft.GetFSM().GetState()); !strings.Contains(managerViews(v), "m2 online=false") {
		t.Fatalf("the rotation status beside the refusal says otherwise: %s", managerViews(v))
	}

	defer open()()
	if _, err := mgr.RotateJWTKey(); err != nil {
		t.Fatalf("with m2 back: %v", err)
	}
}

func managerViews(st JWTRotationStatus) string {
	var b strings.Builder
	for _, v := range st.Managers {
		b.WriteString(v.Name)
		if v.Online {
			b.WriteString(" online=true; ")
		} else {
			b.WriteString(" online=false; ")
		}
	}
	return b.String()
}

// A node that reconnects opens its new stream before its old one ends, or
// after: neither is an outage once it has pinged on the new one.
func TestAnEndedStreamBesideAnOpenOneIsNoOutage(t *testing.T) {
	hm := NewHeartbeatManager(time.Second, time.Minute)
	hm.RecordHeartbeat(&NodeMetrics{NodeID: "m2"})
	hm.StreamOpened("m2")
	hm.RecordHeartbeat(&NodeMetrics{NodeID: "m2"})
	hm.StreamOpened("m2")
	hm.StreamEnded("m2")
	if got := hm.LiveHealth()["m2"]; got != StatusOnline {
		t.Fatalf("the old stream ending beside the new one: %s", got)
	}
	hm.StreamEnded("m2")
	if got := hm.LiveHealth()["m2"]; got != StatusOffline {
		t.Fatalf("with no stream left: %s", got)
	}
	if got := hm.CheckHealth()["m2"]; got != StatusOnline {
		t.Fatalf("the node list's health changed too: %s", got)
	}
	hm.RecordHeartbeat(&NodeMetrics{NodeID: "m2"})
	if got := hm.LiveHealth()["m2"]; got != StatusOnline {
		t.Fatalf("after a ping on a new stream: %s", got)
	}
}

// A leave counts the voters that would remain by the same measure: a voter
// stopped a moment ago is not one of them.
func TestALeaveDoesNotCountAVoterWhoseStreamEnded(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	hb := mgr.GetHeartbeat()
	for _, id := range []string{"m2", "m3"} {
		addTestNode(t, mgr.raft.GetFSM(), Node{ID: id, Name: id, Kind: KindManager, Role: RoleVoter})
		hb.RecordHeartbeat(&NodeMetrics{NodeID: id})
		hb.StreamOpened(id)
	}
	if reason, refuse := mgr.WouldDropBelowQuorumOnLeave(); refuse {
		t.Fatalf("with both up: %s", reason)
	}
	hb.StreamEnded("m3")
	if reason, refuse := mgr.WouldDropBelowQuorumOnLeave(); !refuse || !strings.Contains(reason, "only 1 of 2 remaining voters reachable") {
		t.Fatalf("with m3 stopped a moment ago: %q %v", reason, refuse)
	}
}
