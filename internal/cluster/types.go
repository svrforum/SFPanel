package cluster

import (
	"encoding/json"
	"time"
)

// NodeRole defines the Raft role of a node.
type NodeRole string

const (
	RoleVoter    NodeRole = "voter"
	RoleNonVoter NodeRole = "nonvoter"
)

// NodeStatus tracks the health state of a node.
type NodeStatus string

const (
	StatusOnline  NodeStatus = "online"
	StatusSuspect NodeStatus = "suspect"
	StatusOffline NodeStatus = "offline"
	StatusJoining NodeStatus = "joining"
)

// Node represents a cluster member.
type Node struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	APIAddress  string            `json:"api_address"`
	GRPCAddress string            `json:"grpc_address"`
	Role        NodeRole          `json:"role"`
	Status      NodeStatus        `json:"status"`
	Labels      map[string]string `json:"labels,omitempty"`
	JoinedAt    time.Time         `json:"joined_at"`
	LastSeen    time.Time         `json:"last_seen"`
}

// ClusterState holds the full cluster state managed by Raft FSM.
type ClusterState struct {
	Name     string                   `json:"name"`
	Nodes    map[string]*Node         `json:"nodes"`
	Config   map[string]string        `json:"config"`
	Accounts map[string]*AdminAccount `json:"accounts,omitempty"`
	Forks    map[string]*ForkRecord   `json:"forks,omitempty"`
}

// ForkRecord is a user-saved AppStore template, replicated via the FSM.
// Stored as a JSON blob — the appstore package owns the schema; this is
// just opaque storage from the cluster layer's perspective.
type ForkRecord struct {
	ID          string          `json:"id"`         // "fork-<short-uuid>"
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Compose     string          `json:"compose"` // verbatim YAML
	Meta        json.RawMessage `json:"meta"`    // serialized AppStoreMeta
	CreatedAt   int64           `json:"created_at"` // unix millis
	CreatedBy   string          `json:"created_by"`
}

// JoinToken is a time-limited, single-use token for node joining.
type JoinToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	CreatedBy string    `json:"created_by"`
}

// NodeMetrics holds the latest metrics from a heartbeat.
type NodeMetrics struct {
	NodeID         string  `json:"node_id"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryPercent  float64 `json:"memory_percent"`
	DiskPercent    float64 `json:"disk_percent"`
	ContainerCount int     `json:"container_count"`
	UptimeSeconds  int64   `json:"uptime_seconds"`
	Version        string  `json:"version"`
	Timestamp      int64   `json:"timestamp"`
}

// ClusterOverview aggregates metrics from all nodes.
type ClusterOverview struct {
	Name      string         `json:"name"`
	NodeCount int            `json:"node_count"`
	LeaderID  string         `json:"leader_id"`
	Nodes     []*Node        `json:"nodes"`
	Metrics   []*NodeMetrics `json:"metrics,omitempty"`
}

// Default ports and timeouts
const (
	DefaultGRPCPort          = 9443
	DefaultHeartbeatInterval = 60 * time.Second
	DefaultHeartbeatTimeout  = 180 * time.Second
	DefaultTokenTTL          = 24 * time.Hour
	DefaultDataDir           = "/var/lib/sfpanel/cluster"
	DefaultCertDir           = "/etc/sfpanel/cluster"
	MaxNodes                 = 32
)
