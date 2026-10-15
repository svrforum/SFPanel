package sysguard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/platform"
)

var (
	ubuntu = platform.Info{OSID: "ubuntu", NetworkStack: platform.StackNetplan}
	debian = platform.Info{OSID: "debian", NetworkStack: platform.StackIfupdown}
	pve    = platform.Info{OSID: "debian", PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2}
)

func TestUnitPolicy(t *testing.T) {
	cases := []struct {
		host           platform.Info
		unit, action   string
		wantTier, hint string
	}{
		{ubuntu, "nginx.service", ActionStop, "", ""},
		{ubuntu, "sfpanel.service", ActionRestart, TierRefuse, ""},
		{ubuntu, "networking.service", ActionStop, "", ""},
		{debian, "networking.service", ActionStop, TierRefuse, "ifreload -a"},
		{debian, "networking", ActionRestart, TierRefuse, "ifreload -a"},
		{pve, "networking.service", ActionDisable, TierRefuse, "ifreload -a"},
		{ubuntu, "ssh.service", ActionStop, TierConfirm, ""},
		{debian, "sshd.service", ActionDisable, TierConfirm, ""},
		{ubuntu, "ssh.service", ActionRestart, "", ""},
		{pve, "pve-cluster.service", ActionStop, TierRefuse, ""},
		{pve, "pve-cluster.service", ActionRestart, TierConfirm, ""},
		{pve, "corosync.service", ActionDisable, TierRefuse, ""},
		{pve, "watchdog-mux.service", ActionRestart, TierRefuse, ""},
		{pve, "pve-firewall.service", ActionStop, TierRefuse, ""},
		{pve, "pve-firewall.service", ActionRestart, "", ""},
		{pve, "pve-guests.service", ActionDisable, TierRefuse, ""},
		{pve, "pve-guests.service", ActionStop, "", ""},
		{pve, "pveproxy.service", ActionRestart, TierConfirm, ""},
		{pve, "lxcfs.service", ActionStop, TierConfirm, ""},
		{pve, "pve-container@101.service", ActionStop, "", ""},
		// Proxmox's rules stay on Proxmox: corosync and lxc run elsewhere too.
		{debian, "corosync.service", ActionStop, "", ""},
		{ubuntu, "lxc.service", ActionStop, "", ""},
	}
	for _, c := range cases {
		tier, hint := UnitPolicy(c.host, c.unit, c.action)
		if tier != c.wantTier || hint != c.hint {
			t.Errorf("%s %s on %s/%v: tier=%q hint=%q, want %q %q", c.action, c.unit, c.host.NetworkStack, c.host.PVE, tier, hint, c.wantTier, c.hint)
		}
	}
}

func TestParseCgroup(t *testing.T) {
	cases := map[string]string{
		// Proxmox VE 9, read from a running VM's process.
		"0::/qemu.slice/100.scope\n": "/qemu.slice/100.scope",
		// A hybrid host carries both; the unified line wins.
		"12:pids:/system.slice/a.service\n1:name=systemd:/system.slice/a.service\n0::/system.slice/a.service\n": "/system.slice/a.service",
		// v1 only: the systemd hierarchy.
		"3:cpu,cpuacct:/\n1:name=systemd:/system.slice/b.service\n": "/system.slice/b.service",
		"": "",
	}
	for in, want := range cases {
		if got := parseCgroup(in); got != want {
			t.Errorf("parseCgroup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyCgroup(t *testing.T) {
	cases := []struct {
		host platform.Info
		path string
		want ProcessGuard
	}{
		{pve, "/qemu.slice/100.scope", ProcessGuard{VMID: 100}},
		{ubuntu, "/qemu.slice/101.scope", ProcessGuard{VMID: 101}},
		{pve, "/qemu.slice/x.scope", ProcessGuard{}},
		{pve, "/qemu.slice", ProcessGuard{}},
		{pve, "/system.slice/watchdog-mux.service", ProcessGuard{Protected: true}},
		{pve, "/system.slice/pve-cluster.service", ProcessGuard{Protected: true}},
		{pve, "/system.slice/pve-ha-lrm.service", ProcessGuard{Protected: true}},
		// Killing pveproxy or the firewall daemon does not reset the node.
		{pve, "/system.slice/pveproxy.service", ProcessGuard{}},
		{pve, "/system.slice/pve-firewall.service", ProcessGuard{}},
		// A container's own processes are ordinary administration.
		{pve, "/lxc/101/ns/init.scope", ProcessGuard{}},
		{ubuntu, "/system.slice/corosync.service", ProcessGuard{}},
		{pve, "/user.slice/user-0.slice/session-3.scope", ProcessGuard{}},
	}
	for _, c := range cases {
		if got := ClassifyCgroup(c.host, c.path); got != c.want {
			t.Errorf("ClassifyCgroup(%q, pve=%v) = %+v, want %+v", c.path, c.host.PVE, got, c.want)
		}
	}
}

func TestProcessCgroupReadsProc(t *testing.T) {
	dir := t.TempDir()
	prev := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = prev })

	if err := os.MkdirAll(filepath.Join(dir, "4242"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "4242", "cgroup"), []byte("0::/qemu.slice/100.scope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessCgroup(4242)
	if err != nil || got != "/qemu.slice/100.scope" {
		t.Errorf("ProcessCgroup = %q, %v", got, err)
	}
	if _, err := ProcessCgroup(4243); !os.IsNotExist(err) {
		t.Errorf("missing pid: err = %v, want not-exist", err)
	}
}
