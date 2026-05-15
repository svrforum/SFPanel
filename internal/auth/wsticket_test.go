package auth

import (
	"sync"
	"testing"
	"time"
)

func TestMintAndConsumeWSTicket_HappyPath(t *testing.T) {
	tok := MintWSTicket("alice")
	if len(tok) != wsTicketBytes*2 {
		t.Errorf("ticket length = %d, want %d", len(tok), wsTicketBytes*2)
	}
	got, ok := ConsumeWSTicket(tok)
	if !ok {
		t.Fatal("ConsumeWSTicket(fresh ticket) returned false")
	}
	if got != "alice" {
		t.Errorf("username = %q, want alice", got)
	}
}

// TestConsumeWSTicket_IsSingleUse — the single-use property is the entire
// point of the ticket flow. A second use must fail or an attacker who
// captured the URL gets a free WebSocket handshake.
func TestConsumeWSTicket_IsSingleUse(t *testing.T) {
	tok := MintWSTicket("alice")
	if _, ok := ConsumeWSTicket(tok); !ok {
		t.Fatal("first consume should succeed")
	}
	if _, ok := ConsumeWSTicket(tok); ok {
		t.Error("second consume of the same ticket should fail")
	}
}

func TestConsumeWSTicket_UnknownTicket(t *testing.T) {
	if _, ok := ConsumeWSTicket("not-minted"); ok {
		t.Error("unknown ticket should not validate")
	}
	if _, ok := ConsumeWSTicket(""); ok {
		t.Error("empty ticket should not validate")
	}
}

// TestConsumeWSTicket_ExpiredRejected — overwrite the in-store expiry to
// simulate a stale ticket without sleeping.
func TestConsumeWSTicket_ExpiredRejected(t *testing.T) {
	tok := MintWSTicket("alice")
	wsTicketsMu.Lock()
	wsTickets[tok] = wsTicketEntry{username: "alice", expires: time.Now().Add(-1 * time.Minute)}
	wsTicketsMu.Unlock()
	if _, ok := ConsumeWSTicket(tok); ok {
		t.Error("expired ticket should not validate")
	}
}

// TestMintWSTicket_ConcurrencySafe — exercise the mutex under load. The
// previous implementation used a plain map without sync, which would race
// under N concurrent terminal opens.
func TestMintWSTicket_ConcurrencySafe(t *testing.T) {
	var wg sync.WaitGroup
	tickets := make(chan string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tickets <- MintWSTicket("alice")
		}()
	}
	wg.Wait()
	close(tickets)
	seen := make(map[string]bool)
	for tok := range tickets {
		if tok == "" {
			t.Error("MintWSTicket returned empty under concurrency")
		}
		if seen[tok] {
			t.Errorf("duplicate ticket %q — RNG or map race", tok)
		}
		seen[tok] = true
	}
}
