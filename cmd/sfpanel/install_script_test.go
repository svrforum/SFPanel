package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/svrforum/SFPanel/internal/config"
)

// scripts/install.sh is what puts this binary on a host. Its closing advice
// is where a Proxmox VE host differs: it must never send the operator to ufw
// there, which beside pve-firewall cuts the web UI on 8006 and the cluster,
// and with the Proxmox firewall on it has to name the rule that lets the
// panel's port through. Each case runs print_success with only the commands
// the case gives it on PATH, so whatever the machine running the test has
// installed (sudo, ufw, dpkg) cannot leak in.

const installScript = "../../scripts/install.sh"

// installHarness runs install.sh's functions in a tree of its own: the script
// without its closing main "$@", a config dir holding a config.yaml, install,
// data and log dirs, and a PATH with only the tools the functions use and the
// stubs a test gives. What it runs never reaches the real /etc/sfpanel or
// /usr/local/bin.
type installHarness struct {
	t                                 *testing.T
	bash, dir, lib, bin               string
	cfgDir, installDir, dataDir, logs string
}

func newInstallHarness(t *testing.T, stubs map[string]string) *installHarness {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := os.ReadFile(installScript)
	if err != nil {
		t.Fatal(err)
	}
	// Everything but the closing `main "$@"`, so sourcing it runs nothing.
	lib, found := strings.CutSuffix(string(script), "\nmain \"$@\"\n")
	if !found {
		t.Fatal(`install.sh no longer ends with main "$@"; this test sources everything before it`)
	}
	dir := t.TempDir()
	h := &installHarness{t: t, bash: bash, dir: dir,
		lib: filepath.Join(dir, "install-lib.sh"), bin: filepath.Join(dir, "bin"),
		cfgDir: filepath.Join(dir, "etc"), installDir: filepath.Join(dir, "usr-local-bin"),
		dataDir: filepath.Join(dir, "var-lib"), logs: filepath.Join(dir, "var-log")}
	if err := os.WriteFile(h.lib, []byte(lib+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{h.cfgDir, h.installDir, h.dataDir, h.logs, h.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.writeConfig([]byte("server:\n  port: 3628\n  tls:\n    enabled: true\n"))

	for _, tool := range []string{"awk", "grep", "sed", "cat", "head"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed", tool)
		}
		if err := os.Symlink(p, filepath.Join(h.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := stubs["systemctl"]; !ok {
		stubs["systemctl"] = "exit 0" // is-active: the service is up
	}
	for name, body := range stubs {
		h.stub(filepath.Join(h.bin, name), body)
	}
	return h
}

func (h *installHarness) stub(path, body string) {
	h.t.Helper()
	if err := os.WriteFile(path, []byte("#!"+h.bash+"\n"+body+"\n"), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

// writeConfig replaces the harness's config.yaml.
func (h *installHarness) writeConfig(data []byte) {
	h.t.Helper()
	if err := os.WriteFile(h.configPath(), data, 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *installHarness) configPath() string { return filepath.Join(h.cfgDir, "config.yaml") }

// stubSfpanel installs an sfpanel that records its arguments as a call, then
// runs body.
func (h *installHarness) stubSfpanel(body string) {
	h.stub(filepath.Join(h.installDir, "sfpanel"), `echo "sfpanel $*" >> "$HOME/calls"`+"\n"+body)
}

// calls are what the recording stubs were run with, in order.
func (h *installHarness) calls() []string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, "calls"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// run sources install.sh with its directories pointed into the harness, runs
// script with args as its "$@", and returns the output and the exit code.
func (h *installHarness) run(script string, args ...string) (string, int) {
	h.t.Helper()
	argv := append([]string{"-c", `source "$1"
CONFIG_DIR="$2"; INSTALL_DIR="$3"; DATA_DIR="$4"; LOG_DIR="$5"
shift 5
RED=''; GREEN=''; YELLOW=''; CYAN=''; NC=''
check_systemd() { return 0; }
` + script, "install.sh", h.lib, h.cfgDir, h.installDir, h.dataDir, h.logs}, args...)
	cmd := exec.Command(h.bash, argv...)
	cmd.Env = []string{"PATH=" + h.bin, "HOME=" + h.dir, "LC_ALL=C.UTF-8"}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	}
	h.t.Fatalf("run bash: %v", err)
	return "", 0
}

func runInstallSummary(t *testing.T, stubs map[string]string) string {
	t.Helper()
	out, code := newInstallHarness(t, stubs).run("print_success 1.2.3 install")
	if code != 0 {
		t.Fatalf("print_success: exit %d\n%s", code, out)
	}
	return out
}

// runInstallFunc runs script after sourcing install.sh, on a host that is not
// Proxmox VE and has no ufw, with the service running.
func runInstallFunc(t *testing.T, script string) (string, int) {
	t.Helper()
	return newInstallHarness(t, map[string]string{"dpkg-query": pveNeverThere}).run(script)
}

// dpkg-query as it answers for pve-manager: ii installed, rc removed with its
// configuration left, nothing at all (exit 1) on a host that never had it.
const (
	pveInstalled  = `echo "ii "`
	pveRemoved    = `echo "rc "`
	pveNeverThere = `echo "dpkg-query: no packages found matching pve-manager" >&2; exit 1`
)

func TestInstallSummaryOnProxmoxWithFirewallOn(t *testing.T) {
	out := runInstallSummary(t, map[string]string{
		"dpkg-query":   pveInstalled,
		"pve-firewall": `echo "Status: enabled/running"`,
	})
	for _, want := range []string{
		"The Proxmox VE firewall is on",
		"/etc/pve/firewall/cluster.fw",
		"IN ACCEPT -source <trusted-cidr> -p tcp -dport 3628",
		"IN ACCEPT -source <sfpanel-node-ips> -p tcp -dport 3628:3630",
		"on an existing node run  sfpanel cluster token",
		"allow them between nodes in the Proxmox firewall",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "ufw") {
		t.Errorf("summary on Proxmox VE mentions ufw:\n%s", out)
	}
	if strings.Contains(out, "sudo ") {
		t.Errorf("summary on a host without sudo prints sudo:\n%s", out)
	}
}

func TestInstallSummaryOnProxmoxWithFirewallOff(t *testing.T) {
	out := runInstallSummary(t, map[string]string{
		"dpkg-query":   pveInstalled,
		"pve-firewall": `echo "Status: disabled/running"`,
	})
	if !strings.Contains(out, "The Proxmox VE firewall is off — 3628 may be reachable from anywhere") {
		t.Errorf("summary does not say the Proxmox firewall is off:\n%s", out)
	}
	if strings.Contains(out, "IN ACCEPT") {
		t.Errorf("summary prints a rule for a firewall that is off:\n%s", out)
	}
	if strings.Contains(out, "No active ufw firewall") || strings.Contains(out, "ufw default deny") || strings.Contains(out, "between nodes in ufw") {
		t.Errorf("summary on Proxmox VE advises ufw:\n%s", out)
	}
}

func TestInstallSummaryElsewhereKeepsUfwAndSudo(t *testing.T) {
	for name, dpkg := range map[string]string{"no pve-manager": pveNeverThere, "pve-manager removed": pveRemoved} {
		t.Run(name, func(t *testing.T) {
			out := runInstallSummary(t, map[string]string{
				"dpkg-query":   dpkg,
				"pve-firewall": `echo "Status: enabled/running"`,
				"sudo":         "exit 0",
			})
			for _, want := range []string{
				"No active ufw firewall detected — 3628 may be reachable from anywhere",
				"ufw default deny + allow from trusted CIDR",
				"on an existing node run  sudo sfpanel cluster token",
				"sudo sfpanel cluster join <leader-ip>:3629 <token>",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("summary is missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Proxmox VE firewall") || strings.Contains(out, "Proxmox firewall") {
				t.Errorf("summary on a host that is not Proxmox VE advises the Proxmox firewall:\n%s", out)
			}
		})
	}
}

// RestrictSUIDSGID makes every chmod that sets a setuid or setgid bit fail
// with EPERM, in the panel and every child: apt and dpkg installing sudo,
// mount.nfs or postfix's postdrop then fail mid-transaction.
func TestInstallScriptUnitLetsDpkgSetSetuidBits(t *testing.T) {
	script, err := os.ReadFile(installScript)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(script), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "RestrictSUIDSGID=") {
			t.Errorf("the unit template sets %q", line)
		}
	}
}

// agentArgs are what the installer is handed by the command 'sfpanel cluster
// token --agent' prints.
var agentArgs = []string{"--agent", "192.0.2.1:3629", "tok", "--ca", "sha256:abc"}

// agentCluster is an agent's cluster block as 'sfpanel agent join' saves it.
var agentCluster = config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent, Name: "c", NodeID: "a1", GRPCPort: 3629,
	AdvertiseAddress: "192.0.2.10", Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}}}

// installConfig is config.yaml as the panel writes it (yaml.v3, four-space
// indent) for a panel on port with this cluster block.
func installConfig(t *testing.T, port int, c config.ClusterConfig) []byte {
	t.Helper()
	cfg := &config.Config{}
	cfg.Server.Port = port
	cfg.Server.TLS.Enabled = true
	cfg.Cluster = c
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInstallParsesAgentArgs(t *testing.T) {
	out, code := runInstallFunc(t, `parse_agent_args --agent 192.0.2.1:3629 tok --ca sha256:abc --advertise 192.0.2.10; echo "$AGENT_MANAGER|$AGENT_TOKEN|$AGENT_CA|$AGENT_ADVERTISE"`)
	if code != 0 || !strings.Contains(out, "192.0.2.1:3629|tok|sha256:abc|192.0.2.10") {
		t.Fatalf("exit %d: %s", code, out)
	}
	// In any order, and --advertise may be left out.
	out, code = runInstallFunc(t, `parse_agent_args --ca sha256:abc --agent 192.0.2.1:3629 tok; echo "$AGENT_MANAGER|$AGENT_TOKEN|$AGENT_CA|$AGENT_ADVERTISE|"`)
	if code != 0 || !strings.Contains(out, "192.0.2.1:3629|tok|sha256:abc||") {
		t.Fatalf("exit %d: %s", code, out)
	}
	// What it can tell is wrong stops the installer with the reason, before
	// anything is downloaded. An argument it does not know is not skipped: a
	// misspelt --advertise would have the agent registered at another address.
	for _, tc := range []struct{ args, reason string }{
		{"--agent 192.0.2.1:3629 tok", "--ca is missing"},
		{"--agent 192.0.2.1:3629 --ca sha256:abc", "--agent needs the manager's address and the join token"},
		{"--agent 192.0.2.1:3629", "--agent needs the manager's address and the join token"},
		{"--ca sha256:abc", "--agent <manager>:3629 <token> is missing"},
		{"--agent 192.0.2.1 tok --ca sha256:abc", "needs a port"},
		{"--agent 192.0.2.1:http tok --ca sha256:abc", "needs a port"},
		{"--agent 192.0.2.1:65536 tok --ca sha256:abc", "needs a port"},
		{"--agent :3629 tok --ca sha256:abc", "needs a port"},
		{"--agent 192.0.2.1:3629 tok --ca", "--ca needs a value"},
		{"--agent 192.0.2.1:3629 tok --ca sha256:abc --advertise", "--advertise needs a value"},
		{"--agent 192.0.2.1:3629 tok --ca sha256:abc --name", "--name needs a value"},
		{"--agent 192.0.2.1:3629 tok --ca sha256:abc --advertize 192.0.2.10", "Unknown argument: --advertize"},
		{"--agent 192.0.2.1:3629 tok extra --ca sha256:abc", "Unknown argument: extra"},
	} {
		out, code := runInstallFunc(t, "parse_agent_args "+tc.args+"; echo parsed")
		if code != 1 || !strings.Contains(out, tc.reason) || strings.Contains(out, "parsed") {
			t.Errorf("parse_agent_args %s: exit %d, want 1 saying %q:\n%s", tc.args, code, tc.reason, out)
		}
	}

	// --name reaches 'sfpanel agent join' when it is given, and only then:
	// this script is fetched from main, and a release from before --name
	// refuses an option it does not know.
	for _, tc := range []struct{ args, want string }{
		{"--agent 192.0.2.1:3629 tok --name web-7 --ca sha256:abc", "sfpanel agent join 192.0.2.1:3629 tok --ca sha256:abc --name web-7 --config "},
		{"--agent 192.0.2.1:3629 tok --ca sha256:abc", "sfpanel agent join 192.0.2.1:3629 tok --ca sha256:abc --config "},
	} {
		h := newInstallHarness(t, map[string]string{})
		h.stubSfpanel("exit 0")
		if out, code := h.run("parse_agent_args " + tc.args + "; run_agent_join no-restart"); code != 0 {
			t.Fatalf("%s: exit %d:\n%s", tc.args, code, out)
		}
		calls := h.calls()
		if len(calls) != 1 || !strings.HasPrefix(calls[0], tc.want) || strings.Count(calls[0], "--name") != strings.Count(tc.want, "--name") {
			t.Fatalf("%s: ran %q, want %q…", tc.args, calls, tc.want)
		}
	}
}

// An agent's closing summary has no admin-setup step and no browser URL —
// nobody signs in to an agent — and says which ports its managers need.
func TestInstallSummaryForAnAgent(t *testing.T) {
	out, code := runInstallFunc(t, `AGENT_MANAGER=192.0.2.1:3629; print_success 0.0.0 install`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "set up") || strings.Contains(out, "First visit") {
		t.Fatalf("agent summary offers admin setup:\n%s", out)
	}
	for _, want := range []string{"agent", "3628", "3629"} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent summary lacks %q:\n%s", want, out)
		}
	}
	// "Set up the admin account" is capitalised, and the setup line is not
	// the only one that sends a browser to the panel.
	lower := strings.ToLower(out)
	for _, reject := range []string{"set up", "setup", "access:", "<server-ip>", "2fa"} {
		if strings.Contains(lower, reject) {
			t.Errorf("agent summary says %q:\n%s", reject, out)
		}
	}
	for _, want := range []string{
		"joined the cluster at 192.0.2.1:3629 as an agent; manage it from a manager's panel",
		"Its managers reach this server on TCP 3628 (panel) and 3629 (cluster gRPC)",
		"no Raft port (3630)",
		"sfpanel agent status",
		"sfpanel agent reset --yes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("agent summary lacks %q:\n%s", want, out)
		}
	}
	// Without systemd nothing was started: it says how, and still no URL.
	out, code = runInstallFunc(t, `check_systemd() { return 1; }; AGENT_MANAGER=192.0.2.1:3629; print_success 0.0.0 install`)
	if code != 0 || !strings.Contains(out, "Launch it manually") || strings.Contains(out, "Then open") || strings.Contains(out, "<server-ip>") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	// A gRPC port the shell would read as octal is not one: after a join that
	// worked, the summary must not end the installer with an error.
	h := newInstallHarness(t, map[string]string{"dpkg-query": pveNeverThere})
	h.writeConfig([]byte("server:\n  port: 3628\ncluster:\n  role: agent\n  grpc_port: 03629\n"))
	if out, code := h.run(`AGENT_MANAGER=192.0.2.1:3629; print_success 0.0.0 install`); code != 0 || !strings.Contains(out, "3628 (panel) and 3629 (cluster gRPC)") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// read_cluster_value reads config.yaml as the panel writes it (yaml.v3, four
// spaces) and as an operator may (two spaces, quotes, comments), and only
// the keys directly under cluster: — a nested key of the same name, or one
// under another block, is not the cluster's.
func TestInstallReadsTheClusterBlock(t *testing.T) {
	const handWritten = "server:\n  port: 3628\n  role: manager\ncluster:\n  # joined by hand\n\n  enabled: true\n" +
		"  role: \"agent\"   # set by the join\n  grpc_port: '4000'\n  managers:\n    - id: m1\n      role: manager\nlog:\n  level: info\n"
	const nestedOnly = "cluster:\n  enabled: true\n  raft:\n    role: agent\n"
	for _, tc := range []struct {
		name   string
		config []byte // nil: no config.yaml at all
		key    string
		want   string
	}{
		{"as the panel writes it", installConfig(t, 3628, agentCluster), "role", "agent"},
		{"as the panel writes it", installConfig(t, 3628, agentCluster), "grpc_port", "3629"},
		{"as the panel writes it", installConfig(t, 3628, agentCluster), "node_id", "a1"},
		{"by hand", []byte(handWritten), "role", "agent"},
		{"by hand", []byte(handWritten), "grpc_port", "4000"},
		{"by hand", []byte(handWritten), "enabled", "true"},
		{"nested only", []byte(nestedOnly), "role", ""},
		{"no cluster block", []byte("server:\n  role: agent\n"), "role", ""},
		{"no config", nil, "role", ""},
	} {
		h := newInstallHarness(t, map[string]string{})
		if tc.config == nil {
			if err := os.Remove(h.configPath()); err != nil {
				t.Fatal(err)
			}
		} else {
			h.writeConfig(tc.config)
		}
		out, code := h.run(`echo "[$(read_cluster_value ` + tc.key + `)]"`)
		if code != 0 || strings.TrimSpace(out) != "["+tc.want+"]" {
			t.Errorf("%s, %s: exit %d, %q, want [%s]", tc.name, tc.key, code, out, tc.want)
		}
	}
}

// The ports an agent's managers have to get through to are the agent's own:
// the panel port and the cluster gRPC port of its config.yaml, as the panel
// writes it. The firewall named is the host's — never ufw on Proxmox VE.
func TestInstallAgentFirewallAdvice(t *testing.T) {
	cluster := agentCluster
	cluster.GRPCPort = 4000
	for _, tc := range []struct {
		name         string
		stubs        map[string]string
		want, reject []string
	}{
		{"no firewall", map[string]string{"dpkg-query": pveNeverThere},
			[]string{"No active ufw firewall detected — 8443 and 4000 may be reachable from anywhere"},
			[]string{"Proxmox", "ufw allow"}},
		{"ufw on", map[string]string{"dpkg-query": pveNeverThere, "ufw": `echo "Status: active"`, "sudo": "exit 0"},
			[]string{"sudo ufw allow from <manager-ip> to any port 8443,4000 proto tcp"},
			[]string{"Proxmox", "No active ufw"}},
		{"Proxmox, firewall on", map[string]string{"dpkg-query": pveInstalled, "pve-firewall": `echo "Status: enabled/running"`},
			[]string{"The Proxmox VE firewall is on", "IN ACCEPT -source <manager-ips> -p tcp -dport 8443,4000"},
			[]string{"ufw"}},
		{"Proxmox, firewall off", map[string]string{"dpkg-query": pveInstalled, "pve-firewall": `echo "Status: disabled/running"`},
			[]string{"The Proxmox VE firewall is off — 8443 and 4000 may be reachable from anywhere"},
			[]string{"IN ACCEPT", "No active ufw", "ufw allow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallHarness(t, tc.stubs)
			h.writeConfig(installConfig(t, 8443, cluster))
			out, code := h.run(`AGENT_MANAGER=192.0.2.1:3629; print_success 1.2.3 install`)
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			for _, want := range append(tc.want, "TCP 8443 (panel) and 4000 (cluster gRPC)", "no Raft port (4001)") {
				if !strings.Contains(out, want) {
					t.Errorf("summary is missing %q:\n%s", want, out)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(out, reject) {
					t.Errorf("summary says %q:\n%s", reject, out)
				}
			}
		})
	}
}

// An agent upgraded by the plain installer is told what it is, not handed a
// browser URL its panel would refuse. The firewall advice was its join's.
func TestInstallSummaryForAnUpgradedAgent(t *testing.T) {
	h := newInstallHarness(t, map[string]string{"dpkg-query": pveNeverThere})
	h.writeConfig(installConfig(t, 3628, agentCluster))
	out, code := h.run(`print_success 1.2.3 upgrade`)
	if code != 0 || !strings.Contains(out, "SFPanel upgraded to v1.2.3") || !strings.Contains(out, "an agent of a cluster; manage it from a manager's panel") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, reject := range []string{"Access:", "<server-ip>", "Its managers reach"} {
		if strings.Contains(out, reject) {
			t.Errorf("summary says %q:\n%s", reject, out)
		}
	}
}

// run_agent_join hands 'sfpanel agent join' what parse_agent_args read and
// the installer's config, with --no-restart while the service is stopped or
// not set up yet. A join that fails stops the installer, saying how to run it
// again — with no token in it: the one used may be spent. Once the config is
// an agent's, a new binary that fails to start is not swapped back for the
// old one, which may predate agents and would come up open as a manager.
func TestInstallRunsTheAgentJoin(t *testing.T) {
	const set = `AGENT_MANAGER=192.0.2.1:3629; AGENT_TOKEN=0123abcd.4567ef; AGENT_CA=sha256:abc; `
	h := newInstallHarness(t, map[string]string{})
	h.stubSfpanel("exit 0")
	if out, code := h.run(set + `AGENT_ADVERTISE=192.0.2.10; BINARY_BACKED_UP=1; run_agent_join no-restart; echo "backed up: $BINARY_BACKED_UP"`); code != 0 || !strings.Contains(out, "backed up: 0") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if out, code := h.run(set + `run_agent_join restart`); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := []string{
		"sfpanel agent join 192.0.2.1:3629 0123abcd.4567ef --ca sha256:abc --advertise 192.0.2.10 --config " + h.configPath() + " --no-restart",
		"sfpanel agent join 192.0.2.1:3629 0123abcd.4567ef --ca sha256:abc --config " + h.configPath(),
	}
	if got := h.calls(); !slices.Equal(got, want) {
		t.Fatalf("calls:\n%q\nwant:\n%q", got, want)
	}

	// And they are what 'sfpanel agent join' takes: its parser is the other
	// half of this command line.
	pin := "sha256:" + strings.Repeat("ab", 32)
	c := newInstallHarness(t, map[string]string{})
	c.stub(filepath.Join(c.installDir, "sfpanel"), `printf '%s\n' "$@" > "$HOME/args"`)
	if out, code := c.run(`AGENT_MANAGER=192.0.2.1:3629; AGENT_TOKEN=tok; AGENT_CA=` + pin + `; AGENT_ADVERTISE=192.0.2.10; AGENT_NAME=web-7; run_agent_join no-restart`); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	data, err := os.ReadFile(filepath.Join(c.dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(args) < 2 || args[0] != "agent" || args[1] != "join" {
		t.Fatalf("ran sfpanel %q", args)
	}
	if a, err := parseAgentJoinArgs(args[2:]); err != nil || a.Manager != "192.0.2.1:3629" || a.Token != "tok" || a.Pin != pin ||
		a.Advertise != "192.0.2.10" || a.Name != "web-7" || a.ConfigPath != c.configPath() || !a.NoRestart {
		t.Fatalf("parseAgentJoinArgs(%q) = %+v, %v", args[2:], a, err)
	}

	failing := newInstallHarness(t, map[string]string{})
	failing.stubSfpanel(`echo "Agent join failed: join token already used" >&2; exit 1`)
	out, code := failing.run(set + `AGENT_NAME=web-7; run_agent_join no-restart; echo "went on"`)
	if code != 1 || strings.Contains(out, "went on") {
		t.Fatalf("exit %d, want 1 and a stop:\n%s", code, out)
	}
	for _, want := range []string{
		"Agent join failed: join token already used",
		"sfpanel was not started",
		"bash -s -- --agent 192.0.2.1:3629 <token> --ca sha256:abc --name web-7",
		"sfpanel cluster token --agent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure says nothing of %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0123abcd.4567ef") {
		t.Errorf("failure prints the token:\n%s", out)
	}
}

// installSteps is a script that runs main with every step that touches the
// host replaced by a line naming it, on a host with the installed version
// given (none: a fresh install) and latest the newest release.
func installSteps(installed, latest string) string {
	return `check_root() { :; }
check_os() { :; }
check_commands() { :; }
detect_arch() { echo amd64; }
get_current_version() { echo "` + installed + `"; }
get_latest_version() { echo "` + latest + `"; }
download_binary() { echo "STEP download_binary"; }
setup_dirs() { echo "STEP setup_dirs"; }
generate_config() { echo "STEP generate_config"; }
setup_logrotate() { echo "STEP setup_logrotate"; }
setup_systemd() { echo "STEP setup_systemd"; }
print_success() { echo "STEP print_success $*"; }
run_agent_join() { echo "STEP run_agent_join $*"; }
main "$@"`
}

// stepsRun are the steps an installSteps script ran, in order.
func stepsRun(out string) []string {
	var steps []string
	for _, line := range strings.Split(out, "\n") {
		if step, ok := strings.CutPrefix(line, "STEP "); ok {
			steps = append(steps, strings.TrimSpace(step))
		}
	}
	return steps
}

// setup_systemd starts the service. The join runs before it, so the first
// start is already an agent's: a standalone panel's setup screen, open to
// the LAN until someone claims it, never comes up. On an upgrade
// download_binary has stopped the service; at the same version — the way
// back after a join that failed — a running panel is restarted by the join.
func TestInstallJoinsBeforeTheServiceStarts(t *testing.T) {
	joined := []string{"download_binary", "setup_dirs", "generate_config", "run_agent_join no-restart", "setup_logrotate", "setup_systemd"}
	for _, tc := range []struct {
		name, installed string
		args            []string
		want            []string
	}{
		{"fresh install", "", agentArgs, append(slices.Clone(joined), "print_success 1.2.3 install")},
		{"upgrade", "1.2.2", agentArgs, append(slices.Clone(joined), "print_success 1.2.3 upgrade")},
		{"same version", "1.2.3", agentArgs, []string{"setup_dirs", "setup_logrotate", "run_agent_join restart", "setup_systemd", "print_success 1.2.3 install"}},
		{"fresh install, no agent", "", nil, []string{"download_binary", "setup_dirs", "generate_config", "setup_logrotate", "setup_systemd", "print_success 1.2.3 install"}},
		{"same version, no agent", "1.2.3", nil, []string{"setup_dirs", "setup_logrotate", "setup_systemd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := newInstallHarness(t, map[string]string{}).run(installSteps(tc.installed, "1.2.3"), tc.args...)
			if got := stepsRun(out); code != 0 || !slices.Equal(got, tc.want) {
				t.Fatalf("exit %d, steps:\n%q\nwant:\n%q\n%s", code, got, tc.want, out)
			}
		})
	}
}

// --agent on a server in a cluster already is refused before anything is
// downloaded or stopped. 'sfpanel agent join' refuses it as well, but only
// once download_binary has stopped the service, which would leave a manager
// — a voter — or a running agent down.
func TestInstallRefusesAnAgentJoinOnAClusterMember(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cluster config.ClusterConfig
		reason  string
	}{
		{"manager", config.ClusterConfig{Enabled: true, NodeID: "m1"}, "This server is a cluster manager"},
		{"agent", agentCluster, "This server is an agent already"},
		// An agent config boots as an agent whatever cluster.enabled says.
		{"agent, enabled off", config.ClusterConfig{Role: config.ClusterRoleAgent, NodeID: "a1"}, "This server is an agent already"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallHarness(t, map[string]string{})
			h.writeConfig(installConfig(t, 3628, tc.cluster))
			out, code := h.run(installSteps("1.2.2", "1.2.3"), agentArgs...)
			if code != 1 || !strings.Contains(out, tc.reason) {
				t.Fatalf("exit %d, want 1 saying %q:\n%s", code, tc.reason, out)
			}
			if steps := stepsRun(out); len(steps) != 0 {
				t.Fatalf("ran %q before refusing", steps)
			}
		})
	}
}

// An upgrade by script stops the service for its whole length, so a manager
// — a voter — is told to upgrade one node at a time. An agent does not vote.
func TestInstallUpgradeWarnsManagersOnly(t *testing.T) {
	const warning = "This node is part of a cluster"
	for _, tc := range []struct {
		name    string
		cluster config.ClusterConfig
		warned  bool
	}{
		{"manager", config.ClusterConfig{Enabled: true, NodeID: "m1"}, true},
		{"agent", agentCluster, false},
		{"standalone", config.ClusterConfig{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallHarness(t, map[string]string{})
			h.writeConfig(installConfig(t, 3628, tc.cluster))
			out, code := h.run(installSteps("1.2.2", "1.2.3"))
			if code != 0 || strings.Contains(out, warning) != tc.warned {
				t.Fatalf("exit %d, warned %v, want %v:\n%s", code, !tc.warned, tc.warned, out)
			}
		})
	}
}

// An agent is outside Raft and cannot take itself out of the cluster, and
// 'cluster leave' is a manager's: uninstalling an agent resets it instead —
// once the service is stopped, which the reset would otherwise restart as a
// standalone panel moments before it goes — and says where its record is
// removed. A manager still leaves.
func TestInstallUninstallResetsAnAgent(t *testing.T) {
	const record = `echo "systemctl $*" >> "$HOME/calls"`
	// rm only says what it would remove: uninstall names /etc paths outright.
	const uninstall = `rm() { echo "rm $*"; }; uninstall`

	h := newInstallHarness(t, map[string]string{"systemctl": record})
	h.writeConfig(installConfig(t, 3628, agentCluster))
	h.stubSfpanel("exit 0")
	out, code := h.run(uninstall)
	if code != 0 || !strings.Contains(out, "rm -f /etc/logrotate.d/sfpanel") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	calls := h.calls()
	stop, reset := slices.Index(calls, "systemctl stop sfpanel"), slices.Index(calls, "sfpanel agent reset --yes --config "+h.configPath())
	if reset < 0 || stop < 0 || stop > reset {
		t.Fatalf("calls %q: want the service stopped, then the agent reset", calls)
	}
	if slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "cluster leave") }) {
		t.Fatalf("calls %q: an agent ran 'cluster leave'", calls)
	}
	if !strings.Contains(out, "sfpanel cluster remove a1") {
		t.Errorf("uninstall does not say where the agent's record is removed:\n%s", out)
	}

	m := newInstallHarness(t, map[string]string{"systemctl": record})
	m.writeConfig(installConfig(t, 3628, config.ClusterConfig{Enabled: true, NodeID: "m1"}))
	m.stubSfpanel("exit 0")
	if out, code := m.run(uninstall); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if calls := m.calls(); !slices.Contains(calls, "sfpanel cluster leave --config "+m.configPath()) ||
		slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "agent reset") }) {
		t.Fatalf("calls %q: want a manager to leave the cluster", calls)
	}
}
