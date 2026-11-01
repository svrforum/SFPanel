package cluster

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

// An installed agent joins at the leader's address on its own network: a
// server reached over Tailscale may reach no LAN address of the leader, and a
// LAN server no Tailscale one. The port is the leader's gRPC port.
func TestJoinAddressForPicksTheLeadersAddressOnTheTargetsNetwork(t *testing.T) {
	if _, err := (&Manager{}).JoinAddressFor(net.ParseIP("192.0.2.9")); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("off the leader: %v", err)
	}
	mgr, _, _, _ := realLeader(t)
	fsm := mgr.raft.GetFSM()
	port := mgr.GetConfig().GRPCPort
	self := fsm.GetNode(mgr.LocalNodeID())
	at := func(grpcHost string, addrs ...string) {
		t.Helper()
		n := *self
		n.GRPCAddress = net.JoinHostPort(grpcHost, fmt.Sprint(port))
		n.Addresses = addrs
		addTestNode(t, fsm, n)
	}

	at("192.0.2.1", "192.0.2.1", "100.64.0.1")
	for target, want := range map[string]string{
		"100.64.0.9":   fmt.Sprintf("100.64.0.1:%d", port),
		"192.0.2.9":    fmt.Sprintf("192.0.2.1:%d", port),
		"198.51.100.7": fmt.Sprintf("192.0.2.1:%d", port),
	} {
		if got, err := mgr.JoinAddressFor(net.ParseIP(target)); err != nil || got != want {
			t.Errorf("for %s: %q %v, want %q", target, got, err, want)
		}
	}

	at("192.0.2.1", "192.0.2.1")
	if got, err := mgr.JoinAddressFor(net.ParseIP("100.64.0.9")); err == nil || !strings.Contains(err.Error(), "no Tailscale address") {
		t.Errorf("a Tailscale target and a leader without a Tailscale address: %q %v", got, err)
	}
	at("100.64.0.1", "100.64.0.1")
	if got, err := mgr.JoinAddressFor(net.ParseIP("192.0.2.9")); err == nil || !strings.Contains(err.Error(), "no address outside Tailscale") {
		t.Errorf("a LAN target and a leader on Tailscale only: %q %v", got, err)
	}
}
