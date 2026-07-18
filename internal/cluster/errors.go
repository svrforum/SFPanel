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
	ErrMaxNodesReached    = errors.New("maximum node count reached")
	ErrSelfRemove         = errors.New("cannot remove self from cluster")
	ErrCertGenFailed      = errors.New("certificate generation failed")
	ErrRaftTimeout        = errors.New("raft operation timed out")
	ErrGRPCConnFailed     = errors.New("gRPC connection failed")
	// ErrCAKeyUnavailable is returned when a leader must sign a joining node's
	// cert but the cluster CA private key is present neither on local disk nor
	// in replicated FSM state. Surfaced to the joining operator via
	// "join rejected: …" so the message must stay actionable.
	ErrCAKeyUnavailable = errors.New("cluster CA private key unavailable on the leader — the leader cannot sign new node certificates; restore ca.key on the leader or re-initialize the cluster")
)
