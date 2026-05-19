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
		{HostPort: 5432, Proto: "tcp", ContainerID: "abc", ContainerName: "npg-db", Stack: "npg"},
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
	require.Len(t, got[0].Containers, 1)
	require.Equal(t, "npg-db", got[0].Containers[0].Name)
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
	require.Empty(t, got[0].Containers)
	require.NotNil(t, got[0].Process)
}

func TestAggregate_OnlyContainer_BoundButNotListening(t *testing.T) {
	dnat := []PortBinding{
		{HostPort: 8080, Proto: "tcp", ContainerID: "x", ContainerName: "myapp", Stack: ""},
	}
	got := Aggregate(nil, dnat, nil)
	require.Len(t, got, 1)
	require.Equal(t, 8080, got[0].Port)
	require.Equal(t, "bound", got[0].State)
	require.Len(t, got[0].Containers, 1)
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

// Regression for P0-14: UDP bindings used to be silently coerced to tcp,
// so a UDP-only service either disappeared or was wrongly merged into a
// tcp row that happened to share its host port.
func TestAggregate_UDPBindingsPreserveProtocol(t *testing.T) {
	dnat := []PortBinding{
		{HostPort: 53, Proto: "udp", ContainerID: "dnsId", ContainerName: "pihole", Stack: "pihole"},
		{HostPort: 53, Proto: "tcp", ContainerID: "dnsId", ContainerName: "pihole", Stack: "pihole"},
	}
	ss := []SsEntry{
		{Port: 53, Proto: "udp", PID: 100, Name: "docker-proxy"},
		{Port: 53, Proto: "tcp", PID: 101, Name: "docker-proxy"},
	}
	got := Aggregate(nil, dnat, ss)
	require.Len(t, got, 2)

	var udp, tcp *PortMapRow
	for i := range got {
		switch got[i].Proto {
		case "udp":
			udp = &got[i]
		case "tcp":
			tcp = &got[i]
		}
	}
	require.NotNil(t, udp, "UDP row missing")
	require.NotNil(t, tcp, "TCP row missing")
	require.Len(t, udp.Containers, 1)
	require.Len(t, tcp.Containers, 1)
}

// Regression for P0-15: two containers publishing the same host port
// used to last-write-win — the second binding overwrote the first.
// Both should now appear in Containers.
func TestAggregate_MultipleContainersSamePort(t *testing.T) {
	dnat := []PortBinding{
		{HostPort: 8080, Proto: "tcp", ContainerID: "id-a", ContainerName: "web-a", Stack: "site-a"},
		{HostPort: 8080, Proto: "tcp", ContainerID: "id-b", ContainerName: "web-b", Stack: "site-b"},
	}
	got := Aggregate(nil, dnat, nil)
	require.Len(t, got, 1)
	require.Len(t, got[0].Containers, 2)

	names := []string{got[0].Containers[0].Name, got[0].Containers[1].Name}
	require.Contains(t, names, "web-a")
	require.Contains(t, names, "web-b")
}
