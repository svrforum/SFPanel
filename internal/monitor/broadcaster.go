package monitor

import (
	"sync"
	"time"
)

// broadcastInterval is how often the shared sampler reads system metrics. It's
// a var (not const) so tests can shorten it. Matches the old per-client WS
// cadence (2s) so dashboards refresh at the same rate.
var broadcastInterval = 2 * time.Second

// metricsBroadcaster collapses every dashboard viewer's metrics polling into a
// single sampler. Before this, each /ws/metrics client ran its own 2s ticker
// calling GetMetrics() (≈5 syscalls each), so N viewers meant N×5 syscalls per
// tick. Now one goroutine samples once per interval and fans the result out to
// all subscribers; the sampler only runs while at least one client is attached.
type metricsBroadcasterT struct {
	mu      sync.Mutex
	subs    map[chan *Metrics]struct{}
	latest  *Metrics
	running bool
}

var broadcaster = &metricsBroadcasterT{subs: make(map[chan *Metrics]struct{})}

// SubscribeMetrics registers a subscriber and returns a receive channel that
// gets a *Metrics every broadcastInterval, plus an unsubscribe func the caller
// MUST call (defer) to avoid leaking the channel. The channel is buffered by 1
// and the sampler drops a tick for any subscriber that hasn't drained — a slow
// client never stalls the shared sampler or the other viewers. The most recent
// cached sample (if any) is delivered immediately so a freshly opened dashboard
// paints without waiting a full interval.
func SubscribeMetrics() (<-chan *Metrics, func()) {
	b := broadcaster
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan *Metrics, 1)
	b.subs[ch] = struct{}{}
	if b.latest != nil {
		ch <- b.latest // non-blocking: buffer is empty and size 1
	}
	if !b.running {
		b.running = true
		go b.run()
	}

	var once sync.Once
	return ch, func() {
		once.Do(func() { b.unsubscribe(ch) })
	}
}

func (b *metricsBroadcasterT) unsubscribe(ch chan *Metrics) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}

func (b *metricsBroadcasterT) run() {
	// Read the interval under the lock so a test that adjusts broadcastInterval
	// (also under the lock) never races the sampler's startup.
	b.mu.Lock()
	interval := broadcastInterval
	b.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		// Stop the sampler once the last viewer leaves so an idle panel does no
		// background sampling at all.
		b.mu.Lock()
		if len(b.subs) == 0 {
			b.running = false
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()

		// Sample outside the lock — GetMetrics does blocking syscalls and must
		// not hold up subscribe/unsubscribe.
		m, err := GetMetrics()
		if err != nil {
			continue
		}

		// Send under the lock so a channel can't be closed by unsubscribe()
		// between our membership check and the send (delete+close and the send
		// are both guarded by b.mu, so we never send on a closed channel).
		b.mu.Lock()
		b.latest = m
		for ch := range b.subs {
			select {
			case ch <- m:
			default: // subscriber hasn't drained the previous sample — skip it
			}
		}
		b.mu.Unlock()
	}
}
