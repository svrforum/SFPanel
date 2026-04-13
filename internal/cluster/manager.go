package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
)

// Manager is the central coordinator for all cluster operations.
type Manager struct {
	config    *config.ClusterConfig
	raft      *RaftNode
	tls       *TLSManager
	tokens    *TokenManager
	heartbeat *HeartbeatManager
	events    *EventBus
	connPool  *ConnPool
	nodeID    string
	nodeName  string
	version   string
}

// SetVersion sets the panel version for heartbeat reporting.
func (m *Manager) SetVersion(v string) { m.version = v }

// NewManager creates a Manager but does not start any services.
func NewManager(cfg *config.ClusterConfig) *Manager {
	tlsMgr := NewTLSManager(cfg.CertDir)
	return &Manager{
		config:    cfg,
		tls:       tlsMgr,
		tokens:    NewTokenManager(),
		heartbeat: NewHeartbeatManager(DefaultHeartbeatInterval, DefaultHeartbeatTimeout),
		events:    NewEventBus(),
		connPool:  NewConnPool(tlsMgr),
		nodeID:    cfg.NodeID,
		nodeName:  cfg.NodeName,
	}
}

// GetConnPool returns the gRPC connection pool for proxy middleware.
func (m *Manager) GetConnPool() *ConnPool {
	return m.connPool
}

// Init bootstraps a new cluster (first node, becomes leader).
func (m *Manager) Init(clusterName string) error {
	if m.config.Enabled {
		return ErrAlreadyInitialized
	}

	if m.nodeID == "" {
		m.nodeID = uuid.New().String()
	}
	if m.nodeName == "" {
		hostname, _ := os.Hostname()
		m.nodeName = hostname
	}

	if err := m.tls.InitCA(clusterName); err != nil {
		return fmt.Errorf("init CA: %w", err)
	}

	advertise := m.config.AdvertiseAddress
	if advertise == "" {
		advertise = DetectFallbackIP()
		if advertise == "" {
			return fmt.Errorf("cannot detect advertise address: no non-loopback IPv4 found")
		}
		slog.Info("auto-detected advertise address", "component", "cluster", "address", advertise)
	}

	certPEM, keyPEM, err := m.tls.IssueNodeCert(m.nodeID, []string{advertise})
	if err != nil {
		return fmt.Errorf("issue node cert: %w", err)
	}
	if err := m.tls.SaveNodeCert(certPEM, keyPEM); err != nil {
		return fmt.Errorf("save node cert: %w", err)
	}

	raftAddr := fmt.Sprintf("%s:%d", advertise, m.config.GRPCPort+1)
	raftNode, err := NewRaftNode(RaftConfig{
		NodeID:    m.nodeID,
		BindAddr:  raftAddr,
		DataDir:   m.config.DataDir,
		Bootstrap: true,
		TLS:       m.tls,
	})
	if err != nil {
		return fmt.Errorf("start raft: %w", err)
	}
	m.raft = raftNode

	// Wait for Raft leader election (up to 10 seconds)
	for i := 0; i < 20; i++ {
		if m.raft.IsLeader() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !m.raft.IsLeader() {
		return fmt.Errorf("timed out waiting for leader election")
	}

	apiAddr := fmt.Sprintf("%s:%d", advertise, m.config.APIPort)
	grpcAddr := fmt.Sprintf("%s:%d", advertise, m.config.GRPCPort)

	selfNode := Node{
		ID:          m.nodeID,
		Name:        m.nodeName,
		APIAddress:  apiAddr,
		GRPCAddress: grpcAddr,
		Role:        RoleVoter,
		Status:      StatusOnline,
		JoinedAt:    time.Now(),
		LastSeen:    time.Now(),
	}

	nodeJSON, _ := json.Marshal(selfNode)
	if err := m.raft.Apply(Command{
		Type:  CmdAddNode,
		Value: nodeJSON,
	}, 5*time.Second); err != nil {
		return fmt.Errorf("register self: %w", err)
	}

	nameJSON, _ := json.Marshal(clusterName)
	if err := m.raft.Apply(Command{
		Type:  CmdSetConfig,
		Key:   "cluster_name",
		Value: nameJSON,
	}, 5*time.Second); err != nil {
		return fmt.Errorf("set cluster name: %w", err)
	}

	m.heartbeat.StartMonitor(m.onNodeStatusChange)
	m.events.Emit(EventNodeJoined, m.nodeID, m.nodeName, "cluster initialized as leader")

	slog.Info("cluster initialized", "component", "cluster", "name", clusterName, "node_id", m.nodeID)
	return nil
}

// Start resumes an already-initialized cluster node.
func (m *Manager) Start() error {
	if !m.config.Enabled {
		return ErrNotInitialized
	}

	advertise := m.config.AdvertiseAddress
	if advertise == "" {
		advertise = DetectFallbackIP()
		if advertise == "" {
			return fmt.Errorf("cannot detect advertise address: no non-loopback IPv4 found")
		}
		slog.Info("auto-detected advertise address", "component", "cluster", "address", advertise)
	}

	raftAddr := fmt.Sprintf("%s:%d", advertise, m.config.GRPCPort+1)
	raftNode, err := NewRaftNode(RaftConfig{
		NodeID:    m.nodeID,
		BindAddr:  raftAddr,
		DataDir:   m.config.DataDir,
		Bootstrap: false,
		TLS:       m.tls,
	})
	if err != nil {
		return fmt.Errorf("start raft: %w", err)
	}
	m.raft = raftNode

	m.heartbeat.StartMonitor(m.onNodeStatusChange)

	// Auto-fix own node address if it doesn't match config
	go m.verifySelfAddress()

	slog.Info("cluster node started", "component", "cluster", "node_id", m.nodeID)
	return nil
}

// verifySelfAddress checks if this node's registered addresses in the FSM
// match its current config, and auto-corrects if not. This prevents stale
// gRPC addresses from causing heartbeat/metrics collection failures.
func (m *Manager) verifySelfAddress() {
	// Wait for Raft to stabilize and leader to be elected
	time.Sleep(10 * time.Second)

	advertise := m.config.AdvertiseAddress
	if advertise == "" {
		advertise = DetectFallbackIP()
		if advertise == "" {
			return
		}
	}
	expectedAPI := fmt.Sprintf("%s:%d", advertise, m.config.APIPort)
	expectedGRPC := fmt.Sprintf("%s:%d", advertise, m.config.GRPCPort)

	node := m.raft.GetFSM().GetNode(m.nodeID)
	if node == nil {
		return
	}

	if node.APIAddress == expectedAPI && node.GRPCAddress == expectedGRPC {
		return // All good
	}

	slog.Warn("node address mismatch detected, auto-correcting",
		"component", "cluster",
		"node_id", m.nodeID,
		"fsm_api", node.APIAddress, "expected_api", expectedAPI,
		"fsm_grpc", node.GRPCAddress, "expected_grpc", expectedGRPC,
	)

	update := Node{
		ID:          m.nodeID,
		APIAddress:  expectedAPI,
		GRPCAddress: expectedGRPC,
	}
	updateJSON, _ := json.Marshal(update)
	if err := m.raft.Apply(Command{
		Type:  CmdUpdateNode,
		Key:   m.nodeID,
		Value: updateJSON,
	}, 5*time.Second); err != nil {
		slog.Warn("failed to auto-correct node address (not leader?)", "component", "cluster", "error", err)
	} else {
		slog.Info("auto-corrected node address in FSM", "component", "cluster",
			"api", expectedAPI, "grpc", expectedGRPC)
	}
}

// CreateJoinToken generates a time-limited token for new nodes.
func (m *Manager) CreateJoinToken(ttl time.Duration) (*JoinToken, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return nil, ErrNotLeader
	}

	state := m.raft.GetFSM().GetState()
	if len(state.Nodes) >= MaxNodes {
		return nil, ErrMaxNodesReached
	}

	return m.tokens.Create(ttl, m.nodeID)
}

// HandleJoin processes a join request from a new node (leader-only).
func (m *Manager) HandleJoin(nodeID, nodeName, apiAddr, grpcAddr, token string) (caCert, nodeCert, nodeKey []byte, peers []Node, err error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return nil, nil, nil, nil, ErrNotLeader
	}

	if err := m.tokens.Validate(token); err != nil {
		return nil, nil, nil, nil, err
	}

	state := m.raft.GetFSM().GetState()
	if len(state.Nodes) >= MaxNodes {
		return nil, nil, nil, nil, ErrMaxNodesReached
	}
	if _, exists := state.Nodes[nodeID]; exists {
		return nil, nil, nil, nil, ErrNodeAlreadyExists
	}

	host, _, _ := net.SplitHostPort(grpcAddr)
	certPEM, keyPEM, tlsErr := m.tls.IssueNodeCert(nodeID, []string{host})
	if tlsErr != nil {
		return nil, nil, nil, nil, fmt.Errorf("issue cert: %w", tlsErr)
	}

	caCertPEM, caErr := m.tls.LoadCACert()
	if caErr != nil {
		return nil, nil, nil, nil, fmt.Errorf("load CA: %w", caErr)
	}

	newNode := Node{
		ID:          nodeID,
		Name:        nodeName,
		APIAddress:  apiAddr,
		GRPCAddress: grpcAddr,
		Role:        RoleVoter,
		Status:      StatusOnline,
		JoinedAt:    time.Now(),
		LastSeen:    time.Now(),
	}
	nodeJSON, _ := json.Marshal(newNode)
	if applyErr := m.raft.Apply(Command{
		Type:  CmdAddNode,
		Value: nodeJSON,
	}, 5*time.Second); applyErr != nil {
		m.tokens.RestoreToken(token) // allow retry
		return nil, nil, nil, nil, fmt.Errorf("register node: %w", applyErr)
	}

	// Derive Raft port from joining node's gRPC port (+1), not leader's config
	_, grpcPortStr, _ := net.SplitHostPort(grpcAddr)
	grpcPort, _ := strconv.Atoi(grpcPortStr)
	raftAddr := fmt.Sprintf("%s:%d", host, grpcPort+1)
	if addErr := m.raft.AddVoter(nodeID, raftAddr); addErr != nil {
		// Rollback: remove node from FSM and restore token
		m.raft.Apply(Command{Type: CmdRemoveNode, Key: nodeID}, 5*time.Second)
		m.tokens.RestoreToken(token)
		return nil, nil, nil, nil, fmt.Errorf("add voter: %w", addErr)
	}

	updatedState := m.raft.GetFSM().GetState()
	peerList := make([]Node, 0, len(updatedState.Nodes))
	for _, n := range updatedState.Nodes {
		peerList = append(peerList, *n)
	}

	m.events.Emit(EventNodeJoined, nodeID, nodeName, fmt.Sprintf("joined at %s", grpcAddr))

	slog.Info("node joined", "component", "cluster", "name", nodeName, "node_id", nodeID, "grpc_addr", grpcAddr)
	return caCertPEM, certPEM, keyPEM, peerList, nil
}

// HandlePreFlight validates a token without consuming it (for pre-flight checks).
func (m *Manager) HandlePreFlight(token string) (clusterName string, nodeCount, maxNodes int, err error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return "", 0, 0, ErrNotLeader
	}

	if err := m.tokens.Peek(token); err != nil {
		return "", 0, 0, err
	}

	state := m.raft.GetFSM().GetState()
	return state.Config["cluster_name"], len(state.Nodes), MaxNodes, nil
}

// GetJWTAndAdmin returns the leader's JWT secret and primary admin account
// for inclusion in join responses.
func (m *Manager) GetJWTAndAdmin() (jwtSecret, adminUser, adminPassHash string) {
	if m.raft == nil {
		return "", "", ""
	}
	state := m.raft.GetFSM().GetState()
	jwtSecret = state.Config["jwt_secret"]

	// Return the first (primary) admin account
	for _, acct := range state.Accounts {
		return jwtSecret, acct.Username, acct.Password
	}
	return jwtSecret, "", ""
}

// RemoveNode removes a node from the cluster (leader-only).
func (m *Manager) RemoveNode(nodeID string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	if nodeID == m.nodeID {
		return ErrSelfRemove
	}

	if err := m.raft.RemoveServer(nodeID); err != nil {
		return fmt.Errorf("remove from raft: %w", err)
	}

	if err := m.raft.Apply(Command{
		Type: CmdRemoveNode,
		Key:  nodeID,
	}, 5*time.Second); err != nil {
		return fmt.Errorf("remove from state: %w", err)
	}

	// Clean up heartbeat + connection pool for removed node
	state := m.raft.GetFSM().GetState()
	if node, ok := state.Nodes[nodeID]; ok && node.GRPCAddress != "" {
		m.connPool.Remove(node.GRPCAddress)
	}
	m.heartbeat.RemoveNode(nodeID)
	m.events.Emit(EventNodeLeft, nodeID, "", "removed from cluster")
	slog.Info("node removed", "component", "cluster", "node_id", nodeID)
	return nil
}

// Leave gracefully leaves the cluster by notifying the leader to remove this node.
func (m *Manager) Leave() error {
	if m.raft == nil {
		return ErrNotInitialized
	}
	m.heartbeat.Stop()

	// If we are the leader, remove ourselves from the cluster
	if m.raft.IsLeader() {
		if err := m.raft.RemoveServer(m.nodeID); err != nil {
			slog.Error("failed to remove self from Raft", "component", "cluster", "error", err)
		}
	} else {
		// Notify the leader to remove us via gRPC
		leaderAddr := m.raft.LeaderGRPCAddress()
		if leaderAddr != "" && m.connPool != nil {
			client, err := m.connPool.Get(leaderAddr)
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, _ = client.client.Leave(ctx, &pb.LeaveRequest{NodeId: m.nodeID})
				cancel()
			}
		}
	}

	return m.raft.Shutdown()
}

// LocalNodeID returns this node's ID.
func (m *Manager) LocalNodeID() string {
	return m.nodeID
}

// IsLeader returns true if this node is the current Raft leader.
func (m *Manager) IsLeader() bool {
	if m.raft == nil {
		return false
	}
	return m.raft.IsLeader()
}

// GetNodes returns all known cluster nodes with real-time health status.
func (m *Manager) GetNodes() []*Node {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	health := m.heartbeat.CheckHealth()
	lastSeenMap := m.heartbeat.GetLastSeen()

	nodes := make([]*Node, 0, len(state.Nodes))
	for _, n := range state.Nodes {
		cp := *n
		// Override with real-time heartbeat data
		if ls, ok := lastSeenMap[n.ID]; ok {
			cp.LastSeen = ls
		}
		if status, ok := health[n.ID]; ok {
			cp.Status = status
		}
		nodes = append(nodes, &cp)
	}
	return nodes
}

// GetNode returns a single node by ID, or nil if not found.
func (m *Manager) GetNode(nodeID string) *Node {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	n := state.Nodes[nodeID]
	if n == nil {
		return nil
	}
	cp := *n
	if ls, ok := m.heartbeat.GetLastSeen()[cp.ID]; ok {
		cp.LastSeen = ls
	}
	health := m.heartbeat.CheckHealth()
	if status, ok := health[cp.ID]; ok {
		cp.Status = status
	}
	return &cp
}

// GetOverview returns the cluster overview with metrics.
func (m *Manager) GetOverview() *ClusterOverview {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	health := m.heartbeat.CheckHealth()
	lastSeenMap := m.heartbeat.GetLastSeen()

	nodes := make([]*Node, 0, len(state.Nodes))
	for _, n := range state.Nodes {
		cp := *n
		if ls, ok := lastSeenMap[n.ID]; ok {
			cp.LastSeen = ls
		}
		if status, ok := health[n.ID]; ok {
			cp.Status = status
		}
		nodes = append(nodes, &cp)
	}
	return &ClusterOverview{
		Name:      state.Config["cluster_name"],
		NodeCount: len(nodes),
		LeaderID:  m.raft.LeaderID(),
		Nodes:     nodes,
		Metrics:   m.heartbeat.GetAllMetrics(),
	}
}

// GetLeaderGRPCAddress returns the gRPC address of the current Raft leader.
func (m *Manager) GetLeaderGRPCAddress() string {
	if m.raft == nil {
		return ""
	}
	return m.raft.LeaderGRPCAddress()
}

// GetTLS returns the TLS manager (for gRPC server setup).
func (m *Manager) GetTLS() *TLSManager {
	return m.tls
}

// ProxySecret returns the cluster-internal proxy authentication secret
// derived from the CA certificate hash. Returns empty if TLS is not configured.
func (m *Manager) ProxySecret() string {
	if m.tls == nil {
		return ""
	}
	caCert, err := m.tls.LoadCACert()
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(caCert)
	return hex.EncodeToString(hash[:])
}

// SetConfig stores a key-value pair in the Raft FSM config.
func (m *Manager) SetConfig(key, value string) error {
	if m.raft == nil {
		return fmt.Errorf("raft not initialized")
	}
	return m.raft.Apply(Command{
		Type:  CmdSetConfig,
		Key:   key,
		Value: mustJSON(value),
	}, 5*time.Second)
}

// GetFSMConfig reads a value from the Raft FSM config map.
func (m *Manager) GetFSMConfig(key string) string {
	if m.raft == nil {
		return ""
	}
	state := m.raft.GetFSM().GetState()
	return state.Config[key]
}

// SetAccount proposes an account upsert to the Raft cluster.
// Only the leader can apply this; returns ErrNotLeader on followers.
func (m *Manager) SetAccount(acct AdminAccount) error {
	if m.raft == nil {
		return fmt.Errorf("raft not initialized")
	}
	acct.UpdatedAt = time.Now().Unix()
	return m.raft.Apply(Command{
		Type:  CmdSetAccount,
		Key:   acct.Username,
		Value: mustJSON(acct),
	}, 5*time.Second)
}

// DeleteAccount proposes an account deletion to the Raft cluster.
func (m *Manager) DeleteAccount(username string) error {
	if m.raft == nil {
		return fmt.Errorf("raft not initialized")
	}
	return m.raft.Apply(Command{
		Type: CmdDeleteAccount,
		Key:  username,
	}, 5*time.Second)
}

// GetAccount returns an account from the FSM state, or nil.
func (m *Manager) GetAccount(username string) *AdminAccount {
	if m.raft == nil {
		return nil
	}
	return m.raft.GetFSM().GetAccount(username)
}

// GetAccounts returns all accounts from the FSM state.
func (m *Manager) GetAccounts() map[string]*AdminAccount {
	if m.raft == nil {
		return nil
	}
	return m.raft.GetFSM().GetState().Accounts
}

// SyncAccountFromDB imports a local DB account into Raft (used on init/join).
func (m *Manager) SyncAccountFromDB(username, passwordHash, totpSecret string) error {
	return m.SetAccount(AdminAccount{
		Username:   username,
		Password:   passwordHash,
		TOTPSecret: totpSecret,
	})
}

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// GetRaft returns the Raft node (for gRPC server to read FSM).
func (m *Manager) GetRaft() *RaftNode {
	return m.raft
}

// GetHeartbeat returns the heartbeat manager.
func (m *Manager) GetHeartbeat() *HeartbeatManager {
	return m.heartbeat
}

// GetEvents returns the event bus.
func (m *Manager) GetEvents() *EventBus {
	return m.events
}

// UpdateNodeLabels sets labels on a node (leader-only).
func (m *Manager) UpdateNodeLabels(nodeID string, labels map[string]string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	state := m.raft.GetFSM().GetState()
	node, exists := state.Nodes[nodeID]
	if !exists {
		return ErrNodeNotFound
	}

	node.Labels = labels
	data, _ := json.Marshal(node)
	if err := m.raft.Apply(Command{
		Type:  CmdUpdateNode,
		Value: data,
	}, 5*time.Second); err != nil {
		return fmt.Errorf("update labels: %w", err)
	}

	m.events.Emit(EventNodeLabelsUpdate, nodeID, node.Name, fmt.Sprintf("labels updated: %v", labels))
	return nil
}

// TransferLeadership transfers Raft leadership to the specified node.
func (m *Manager) TransferLeadership(targetNodeID string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	if targetNodeID == m.nodeID {
		return fmt.Errorf("already the leader")
	}

	state := m.raft.GetFSM().GetState()
	target, exists := state.Nodes[targetNodeID]
	if !exists {
		return ErrNodeNotFound
	}
	if target.Status != StatusOnline {
		return fmt.Errorf("target node is not online")
	}

	if err := m.raft.TransferLeadership(targetNodeID); err != nil {
		return fmt.Errorf("transfer leadership: %w", err)
	}

	m.events.Emit(EventLeaderChanged, targetNodeID, target.Name, fmt.Sprintf("leadership transferred from %s", m.nodeName))
	return nil
}

// MetricsCollector is a function that collects local system metrics.
type MetricsCollector func() (cpuPercent, memPercent, diskPercent float64, containerCount int)

// StartLocalMetrics starts a goroutine that periodically collects local metrics.
// On the leader, metrics are recorded locally. On followers, metrics are sent
// to the leader via gRPC heartbeat streaming.
func (m *Manager) StartLocalMetrics(collector MetricsCollector) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		var grpcStream pb.ClusterService_HeartbeatClient
		var grpcClient *GRPCClient
		var streamCancel context.CancelFunc
		var connectedLeaderAddr string
		var dialFailures int
		var dialSkipCount int

		closeStream := func() {
			if streamCancel != nil {
				streamCancel()
			}
			if grpcClient != nil {
				grpcClient.Close()
			}
			grpcStream = nil
			grpcClient = nil
			streamCancel = nil
			connectedLeaderAddr = ""
			dialFailures = 0
		}

		collect := func() {
			cpu, mem, disk, containers := collector()
			metrics := &NodeMetrics{
				NodeID:         m.nodeID,
				CPUPercent:     cpu,
				MemoryPercent:  mem,
				DiskPercent:    disk,
				ContainerCount: containers,
				Version:        m.version,
				Timestamp:      time.Now().Unix(),
			}

			// Always record locally
			m.heartbeat.RecordHeartbeat(metrics)

			// If follower, also send to leader via gRPC
			if m.raft != nil && !m.raft.IsLeader() {
				leaderAddr := m.raft.LeaderGRPCAddress()

				// If leader changed, close old stream
				if grpcStream != nil && leaderAddr != connectedLeaderAddr {
					slog.Info("leader changed, reconnecting heartbeat", "component", "cluster", "old_leader", connectedLeaderAddr, "new_leader", leaderAddr)
					closeStream()
				}

				if grpcStream == nil && leaderAddr != "" {
					// Skip dial attempts during backoff (non-blocking)
					if dialFailures > 0 {
						backoffTicks := 1 << min(dialFailures, 5)
						dialSkipCount++
						if dialSkipCount < backoffTicks {
							return // skip this tick
						}
						dialSkipCount = 0
					}

					client, err := DialNode(leaderAddr, m.tls)
					if err != nil {
						dialFailures++
						slog.Warn("heartbeat dial failed", "component", "cluster", "error", err)
						return
					}
					ctx, cancel := context.WithCancel(context.Background())
					stream, err := client.client.Heartbeat(ctx)
					if err != nil {
						cancel()
						client.Close()
						dialFailures++
						slog.Warn("heartbeat stream failed", "component", "cluster", "error", err)
						return
					}
					grpcClient = client
					grpcStream = stream
					streamCancel = cancel
					connectedLeaderAddr = leaderAddr
					dialFailures = 0
				}

				if grpcStream != nil {
					err := grpcStream.Send(&pb.HeartbeatPing{
						NodeId:         m.nodeID,
						CpuPercent:     cpu,
						MemoryPercent:  mem,
						ContainerCount: int32(containers),
						DiskPercent:    disk,
						Version:        m.version,
						Timestamp:      metrics.Timestamp,
					})
					if err != nil {
						slog.Warn("heartbeat send failed", "component", "cluster", "error", err)
						closeStream()
					}
				}
			} else if grpcStream != nil {
				// Became leader, close follower stream
				closeStream()
			}
		}

		collect() // immediate first collection
		for {
			select {
			case <-ticker.C:
				collect()
			case <-m.heartbeat.stopCh:
				closeStream()
				return
			}
		}
	}()
}

// Shutdown gracefully stops all cluster services.
func (m *Manager) Shutdown() {
	if m.heartbeat != nil {
		m.heartbeat.Stop()
	}

	// If we're the leader, try to transfer leadership before shutting down
	if m.raft != nil && m.raft.IsLeader() {
		state := m.raft.GetFSM().GetState()
		// Use live heartbeat health instead of stale FSM status
		liveHealth := m.heartbeat.CheckHealth()
		for id, node := range state.Nodes {
			if id != m.nodeID {
				if status, ok := liveHealth[id]; ok && status == StatusOnline {
					slog.Info("transferring leadership before shutdown", "component", "cluster", "target", node.Name)
					if err := m.raft.TransferLeadership(id); err != nil {
						slog.Error("leadership transfer failed", "component", "cluster", "error", err)
					} else {
						slog.Info("leadership transferred", "component", "cluster", "target", node.Name)
					}
					break
				}
			}
		}
	}

	if m.connPool != nil {
		m.connPool.Close()
	}
	if m.raft != nil {
		m.raft.Shutdown()
	}
	slog.Info("cluster manager stopped", "component", "cluster")
}

// GetConfig returns the current cluster config for writing to YAML.
func (m *Manager) GetConfig() *config.ClusterConfig {
	clusterName := ""
	if m.raft != nil {
		state := m.raft.GetFSM().GetState()
		clusterName = state.Config["cluster_name"]
	}
	return &config.ClusterConfig{
		Enabled:          true,
		Name:             clusterName,
		NodeID:           m.nodeID,
		NodeName:         m.nodeName,
		GRPCPort:         m.config.GRPCPort,
		DataDir:          m.config.DataDir,
		CertDir:          m.config.CertDir,
		AdvertiseAddress: m.config.AdvertiseAddress,
	}
}

func (m *Manager) onNodeStatusChange(nodeID string, status NodeStatus) {
	if m.raft == nil || !m.raft.IsLeader() {
		return
	}

	update := Node{ID: nodeID, Status: status}
	data, _ := json.Marshal(update)
	m.raft.Apply(Command{
		Type:  CmdUpdateNode,
		Value: data,
	}, 5*time.Second)

	// Emit event for status transitions
	nodeName := ""
	if state := m.raft.GetFSM().GetState(); state.Nodes[nodeID] != nil {
		nodeName = state.Nodes[nodeID].Name
	}
	switch status {
	case StatusOnline:
		m.events.Emit(EventNodeOnline, nodeID, nodeName, "node is online")
	case StatusSuspect:
		m.events.Emit(EventNodeSuspect, nodeID, nodeName, "node is suspect (heartbeat delayed)")
	case StatusOffline:
		m.events.Emit(EventNodeOffline, nodeID, nodeName, "node is offline (heartbeat timeout)")
	}
}

// UpdateNodeAddress updates the API and gRPC addresses of a node (leader-only).
func (m *Manager) UpdateNodeAddress(nodeID, apiAddr, grpcAddr string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	state := m.raft.GetFSM().GetState()
	node, exists := state.Nodes[nodeID]
	if !exists {
		return ErrNodeNotFound
	}

	node.APIAddress = apiAddr
	node.GRPCAddress = grpcAddr
	data, _ := json.Marshal(node)
	if err := m.raft.Apply(Command{
		Type:  CmdUpdateNode,
		Value: data,
	}, 5*time.Second); err != nil {
		return fmt.Errorf("update address: %w", err)
	}

	m.events.Emit(EventNodeJoined, nodeID, node.Name, fmt.Sprintf("address updated: api=%s grpc=%s", apiAddr, grpcAddr))
	return nil
}

