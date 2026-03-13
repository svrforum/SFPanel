package cluster

import (
	"sync"
	"time"
)

// EventType identifies cluster event categories.
type EventType string

const (
	EventNodeJoined       EventType = "node_joined"
	EventNodeLeft         EventType = "node_left"
	EventNodeOnline       EventType = "node_online"
	EventNodeSuspect      EventType = "node_suspect"
	EventNodeOffline      EventType = "node_offline"
	EventLeaderChanged    EventType = "leader_changed"
	EventNodeLabelsUpdate EventType = "node_labels_updated"
)

// ClusterEvent represents a single event in the cluster.
type ClusterEvent struct {
	ID        int       `json:"id"`
	Type      EventType `json:"type"`
	NodeID    string    `json:"node_id"`
	NodeName  string    `json:"node_name,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

const maxEvents = 200

// EventBus stores and distributes cluster events.
type EventBus struct {
	mu     sync.RWMutex
	events []ClusterEvent
	nextID int
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		events: make([]ClusterEvent, 0, maxEvents),
		nextID: 1,
	}
}

// Emit records a new event.
func (eb *EventBus) Emit(eventType EventType, nodeID, nodeName, detail string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	event := ClusterEvent{
		ID:        eb.nextID,
		Type:      eventType,
		NodeID:    nodeID,
		NodeName:  nodeName,
		Detail:    detail,
		Timestamp: time.Now(),
	}
	eb.nextID++
	eb.events = append(eb.events, event)

	// Trim old events
	if len(eb.events) > maxEvents {
		eb.events = eb.events[len(eb.events)-maxEvents:]
	}
}

// Recent returns the last N events, newest first.
func (eb *EventBus) Recent(limit int) []ClusterEvent {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if limit <= 0 || limit > len(eb.events) {
		limit = len(eb.events)
	}

	result := make([]ClusterEvent, limit)
	for i := 0; i < limit; i++ {
		result[i] = eb.events[len(eb.events)-1-i]
	}
	return result
}

// Since returns events after the given ID, newest first.
func (eb *EventBus) Since(afterID int) []ClusterEvent {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	var result []ClusterEvent
	for i := len(eb.events) - 1; i >= 0; i-- {
		if eb.events[i].ID <= afterID {
			break
		}
		result = append(result, eb.events[i])
	}
	return result
}
