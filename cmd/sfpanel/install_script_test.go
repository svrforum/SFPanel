package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/install.sh is what puts this binary on a host. Its closing advice
// is where a Proxmox VE host differs: it must never send the operator to ufw
// there, which beside pve-firewall cuts the web UI on 8006 and the cluster,
// and with the Proxmox firewall on it has to name the rule that lets the
// panel's port through. Each case runs print_success with only the commands
// the case gives it on PATH, so whatever the machine running the test has
// installed (sudo, ufw, dpkg) cannot leak in.

const installScript = "../../scripts/install.sh"

func runInstallSummary(t *testing.T, stubs map[string]string) string {
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
	libPath := filepath.Join(dir, "install-lib.sh")
	if err := os.WriteFile(libPath, []byte(lib+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(dir, "etc")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("server:\n  port: 3628\n  tls:\n    enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"awk", "grep", "sed", "cat", "head"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	stubs["systemctl"] = "exit 0" // is-active: the service is up
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!"+bash+"\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command(bash, "-c", `source "$1"
CONFIG_DIR="$2"
RED=''; GREEN=''; YELLOW=''; CYAN=''; NC=''
check_systemd() { return 0; }
print_success 1.2.3 install`, "install.sh", libPath, cfgDir)
	cmd.Env = []string{"PATH=" + bin, "HOME=" + dir, "LC_ALL=C.UTF-8"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("print_success: %v\n%s", err, out)
	}
	return string(out)
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
