package cluster

import (
	"log/slog"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
)

// HeartbeatManager tracks node health via periodic heartbeats.
type HeartbeatManager struct {
	mu       sync.RWMutex
	metrics  map[string]*NodeMetrics
	lastSeen map[string]time.Time
	// streams counts each node's open heartbeat streams to this manager, and
	// dropped holds the nodes whose last one ended since they last pinged
	// (LiveHealth).
	streams  map[string]int
	dropped  map[string]bool
	interval time.Duration
	timeout  time.Duration
	stopCh   chan struct{}
	stopped  sync.Once
}

func NewHeartbeatManager(interval, timeout time.Duration) *HeartbeatManager {
	return &HeartbeatManager{
		metrics:  make(map[string]*NodeMetrics),
		lastSeen: make(map[string]time.Time),
		streams:  make(map[string]int),
		dropped:  make(map[string]bool),
		interval: interval,
		timeout:  timeout,
		stopCh:   make(chan struct{}),
	}
}

// RecordHeartbeat updates the metrics and last-seen time for a node.
func (hm *HeartbeatManager) RecordHeartbeat(m *NodeMetrics) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.metrics[m.NodeID] = m
	hm.lastSeen[m.NodeID] = time.Now()
	delete(hm.dropped, m.NodeID)
}

// RecordStreamPing is RecordHeartbeat for a ping on a heartbeat stream, and
// counts the stream at its first. Both under one lock: recorded and counted
// apart, a node's old stream could end between its new stream's first ping
// and its count, and LiveHealth then held the node offline, its new stream
// open, until it pinged again.
func (hm *HeartbeatManager) RecordStreamPing(m *NodeMetrics, first bool) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if first {
		hm.streams[m.NodeID]++
	}
	hm.metrics[m.NodeID] = m
	hm.lastSeen[m.NodeID] = time.Now()
	delete(hm.dropped, m.NodeID)
}

// StreamEnded is the end of one. A node with none left open has dropped
// until it pings again: a node that stops closes its stream at once, and a
// cut connection fails within the keepalive's thirty seconds.
func (hm *HeartbeatManager) StreamEnded(nodeID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if hm.streams[nodeID] > 1 {
		hm.streams[nodeID]--
		return
	}
	delete(hm.streams, nodeID)
	hm.dropped[nodeID] = true
}

// LiveHealth is CheckHealth for what an operator starts — a rotation, a
// conversion, a leave, an update — and the rotation status beside it: a node
// whose heartbeat streams to this manager have all ended is offline, though
// its last ping stays inside the timeout for minutes. A manager stopped a
// minute before a JWT rotation passed its gate. Not for the node list or a
// rotation's own progress: a stream also ends when its node reconnects, and
// that is no outage there.
func (hm *HeartbeatManager) LiveHealth() map[string]NodeStatus {
	health := hm.CheckHealth()
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	for id := range hm.dropped {
		health[id] = StatusOffline
	}
	return health
}

// GetMetrics returns the latest metrics for a node.
func (hm *HeartbeatManager) GetMetrics(nodeID string) *NodeMetrics {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.metrics[nodeID]
}

// GetAllMetrics returns metrics for all known nodes.
func (hm *HeartbeatManager) GetAllMetrics() []*NodeMetrics {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	result := make([]*NodeMetrics, 0, len(hm.metrics))
	for _, m := range hm.metrics {
		cp := *m
		result = append(result, &cp)
	}
	return result
}

// GetLastSeen returns a snapshot of the last-seen times for all nodes.
func (hm *HeartbeatManager) GetLastSeen() map[string]time.Time {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	result := make(map[string]time.Time, len(hm.lastSeen))
	for id, t := range hm.lastSeen {
		result[id] = t
	}
	return result
}

// CheckHealth returns a map of nodeID to NodeStatus based on last-seen times.
func (hm *HeartbeatManager) CheckHealth() map[string]NodeStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	now := time.Now()
	result := make(map[string]NodeStatus)

	for nodeID, lastSeen := range hm.lastSeen {
		elapsed := now.Sub(lastSeen)
		switch {
		case elapsed < hm.timeout:
			result[nodeID] = StatusOnline
		case elapsed < hm.timeout*2:
			result[nodeID] = StatusSuspect
		default:
			result[nodeID] = StatusOffline
		}
	}
	return result
}

// StartMonitor runs a background goroutine that detects status changes.
func (hm *HeartbeatManager) StartMonitor(onStatusChange func(nodeID string, status NodeStatus)) {
	safe.Go("cluster-heartbeat-monitor", func() {
		ticker := time.NewTicker(hm.interval)
		defer ticker.Stop()

		prev := make(map[string]NodeStatus)

		for {
			select {
			case <-ticker.C:
				health := hm.CheckHealth()
				for nodeID, status := range health {
					if prev[nodeID] != status {
						slog.Info("node status changed", "component", "cluster", "node_id", nodeID, "from", prev[nodeID], "to", status)
						if onStatusChange != nil {
							onStatusChange(nodeID, status)
						}
						prev[nodeID] = status
					}
				}
			case <-hm.stopCh:
				return
			}
		}
	})
}

// RemoveNode removes a node from tracking.
func (hm *HeartbeatManager) RemoveNode(nodeID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	delete(hm.metrics, nodeID)
	delete(hm.lastSeen, nodeID)
	delete(hm.streams, nodeID)
	delete(hm.dropped, nodeID)
}

// Done returns a channel that is closed when the heartbeat manager stops.
func (hm *HeartbeatManager) Done() <-chan struct{} {
	return hm.stopCh
}

// Stop shuts down the heartbeat monitor. Safe to call multiple times.
func (hm *HeartbeatManager) Stop() {
	hm.stopped.Do(func() {
		close(hm.stopCh)
	})
}
