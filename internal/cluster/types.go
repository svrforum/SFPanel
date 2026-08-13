package cluster

import (
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
	// RecoveryCodes maps username -> list of 2FA recovery-code hashes (unused
	// codes only; consumed ones are removed). Kept separate from AdminAccount
	// so a password/TOTP update doesn't clobber it.
	RecoveryCodes map[string][]string `json:"recovery_codes,omitempty"`
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

// Default ports and timeouts.
//
// DefaultGRPCPort is informational — config.go is the authoritative source
// for the default (3629), and `Load()` in that package fills the field on
// fresh configs. Raft transport binds to GRPCPort+1.
const (
	DefaultGRPCPort          = 3629
	DefaultHeartbeatInterval = 60 * time.Second
	DefaultHeartbeatTimeout  = 180 * time.Second
	// metricsStreamSendInterval is how often a follower pushes its metrics to
	// the leader over the gRPC Heartbeat stream (StartLocalMetrics). The leader
	// closes a Heartbeat stream idle for metricsStreamIdleTimeout, resetting
	// that timer on each ping RECEIVED while the follower sends on its own fixed
	// schedule — so the timeout MUST stay a comfortable multiple of the send
	// interval. When they were equal (both 30s) any positive latency jitter let
	// the idle timer fire just before the next ping arrived, killing the stream
	// and producing an endless reconnect/EOF loop (~1/min per follower). These
	// two are kept coupled here so they can't silently drift back into a race.
	metricsStreamSendInterval = 30 * time.Second
	metricsStreamIdleTimeout  = 3 * metricsStreamSendInterval
	// HTTP/2 keepalive on the cluster gRPC connections. Without it, a
	// connection killed while the process couldn't observe it — host suspend,
	// a NAT/conntrack entry expiring overnight — stays "open" to the client:
	// grpcStream.Send() writes into the local buffer and returns nil, so the
	// redial path in StartLocalMetrics (send error → closeStream → dial next
	// tick) never fires and the follower heartbeats into a black hole until
	// someone restarts it. Observed on a node that slept ~13h: it marked
	// ITSELF offline and stayed that way for 16 hours, with zero "heartbeat
	// send failed" lines. Same failure mode the WS handler already guards
	// against (internal/feature/websocket/handler.go).
	//
	// clusterKeepaliveTime is the idle interval before a ping; the peer must
	// ack within clusterKeepaliveTimeout or the transport fails the
	// connection, surfacing as a stream error the existing redial handles.
	// Detection therefore takes at most Time+Timeout (30s) — well inside
	// DefaultHeartbeatTimeout, so a suspend no longer costs a node its
	// membership. clusterKeepaliveMinTime is the server's tolerance for
	// client ping frequency and MUST stay <= clusterKeepaliveTime, or the
	// server answers pings with GOAWAY/ENHANCE_YOUR_CALM and kills the very
	// connection keepalive exists to preserve.
	clusterKeepaliveTime    = 20 * time.Second
	clusterKeepaliveTimeout = 10 * time.Second
	clusterKeepaliveMinTime = 10 * time.Second

	DefaultTokenTTL          = 24 * time.Hour
	// MaxTokenTTL caps user-requested join token lifetimes. A join token is
	// a bearer credential that grants membership in the cluster; an unbounded
	// TTL means a leaked token from a year ago is still usable. 30 days
	// covers any reasonable operator workflow without leaving long-lived
	// credentials lying around indefinitely.
	MaxTokenTTL = 30 * 24 * time.Hour
	DefaultDataDir           = "/var/lib/sfpanel/cluster"
	DefaultCertDir           = "/etc/sfpanel/cluster"
	MaxNodes                 = 32
)
