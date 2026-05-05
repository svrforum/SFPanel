package portmap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAggregate_AllThreeSources(t *testing.T) {
	ufw := []FirewallInfo{
		{Action: "ALLOW", Scope: "192.168.1.0/24", RuleID: 12, Source: "ufw"},
	}
	dnat := []PortBinding{
		{HostPort: 5432, ContainerID: "abc", ContainerName: "npg-db", Stack: "npg"},
	}
	ss := []SsEntry{
		{Port: 5432, Proto: "tcp", PID: 9912, Name: "docker-proxy"},
	}
	got := Aggregate(map[int]FirewallInfo{5432: ufw[0]}, dnat, ss)
	require.Len(t, got, 1)
	require.Equal(t, 5432, got[0].Port)
	require.Equal(t, "tcp", got[0].Proto)
	require.NotNil(t, got[0].Firewall)
	require.Equal(t, "ALLOW", got[0].Firewall.Action)
	require.NotNil(t, got[0].Container)
	require.Equal(t, "npg-db", got[0].Container.Name)
	require.NotNil(t, got[0].Process)
	require.Equal(t, "docker-proxy", got[0].Process.Name)
}

func TestAggregate_OnlyProcess_NoFirewall_NoContainer(t *testing.T) {
	ss := []SsEntry{
		{Port: 22, Proto: "tcp", PID: 1024, Name: "sshd"},
	}
	got := Aggregate(nil, nil, ss)
	require.Len(t, got, 1)
	require.Equal(t, 22, got[0].Port)
	require.Nil(t, got[0].Firewall)
	require.Nil(t, got[0].Container)
	require.NotNil(t, got[0].Process)
}

func TestAggregate_OnlyContainer_BoundButNotListening(t *testing.T) {
	dnat := []PortBinding{
		{HostPort: 8080, ContainerID: "x", ContainerName: "myapp", Stack: ""},
	}
	got := Aggregate(nil, dnat, nil)
	require.Len(t, got, 1)
	require.Equal(t, 8080, got[0].Port)
	require.Equal(t, "bound", got[0].State)
	require.NotNil(t, got[0].Container)
	require.Nil(t, got[0].Process)
}

func TestAggregate_SortedByPortAsc(t *testing.T) {
	ss := []SsEntry{
		{Port: 80, Proto: "tcp"},
		{Port: 22, Proto: "tcp"},
		{Port: 443, Proto: "tcp"},
	}
	got := Aggregate(nil, nil, ss)
	require.Len(t, got, 3)
	require.Equal(t, 22, got[0].Port)
	require.Equal(t, 80, got[1].Port)
	require.Equal(t, 443, got[2].Port)
}

func TestAggregate_DedupesMultiProcessSameSocket(t *testing.T) {
	// Same port, two processes from `ss` (e.g. docker-proxy + actual server).
	// Aggregate should produce ONE row, processes joined into a single Process info
	// (preferring docker-proxy if present, since DNAT containers always go through it).
	ss := []SsEntry{
		{Port: 5432, Proto: "tcp", PID: 9912, Name: "docker-proxy"},
		{Port: 5432, Proto: "tcp", PID: 10001, Name: "postgres"},
	}
	got := Aggregate(nil, nil, ss)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Process)
}
