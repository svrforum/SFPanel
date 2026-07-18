package cluster

import "testing"

// TestMetricsStreamIdleTimeoutHasMargin guards the invariant whose violation
// caused the follower→leader heartbeat EOF storm (issue investigated 2026-07-18):
// the leader closes a Heartbeat stream idle for metricsStreamIdleTimeout and
// resets that timer on each ping RECEIVED, while the follower sends on its own
// fixed metricsStreamSendInterval schedule. When the two were equal (both 30s),
// any positive latency jitter let the idle timer fire just before the next ping
// arrived — killing the stream and producing an endless reconnect/EOF loop.
// The timeout must stay a comfortable multiple of the send interval.
func TestMetricsStreamIdleTimeoutHasMargin(t *testing.T) {
	if metricsStreamSendInterval <= 0 {
		t.Fatalf("metricsStreamSendInterval must be positive, got %v", metricsStreamSendInterval)
	}
	if metricsStreamIdleTimeout < 2*metricsStreamSendInterval {
		t.Fatalf("metricsStreamIdleTimeout (%v) must be >= 2x metricsStreamSendInterval (%v) "+
			"or the idle timer races ahead of the next ping and kills the stream",
			metricsStreamIdleTimeout, metricsStreamSendInterval)
	}
}
