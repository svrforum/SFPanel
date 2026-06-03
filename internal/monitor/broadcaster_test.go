package monitor

import (
	"testing"
	"time"
)

// recvSample waits for one non-nil sample (or reports closed/timeout).
func recvSample(ch <-chan *Metrics) bool {
	select {
	case m, ok := <-ch:
		return ok && m != nil
	case <-time.After(2 * time.Second):
		return false
	}
}

// drainUntilClosed reads buffered samples then reports whether the channel
// closed. A subscribe channel buffers 1, so this returns within a couple reads.
func drainUntilClosed(ch <-chan *Metrics) bool {
	for i := 0; i < 10; i++ {
		select {
		case _, ok := <-ch:
			if !ok {
				return true
			}
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}
	return false
}

func TestMetricsBroadcaster_FanOutAndStop(t *testing.T) {
	broadcaster.mu.Lock()
	old := broadcastInterval
	broadcastInterval = 10 * time.Millisecond
	broadcaster.mu.Unlock()
	defer func() {
		broadcaster.mu.Lock()
		broadcastInterval = old
		broadcaster.mu.Unlock()
	}()

	ch1, unsub1 := SubscribeMetrics()
	ch2, unsub2 := SubscribeMetrics()

	// Both subscribers receive a sample from the single shared sampler.
	if !recvSample(ch1) {
		t.Fatal("subscriber 1 received no sample")
	}
	if !recvSample(ch2) {
		t.Fatal("subscriber 2 received no sample")
	}

	// Sampler runs while at least one subscriber is attached.
	broadcaster.mu.Lock()
	running := broadcaster.running
	broadcaster.mu.Unlock()
	if !running {
		t.Fatal("sampler should be running while subscribers are attached")
	}

	unsub1()
	unsub2()

	// After the last subscriber leaves, the sampler stops on the next tick.
	deadline := time.Now().Add(2 * time.Second)
	for {
		broadcaster.mu.Lock()
		running = broadcaster.running
		broadcaster.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sampler did not stop after all subscribers left")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Unsubscribed channels are closed.
	if !drainUntilClosed(ch1) {
		t.Error("ch1 was not closed after unsubscribe")
	}
}

// TestMetricsBroadcaster_DoubleUnsubscribe verifies the unsubscribe func is
// idempotent (safe to defer + call explicitly) and doesn't double-close.
func TestMetricsBroadcaster_DoubleUnsubscribe(t *testing.T) {
	broadcaster.mu.Lock()
	old := broadcastInterval
	broadcastInterval = 10 * time.Millisecond
	broadcaster.mu.Unlock()
	defer func() {
		broadcaster.mu.Lock()
		broadcastInterval = old
		broadcaster.mu.Unlock()
	}()

	_, unsub := SubscribeMetrics()
	unsub()
	unsub() // must not panic (no double close)
}
