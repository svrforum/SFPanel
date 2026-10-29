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
	// ErrLeaveUnacknowledged: this node's Raft has stopped, but no leader
	// took it out — the others may still list it.
	ErrLeaveUnacknowledged = errors.New("no leader took this node out of the cluster: the others may still list it; remove it on the leader with 'sfpanel cluster remove <node-id>'")
	// ErrNotRaftNonvoter: ConfirmJoin found the node in the FSM but not in
	// the Raft configuration as a non-voter — not a joiner, so not promoted.
	ErrNotRaftNonvoter = errors.New("this node is not a Raft non-voter: it left Raft or never joined it, and must join again")
	// ErrTokenBinding: the token was made for one node at its address
	// (CreateBoundJoinToken) and this is another node or another address,
	// or a token made for no node asked for a node ID.
	ErrTokenBinding = errors.New("this join token was not made for this node at this address")
	// ErrConvertRefused is wrapped by every gate a conversion to an agent
	// refuses on (ReadyForAgentConversion): nothing has changed, and the
	// reason after it says what stands in the way.
	ErrConvertRefused = errors.New("the conversion to an agent was refused")
	// ErrConvertLeader: the leader runs the conversion, and the managers
	// left keep their quorum through it.
	ErrConvertLeader     = errors.New("the leader cannot be converted: hand its leadership to another manager first ('sfpanel cluster leader-transfer <node-id>')")
	ErrConvertNotManager = errors.New("only a manager is converted: this node is an agent already")
	// ErrPromoteRefused is wrapped by every gate a promotion to a manager
	// refuses on (PrepareManagerPromotion): nothing has changed, and the
	// reason after it says what stands in the way.
	ErrPromoteRefused  = errors.New("the promotion to a manager was refused")
	ErrPromoteNotAgent = errors.New("only an agent is promoted: this node is a manager already")
	// ErrRaftPortUnreachable: an agent's join as a manager is refused because
	// the leader could not reach the Raft port the agent held open for it.
	// Surfaced to the operator via "join rejected: …" in the agent's log.
	ErrRaftPortUnreachable = errors.New("the leader cannot reach this node's Raft port, the cluster gRPC port + 1, which a manager needs open to the other managers: open it to them, then promote the node again")
	// ErrCAKeyMismatch: a CA certificate and a key that are not one pair —
	// what a CA swapped by hand in one file and not the other leaves
	// (ParseCAPair). Taken as a pair, they fail every signature later, deep
	// in x509.
	ErrCAKeyMismatch = errors.New("the CA key is not the CA certificate's: every certificate signed with the pair would be refused")
)
