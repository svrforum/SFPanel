package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// TestDeliverToChannels_ConfigItCannotReadIsNotDelivered: a channel whose
// config did not parse, or lacked the field its type needs, came back as
// delivered — nothing was sent, so there was no send error, and no error was
// all the loop checked. History then listed the channel among those that
// received the alert and the cooldown started, for a send that never
// happened. The reachable channel is the control: it is looked up and sent
// to through the same path, so the others are missing for their config alone.
func TestDeliverToChannels_ConfigItCannotReadIsNotDelivered(t *testing.T) {
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&posts, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	db := openMigratedDB(t)
	ids := []int64{
		insertChannel(t, db, "reachable", "webhook", `{"webhook_url":"`+srv.URL+`"}`, 1),
		insertChannel(t, db, "no-url", "webhook", `{}`, 1),
		insertChannel(t, db, "not-an-object", "discord", `"{\"webhook_url\":\"https://discord.com/api/webhooks/000000/test\"}"`, 1),
		insertChannel(t, db, "no-chat-id", "telegram", `{"bot_token":"000000:test-token"}`, 1),
	}

	m := NewManager(db)
	sent := m.deliverToChannels(AlertFire{
		RuleName:   "r",
		Severity:   "warning",
		Message:    "m",
		ChannelIDs: fmt.Sprintf("[%d,%d,%d,%d]", ids[0], ids[1], ids[2], ids[3]),
	})
	if want := []string{"reachable"}; !reflect.DeepEqual(sent, want) {
		t.Errorf("delivered to %q, want %q", sent, want)
	}
	if got := atomic.LoadInt32(&posts); got != 1 {
		t.Errorf("receiver got %d posts, want 1", got)
	}
}
