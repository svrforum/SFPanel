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
	// RegisterAgent's refusals reach the operator through the agent's join
	// command, so each says what is wrong in terms the operator can act on.
	ErrProxyRotationPending = errors.New("the cluster is still switching its internal proxy secret, which waits for every manager to be online; agents can register about five minutes after it finishes")
	// ErrManagersNotReadyForAgents is followed by the managers it waits for.
	ErrManagersNotReadyForAgents = errors.New("agents can register once every manager is online and runs a version that supports agents")
	ErrJoinerPredatesAgents      = errors.New("this cluster has agents, and this server's SFPanel predates them: upgrade it to the managers' version, then join again")
	ErrAgentPanelCARequired      = errors.New("the agent's panel must serve HTTPS, and the registration carried no panel CA")
	ErrNodeIdentityTaken         = errors.New("another node already uses this name or address")
	ErrBadCSR                    = errors.New("the certificate request is not a valid ECDSA P-256 request")
	ErrPanelCAChangedRecently    = errors.New("this node's panel CA changed less than a minute ago; it is taken again after that")
)
