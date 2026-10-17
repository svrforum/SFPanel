package system

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
)

// operatorSysctlDir holds the sysctl files an operator writes, and
// etcSysctlConf the admin's traditional file, applied through a
// 99-sysctl.conf link in that directory where the distribution keeps one.
// Vars for tests.
var (
	operatorSysctlDir = "/etc/sysctl.d"
	etcSysctlConf     = "/etc/sysctl.conf"
)

// OperatorParam is a parameter an operator's own sysctl file sets. The panel
// leaves it as set there and says where.
type OperatorParam struct {
	Key         string `json:"key"`
	Current     string `json:"current"`
	Value       string `json:"value"`
	File        string `json:"file"`
	Description string `json:"description"`
}

type operatorSetting struct{ key, file, value string }

// operatorSysctls are the settings in an operator's own sysctl files, in the
// order systemd-sysctl applies them.
type operatorSysctls []operatorSetting

// readOperatorSysctls reads what an operator's own sysctl files set. Their
// own means shipped by no package: Ubuntu releases before 25.04 keep their
// defaults in /etc/sysctl.d too (10-kernel-hardening.conf and the like), and
// those are the panel's to tune. /etc/sysctl.conf counts as the operator's
// although a package ships it. The panel's file is not one. The panel used to
// write over these — its file sorts after most — or be written over by one
// that sorts after it, and show as applied a value the next boot replaced.
func readOperatorSysctls(cmd commonExec.Commander) operatorSysctls {
	files, _ := filepath.Glob(filepath.Join(operatorSysctlDir, "*.conf"))
	sort.Strings(files)
	var candidates []string
	for _, f := range files {
		if filepath.Base(f) != filepath.Base(sysctlConfPath) {
			candidates = append(candidates, f)
		}
	}
	owned := packageOwned(cmd, candidates)

	var settings operatorSysctls
	for _, f := range candidates {
		target, err := filepath.EvalSymlinks(f)
		if err != nil {
			continue
		}
		if owned[f] && target != etcSysctlConf {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if key, value, ok := parseSysctlLine(line); ok {
				settings = append(settings, operatorSetting{key, filepath.Base(f), value})
			}
		}
	}
	return settings
}

// parseSysctlLine reads one line of a sysctl.d file: key = value, a key
// written with / instead of . or with - in front (ignore a failure), and #
// or ; for comments.
func parseSysctlLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' || line[0] == ';' {
		return "", "", false
	}
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(k), "-"), "/", ".")
	value = strings.Join(strings.Fields(v), " ")
	return key, value, key != ""
}

// lookup finds the setting that wins for key: the last one that names it, or
// a glob over it (net.ipv4.conf.*.rp_filter), as systemd-sysctl allows.
func (o operatorSysctls) lookup(key string) (operatorSetting, bool) {
	for i := len(o) - 1; i >= 0; i-- {
		s := o[i]
		if s.key == key {
			return s, true
		}
		if strings.ContainsAny(s.key, "*?[") {
			if matched, _ := path.Match(s.key, key); matched {
				return s, true
			}
		}
	}
	return operatorSetting{}, false
}

// leave takes the parameters these files set out of the recommendations and
// lists them, with the value and the file that sets it.
func (o operatorSysctls) leave(cats []TuningCategory) ([]TuningCategory, []OperatorParam) {
	operator := []OperatorParam{}
	for i := range cats {
		var kept []TuningParam
		for _, p := range cats[i].Params {
			if s, ok := o.lookup(p.Key); ok {
				operator = append(operator, OperatorParam{Key: p.Key, Value: s.value, File: s.file, Description: p.Description})
				continue
			}
			kept = append(kept, p)
		}
		cats[i].Params = kept
	}
	return cats, operator
}

// packageOwned reports which of paths a Debian package ships. dpkg-query -S
// prints "package: /path" for each, and an error for the rest.
func packageOwned(cmd commonExec.Commander, paths []string) map[string]bool {
	owned := map[string]bool{}
	if len(paths) == 0 {
		return owned
	}
	out, _ := cmd.Run("dpkg-query", append([]string{"-S"}, paths...)...)
	for _, line := range strings.Split(out, "\n") {
		if _, p, found := strings.Cut(line, ": "); found && strings.HasPrefix(p, "/") {
			owned[strings.TrimSpace(p)] = true
		}
	}
	return owned
}

// procSwapsPath and zfsARCMaxPath are where pveAdvisories looks. Vars for
// tests.
var (
	procSwapsPath = "/proc/swaps"
	zfsARCMaxPath = "/sys/module/zfs/parameters/zfs_arc_max"
)

// TuningAdvisory is something the tuning page tells a Proxmox operator that
// sysctl does not cover.
type TuningAdvisory struct {
	ID     string `json:"id"`
	Detail string `json:"detail,omitempty"`
}

// pveAdvisories checks two things Proxmox's own guidance says to look at.
// Swap on a zvol "may generate some troubles, like blocking the server or
// generating a high IO load, often seen when starting a Backup to an external
// Storage" (Proxmox VE admin guide, ZFS on Linux). An ARC with no limit takes,
// since OpenZFS 2.3, the larger of all memory less 1 GiB and five eighths of
// it — out from under the guests.
func pveAdvisories() []TuningAdvisory {
	advisories := []TuningAdvisory{}
	if data, err := os.ReadFile(procSwapsPath); err == nil {
		for _, line := range strings.Split(string(data), "\n")[1:] {
			if f := strings.Fields(line); len(f) > 0 && (strings.HasPrefix(f[0], "/dev/zd") || strings.HasPrefix(f[0], "/dev/zvol/")) {
				advisories = append(advisories, TuningAdvisory{ID: "swap_on_zvol", Detail: f[0]})
			}
		}
	}
	if data, err := os.ReadFile(zfsARCMaxPath); err == nil && strings.TrimSpace(string(data)) == "0" {
		advisories = append(advisories, TuningAdvisory{ID: "zfs_arc_unlimited"})
	}
	return advisories
}
