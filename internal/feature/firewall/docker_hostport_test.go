package firewall

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// withRulesFiles points the saved-rules files at a temp directory: the
// handlers write and remove them.
func withRulesFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, prevLegacy := dockerUserRulesFile, dockerUserRulesFileLegacy
	dockerUserRulesFile = filepath.Join(dir, "docker-user.rules")
	dockerUserRulesFileLegacy = filepath.Join(dir, "legacy-docker-user.rules")
	t.Cleanup(func() { dockerUserRulesFile, dockerUserRulesFileLegacy = prev, prevLegacy })
	return dockerUserRulesFile
}

// A rule matched the container's address and port, which a container gets
// anew when it restarts: after a reboot the port it blocked was open again
// (#112). It matches the host port, as conntrack recorded it before Docker's
// DNAT, and names no container at all.
func TestADockerRuleMatchesTheHostPortNotTheContainer(t *testing.T) {
	withRulesFiles(t)
	cmd := exec.NewMockCommander()
	cmd.SetOutput("exists:iptables", "", nil)
	// Were the handler still looking the container up, it would find one.
	cmd.SetOutput("iptables", iptablesNatDockerFixture, nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/firewall/docker/rules",
		strings.NewReader(`{"port":443,"protocol":"tcp","action":"drop"}`)), rec)
	c.Request().Header.Set("Content-Type", "application/json")
	if err := (&Handler{Cmd: cmd}).AddDockerUserRule(c); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	// Tagged, so that only the panel's own rules are saved and put back.
	match := []string{"-p", "tcp", "-m", "conntrack", "--ctstate", "DNAT", "--ctorigdstport", "443", "--ctdir", "ORIGINAL",
		"-m", "comment", "--comment", "sfpanel"}
	want := [][]string{
		append(append([]string{"-I", "DOCKER-USER"}, match...), "-j", "DROP"),
		append(append([]string{"-I", "DOCKER-USER"}, match...), "-j", "LOG", "--log-prefix", "[DOCKER-USER DROP] HP=443 ", "--log-level", "4"),
	}
	var got [][]string
	for _, call := range cmd.Calls {
		if call.Name == "iptables" && len(call.Args) > 0 && call.Args[0] == "-I" {
			got = append(got, call.Args)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("iptables inserts\n got %q\nwant %q", got, want)
	}
}

func TestDockerRulesAreListedByHostPort(t *testing.T) {
	const chain = `Chain DOCKER-USER (1 references)
num  target     prot opt source               destination
1    ACCEPT     tcp  --  198.51.100.0/24      0.0.0.0/0            ctstate DNAT ctorigdstport 81 ctdir ORIGINAL
2    LOG        tcp  --  0.0.0.0/0            0.0.0.0/0            ctstate DNAT ctorigdstport 81 ctdir ORIGINAL LOG flags 0 level 4 prefix "[DOCKER-USER DROP] HPORT=81 "
3    DROP       tcp  --  0.0.0.0/0            0.0.0.0/0            ctstate DNAT ctorigdstport 81 ctdir ORIGINAL
4    DROP       tcp  --  0.0.0.0/0            172.19.0.5           tcp dpt:443
5    DROP       tcp  --  0.0.0.0/0            172.18.0.2           tcp dpt:3001
`
	cmd := exec.NewMockCommander()
	cmd.SetOutput("iptables", chain, nil)
	// 172.19.0.5 was npg-ui's address before the reboot; uptime-kuma kept
	// 172.18.0.2, so its rule still leads somewhere.
	rules, err := (&Handler{Cmd: cmd}).getDockerUserRulesWith(map[string]int{
		"172.19.0.4:443/tcp":  81,
		"172.18.0.2:3001/tcp": 3001,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []DockerUserRule{
		{Number: 1, Port: 81, Protocol: "tcp", Source: "198.51.100.0/24", Action: "accept"},
		{Number: 3, Port: 81, Protocol: "tcp", Action: "drop"},
		{Number: 4, Port: 443, Protocol: "tcp", Action: "drop", Stale: true},
		{Number: 5, Port: 3001, Protocol: "tcp", Action: "drop"},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("got  %+v\nwant %+v", rules, want)
	}
}

// What iptables-save printed for the rules the panel wrote before #112, on
// an emulation of Docker's path: allow one client, block the rest of host
// port 81, which Docker forwards to 172.19.0.5:443.
var savedBeforeHostPorts = []string{
	"*filter",
	":DOCKER-USER - [0:0]",
	`-A DOCKER-USER -s 10.112.1.2/32 -d 172.19.0.5/32 -p tcp -m tcp --dport 443 -j ACCEPT`,
	`-A DOCKER-USER -d 172.19.0.5/32 -p tcp -m tcp --dport 443 -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=81 "`,
	`-A DOCKER-USER -d 172.19.0.5/32 -p tcp -m tcp --dport 443 -j DROP`,
	// Written when the container was not running, so with no address. As
	// iptables keeps it: 29 characters, which a four-digit port's space is past.
	`-A DOCKER-USER -p udp -m udp --dport 5353 -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=5353"`,
	`-A DOCKER-USER -p udp -m udp --dport 5353 -j DROP`,
	// Nothing names a host port for these: an ACCEPT with no DROP beside
	// it, and an operator's own rule.
	`-A DOCKER-USER -s 10.0.0.0/8 -d 172.18.0.9/32 -p tcp -m tcp --dport 8080 -j ACCEPT`,
	`-A DOCKER-USER -i eth1 -j DROP`,
	"COMMIT",
}

func TestRulesSavedBeforeHostPortsAreMovedToThem(t *testing.T) {
	got, moved := migrateDockerUserRules(savedBeforeHostPorts, nil)
	want := []string{
		"*filter",
		":DOCKER-USER - [0:0]",
		`-A DOCKER-USER -s 10.112.1.2/32 -p tcp -m conntrack --ctstate DNAT --ctorigdstport 81 --ctdir ORIGINAL -j ACCEPT`,
		`-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 81 --ctdir ORIGINAL -j LOG --log-prefix "[DOCKER-USER DROP] HPORT=81 "`,
		`-A DOCKER-USER -p tcp -m conntrack --ctstate DNAT --ctorigdstport 81 --ctdir ORIGINAL -j DROP`,
		`-A DOCKER-USER -p udp -m conntrack --ctstate DNAT --ctorigdstport 5353 --ctdir ORIGINAL -j LOG --log-prefix "[DOCKER-USER DROP] HP=5353 "`,
		`-A DOCKER-USER -p udp -m conntrack --ctstate DNAT --ctorigdstport 5353 --ctdir ORIGINAL -j DROP`,
		`-A DOCKER-USER -s 10.0.0.0/8 -d 172.18.0.9/32 -p tcp -m tcp --dport 8080 -j ACCEPT`,
		`-A DOCKER-USER -i eth1 -j DROP`,
		"COMMIT",
	}
	if !reflect.DeepEqual(got, want) || moved != 5 {
		t.Errorf("moved %d, got\n%s\nwant\n%s", moved, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if again, n := migrateDockerUserRules(got, nil); n != 0 || !reflect.DeepEqual(again, got) {
		t.Errorf("a second pass moved %d rules", n)
	}
}

// The rules are put back at every start, so that is where they move: the
// saved file is rewritten before the rules are read from it.
func TestRestoreMovesSavedRulesToHostPortsFirst(t *testing.T) {
	path := withRulesFiles(t)
	if err := os.WriteFile(path, []byte(strings.Join(savedBeforeHostPorts, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.NewMockCommander()
	RestoreDockerUserRules(cmd)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "-d 172.19.0.5/32") || !strings.Contains(string(data), "--ctorigdstport 81") {
		t.Errorf("the saved rules were not moved:\n%s", data)
	}
	// iptables-restore empties a chain the file declares; the restore puts
	// rules back one by one instead (TestRestoreLeavesOtherRulesAlone).
	for _, call := range cmd.Calls {
		if call.Name == "iptables-restore" {
			t.Errorf("ran iptables-restore %v", call.Args)
		}
	}
}
