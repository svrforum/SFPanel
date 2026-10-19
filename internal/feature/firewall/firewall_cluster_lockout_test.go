package firewall

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/platform"
)

func TestClusterPortsMissing(t *testing.T) {
	rules := []UFWRule{{Action: "ALLOW", To: "22/tcp"}, {Action: "ALLOW", To: "3628/tcp"}}
	if got := clusterPortsMissing(rules, []int{3629, 3628}); !slices.Equal(got, []int{3629}) {
		t.Fatalf("missing %v", got)
	}
	if got := clusterPortsMissing(rules, nil); len(got) != 0 {
		t.Fatal("a standalone node has no cluster ports")
	}
	for _, tc := range []struct {
		name  string
		rules []UFWRule
		want  []int
	}{
		{"a range covers both", []UFWRule{{Action: "ALLOW IN", To: "3629:3630/tcp"}}, nil},
		{"a list covers both", []UFWRule{{Action: "ALLOW IN", To: "3630,3629/tcp"}}, nil},
		{"a deny is no allow", []UFWRule{{Action: "DENY IN", To: "3629/tcp"}, {Action: "ALLOW IN", To: "3630/tcp"}}, []int{3629}},
		{"nothing open, in the order asked", nil, []int{3629, 3630}},
	} {
		if got := clusterPortsMissing(tc.rules, []int{3629, 3630}); !slices.Equal(got, tc.want) {
			t.Errorf("%s: missing %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDenyOnAClusterPortIsALockout(t *testing.T) {
	if p, bad := wouldCloseClusterPort(AddRuleRequest{Action: "deny", Port: "3629", Protocol: "tcp"}, []int{3629, 3630}); !bad || p != 3629 {
		t.Fatalf("deny 3629: %d %v", p, bad)
	}
	if _, bad := wouldCloseClusterPort(AddRuleRequest{Action: "deny", Port: "8080", Protocol: "tcp"}, []int{3629, 3630}); bad {
		t.Fatal("an unrelated deny flagged")
	}
	for _, tc := range []struct {
		name string
		req  AddRuleRequest
		want int // 0: not a lockout
	}{
		{"reject", AddRuleRequest{Action: "reject", Port: "3630", Protocol: "tcp"}, 3630},
		{"limit", AddRuleRequest{Action: "limit", Port: "3629", Protocol: "tcp"}, 3629},
		{"a range names the first port it covers", AddRuleRequest{Action: "deny", Port: "3600:3700", Protocol: "tcp"}, 3629},
		{"a list", AddRuleRequest{Action: "deny", Port: "80,3630", Protocol: "tcp"}, 3630},
		{"upper case", AddRuleRequest{Action: "DENY", Port: "3629"}, 3629},
		{"a port with its protocol", AddRuleRequest{Action: "deny", Port: "3629/tcp"}, 3629},
		{"allow", AddRuleRequest{Action: "allow", Port: "3629", Protocol: "tcp"}, 0},
		{"no action", AddRuleRequest{Port: "3629"}, 0},
		{"no port", AddRuleRequest{Action: "deny"}, 0},
		{"a range beside them", AddRuleRequest{Action: "deny", Port: "3631:3700", Protocol: "tcp"}, 0},
	} {
		p, bad := wouldCloseClusterPort(tc.req, []int{3629, 3630})
		if want := tc.want != 0; bad != want || p != tc.want {
			t.Errorf("%s: got %d %v, want %d %v", tc.name, p, bad, tc.want, want)
		}
	}
	if _, bad := wouldCloseClusterPort(AddRuleRequest{Action: "deny", Port: "3629", Protocol: "tcp"}, nil); bad {
		t.Error("a standalone node has no cluster ports to close")
	}
}

// portsOf is a Handler.ClusterPorts that always answers ports.
func portsOf(ports ...int) func() []int { return func() []int { return ports } }

var (
	agentPorts   = portsOf(3629, 3628) // gRPC and the panel's port
	managerPorts = portsOf(3629, 3630) // gRPC and Raft
)

// wantClusterLockout requires the lockout refusal naming exactly the cluster
// ports ports, and that nothing starting with write ran. The details are the
// reason: the SSH and panel refusal has the same status and code.
func wantClusterLockout(t *testing.T, m *ufwArgsCommander, rec *httptest.ResponseRecorder, ports, write string) {
	t.Helper()
	e := decode(t, rec)
	if rec.Code != http.StatusConflict || e.Error.Code != "UFW_LOCKOUT" || e.Error.Details["cluster_ports"] != ports ||
		!strings.Contains(e.Error.Message, "cluster port") {
		t.Errorf("got %d %s, want 409 UFW_LOCKOUT naming cluster ports %q", rec.Code, rec.Body.String(), ports)
	}
	for _, c := range m.calls {
		if strings.HasPrefix(c, write) {
			t.Errorf("ran %q", c)
		}
	}
}

// ran reports whether m ran exactly cmdline.
func ran(m *ufwArgsCommander, cmdline string) bool { return slices.Contains(m.calls, cmdline) }

const stagedRulesHeader = "Added user rules (see 'ufw status' for running firewall):\n"

// Enabling ufw with SSH allowed and nothing else used to pass the guard. An
// agent is managed through its gRPC port and its panel's, and managers reach
// each other on gRPC and Raft: with those closed the cluster loses the node.
func TestEnablingUFWKeepsTheClusterPortsOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ports  func() []int
		staged string
		target string
		refuse string // the cluster ports the refusal names; "" when the enable goes through
	}{
		{"an agent with only SSH open", agentPorts, "ufw allow 22/tcp\n", "/firewall/enable", "3629, 3628"},
		{"an agent with SSH and its panel's port open", agentPorts, "ufw allow 22/tcp\nufw allow 3628/tcp\n", "/firewall/enable", "3629"},
		{"a manager with only SSH open", managerPorts, "ufw allow 22/tcp\n", "/firewall/enable", "3629, 3630"},
		{"an agent with both open", agentPorts, "ufw allow 22/tcp\nufw allow 3628,3629/tcp\n", "/firewall/enable", ""},
		{"a manager with gRPC and Raft open", managerPorts, "ufw allow 22/tcp\nufw allow 3629:3630/tcp\n", "/firewall/enable", ""},
		{"forced", agentPorts, "ufw allow 22/tcp\n", "/firewall/enable?force=true", ""},
		{"a standalone panel", nil, "ufw allow 22/tcp\n", "/firewall/enable", ""},
		{"a node with no cluster ports", portsOf(), "ufw allow 22/tcp\n", "/firewall/enable", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform.SetForTest(t, platform.Info{})
			m := newUFWArgsCommander()
			m.setExists("ufw", true)
			m.set("ufw status numbered", "Status: inactive\n", nil)
			m.set("ufw show added", stagedRulesHeader+tc.staged, nil)
			h := &Handler{Cmd: m, PanelPort: 3628, ClusterPorts: tc.ports}
			rec := call(t, h.EnableUFW, http.MethodPost, tc.target, "")
			if tc.refuse != "" {
				wantClusterLockout(t, m, rec, tc.refuse, "ufw --force enable")
				return
			}
			if rec.Code != http.StatusOK || !ran(m, "ufw --force enable") {
				t.Errorf("got %d %s, ran %q; want the enable to run", rec.Code, rec.Body.String(), m.calls)
			}
		})
	}
}

// ufw puts a deny, reject or limit that differs from an allow only in its
// action in that allow's place, so one for a cluster port can close it to
// the rest of the cluster. Refused unless forced, as for SSH and the panel.
func TestAddingADenyKeepsTheClusterPortsOpen(t *testing.T) {
	for _, tc := range []struct {
		name, target, body string
		ports              func() []int
		refuse             string
	}{
		{"deny gRPC on a manager", "/firewall/rules", `{"action":"deny","port":"3629","protocol":"tcp"}`, managerPorts, "3629"},
		{"deny gRPC on an agent", "/firewall/rules", `{"action":"deny","port":"3629","protocol":"tcp"}`, agentPorts, "3629"},
		{"reject a range over Raft", "/firewall/rules", `{"action":"reject","port":"3630:3700","protocol":"tcp"}`, managerPorts, "3630"},
		{"limit a list with gRPC in it", "/firewall/rules", `{"action":"limit","port":"80,3629","protocol":"tcp"}`, managerPorts, "3629"},
		{"deny another port", "/firewall/rules", `{"action":"deny","port":"8080","protocol":"tcp"}`, managerPorts, ""},
		{"allow gRPC", "/firewall/rules", `{"action":"allow","port":"3629","protocol":"tcp"}`, managerPorts, ""},
		{"forced", "/firewall/rules?force=true", `{"action":"deny","port":"3629","protocol":"tcp"}`, managerPorts, ""},
		{"a standalone panel", "/firewall/rules", `{"action":"deny","port":"3629","protocol":"tcp"}`, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform.SetForTest(t, platform.Info{})
			m := newUFWArgsCommander()
			m.setExists("ufw", true)
			h := &Handler{Cmd: m, PanelPort: 3628, ClusterPorts: tc.ports}
			rec := call(t, h.AddRule, http.MethodPost, tc.target, tc.body)
			if tc.refuse != "" {
				wantClusterLockout(t, m, rec, tc.refuse, "ufw ")
				return
			}
			if rec.Code != http.StatusOK || len(m.calls) == 0 || !strings.HasPrefix(m.calls[len(m.calls)-1], "ufw ") {
				t.Errorf("got %d %s, ran %q; want the rule added", rec.Code, rec.Body.String(), m.calls)
			}
		})
	}
}

// Deleting one of two allows for a cluster port is fine; deleting the last
// one is not. A port no rule allowed before is not the deleted rule's to keep.
func TestDeletingTheLastAllowForAClusterPort(t *testing.T) {
	const header = "Status: active\n\n     To                         Action      From\n     --                         ------      ----\n"
	const rules = header + `[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 3629/tcp                   ALLOW IN    198.51.100.0/24
[ 3] 3629/tcp                   ALLOW IN    203.0.113.0/24
[ 4] 3628/tcp                   ALLOW IN    198.51.100.0/24
[ 5] 3629:3630/tcp              ALLOW IN    198.51.100.0/24
`
	const noRaft = header + `[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 3629/tcp                   ALLOW IN    198.51.100.0/24
[ 3] 3629/tcp                   ALLOW IN    203.0.113.0/24
`
	for _, tc := range []struct {
		name     string
		ports    func() []int
		numbered string
		target   string
		number   string
		refuse   string
	}{
		{"an agent's only allow for its panel's port", agentPorts, rules, "/firewall/rules/4", "4", "3628"},
		{"a manager's only allow for Raft", managerPorts, rules, "/firewall/rules/5", "5", "3630"},
		{"both of a manager's last allows", managerPorts, header + "[ 1] 22/tcp                     ALLOW IN    Anywhere\n[ 2] 3629:3630/tcp              ALLOW IN    Anywhere\n", "/firewall/rules/2", "2", "3629, 3630"},
		{"one of two allows for gRPC", managerPorts, rules, "/firewall/rules/2", "2", ""},
		{"SSH, with the cluster ports still open", agentPorts, rules, "/firewall/rules/1", "1", ""},
		{"gRPC, while Raft was never open", managerPorts, noRaft, "/firewall/rules/2", "2", ""},
		{"forced", managerPorts, rules, "/firewall/rules/5?force=true", "5", ""},
		{"a standalone panel", nil, rules, "/firewall/rules/5", "5", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform.SetForTest(t, platform.Info{})
			m := newUFWArgsCommander()
			m.setExists("ufw", true)
			m.set("ufw status numbered", tc.numbered, nil)
			h := &Handler{Cmd: m, PanelPort: 3628, ClusterPorts: tc.ports}
			rec := call(t, h.DeleteRule, http.MethodDelete, tc.target, "", "number", tc.number)
			if tc.refuse != "" {
				wantClusterLockout(t, m, rec, tc.refuse, "ufw --force delete")
				return
			}
			if rec.Code != http.StatusOK || !ran(m, "ufw --force delete "+tc.number) {
				t.Errorf("got %d %s, ran %q; want the rule deleted", rec.Code, rec.Body.String(), m.calls)
			}
		})
	}
}
