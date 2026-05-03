package firewall

import (
	"reflect"
	"sort"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// Real `iptables -t nat -L DOCKER -n --line-numbers` output. Includes:
// - IPv4 DNAT rule (protocol "tcp")
// - The numeric protocol form ("6" → tcp) some kernels emit
// - A non-DNAT line that must be skipped
const iptablesNatDockerFixture = `Chain DOCKER (2 references)
num  target     prot opt source               destination
1    DNAT       tcp  --  0.0.0.0/0            0.0.0.0/0            tcp dpt:80 to:172.17.0.2:80
2    DNAT       6    --  0.0.0.0/0            0.0.0.0/0            tcp dpt:443 to:172.18.0.3:443
3    DNAT       udp  --  0.0.0.0/0            0.0.0.0/0            udp dpt:53 to:172.19.0.4:53
4    RETURN     all  --  0.0.0.0/0            0.0.0.0/0
`

func TestGetDockerPublishedPorts(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("iptables", iptablesNatDockerFixture, nil)
	mock.SetOutput("docker", "", nil) // empty docker ps → empty IP map
	h := &Handler{Cmd: mock}

	ports, err := h.getDockerPublishedPorts()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(ports) != 3 {
		t.Fatalf("expected 3 ports, got %d (%+v)", len(ports), ports)
	}

	// Sort by HostPort for stable comparison.
	sort.Slice(ports, func(i, j int) bool { return ports[i].HostPort < ports[j].HostPort })

	want := []DockerPublishedPort{
		{ContainerName: "172.19.0.4", ContainerIP: "172.19.0.4", HostPort: 53, ContainerPort: 53, Protocol: "udp", HostIP: "0.0.0.0"},
		{ContainerName: "172.17.0.2", ContainerIP: "172.17.0.2", HostPort: 80, ContainerPort: 80, Protocol: "tcp", HostIP: "0.0.0.0"},
		{ContainerName: "172.18.0.3", ContainerIP: "172.18.0.3", HostPort: 443, ContainerPort: 443, Protocol: "tcp", HostIP: "0.0.0.0"},
	}
	if !reflect.DeepEqual(ports, want) {
		t.Errorf("got %+v\nwant %+v", ports, want)
	}
}

const iptablesDockerUserFixture = `Chain DOCKER-USER (1 references)
num  target     prot opt source               destination
1    DROP       tcp  --  192.168.1.0/24       172.18.0.5           tcp dpt:3306
2    ACCEPT     tcp  --  10.0.0.0/8           0.0.0.0/0            tcp dpt:80
3    RETURN     all  --  0.0.0.0/0            0.0.0.0/0
`

func TestGetDockerUserRules(t *testing.T) {
	mock := exec.NewMockCommander()
	// First call: DOCKER-USER chain.
	// Second call: NAT DOCKER chain (for buildReverseDNATMap) → empty.
	mock.SetOutput("iptables", iptablesDockerUserFixture, nil)
	h := &Handler{Cmd: mock}

	rules, err := h.getDockerUserRules()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (RETURN skipped), got %d (%+v)", len(rules), rules)
	}

	want := []DockerUserRule{
		{Number: 1, Port: 3306, Protocol: "tcp", Source: "192.168.1.0/24", Action: "drop"},
		{Number: 2, Port: 80, Protocol: "tcp", Source: "10.0.0.0/8", Action: "accept"},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("got %+v\nwant %+v", rules, want)
	}
}

func TestNormalizeProtocol(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tcp", "tcp"}, {"udp", "udp"},
		{"6", "tcp"}, {"17", "udp"},
		{"all", "all"},
	}
	for _, c := range cases {
		if got := normalizeProtocol(c.in); got != c.want {
			t.Errorf("normalizeProtocol(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
