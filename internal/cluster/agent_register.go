package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// RegisterAgentInput is an agent's registration (the RegisterAgent RPC). CSR
// is a DER certificate request for a key the agent made and keeps; PanelCA is
// the PEM of the CA behind its panel's HTTPS certificate.
type RegisterAgentInput struct {
	Token, NodeName, APIAddress, GRPCAddress, Version string
	CSR, PanelCA                                      []byte
}

// RegisterAgentResult is what a registered agent gets: its node ID and
// certificate, the cluster CA, its own internal-proxy key and the managers.
// Nothing the managers share — JWT secret, accounts, CA key, the managers'
// proxy secret — is in it.
type RegisterAgentResult struct {
	NodeID, AgentKey, ClusterName string
	NodeCert, CACert              []byte
	Managers                      []*pb.NodeInfo
}

// CapAgents is the heartbeat capability of a manager whose code knows agents:
// it signs what it sends an agent with that agent's key, forwards no
// operator's credentials, and never makes an agent a Raft voter. A manager
// from before agents does all three wrong, so agents register only once every
// manager reports CapAgents (managersNotReadyForAgents), and a cluster with
// agents takes no manager join without it (HandleJoin).
const CapAgents = "agents-v1"

// managersNotReadyForAgents names the managers an agent registration waits
// for, each with why: every manager but this leader must be online and report
// CapAgents on its heartbeat. One that has not been heard from may be down,
// and come back running a release from before agents.
func (m *Manager) managersNotReadyForAgents(state ClusterState) []string {
	health := m.heartbeat.CheckHealth()
	var waiting []string
	for id, n := range state.Nodes {
		if id == m.nodeID || !n.IsManager() {
			continue
		}
		name := n.Name
		if name == "" {
			name = id
		}
		met := m.heartbeat.GetMetrics(id)
		switch {
		case met == nil:
			waiting = append(waiting, name+" (not heard from)")
		case health[id] != StatusOnline:
			waiting = append(waiting, name+" (offline)")
		case !slices.Contains(met.Capabilities, CapAgents):
			waiting = append(waiting, fmt.Sprintf("%s (%s)", name, met.Version))
		}
	}
	sort.Strings(waiting)
	return waiting
}

var errAgentAddress = errors.New("register agent: api and grpc addresses must be ip:port on the same host")

// HandleRegisterAgent registers an agent (leader only): it signs the agent's
// certificate request, makes the agent's internal-proxy key, and records the
// agent, the key and the agent's panel CA in replicated state. The agent never
// enters Raft. Every refusal comes before the token is spent; a failure after
// takes back what was written and puts the token back.
func (m *Manager) HandleRegisterAgent(in RegisterAgentInput) (*RegisterAgentResult, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return nil, ErrNotLeader
	}
	m.registerAgentMu.Lock()
	defer m.registerAgentMu.Unlock()

	// The token before the barrier: the RPC is open to callers without a
	// certificate, and a barrier is a Raft entry every voter writes.
	jt, err := m.tokens.Peek(in.Token)
	if err != nil {
		return nil, err
	}
	if jt.EffectiveKind() != KindAgent {
		return nil, ErrTokenKind
	}

	// As in HandleJoin: still the leader of a quorum, and the FSM holds what
	// earlier terms committed — the checks below read it.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return nil, fmt.Errorf("leader barrier failed: %w", err)
	}
	// An agent holds ca.crt, from which the old proxy secret is derived.
	if !m.ProxyRotationSettled(time.Now()) {
		return nil, ErrProxyRotationPending
	}

	state := m.raft.GetFSM().GetState()
	if waiting := m.managersNotReadyForAgents(state); len(waiting) > 0 {
		return nil, fmt.Errorf("%w; waiting for %s", ErrManagersNotReadyForAgents, strings.Join(waiting, ", "))
	}
	if countKind(state, KindAgent) >= m.agentCap() {
		return nil, ErrMaxAgentsReached
	}
	if err := ValidNodeName(in.NodeName); err != nil {
		return nil, err
	}
	ip, err := agentIP(in.APIAddress, in.GRPCAddress)
	if err != nil {
		return nil, err
	}
	if identityTaken(state, in.NodeName, ip) {
		return nil, ErrNodeIdentityTaken
	}
	// The managers' streaming relays reach an agent's panel over HTTPS only
	// (PanelEndpointChecked): an agent registered without its panel CA would
	// have its terminal, log and file streams refused.
	if validatePanelCA(string(in.PanelCA)) != nil {
		return nil, ErrAgentPanelCARequired
	}
	if _, err := csrPublicKey(in.CSR); err != nil {
		return nil, err
	}
	if err := m.ensureCAKey(); err != nil {
		return nil, err
	}

	id := uuid.New().String()
	// The certificate names the registered IP and nothing else; the request's
	// subject and SANs are ignored (SignNodeCSR).
	certPEM, err := m.tls.SignNodeCSR(id, []string{ip.String()}, CertRoleAgent, in.CSR)
	if err != nil {
		return nil, fmt.Errorf("sign agent certificate: %w", err)
	}
	caPEM, err := m.tls.LoadCACert()
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	keyBuf := make([]byte, 32)
	if _, err := rand.Read(keyBuf); err != nil {
		return nil, fmt.Errorf("generate agent key: %w", err)
	}
	agentKey := hex.EncodeToString(keyBuf) // the form of the managers' proxy secret

	// Spend the token only now, with nothing left but the writes (HandleJoin).
	used, err := m.tokens.Validate(in.Token, KindAgent)
	if err != nil {
		return nil, err
	}
	// The key before the record: managers never see the agent without the key
	// they sign for it with, and a key with no record is never read.
	if err := m.SetConfig(AgentKeyConfigKey(id), agentKey); err != nil {
		m.undoRegisterAgent(id, used)
		return nil, fmt.Errorf("register agent key: %w", err)
	}
	now := time.Now()
	node := Node{
		ID:          id,
		Name:        in.NodeName,
		APIAddress:  in.APIAddress,
		GRPCAddress: in.GRPCAddress,
		Kind:        KindAgent, // and no Raft role: never a voter or a non-voter
		Status:      StatusOnline,
		JoinedAt:    now,
		LastSeen:    now,
	}
	if err := m.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(node)}, 5*time.Second); err != nil {
		m.undoRegisterAgent(id, used)
		return nil, fmt.Errorf("register agent: %w", err)
	}
	// Never cleared, not even by the undo below: once a cluster has had an
	// agent the derived secret stays dead (configKeyAgentsSeen). A failed
	// write is tried again by the next registration; until then the agent's
	// record does the same.
	if v, _ := m.raft.GetFSM().GetConfigValue(configKeyAgentsSeen); v == "" {
		if err := m.SetConfig(configKeyAgentsSeen, "true"); err != nil {
			slog.Warn("could not record that this cluster has had an agent", "component", "cluster", "node_id", id, "error", err)
		}
	}
	if err := m.SetPanelCA(id, string(in.PanelCA)); err != nil {
		m.undoRegisterAgent(id, used)
		return nil, fmt.Errorf("register agent panel CA: %w", err)
	}

	m.events.Emit(EventNodeJoined, id, in.NodeName, "agent registered")
	slog.Info("agent registered", "component", "cluster", "node_id", id, "name", in.NodeName, "address", ip.String(), "version", in.Version)
	return &RegisterAgentResult{
		NodeID:      id,
		AgentKey:    agentKey,
		ClusterName: state.Config["cluster_name"],
		NodeCert:    certPEM,
		CACert:      caPEM,
		Managers:    m.managerInfos(),
	}, nil
}

// undoRegisterAgent takes back a registration that failed after its token was
// spent and puts the token back for a retry. Best-effort: a write fails most
// often because this node lost leadership, and then these fail too; what is
// left is the record of an agent that never got its certificate, removed like
// any node.
func (m *Manager) undoRegisterAgent(id string, used *JoinToken) {
	if err := m.raft.Apply(Command{Type: CmdDeleteConfig, Key: AgentKeyConfigKey(id)}, 5*time.Second); err != nil {
		slog.Warn("agent registration rollback: key not removed", "component", "cluster", "node_id", id, "error", err)
	}
	if err := m.raft.Apply(Command{Type: CmdRemoveNode, Key: id}, 5*time.Second); err != nil {
		slog.Warn("agent registration rollback: record not removed", "component", "cluster", "node_id", id, "error", err)
	}
	m.forgetPanelCA(id)
	m.tokens.Restore(used)
}

// agentIP is the IP an agent registers at. Its panel and gRPC addresses must
// both be ip:port on it: the certificate names that IP, and the gRPC address
// is where managers dial the agent, so one on another host would be an
// address nothing here checked.
func agentIP(apiAddr, grpcAddr string) (net.IP, error) {
	apiIP, apiOK := ipAndPort(apiAddr)
	grpcIP, grpcOK := ipAndPort(grpcAddr)
	if !apiOK || !grpcOK || !apiIP.Equal(grpcIP) {
		return nil, errAgentAddress
	}
	return apiIP, nil
}

func ipAndPort(addr string) (net.IP, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, false
	}
	ip := net.ParseIP(host)
	p, err := strconv.Atoi(port)
	return ip, ip != nil && err == nil && p > 0 && p <= 65535
}

// identityTaken reports whether a node already has this name or an address on
// this IP. Peers check a node's certificate against the address they dial, so
// an agent holding a certificate for another node's IP could answer as that
// node. IPs are compared as addresses, not as text: an IPv4-mapped address
// and its IPv4 form are one SAN.
func identityTaken(state ClusterState, name string, ip net.IP) bool {
	for _, n := range state.Nodes {
		if n.Name == name || addressOn(n.APIAddress, ip) || addressOn(n.GRPCAddress, ip) {
			return true
		}
	}
	return false
}

// addressOn reports whether a stored node address is on ip. A record may carry
// a scheme from an older version, which normalisePanelAuthority strips. A node
// recorded by host name never matches: a certificate for an IP does not
// answer for a name.
func addressOn(addr string, ip net.IP) bool {
	host, _, err := net.SplitHostPort(normalisePanelAuthority(addr, fallbackPanelPort))
	if err != nil {
		return false
	}
	other := net.ParseIP(host)
	return other != nil && other.Equal(ip)
}
