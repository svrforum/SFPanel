package cluster

import (
	"log/slog"
	"sync"
	"time"
)

// HeartbeatManager tracks node health via periodic heartbeats.
type HeartbeatManager struct {
	mu       sync.RWMutex
	metrics  map[string]*NodeMetrics
	lastSeen map[string]time.Time
	interval time.Duration
	timeout  time.Duration
	stopCh   chan struct{}
	stopped  sync.Once
}

func NewHeartbeatManager(interval, timeout time.Duration) *HeartbeatManager {
	return &HeartbeatManager{
		metrics:  make(map[string]*NodeMetrics),
		lastSeen: make(map[string]time.Time),
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
	go func() {
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
	}()
}

// RemoveNode removes a node from tracking.
func (hm *HeartbeatManager) RemoveNode(nodeID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	delete(hm.metrics, nodeID)
	delete(hm.lastSeen, nodeID)
}

// Stop shuts down the heartbeat monitor. Safe to call multiple times.
func (hm *HeartbeatManager) Stop() {
	hm.stopped.Do(func() {
		close(hm.stopCh)
	})
}
