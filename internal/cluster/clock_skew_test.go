package cluster

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// lockedBuffer is a log destination a test reads while goroutines left over
// from other tests in this package may still be logging to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs sends the default logger to a buffer until the test ends.
func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	var buf lockedBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// clockWarnings counts the lines that warn of node id's clock. Other lines
// may be there: anything else in the process logs to the same place.
func clockWarnings(logs *lockedBuffer, id string) int {
	n := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "clock") && (strings.Contains(line, " node_id="+id+" ") || strings.HasSuffix(line, " node_id="+id)) {
			n++
		}
	}
	return n
}

// A node more than thirty seconds off the leader's clock has every streamed
// relay to it refused, and nothing said why. The leader sees it in the
// pings: their time is the sender's clock.
func TestTheLeaderWarnsOfAClockFarOff(t *testing.T) {
	logs := captureLogs(t)
	m := &Manager{}
	m.noteClockSkew("near", time.Now().Add(-5*time.Second).Unix())
	if clockWarnings(logs, "near") != 0 {
		t.Fatalf("warned for 5 s: %s", logs.String())
	}
	m.noteClockSkew("far", time.Now().Add(-45*time.Second).Unix())
	if clockWarnings(logs, "far") != 1 {
		t.Fatalf("no warning for 45 s: %q", logs.String())
	}
	m.noteClockSkew("far", time.Now().Add(-45*time.Second).Unix())
	if clockWarnings(logs, "far") != 1 {
		t.Fatal("warned again at once")
	}
	// A clock ahead is as far off as one behind.
	m.noteClockSkew("ahead", time.Now().Add(45*time.Second).Unix())
	if clockWarnings(logs, "ahead") != 1 {
		t.Fatalf("no warning for a clock 45 s ahead: %q", logs.String())
	}
	// A ping with no time says nothing about its sender's clock.
	m.noteClockSkew("untimed", 0)
	if clockWarnings(logs, "untimed") != 0 {
		t.Fatalf("warned for a ping with no time: %s", logs.String())
	}
}

// The warning comes from the heartbeats themselves: a manager warns of the
// node whose ping carries a time far from its own clock.
func TestAHeartbeatFromAFarOffClockIsWarnedOf(t *testing.T) {
	logs := captureLogs(t)
	fsm := NewFSM()
	addTestNode(t, fsm, Node{ID: "agent-1", Kind: KindAgent})
	s := &GRPCServer{manager: &Manager{raft: &RaftNode{fsm: fsm}, heartbeat: NewHeartbeatManager(DefaultHeartbeatInterval, DefaultHeartbeatTimeout)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &scriptedHeartbeatStream{ctx: ctxWithPeerCN(ctx, "agent-1"), in: make(chan *pb.HeartbeatPing, 1)}
	done := make(chan error, 1)
	go func() { done <- s.AgentHeartbeat(stream) }()

	stream.in <- &pb.HeartbeatPing{NodeId: "agent-1", Timestamp: time.Now().Add(-45 * time.Second).Unix()}
	for i := 0; i < 100 && stream.pongCount() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if stream.pongCount() == 0 {
		t.Fatal("no pong for a member")
	}
	if clockWarnings(logs, "agent-1") != 1 {
		t.Fatalf("a ping 45 s behind was not warned of: %q", logs.String())
	}
	cancel()
	<-done
}
