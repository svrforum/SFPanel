package alert

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestFire_NonBlockingAndDedupesInFlight verifies the C4/H13 fix: Fire must
// not block its caller on a slow channel send (the docker-events listener and
// the evaluate ticker both call it on latency-sensitive goroutines), and two
// concurrent fires for the same rule must not both dispatch (cooldown + the
// in-flight reservation are checked atomically).
func TestFire_NonBlockingAndDedupesInFlight(t *testing.T) {
	m := NewManager(openAlertTestDB(t))

	var delivered int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	m.deliver = func(f AlertFire) []string {
		atomic.AddInt32(&delivered, 1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // block until the test lets the send finish
		return []string{"stub"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	go m.Start(ctx)
	defer func() { close(release); cancel(); m.Stop() }()

	fire := AlertFire{RuleID: 7, RuleName: "r", Type: "cpu", ChannelIDs: "[1]", Cooldown: 60}

	// Fire must return promptly even though deliver blocks indefinitely.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Fire(context.Background(), fire) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Fire blocked on a slow deliver — caller goroutine was not freed")
	}

	// Exactly one dispatch should be in flight; the second fire is deduped.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no dispatch worker picked up the fire")
	}
	time.Sleep(100 * time.Millisecond) // give a (wrongly) second dispatch a chance
	if got := atomic.LoadInt32(&delivered); got != 1 {
		t.Fatalf("expected exactly 1 in-flight dispatch, got %d", got)
	}
}
