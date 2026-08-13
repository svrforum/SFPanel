package cluster

import "testing"

// TestKeepaliveParamsAreConsistent guards the invariants behind the cluster
// gRPC keepalive settings, which bound how long an unobservably-dead
// connection can swallow heartbeats before the transport fails it. Note that
// keepalive does NOT cover an application-level wedge (PING frames are not
// flow controlled and are ACKed by the transport regardless) — the unread
// pong stream that caused the incident is handled by the drain goroutine in
// StartLocalMetrics instead.
func TestKeepaliveParamsAreConsistent(t *testing.T) {
	// The server answers pings that arrive faster than its enforcement policy
	// with GOAWAY/ENHANCE_YOUR_CALM — which would kill the very connections
	// keepalive is meant to keep verifiable. Our own client is the only
	// caller, so the two must stay compatible.
	if clusterKeepaliveMinTime > clusterKeepaliveTime {
		t.Fatalf("server MinTime (%v) must be <= client Time (%v), or the server "+
			"GOAWAYs its own peers' keepalive pings",
			clusterKeepaliveMinTime, clusterKeepaliveTime)
	}

	if clusterKeepaliveTimeout <= 0 || clusterKeepaliveTimeout >= clusterKeepaliveTime {
		t.Fatalf("ping ack timeout (%v) must be positive and shorter than the ping "+
			"interval (%v), otherwise a second ping is due before the first is "+
			"declared lost", clusterKeepaliveTimeout, clusterKeepaliveTime)
	}

	// Worst-case detection is one full idle interval plus the ack timeout. It
	// has to beat the heartbeat timeout, or a dead connection still costs the
	// node its membership before the transport notices — the exact outcome
	// keepalive was added to prevent.
	detection := clusterKeepaliveTime + clusterKeepaliveTimeout
	if detection >= DefaultHeartbeatTimeout {
		t.Fatalf("keepalive detects a dead connection in %v, which is not inside "+
			"DefaultHeartbeatTimeout (%v) — the node would go offline before the "+
			"redial path ever runs", detection, DefaultHeartbeatTimeout)
	}

	// The redial happens on the next metrics tick after the transport fails,
	// so detection should also leave room within the leader's idle timeout for
	// the follower to reconnect and resume pinging.
	if detection+metricsStreamSendInterval >= DefaultHeartbeatTimeout {
		t.Fatalf("detection (%v) plus one send interval (%v) leaves no margin "+
			"before DefaultHeartbeatTimeout (%v)",
			detection, metricsStreamSendInterval, DefaultHeartbeatTimeout)
	}
}

// TestClusterKeepaliveDialOptionSet is a smoke check that the dial option is
// actually constructed — the params themselves are exercised against a live
// peer, but a nil option here would silently disable keepalive everywhere.
func TestClusterKeepaliveDialOptionSet(t *testing.T) {
	if clusterKeepaliveDialOption() == nil {
		t.Fatal("clusterKeepaliveDialOption returned nil; every cluster dial would lose keepalive")
	}
}
