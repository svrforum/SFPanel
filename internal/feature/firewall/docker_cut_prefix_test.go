package firewall

import (
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// #117. iptables keeps 29 characters of a LOG prefix, and the one the panel
// wrote before #112, "[DOCKER-USER DROP] HPORT=<port> ", has 25 before the
// port: a four-digit port lost the space after it, a five-digit one its last
// digit as well. As iptables printed them, for host port 3001 (one network
// let in) and host port 32400, on containers at 172.18.0.2 and 172.18.0.7.
var cutPrefixRules = []string{
	`-A DOCKER-USER -s 10.0.2.0/24 -d 172.18.0.2/32 -p tcp -m tcp --dport 3001 -j ACCEPT`,
	`-A DOCKER-USER -d 172.18.0.2/32 -p tcp -m tcp --dport 3001 -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=3001"`,
	`-A DOCKER-USER -d 172.18.0.2/32 -p tcp -m tcp --dport 3001 -j DROP`,
	`-A DOCKER-USER -d 172.18.0.7/32 -p tcp -m tcp --dport 32400 -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=3240"`,
	`-A DOCKER-USER -d 172.18.0.7/32 -p tcp -m tcp --dport 32400 -j DROP`,
}

// Where they belong: on the host ports, with LOG prefixes that fit.
var cutPrefixMoved = []string{
	`-A DOCKER-USER -s 10.0.2.0/24 -p tcp -m conntrack --ctstate DNAT --ctorigdstport 3001 --ctdir ORIGINAL -j ACCEPT`,
	`-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 3001 --ctdir ORIGINAL -j LOG --log-prefix "[DOCKER-USER DROP] HP=3001 "`,
	`-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 3001 --ctdir ORIGINAL -j DROP`,
	`-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 32400 --ctdir ORIGINAL -j LOG --log-prefix "[DOCKER-USER DROP] HP=32400 "`,
	`-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 32400 --ctdir ORIGINAL -j DROP`,
}

// natListing is Docker's NAT chain as iptables -t nat -L DOCKER -n
// --line-numbers prints it, forwarding each host port to an address.
func natListing(forwards ...string) string {
	out := "Chain DOCKER (2 references)\nnum  target     prot opt source               destination         \n"
	for i, f := range forwards {
		hostPort, to, _ := strings.Cut(f, ">")
		out += strings.Join([]string{strconv.Itoa(i + 1), "   DNAT       tcp  --  0.0.0.0/0            0.0.0.0/0            tcp dpt:", hostPort, " to:", to, "\n"}, "")
	}
	return out
}

func TestCutPrefixesMoveToTheHostPortsDockerPublishes(t *testing.T) {
	for _, tc := range []struct {
		name string
		nat  string
	}{
		// After an update: the containers have the addresses in the rules.
		// Another container also has container port 3001 on a host port the
		// cut "3001" could stand for; the address says which one is meant.
		{"at the rules' addresses", natListing("3001>172.18.0.2:3001", "30011>172.18.0.5:3001", "32400>172.18.0.7:32400")},
		// After a reboot the containers came back on other addresses; one
		// port each could be meant.
		{"at new addresses", natListing("3001>172.18.0.4:3001", "32400>172.18.0.3:32400")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, moved := migrateDockerUserRules(cutPrefixRules, parseDNATPorts(tc.nat))
			if !reflect.DeepEqual(got, cutPrefixMoved) || moved != len(cutPrefixRules) {
				t.Errorf("moved %d, got\n  %s\nwant\n  %s", moved, strings.Join(got, "\n  "), strings.Join(cutPrefixMoved, "\n  "))
			}
		})
	}
}

// A cut prefix that more than one port could have been, and nothing to tell
// them apart, stays where it was rather than move to the wrong port and leave
// the right one open.
func TestCutPrefixesStayWhenTheirPortCannotBeTold(t *testing.T) {
	for _, tc := range []struct {
		name string
		nat  string
	}{
		{"before Docker is up", ""},
		{"two ports it could be, neither at the rules' addresses", natListing("3001>172.18.0.4:3001", "30012>172.18.0.6:3001", "32400>172.18.0.3:32400", "32401>172.18.0.8:32400")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, moved := migrateDockerUserRules(cutPrefixRules, parseDNATPorts(tc.nat))
			if moved != 0 || !reflect.DeepEqual(got, cutPrefixRules) {
				t.Errorf("moved %d:\n  %s", moved, strings.Join(got, "\n  "))
			}
		})
	}
}

// Saving after any change to the Docker rules dropped these from the file:
// nothing in the DROP and the ACCEPT said they were the panel's, and the LOG
// that does was not read past its cut prefix. The next reboot opened the ports.
func TestSaveKeepsRulesWhosePrefixWasCut(t *testing.T) {
	path := withRulesFiles(t)
	operatorPinned := `-A DOCKER-USER -d 172.18.0.9/32 -p tcp -m tcp --dport 8080 -j DROP`
	save := strings.Join(append(append([]string{"*filter", ":DOCKER-USER - [0:0]", guestBridge}, cutPrefixRules...),
		operatorPinned, acceptNet, logPort, dropPort, "-A DOCKER-USER -j RETURN", "COMMIT"), "\n") + "\n"
	cmd := exec.NewMockCommander()
	cmd.SetOutput("iptables-save", save, nil)
	if err := (&Handler{Cmd: cmd}).saveDockerUserRules(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(append(append([]string{"*filter"}, cutPrefixRules...), acceptNet, logPort, dropPort, "COMMIT"), "\n") + "\n"
	if string(got) != want {
		t.Errorf("saved\n%s\nwant\n%s", got, want)
	}
}

// Where a release before this one has already dropped them from the file,
// the chain still has them until the next reboot. The panel takes them back
// at its next start, moves them, and saves them.
func TestRestoreTakesBackRulesTheFileLost(t *testing.T) {
	for _, tc := range []struct {
		name  string
		saved []string
	}{
		{"another rule still saved", []string{acceptNet, logPort, dropPort}},
		{"nothing saved", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withRulesFiles(t)
			if tc.saved != nil {
				path = writeSaved(t, tc.saved...)
			}
			chain := newDockerUserChain(append(slices.Clone(tc.saved), cutPrefixRules...)...)
			chain.nat = natListing("3001>172.18.0.2:3001", "32400>172.18.0.7:32400")
			RestoreDockerUserRules(chain)

			want := append(slices.Clone(tc.saved), cutPrefixMoved...)
			if !reflect.DeepEqual(chain.rules, want) {
				t.Errorf("chain\n  %s\nwant\n  %s", strings.Join(chain.rules, "\n  "), strings.Join(want, "\n  "))
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("nothing saved: %v", err)
			}
			for _, rule := range cutPrefixMoved {
				if !strings.Contains(string(data), rule+"\n") {
					t.Errorf("saved file lacks %s:\n%s", rule, data)
				}
			}
		})
	}
}

// The LOG prefix the panel writes now fits for every port, so the firewall
// log names the host port it dropped.
func TestDockerLogPrefixFitsEveryPort(t *testing.T) {
	for _, port := range []int{1, 81, 3001, 65535} {
		if p := dockerLogPrefix(port); len(p) > 29 || !strings.HasSuffix(p, " ") {
			t.Errorf("prefix %q for port %d: %d characters, iptables keeps 29", p, port, len(p))
		}
	}
}
