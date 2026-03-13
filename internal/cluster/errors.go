package cluster

import "errors"

var (
	ErrNotInitialized     = errors.New("cluster not initialized")
	ErrAlreadyInitialized = errors.New("cluster already initialized")
	ErrNotLeader          = errors.New("not the cluster leader")
	ErrNodeNotFound       = errors.New("node not found")
	ErrNodeAlreadyExists  = errors.New("node already exists in cluster")
	ErrInvalidToken       = errors.New("invalid or expired join token")
	ErrTokenUsed          = errors.New("join token already used")
	ErrMaxNodesReached    = errors.New("maximum node count reached")
	ErrSelfRemove         = errors.New("cannot remove self from cluster")
	ErrCertGenFailed      = errors.New("certificate generation failed")
	ErrRaftTimeout        = errors.New("raft operation timed out")
	ErrGRPCConnFailed     = errors.New("gRPC connection failed")
)
