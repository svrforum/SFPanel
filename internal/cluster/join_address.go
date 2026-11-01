package cluster

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

// JoinAddressFor is the address a new agent at target joins this leader at
// (leader only): the leader's own address on the target's network, at the
// leader's gRPC port. A target in Tailscale's range (IsTailscaleIP) gets the
// leader's first Tailscale address and any other target its first address
// outside Tailscale, read from the leader's record (AllAddresses). A server
// reached over Tailscale may reach no LAN address of the leader, and a LAN
// server no Tailscale one; the configured advertise address is the LAN one,
// or empty. Without an address of the kind the target needs it refuses,
// saying which kind is missing.
func (m *Manager) JoinAddressFor(target net.IP) (string, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return "", ErrNotLeader
	}
	if target == nil {
		return "", errors.New("a join address is for a target IP, and none was given")
	}
	self := m.raft.GetFSM().GetNode(m.nodeID)
	if self == nil {
		return "", ErrNodeNotFound
	}
	tailscale := IsTailscaleIP(target)
	for _, a := range self.AllAddresses() {
		if ip := net.ParseIP(a); ip != nil && IsTailscaleIP(ip) == tailscale {
			return net.JoinHostPort(ip.String(), strconv.Itoa(m.config.GRPCPort)), nil
		}
	}
	if tailscale {
		return "", fmt.Errorf("the leader has no Tailscale address for %s to join at", target)
	}
	return "", fmt.Errorf("the leader has no address outside Tailscale for %s to join at", target)
}
