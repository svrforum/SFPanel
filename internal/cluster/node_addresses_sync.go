package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"time"
)

// addressReportEvery is how often, at most, a node's recorded addresses
// change: a host whose interfaces flap must not cost a Raft entry on every
// ping. A variable so tests can shorten it.
var addressReportEvery = time.Minute

// primaryHost is the address n is registered at: the host of its gRPC
// address, the one every other address of it is listed after.
func primaryHost(n *Node) string {
	host, _, err := net.SplitHostPort(n.GRPCAddress)
	if err != nil {
		return ""
	}
	return host
}

// checkNodeAddresses returns addrs in canonical form, or why n's record must
// not hold them: every entry an IP, once each, at most maxSelfAddresses, the
// first the address n is registered at.
func checkNodeAddresses(n *Node, addrs []string) ([]string, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: no addresses", ErrInvalidAddresses)
	}
	if len(addrs) > maxSelfAddresses {
		return nil, fmt.Errorf("%w: more than %d addresses", ErrInvalidAddresses, maxSelfAddresses)
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			return nil, fmt.Errorf("%w: %q is not an IP address", ErrInvalidAddresses, a)
		}
		c := ip.String()
		if slices.Contains(out, c) {
			return nil, fmt.Errorf("%w: %s is listed more than once", ErrInvalidAddresses, c)
		}
		out = append(out, c)
	}
	primary := net.ParseIP(primaryHost(n))
	if primary == nil || !primary.Equal(net.ParseIP(out[0])) {
		return nil, fmt.Errorf("%w: the list must start with the address the node is registered at (%s), not %s",
			ErrInvalidAddresses, primaryHost(n), out[0])
	}
	return out, nil
}

// SetNodeAddresses records every IP nodeID can be reached at (Node.Addresses).
// Only the leader writes, and a list it takes is IPs only, once each, at most
// maxSelfAddresses, starting with the address the node is registered at —
// other nodes dial these. A list equal to the record changes nothing and is
// always answered; a different one is taken at most once per
// addressReportEvery per node (ErrAddressesChangedRecently).
func (m *Manager) SetNodeAddresses(nodeID string, addrs []string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	m.nodeAddrMu.Lock()
	defer m.nodeAddrMu.Unlock()

	n := m.raft.GetFSM().GetNode(nodeID)
	if n == nil {
		return ErrNodeNotFound
	}
	list, err := checkNodeAddresses(n, addrs)
	if err != nil {
		return err
	}
	if slices.Equal(n.AllAddresses(), list) {
		return nil
	}
	now := time.Now()
	if at, ok := m.nodeAddrChangedAt[nodeID]; ok && now.Sub(at) < addressReportEvery {
		return ErrAddressesChangedRecently
	}
	// Only the ID and the list: the FSM merges the fields an update sets.
	data, err := json.Marshal(Node{ID: nodeID, Addresses: list})
	if err != nil {
		return err
	}
	if err := m.raft.Apply(Command{Type: CmdUpdateNode, Key: nodeID, Value: data}, 5*time.Second); err != nil {
		return fmt.Errorf("record node addresses: %w", err)
	}
	if m.nodeAddrChangedAt == nil {
		m.nodeAddrChangedAt = map[string]time.Time{}
	}
	m.nodeAddrChangedAt[nodeID] = now
	slog.Info("node addresses recorded", "component", "cluster", "node_id", nodeID, "addresses", list)
	return nil
}

// syncNodeAddresses keeps the managers' node records in step with the
// addresses they report, on the leader's tick: its own from selfAddresses,
// each online manager's from its last ping. A report always starts with the
// address the node is registered at, whatever it put first — a manager
// whose configured advertise address differs from its record does not get
// its record refused; verifySelfAddress moves the record itself. A manager
// that reported nothing (an older one) keeps its record as it is.
func (m *Manager) syncNodeAddresses() {
	if m.raft == nil || !m.raft.IsLeader() {
		return
	}
	m.syncOneNodeAddresses(m.nodeID, m.selfAddresses())
	for id, status := range m.heartbeat.LiveHealth() {
		if id == m.nodeID || status != StatusOnline {
			continue
		}
		met := m.heartbeat.GetMetrics(id)
		if met == nil || len(met.Addresses) == 0 {
			continue
		}
		m.syncOneNodeAddresses(id, met.Addresses)
	}
}

// syncOneNodeAddresses records reported for the manager nodeID, its
// registered address first.
func (m *Manager) syncOneNodeAddresses(nodeID string, reported []string) {
	n := m.raft.GetFSM().GetNode(nodeID)
	if n == nil || !n.IsManager() {
		return
	}
	primary := primaryHost(n)
	if ip := net.ParseIP(primary); ip != nil {
		primary = ip.String()
	}
	list := []string{primary}
	for _, a := range reported {
		if ip := net.ParseIP(a); ip != nil {
			a = ip.String()
		}
		if len(list) < maxSelfAddresses && !slices.Contains(list, a) {
			list = append(list, a)
		}
	}
	err := m.SetNodeAddresses(nodeID, list)
	if err != nil && !errors.Is(err, ErrAddressesChangedRecently) && !errors.Is(err, ErrNotLeader) {
		slog.Warn("cannot record a node's addresses", "component", "cluster", "node_id", nodeID, "error", err)
	}
}
