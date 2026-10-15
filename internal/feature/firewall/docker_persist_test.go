package firewall

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// dockerUserChain is iptables with a DOCKER-USER chain in it: -S lists the
// chain, -C, -I, -D and -N act on it the way iptables does. Rules are kept as
// iptables-save prints them.
type dockerUserChain struct {
	*exec.MockCommander
	exists bool
	rules  []string
	calls  [][]string
}

func newDockerUserChain(rules ...string) *dockerUserChain {
	// A copy: the chain grows in place, and the rules may be a fixture's.
	return &dockerUserChain{MockCommander: exec.NewMockCommander(), exists: true, rules: slices.Clone(rules)}
}

func (f *dockerUserChain) Run(name string, args ...string) (string, error) {
	if name != "iptables" {
		return f.MockCommander.Run(name, args...)
	}
	f.calls = append(f.calls, append([]string{}, args...))
	if len(args) < 2 || args[1] != "DOCKER-USER" {
		return "", errors.New("unexpected call")
	}
	if !f.exists && args[0] != "-N" {
		return "iptables: No chain/target/match by that name.\n", errors.New("exit status 1")
	}
	rule := func(spec []string) string {
		quoted := make([]string, len(spec))
		for i, a := range spec {
			quoted[i] = a
			if strings.ContainsAny(a, " []") {
				quoted[i] = strconv.Quote(a)
			}
		}
		return "-A DOCKER-USER " + strings.Join(quoted, " ")
	}
	switch args[0] {
	case "-S":
		return "-N DOCKER-USER\n" + strings.Join(f.rules, "\n") + "\n", nil
	case "-N":
		if f.exists {
			return "iptables: Chain already exists.\n", errors.New("exit status 1")
		}
		f.exists = true
		return "", nil
	case "-C":
		if slices.Contains(f.rules, rule(args[2:])) {
			return "", nil
		}
		return "iptables: Bad rule (does a matching rule exist in that chain?).\n", errors.New("exit status 1")
	case "-I":
		pos, err := strconv.Atoi(args[2])
		if err != nil || pos < 1 || pos > len(f.rules)+1 {
			return "iptables: Index of insertion too big.\n", errors.New("exit status 1")
		}
		f.rules = slices.Insert(f.rules, pos-1, rule(args[3:]))
		return "", nil
	case "-D":
		i := slices.Index(f.rules, rule(args[2:]))
		if i < 0 {
			return "iptables: Bad rule (does a matching rule exist in that chain?).\n", errors.New("exit status 1")
		}
		f.rules = slices.Delete(f.rules, i, i+1)
		return "", nil
	}
	return "", errors.New("unexpected call")
}

// The panel's rules for host port 81: let one network in, log and drop the rest.
const (
	acceptNet = `-A DOCKER-USER -s 198.51.100.0/24 -p tcp -m conntrack --ctstate DNAT --ctorigdstport 81 --ctdir ORIGINAL -m comment --comment sfpanel -j ACCEPT`
	logPort   = `-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 81 --ctdir ORIGINAL -m comment --comment sfpanel -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=81 "`
	dropPort  = `-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 81 --ctdir ORIGINAL -m comment --comment sfpanel -j DROP`
	// What an operator adds on a Proxmox host running Docker, so that the
	// guests' bridge is not dropped by Docker's FORWARD policy.
	guestBridge = `-A DOCKER-USER -i vmbr0 -o vmbr0 -j ACCEPT`
)

func writeSaved(t *testing.T, rules ...string) string {
	t.Helper()
	path := withRulesFiles(t)
	content := "*filter\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The restore emptied the chain and refilled it from the file: a rule added
// by hand since the panel's last save was gone after every start. Each saved
// rule is now put back only if it is missing, after the saved rule before it,
// and nothing else in the chain is touched.
func TestRestoreLeavesOtherRulesAlone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		live, want []string
	}{
		{"a restart, everything still there",
			[]string{guestBridge, acceptNet, logPort, dropPort},
			[]string{guestBridge, acceptNet, logPort, dropPort}},
		{"after a reboot, the chain empty",
			nil,
			[]string{acceptNet, logPort, dropPort}},
		{"a rule above, and the DROP deleted by hand",
			[]string{guestBridge, acceptNet, logPort},
			[]string{guestBridge, acceptNet, logPort, dropPort}},
		{"the ACCEPT deleted by hand",
			[]string{logPort, dropPort, guestBridge},
			[]string{acceptNet, logPort, dropPort, guestBridge}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeSaved(t, acceptNet, logPort, dropPort)
			chain := newDockerUserChain(tc.live...)
			RestoreDockerUserRules(chain)
			if !reflect.DeepEqual(chain.rules, tc.want) {
				t.Errorf("chain\n  %s\nwant\n  %s", strings.Join(chain.rules, "\n  "), strings.Join(tc.want, "\n  "))
			}
			for _, c := range chain.calls {
				if c[0] == "-F" || c[0] == "-X" || c[0] == "-N" {
					t.Errorf("ran iptables %q", c)
				}
			}
			for _, c := range chain.MockCommander.Calls {
				if c.Name == "iptables-restore" {
					t.Errorf("ran iptables-restore %q", c.Args)
				}
			}
		})
	}
}

// The panel can start before Docker has made the chain. It is made, empty,
// as Docker would, and Docker uses it as it finds it.
func TestRestoreBeforeDockerMakesTheChain(t *testing.T) {
	writeSaved(t, acceptNet, dropPort)
	chain := newDockerUserChain()
	chain.exists = false
	RestoreDockerUserRules(chain)
	if !chain.exists || !reflect.DeepEqual(chain.rules, []string{acceptNet, dropPort}) {
		t.Errorf("exists %v, chain %q", chain.exists, chain.rules)
	}
}

// After an update the panel restarts without a reboot, and the chain still
// holds the container-pinned rules from before #112. Their replacements go in,
// and they go; rules nothing names a host port for stay, as they were.
func TestRestoreReplacesRulesMovedToHostPorts(t *testing.T) {
	writeSaved(t, savedBeforeHostPorts[2:len(savedBeforeHostPorts)-1]...)
	chain := newDockerUserChain(savedBeforeHostPorts[2 : len(savedBeforeHostPorts)-1]...)
	RestoreDockerUserRules(chain)
	moved, _ := migrateDockerUserRules(savedBeforeHostPorts)
	want := moved[2 : len(moved)-1]
	if !reflect.DeepEqual(chain.rules, want) {
		t.Errorf("chain\n  %s\nwant\n  %s", strings.Join(chain.rules, "\n  "), strings.Join(want, "\n  "))
	}
}

// Only the panel's own rules are saved: tagged, or from before the tag, the
// HPORT LOG and the conntrack form. The operator's rules, Docker's RETURN and
// the chain's declaration — which made iptables-restore empty the chain —
// are not.
func TestSaveKeepsOnlyThePanelsRules(t *testing.T) {
	path := withRulesFiles(t)
	untaggedLog := `-A DOCKER-USER -p udp -m conntrack --ctstate DNAT --ctorigdstport 5353 --ctdir ORIGINAL -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=5353 "`
	untaggedDrop := `-A DOCKER-USER -p udp -m conntrack --ctstate DNAT --ctorigdstport 5353 --ctdir ORIGINAL -j DROP`
	save := strings.Join([]string{
		"# Generated by iptables-save v1.8.11 on Sun Oct 11 00:00:00 2026",
		"*filter",
		":INPUT ACCEPT [0:0]",
		":FORWARD DROP [0:0]",
		":OUTPUT ACCEPT [0:0]",
		":DOCKER-USER - [0:0]",
		"-A FORWARD -j DOCKER-USER",
		guestBridge,
		acceptNet, logPort, dropPort,
		untaggedLog, untaggedDrop,
		"-A DOCKER-USER -p tcp -m tcp --dport 8080 -j DROP",
		"-A DOCKER-USER -j RETURN",
		"COMMIT",
	}, "\n") + "\n"
	cmd := exec.NewMockCommander()
	cmd.SetOutput("iptables-save", save, nil)
	if err := (&Handler{Cmd: cmd}).saveDockerUserRules(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"*filter", acceptNet, logPort, dropPort, untaggedLog, untaggedDrop, "COMMIT"}, "\n") + "\n"
	if string(got) != want {
		t.Errorf("saved\n%s\nwant\n%s", got, want)
	}
}

// iptables-save quotes a value with anything but letters, digits, - and _,
// and escapes quotes in it; -C, -I and -D take it as one argument.
func TestSplitSaveArgs(t *testing.T) {
	for in, want := range map[string][]string{
		`-p tcp -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=81 "`: {"-p", "tcp", "-j", "LOG", "--log-prefix", "[DOCKER-USER DROP] HPORT=81 "},
		`-m comment --comment "a \"b\" c" -j ACCEPT`:                {"-m", "comment", "--comment", `a "b" c`, "-j", "ACCEPT"},
		`-m comment --comment "" -j DROP`:                           {"-m", "comment", "--comment", "", "-j", "DROP"},
		`-i vmbr0  -o vmbr0 -j ACCEPT`:                              {"-i", "vmbr0", "-o", "vmbr0", "-j", "ACCEPT"},
	} {
		if got := splitSaveArgs(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", in, got, want)
		}
	}
}

// On a host without Docker — a Proxmox host, say — neither chain exists. The
// tab showed that as an error.
func TestDockerTabWithoutDocker(t *testing.T) {
	cmd := exec.NewMockCommander()
	cmd.SetOutput("exists:iptables", "", nil)
	cmd.SetOutput("iptables", "iptables: No chain/target/match by that name.\n", errors.New("exit status 1"))
	rec := call(t, (&Handler{Cmd: cmd}).GetDockerFirewall, http.MethodGet, "/firewall/docker", "")
	e := decode(t, rec)
	if rec.Code != http.StatusOK || string(e.Data) != `{"docker_installed":false,"ports":[],"rules":[]}` {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}
