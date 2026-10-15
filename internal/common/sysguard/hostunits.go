package sysguard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/svrforum/SFPanel/internal/common/platform"
)

// Host-critical units. protectedSystemdUnits keeps the panel itself working;
// this table keeps the host and its guests up, and which units that means
// depends on the host: networking.service takes the network down only where
// ifupdown runs it, and the Proxmox units exist only on Proxmox VE. The
// services guard and the process guard both read it, so what the Services
// page refuses to stop and what the Processes page refuses to kill cannot
// drift apart.

// Unit actions the policy speaks about. Start and enable bring nothing down.
const (
	ActionStop    = "stop"
	ActionRestart = "restart"
	ActionDisable = "disable"
)

// Tiers. An action with no tier goes ahead (after the page's own confirm).
const (
	// TierConfirm goes ahead once the operator has typed the unit's name.
	TierConfirm = "confirm"
	// TierRefuse is not done through the panel at all.
	TierRefuse = "refuse"
)

type unitRule struct {
	stop, restart, disable string // tier per action
	hint                   string // the safe way, when there is one
	// signals refuses every signal but CONT to the unit's processes: killing
	// or stopping them resets the node or drops it out of its cluster.
	signals bool
}

var (
	refuseAll       = unitRule{stop: TierRefuse, restart: TierRefuse, disable: TierRefuse}
	confirmAll      = unitRule{stop: TierConfirm, restart: TierConfirm, disable: TierConfirm}
	refuseStopOff   = unitRule{stop: TierRefuse, disable: TierRefuse}
	confirmStopOff  = unitRule{stop: TierConfirm, disable: TierConfirm}
	clusterStack    = unitRule{stop: TierRefuse, restart: TierConfirm, disable: TierRefuse, signals: true}
	haService       = unitRule{stop: TierConfirm, restart: TierConfirm, disable: TierConfirm, signals: true}
	ifupdownNetwork = unitRule{stop: TierRefuse, restart: TierRefuse, disable: TierRefuse, hint: "ifreload -a"}
)

// pveUnits apply on a Proxmox VE host only.
var pveUnits = map[string]unitRule{
	// pmxcfs (/etc/pve) and corosync: without them the node leaves its
	// cluster, loses quorum and cannot start a guest. A restart is routine
	// maintenance, so it is only confirmed.
	"pve-cluster.service": clusterStack,
	"corosync.service":    clusterStack,
	// watchdog-mux holds /dev/watchdog. If it stops feeding it, the kernel
	// resets the node and every guest with it.
	"watchdog-mux.service": {stop: TierRefuse, restart: TierRefuse, disable: TierRefuse, signals: true},
	// Stopping either firewall daemon leaves the guests' firewall rules to
	// whatever was loaded last; it is switched off in the Proxmox UI.
	"pve-firewall.service":     refuseStopOff,
	"proxmox-firewall.service": refuseStopOff,
	// pve-guests starts the guests marked onboot and shuts every guest down
	// cleanly at shutdown. systemd itself refuses to stop or start it by hand.
	"pve-guests.service": {disable: TierRefuse},
	// The HA managers hold watchdog-mux clients: killing one fences the node.
	"pve-ha-lrm.service": haService,
	"pve-ha-crm.service": haService,
	// The web UI, its API and the services the guests and their consoles need.
	"pveproxy.service":     confirmAll,
	"pvedaemon.service":    confirmAll,
	"pvestatd.service":     confirmAll,
	"pvescheduler.service": confirmAll,
	"spiceproxy.service":   confirmAll,
	"qmeventd.service":     confirmAll,
	"lxcfs.service":        confirmAll,
	"lxc.service":          confirmAll,
}

func normalizeUnit(name string) string {
	name = strings.ToLower(name)
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	return name
}

func hostUnitRule(info platform.Info, unit string) (unitRule, bool) {
	switch {
	case protectedSystemdUnits[unit]:
		return refuseAll, true
	// ifupdown's and ifupdown2's stop is ifdown -a: every address goes,
	// including the one the panel is reached on, and on Proxmox the bridges
	// every guest hangs off. ifreload -a applies a changed configuration
	// without taking anything down.
	case unit == "networking.service" && info.IfupdownNetworking():
		return ifupdownNetwork, true
	// Stopping sshd closes the other way in.
	case unit == "ssh.service" || unit == "sshd.service":
		return confirmStopOff, true
	}
	if info.PVE {
		r, ok := pveUnits[unit]
		return r, ok
	}
	return unitRule{}, false
}

// UnitPolicy says how the panel treats action on unit on this host: "" goes
// ahead, TierConfirm needs the unit's name typed, TierRefuse is not done.
// hint names the safe way to get what the operator was after, if there is one.
func UnitPolicy(info platform.Info, unit, action string) (tier, hint string) {
	r, ok := hostUnitRule(info, normalizeUnit(unit))
	if !ok {
		return "", ""
	}
	switch action {
	case ActionStop:
		tier = r.stop
	case ActionRestart:
		tier = r.restart
	case ActionDisable:
		tier = r.disable
	}
	if tier == "" {
		return "", ""
	}
	return tier, r.hint
}

// UnitProtection is UnitPolicy for every action at once, as the services list
// carries it so the page knows up front; actions with no tier are left out.
func UnitProtection(info platform.Info, unit string) map[string]string {
	var out map[string]string
	for _, action := range []string{ActionStop, ActionRestart, ActionDisable} {
		if tier, _ := UnitPolicy(info, unit, action); tier != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[action] = tier
		}
	}
	return out
}

// ProcessGuard is what a process's cgroup says about signalling it.
type ProcessGuard struct {
	// VMID is the Proxmox VM the process is (QEMU runs each VM in
	// /qemu.slice/<vmid>.scope); 0 when it is not a VM's process. A signal
	// that ends it pulls the VM's power.
	VMID int
	// Protected is set for a process of a unit the node cannot lose.
	Protected bool
}

// Refused reports whether a signal other than CONT must not be sent.
func (g ProcessGuard) Refused() bool { return g.VMID != 0 || g.Protected }

// ClassifyCgroup reads a cgroup path such as "/qemu.slice/100.scope" or
// "/system.slice/watchdog-mux.service". A container's own processes
// (/lxc/<id>/…) are ordinary administration and stay allowed.
func ClassifyCgroup(info platform.Info, path string) ProcessGuard {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "qemu.slice" {
		if id, ok := strings.CutSuffix(parts[1], ".scope"); ok {
			if n, err := strconv.Atoi(id); err == nil && n > 0 {
				return ProcessGuard{VMID: n}
			}
		}
	}
	for _, p := range parts {
		if strings.HasSuffix(p, ".service") {
			r, ok := hostUnitRule(info, p)
			return ProcessGuard{Protected: ok && r.signals}
		}
	}
	return ProcessGuard{}
}

// procRoot is where the proc file system is read from.
var procRoot = "/proc"

// ProcessCgroup returns pid's cgroup path. cgroup v2 hosts (Proxmox VE 7+,
// Debian 12+, Ubuntu 22.04+) have one line, "0::/qemu.slice/100.scope"; a v1
// or hybrid host's systemd hierarchy is used when there is no such line.
func ProcessCgroup(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	return parseCgroup(string(data)), nil
}

func parseCgroup(data string) string {
	var v1 string
	for _, line := range strings.Split(data, "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			return strings.TrimSpace(p)
		}
		if _, p, ok := strings.Cut(line, ":name=systemd:"); ok {
			v1 = strings.TrimSpace(p)
		}
	}
	return v1
}
