package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func validBase() *Config {
	return &Config{
		Server:   ServerConfig{Port: 3628},
		Database: DatabaseConfig{Path: "./sfpanel.db"},
		Auth:     AuthConfig{JWTSecret: strings.Repeat("a", 64)},
	}
}

func TestClusterRoleValidation(t *testing.T) {
	agent := func(mut func(*ClusterConfig)) *Config {
		c := validBase()
		c.Cluster = ClusterConfig{
			Enabled: true, Role: ClusterRoleAgent, NodeID: "a1", AdvertiseAddress: "192.0.2.10",
			Managers: []ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}},
		}
		if mut != nil {
			mut(&c.Cluster)
		}
		return c
	}
	if err := agent(nil).Validate(); err != nil {
		t.Fatalf("a complete agent config refused: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*ClusterConfig)
		want string
	}{
		{"unknown role", func(c *ClusterConfig) { c.Role = "observer" }, "cluster.role"},
		{"agent without node id", func(c *ClusterConfig) { c.NodeID = "" }, "cluster.node_id"},
		{"agent without advertise address", func(c *ClusterConfig) { c.AdvertiseAddress = "" }, "cluster.advertise_address"},
		{"agent without managers", func(c *ClusterConfig) { c.Managers = nil }, "cluster.managers"},
		{"manager entry without address", func(c *ClusterConfig) { c.Managers[0].GRPCAddress = "" }, "cluster.managers"},
		{"bad allowed source", func(c *ClusterConfig) { c.AgentAllowedSources = []string{"not-an-ip"} }, "cluster.agent_allowed_sources"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := agent(tc.mut).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %s", err, tc.want)
			}
		})
	}
	c := agent(func(c *ClusterConfig) { c.AgentAllowedSources = []string{"192.0.2.0/24", "198.51.100.7"} })
	if err := c.Validate(); err != nil {
		t.Fatalf("CIDR and single-address sources refused: %v", err)
	}
	// A manager or standalone config needs none of the agent fields.
	m := validBase()
	m.Cluster = ClusterConfig{Enabled: true, Role: ClusterRoleManager}
	if err := m.Validate(); err != nil {
		t.Fatalf("manager config refused: %v", err)
	}
}

func TestClusterRoleRoundTripsAndDefaults(t *testing.T) {
	c := ClusterConfig{Role: ClusterRoleAgent, Managers: []ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}}}
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back ClusterConfig
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsAgent() || len(back.Managers) != 1 || back.Managers[0].GRPCAddress != "192.0.2.1:3629" {
		t.Fatalf("round trip lost the agent fields: %+v", back)
	}
	if (ClusterConfig{}).MaxAgentsOrDefault() != DefaultMaxAgents || (ClusterConfig{MaxAgents: 7}).MaxAgentsOrDefault() != 7 {
		t.Fatal("MaxAgentsOrDefault")
	}
	if (ClusterConfig{}).IsAgent() || (ClusterConfig{Role: ClusterRoleManager}).IsAgent() {
		t.Fatal("IsAgent true for a non-agent")
	}
}
