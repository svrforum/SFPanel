package cluster

import "errors"

var (
	ErrNotInitialized     = errors.New("cluster not initialized")
	ErrAlreadyInitialized = errors.New("cluster already initialized")
	ErrNotLeader          = errors.New("not the cluster leader")
	ErrNodeNotFound       = errors.New("node not found")
	ErrNodeAlreadyExists  = errors.New("node already exists in cluster")
	ErrTokenNotFound      = errors.New("token does not exist")
	ErrTokenExpired       = errors.New("token has expired")
	ErrTokenUsed          = errors.New("join token already used")
	ErrTokenKind          = errors.New("this join token is for another kind of node")
	ErrMaxNodesReached    = errors.New("maximum node count reached")
	ErrMaxAgentsReached   = errors.New("maximum agent count reached")
	ErrSelfRemove         = errors.New("cannot remove self from cluster")
	ErrNotManager         = errors.New("this node is an agent and never joins Raft")
	ErrNoAgentKey         = errors.New("no internal-proxy key for this agent")
	ErrAgentPanelInsecure = errors.New("this agent's panel CA is unknown; refusing to relay to it over plain HTTP")
	// ErrCAKeyUnavailable is returned when a leader must sign a joining node's
	// cert but the cluster CA private key is present neither on local disk nor
	// in replicated FSM state. Surfaced to the joining operator via
	// "join rejected: …" so the message must stay actionable.
	ErrCAKeyUnavailable = errors.New("cluster CA private key unavailable on the leader — the leader cannot sign new node certificates; restore ca.key on the leader or re-initialize the cluster")
)
