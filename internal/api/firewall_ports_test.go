package api

import (
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

// The firewall's lockout guard keeps open the ports the rest of the cluster
// reaches this node on: an agent's managers come in on gRPC and the panel's
// port, managers reach each other on gRPC and Raft (gRPC+1).
func TestFirewallClusterPorts(t *testing.T) {
	node := func(role string, enabled bool) *config.Config {
		c := &config.Config{}
		c.Server.Port = 8443
		c.Cluster.GRPCPort = 4000
		c.Cluster.Role = role
		c.Cluster.Enabled = enabled
		return c
	}
	never := func() bool { return false }
	for _, tc := range []struct {
		name string
		cfg  *config.Config
		want []int
	}{
		{"an agent", node(config.ClusterRoleAgent, true), []int{4000, 8443}},
		{"an agent whose config says enabled: false", node(config.ClusterRoleAgent, false), []int{4000, 8443}},
		{"a manager", node("", true), []int{4000, 4001}},
		{"a manager by role", node(config.ClusterRoleManager, true), []int{4000, 4001}},
		{"a standalone panel", node("", false), nil},
	} {
		if got := firewallClusterPorts(tc.cfg, never)(); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}

	// A cluster init or join from the UI makes a standalone panel a manager
	// without a restart; the guard has to know at once.
	live := false
	ports := firewallClusterPorts(node("", false), func() bool { return live })
	if got := ports(); got != nil {
		t.Fatalf("standalone: %v", got)
	}
	live = true
	if got := ports(); !slices.Equal(got, []int{4000, 4001}) {
		t.Errorf("after a runtime init: %v, want [4000 4001]", got)
	}
}
