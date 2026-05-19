package db

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAsyncWriter_DrainsInOrderUntilCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aw := NewAsyncWriter(ctx, nil, "test", 16)

	var mu sync.Mutex
	var seen []int

	for i := 0; i < 10; i++ {
		n := i
		ok := aw.Submit(func(_ *sql.DB) {
			mu.Lock()
			seen = append(seen, n)
			mu.Unlock()
		})
		if !ok {
			t.Fatalf("Submit(%d) returned false (queue full?)", n)
		}
	}

	// Cancel — Wait must drain the rest before returning.
	cancel()
	aw.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 10 {
		t.Fatalf("got %d closures, want 10", len(seen))
	}
	for i, v := range seen {
		if v != i {
			t.Errorf("seen[%d] = %d, want %d (out of order)", i, v, i)
		}
	}
}

func TestAsyncWriter_OverflowDropsAndReports(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Block the drain goroutine so the queue fills.
	block := make(chan struct{})
	released := false
	aw := NewAsyncWriter(ctx, nil, "test", 2)
	aw.Submit(func(_ *sql.DB) {
		<-block
		released = true
	})

	// Fill the queue (cap=2 — the first closure is sitting in the drain
	// goroutine, so cap-1 = 1 more fits in the channel buffer alongside.
	// Push extras and confirm exactly one fits before drops start.
	enqueued := 0
	for i := 0; i < 10; i++ {
		if aw.Submit(func(_ *sql.DB) {}) {
			enqueued++
		}
	}
	if enqueued < 1 || enqueued > 3 {
		t.Errorf("expected 1-3 enqueued after blocking drain, got %d", enqueued)
	}

	close(block)
	cancel()
	aw.Wait()
	if !released {
		t.Error("blocked closure was never released")
	}
}

func TestAsyncWriter_NilSubmitNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aw := NewAsyncWriter(ctx, nil, "test", 4)

	if aw.Submit(nil) {
		t.Error("Submit(nil) should return false")
	}

	cancel()
	done := make(chan struct{})
	go func() { aw.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Wait did not return after ctx cancel")
	}
}

func TestAsyncWriter_NilReceiverNoop(t *testing.T) {
	var aw *AsyncWriter
	if aw.Submit(func(_ *sql.DB) { t.Error("should not run") }) {
		t.Error("nil AsyncWriter Submit should return false")
	}
	aw.Wait() // must not panic
}

// Ensure the panic recover keeps the drain goroutine alive across a
// misbehaving closure.
func TestAsyncWriter_RecoversFromPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aw := NewAsyncWriter(ctx, nil, "test", 4)
	aw.Submit(func(_ *sql.DB) { panic("boom") })

	var ran int32
	aw.Submit(func(_ *sql.DB) { atomic.StoreInt32(&ran, 1) })

	cancel()
	aw.Wait()
	if atomic.LoadInt32(&ran) != 1 {
		t.Error("second closure should run after first panicked")
	}
}
