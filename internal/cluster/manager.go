package cluster

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
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
	nodeID    string
	nodeName  string
}

// NewManager creates a Manager but does not start any services.
func NewManager(cfg *config.ClusterConfig) *Manager {
	return &Manager{
		config:    cfg,
		tls:       NewTLSManager(cfg.CertDir),
		tokens:    NewTokenManager(),
		heartbeat: NewHeartbeatManager(DefaultHeartbeatInterval, DefaultHeartbeatTimeout),
		events:    NewEventBus(),
		nodeID:    cfg.NodeID,
		nodeName:  cfg.NodeName,
	}
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
		advertise = "127.0.0.1"
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
	})
	if err != nil {
		return fmt.Errorf("start raft: %w", err)
	}
	m.raft = raftNode

	time.Sleep(2 * time.Second)

	apiAddr := fmt.Sprintf("%s:%d", advertise, 8443)
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

	log.Printf("[cluster] Cluster '%s' initialized. NodeID=%s", clusterName, m.nodeID)
	return nil
}

// Start resumes an already-initialized cluster node.
func (m *Manager) Start() error {
	if !m.config.Enabled {
		return ErrNotInitialized
	}

	advertise := m.config.AdvertiseAddress
	if advertise == "" {
		advertise = "127.0.0.1"
	}

	raftAddr := fmt.Sprintf("%s:%d", advertise, m.config.GRPCPort+1)
	raftNode, err := NewRaftNode(RaftConfig{
		NodeID:    m.nodeID,
		BindAddr:  raftAddr,
		DataDir:   m.config.DataDir,
		Bootstrap: false,
	})
	if err != nil {
		return fmt.Errorf("start raft: %w", err)
	}
	m.raft = raftNode

	m.heartbeat.StartMonitor(m.onNodeStatusChange)

	log.Printf("[cluster] Cluster node started. NodeID=%s", m.nodeID)
	return nil
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

	raftAddr := fmt.Sprintf("%s:%d", host, m.config.GRPCPort+1)
	if addErr := m.raft.AddVoter(nodeID, raftAddr); addErr != nil {
		return nil, nil, nil, nil, fmt.Errorf("add voter: %w", addErr)
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
		return nil, nil, nil, nil, fmt.Errorf("register node: %w", applyErr)
	}

	updatedState := m.raft.GetFSM().GetState()
	peerList := make([]Node, 0, len(updatedState.Nodes))
	for _, n := range updatedState.Nodes {
		peerList = append(peerList, *n)
	}

	m.events.Emit(EventNodeJoined, nodeID, nodeName, fmt.Sprintf("joined at %s", grpcAddr))

	log.Printf("[cluster] Node joined: %s (%s) at %s", nodeName, nodeID, grpcAddr)
	return caCertPEM, certPEM, keyPEM, peerList, nil
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

	m.heartbeat.RemoveNode(nodeID)
	m.events.Emit(EventNodeLeft, nodeID, "", "removed from cluster")
	log.Printf("[cluster] Node removed: %s", nodeID)
	return nil
}

// Leave gracefully leaves the cluster.
func (m *Manager) Leave() error {
	if m.raft == nil {
		return ErrNotInitialized
	}
	m.heartbeat.Stop()
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

// GetNodes returns all known cluster nodes.
func (m *Manager) GetNodes() []*Node {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	nodes := make([]*Node, 0, len(state.Nodes))
	for _, n := range state.Nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

// GetNode returns a single node by ID, or nil if not found.
func (m *Manager) GetNode(nodeID string) *Node {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	return state.Nodes[nodeID]
}

// GetOverview returns the cluster overview with metrics.
func (m *Manager) GetOverview() *ClusterOverview {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	nodes := make([]*Node, 0, len(state.Nodes))
	for _, n := range state.Nodes {
		nodes = append(nodes, n)
	}
	return &ClusterOverview{
		Name:      state.Config["cluster_name"],
		NodeCount: len(nodes),
		LeaderID:  m.raft.LeaderID(),
		Nodes:     nodes,
		Metrics:   m.heartbeat.GetAllMetrics(),
	}
}

// GetTLS returns the TLS manager (for gRPC server setup).
func (m *Manager) GetTLS() *TLSManager {
	return m.tls
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
func (m *Manager) StartLocalMetrics(collector MetricsCollector) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		collect := func() {
			cpu, mem, disk, containers := collector()
			m.heartbeat.RecordHeartbeat(&NodeMetrics{
				NodeID:         m.nodeID,
				CPUPercent:     cpu,
				MemoryPercent:  mem,
				DiskPercent:    disk,
				ContainerCount: containers,
				Timestamp:      time.Now().Unix(),
			})
		}

		collect() // immediate first collection
		for {
			select {
			case <-ticker.C:
				collect()
			case <-m.heartbeat.stopCh:
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
	if m.raft != nil {
		m.raft.Shutdown()
	}
	log.Println("[cluster] Cluster manager stopped")
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
