package system

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/svrforum/SFPanel/internal/api/response"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

const (
	rollbackTimeout = 60 // seconds
	commandTimeout  = 5 * time.Minute
)

// sysctlConfPath is the panel's own sysctl file. A var so tests can point it
// at a temporary directory.
var sysctlConfPath = "/etc/sysctl.d/99-sfpanel-tuning.conf"

// TuningHandler exposes REST handlers for system kernel parameter tuning.
type TuningHandler struct {
	Cmd commonExec.Commander
}

// rollbackState holds the state for auto-rollback on unconfirmed changes.
var (
	rollbackMu       sync.Mutex
	rollbackTimer    *time.Timer
	rollbackValues   map[string]string
	rollbackConfFile []byte
	rollbackHadFile  bool
	rollbackDeadline time.Time
	rollbackCmd      commonExec.Commander
)

// TuningParam represents a single sysctl parameter with current and recommended values.
type TuningParam struct {
	Key         string `json:"key"`
	Current     string `json:"current"`
	Recommended string `json:"recommended"`
	Description string `json:"description"`
	Applied     bool   `json:"applied"`
}

// TuningCategory groups related tuning parameters.
type TuningCategory struct {
	Name    string        `json:"name"`
	Benefit string        `json:"benefit"`
	Caution string        `json:"caution"`
	Params  []TuningParam `json:"params"`
	Applied int           `json:"applied"`
	Total   int           `json:"total"`
}

// pveManagedKeys are left to Proxmox VE on a Proxmox host: shown, never
// recommended or written. Proxmox ships bridge-nf-call-* off
// (/usr/lib/sysctl.d/10-pve.conf) so the guests' bridged frames bypass the
// host's iptables, and its firewall turns them on when it needs them;
// forwarding and conntrack are its firewall's and its SDN's. The panel
// recommending bridge-nf-call-iptables=1 put every VM's and container's
// frames through iptables and conntrack, beside a firewall that was off. The
// limits Proxmox and systemd raise for guests are theirs too: the panel's
// file loads after theirs, so recommending today's value would pin it there
// when a later release raises it.
var pveManagedKeys = []TuningParam{
	{Key: "net.ipv4.ip_forward", Description: "Routing between the host's networks; Proxmox SDN and routed setups set it"},
	{Key: "net.bridge.bridge-nf-call-iptables", Description: "Off unless the Proxmox firewall is on, which turns it on itself"},
	{Key: "net.bridge.bridge-nf-call-ip6tables", Description: "Off unless the Proxmox firewall is on, which turns it on itself"},
	{Key: "net.bridge.bridge-nf-call-arptables", Description: "Off unless the Proxmox firewall is on, which turns it on itself"},
	{Key: "net.netfilter.nf_conntrack_max", Description: "Set under Node → Firewall → Options in Proxmox"},
	{Key: "net.netfilter.nf_conntrack_tcp_timeout_established", Description: "Set under Node → Firewall → Options in Proxmox"},
	{Key: "fs.inotify.max_user_watches", Description: "Raised by Proxmox for containers (10-pve-ct-inotify-limits.conf)"},
	{Key: "fs.inotify.max_user_instances", Description: "Raised by Proxmox for containers (10-pve-ct-inotify-limits.conf)"},
	{Key: "fs.aio-max-nr", Description: "Raised by Proxmox for guest disk I/O (10-pve.conf)"},
	{Key: "vm.max_map_count", Description: "Raised by Proxmox and systemd"},
	{Key: "kernel.pid_max", Description: "Raised by systemd (50-pid-max.conf)"},
	{Key: "fs.file-max", Description: "Raised to its maximum by systemd at boot"},
}

// pveSkippedKeys are not recommended on a Proxmox host, where they do not fit
// a hypervisor. Reverse-path filtering must be off for an SDN with more than
// one EVPN exit node. JIT hardening taxes the BPF that systemd and LXC run as
// root, while unprivileged BPF is already off. ZFS keeps its own write
// throttle and guest disks skip the host's page cache, so the dirty ratios
// govern only backups to other storage, which is the operator's call. VFS
// cache pressure, TCP Fast Open, unsaved TCP metrics and a wider local port
// range do nothing for a host whose own connections are migrations, backups
// and its web UI.
var pveSkippedKeys = map[string]bool{
	"net.ipv4.conf.all.rp_filter":     true,
	"net.ipv4.conf.default.rp_filter": true,
	"net.core.bpf_jit_harden":         true,
	"vm.dirty_ratio":                  true,
	"vm.dirty_background_ratio":       true,
	"vm.vfs_cache_pressure":           true,
	"net.ipv4.tcp_fastopen":           true,
	"net.ipv4.tcp_no_metrics_save":    true,
	"net.ipv4.ip_local_port_range":    true,
}

// pveManaged reports whether key is one pveManagedKeys leaves to Proxmox.
func pveManaged(key string) bool {
	for _, p := range pveManagedKeys {
		if p.Key == key {
			return true
		}
	}
	return false
}

// TuningSystemInfo contains system specs used for dynamic recommendations.
type TuningSystemInfo struct {
	CPUCores int    `json:"cpu_cores"`
	TotalRAM uint64 `json:"total_ram"`
	Kernel   string `json:"kernel"`
}

// ---------- Helpers ----------

func runTuningCommand(cmd commonExec.Commander, name string, args ...string) (string, error) {
	return cmd.RunWithTimeout(commandTimeout, name, args...)
}

// readSysctl returns a kernel parameter's current value.
//
// Straight from /proc/sys: `sysctl -n key` does nothing but open that file,
// and the tuning page read sixty-two parameters through sixty-two forks on
// every open — about 400 ms here, closer to a second of visible lag on a
// single-vCPU box, for values the kernel hands over in microseconds. The
// subprocess remains as the fallback for a key that has no /proc/sys entry
// (a module's parameter under a name sysctl resolves but the path does not).
func readSysctl(cmd commonExec.Commander, key string) string {
	if v, ok := readProcSys(key); ok {
		return v
	}
	out, err := runTuningCommand(cmd, "sysctl", "-n", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// readProcSys maps a dotted key onto /proc/sys and reads it. A key with a
// component containing a slash is refused rather than resolved: the keys
// here come from the panel's own tables, but the mapping must not be able to
// escape /proc/sys regardless.
func readProcSys(key string) (string, bool) {
	if key == "" || strings.Contains(key, "/") || strings.Contains(key, "..") {
		return "", false
	}
	data, err := os.ReadFile("/proc/sys/" + strings.ReplaceAll(key, ".", "/"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// ---------- GetTuningStatus ----------

func (h *TuningHandler) GetTuningStatus(c echo.Context) error {
	cpuCores := runtime.NumCPU()
	v, _ := mem.VirtualMemory()
	totalRAM := uint64(0)
	if v != nil {
		totalRAM = v.Total
	}
	totalRAMGB := float64(totalRAM) / (1024 * 1024 * 1024)

	categories, operator := readOperatorSysctls(h.Cmd).leave(buildRecommendations(cpuCores, totalRAMGB, totalRAM))
	for i := range operator {
		operator[i].Current = readSysctl(h.Cmd, operator[i].Key)
	}

	configuredKeys := make(map[string]bool)
	if data, err := os.ReadFile(sysctlConfPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				configuredKeys[strings.TrimSpace(parts[0])] = true
			}
		}
	}

	totalParams := 0
	totalApplied := 0
	for i, cat := range categories {
		applied := 0
		for j, p := range cat.Params {
			current := readSysctl(h.Cmd, p.Key)
			categories[i].Params[j].Current = current
			// Never propose a value the host already beats — see atLeastKeys.
			// Done here rather than in buildRecommendations because that
			// function does not read the live system.
			categories[i].Params[j].Recommended = keepCurrentIfBetter(p.Key, current, p.Recommended)
			categories[i].Params[j].Applied = configuredKeys[p.Key]
			if categories[i].Params[j].Applied {
				applied++
			}
		}
		categories[i].Applied = applied
		categories[i].Total = len(cat.Params)
		totalParams += len(cat.Params)
		totalApplied += applied
	}

	rollbackMu.Lock()
	pending := rollbackValues != nil
	remaining := 0
	if pending {
		remaining = int(time.Until(rollbackDeadline).Seconds())
		if remaining < 0 {
			remaining = 0
		}
	}
	rollbackMu.Unlock()

	// On Proxmox VE: what Proxmox keeps for itself, with the values it has.
	// A key the kernel does not have (br_netfilter or nf_conntrack not
	// loaded) is left out.
	managed := []TuningParam{}
	if platform.Get().PVE {
		for _, p := range pveManagedKeys {
			if current := readSysctl(h.Cmd, p.Key); current != "" {
				p.Current = current
				managed = append(managed, p)
			}
		}
	}

	advisories := []TuningAdvisory{}
	if platform.Get().PVE {
		advisories = pveAdvisories()
	}

	return response.OK(c, map[string]interface{}{
		"managed":            managed,
		"operator":           operator,
		"advisories":         advisories,
		"categories":         categories,
		"total_params":       totalParams,
		"applied":            totalApplied,
		"pending_rollback":   pending,
		"rollback_remaining": remaining,
		"system_info": TuningSystemInfo{
			CPUCores: cpuCores,
			TotalRAM: totalRAM,
			Kernel:   readSysctl(h.Cmd, "kernel.osrelease"),
		},
	})
}

// ---------- ApplyTuning ----------

func (h *TuningHandler) ApplyTuning(c echo.Context) error {
	var req struct {
		Categories []string `json:"categories"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	cpuCores := runtime.NumCPU()
	v, _ := mem.VirtualMemory()
	totalRAM := uint64(0)
	if v != nil {
		totalRAM = v.Total
	}
	totalRAMGB := float64(totalRAM) / (1024 * 1024 * 1024)

	operatorSet := readOperatorSysctls(h.Cmd)
	categories, _ := operatorSet.leave(buildRecommendations(cpuCores, totalRAMGB, totalRAM))
	// Same no-downgrade guard the status view applies. This is the path that
	// actually writes sysctl, so without it a host whose kernel already ships
	// a better default would be reduced to the older recommendation.
	for i := range categories {
		for j, p := range categories[i].Params {
			categories[i].Params[j].Recommended =
				keepCurrentIfBetter(p.Key, readSysctl(h.Cmd, p.Key), p.Recommended)
		}
	}

	selectedCategories := make(map[string]bool)
	for _, cat := range req.Categories {
		selectedCategories[cat] = true
	}

	rollbackMu.Lock()
	defer rollbackMu.Unlock()

	// Refuse if an earlier apply is still pending confirmation: otherwise
	// we'd overwrite the original snapshot and lose the ability to roll back
	// to the *real* pre-tuning state.
	if rollbackValues != nil {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists,
			"A previous tuning apply is awaiting /confirm or /reset; resolve that first")
	}

	if rollbackTimer != nil {
		rollbackTimer.Stop()
		rollbackTimer = nil
	}

	preApplyValues := make(map[string]string)
	for _, cat := range categories {
		if len(req.Categories) > 0 && !selectedCategories[cat.Name] {
			continue
		}
		for _, p := range cat.Params {
			preApplyValues[p.Key] = readSysctl(h.Cmd, p.Key)
		}
	}

	var preApplyConfFile []byte
	preApplyHadFile := false
	if data, err := os.ReadFile(sysctlConfPath); err == nil {
		preApplyConfFile = data
		preApplyHadFile = true
	}

	existingParams := make(map[string]string)
	if preApplyHadFile {
		for _, line := range strings.Split(string(preApplyConfFile), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				existingParams[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	for _, cat := range categories {
		if len(req.Categories) > 0 && !selectedCategories[cat.Name] {
			continue
		}
		for _, p := range cat.Params {
			existingParams[p.Key] = p.Recommended
		}
	}
	// A file written by a panel that did not know the host was Proxmox can
	// still hold these; 99-sfpanel-tuning.conf runs after Proxmox's own
	// 10-pve.conf at every boot, so leaving them would keep overriding it.
	// The ones that do not fit a hypervisor go too.
	if platform.Get().PVE {
		for _, p := range pveManagedKeys {
			delete(existingParams, p.Key)
		}
		for k := range pveSkippedKeys {
			delete(existingParams, k)
		}
	}
	// So does what an operator's own file sets, which the panel's file wrote
	// over or lost to at boot.
	for k := range existingParams {
		if _, ok := operatorSet.lookup(k); ok {
			delete(existingParams, k)
		}
	}

	keys := make([]string, 0, len(existingParams))
	for k := range existingParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	lines = append(lines, "# SFPanel System Tuning")
	lines = append(lines, "# Auto-generated — do not edit manually")
	// What each key held before the panel touched it, recorded so Reset can
	// actually put it back. Removing this file and running `sysctl --system`
	// does not restore anything: a key no other file mentions simply keeps
	// the value already in the kernel, so "reset to system defaults" left the
	// host exactly as tuned as it was and said otherwise. The record lives in
	// the file it describes — it needs no storage of its own and survives a
	// restart, and if someone deletes the file by hand there is correctly
	// nothing left to restore.
	lines = append(lines, previousValueComments(preApplyValues, keys)...)
	lines = append(lines, "")
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s = %s", k, existingParams[k]))
	}
	lines = append(lines, "")

	if err := os.MkdirAll(filepath.Dir(sysctlConfPath), 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTuningError,
			"Failed to create config directory: "+err.Error())
	}
	if err := os.WriteFile(sysctlConfPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTuningError,
			"Failed to write config: "+err.Error())
	}

	output, err := runTuningCommand(h.Cmd, "sysctl", "-p", sysctlConfPath)
	if err != nil {
		if preApplyHadFile {
			_ = os.WriteFile(sysctlConfPath, preApplyConfFile, 0644)
		} else {
			_ = os.Remove(sysctlConfPath)
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrTuningError,
			"Failed to apply tuning: "+err.Error())
	}

	rollbackValues = preApplyValues
	rollbackConfFile = preApplyConfFile
	rollbackHadFile = preApplyHadFile
	rollbackCmd = h.Cmd
	rollbackDeadline = time.Now().Add(rollbackTimeout * time.Second)
	rollbackTimer = time.AfterFunc(rollbackTimeout*time.Second, performRollback)

	return response.OK(c, map[string]interface{}{
		"message": "Tuning applied — confirm within 60 seconds or changes will be rolled back",
		"output":  output,
		"timeout": rollbackTimeout,
	})
}

func performRollback() {
	rollbackMu.Lock()
	defer rollbackMu.Unlock()

	if rollbackValues == nil {
		return
	}

	slog.Info("auto-rollback: no confirmation received, reverting changes", "component", "tuning")

	for key, val := range rollbackValues {
		if _, err := runTuningCommand(rollbackCmd, "sysctl", "-w", key+"="+val); err != nil {
			slog.Error("rollback failed", "component", "tuning", "key", key, "error", err)
		}
	}

	if rollbackHadFile && rollbackConfFile != nil {
		if err := os.WriteFile(sysctlConfPath, rollbackConfFile, 0644); err != nil {
			slog.Error("failed to restore config file", "component", "tuning", "error", err)
		}
	} else if !rollbackHadFile {
		if err := os.Remove(sysctlConfPath); err != nil && !os.IsNotExist(err) {
			slog.Error("failed to remove config file", "component", "tuning", "error", err)
		}
	}

	rollbackValues = nil
	rollbackConfFile = nil
	rollbackCmd = nil
	rollbackTimer = nil
	slog.Info("auto-rollback completed", "component", "tuning")
}

// ---------- ConfirmTuning ----------

func (h *TuningHandler) ConfirmTuning(c echo.Context) error {
	rollbackMu.Lock()
	defer rollbackMu.Unlock()

	if rollbackValues == nil {
		return response.OK(c, map[string]interface{}{
			"message": "No pending changes to confirm",
		})
	}

	if rollbackTimer != nil {
		rollbackTimer.Stop()
		rollbackTimer = nil
	}
	rollbackValues = nil
	rollbackConfFile = nil

	return response.OK(c, map[string]interface{}{
		"message": "Tuning confirmed and saved permanently",
	})
}

// ---------- ResetTuning ----------

func (h *TuningHandler) ResetTuning(c echo.Context) error {
	rollbackMu.Lock()
	if rollbackTimer != nil {
		rollbackTimer.Stop()
		rollbackTimer = nil
	}
	rollbackValues = nil
	rollbackConfFile = nil
	rollbackHadFile = false
	rollbackMu.Unlock()

	data, readErr := os.ReadFile(sysctlConfPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return response.OK(c, map[string]interface{}{
				"message": "No tuning configuration to reset",
			})
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrTuningError,
			"Failed to read config: "+readErr.Error())
	}
	previous := parsePreviousValues(string(data))

	if err := os.Remove(sysctlConfPath); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTuningError,
			"Failed to remove config: "+err.Error())
	}

	// Put the recorded values back before reloading. The reload alone only
	// re-applies whatever files remain; it cannot know what a key held before
	// this panel wrote it.
	restored := 0
	for _, key := range sortedKeys(previous) {
		if _, err := runTuningCommand(h.Cmd, "sysctl", "-w", key+"="+previous[key]); err != nil {
			slog.Warn("tuning reset: could not restore a value",
				"component", "tuning", "key", key, "error", err)
			continue
		}
		restored++
	}
	_, _ = runTuningCommand(h.Cmd, "sysctl", "--system")

	msg := "Tuning reset to the values recorded before it was applied"
	if len(previous) == 0 {
		// An older file, written before the values were recorded. Say what
		// actually happened rather than claiming a reset that did not occur.
		msg = "Tuning configuration removed. It will not be applied at the next boot, but values already set stay in effect until then."
	}
	return response.OK(c, map[string]interface{}{
		"message":  msg,
		"restored": restored,
	})
}

// previousValueComments renders the pre-apply values as comments.
//
// Comments rather than a sidecar file: sysctl ignores them, an operator
// reading the file can see what it displaced, and the two cannot drift apart.
func previousValueComments(previous map[string]string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v, ok := previous[k]
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s%s = %s", previousMarker, k, v))
	}
	return out
}

// previousMarker prefixes the recorded values.
const previousMarker = "# sfpanel-previous: "

// validSysctlKey is what a key may look like coming back out of a file the
// operator can edit — dotted lowercase segments, nothing else.
var validSysctlKey = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// parsePreviousValues reads back what previousValueComments wrote.
func parsePreviousValues(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, previousMarker) {
			continue
		}
		rest := strings.TrimPrefix(trimmed, previousMarker)
		key, value, ok := strings.Cut(rest, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// A key must look like a sysctl key and a value must be non-empty:
		// this file is world-readable and an operator may have edited it.
		if key == "" || value == "" || !validSysctlKey.MatchString(key) {
			continue
		}
		out[key] = value
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------- Recommendations ----------

// conntrackModuleLoaded reports whether nf_conntrack is currently in the
// kernel. The conntrack tuning category is conditional on this — on a host
// without Docker (or any netfilter workload) the module is absent, and
// writing nf_conntrack_* via sysctl fails with "No such file or directory".
func conntrackModuleLoaded() bool {
	if _, err := os.Stat("/sys/module/nf_conntrack"); err == nil {
		return true
	}
	// Fallback: kernel may register nf_conntrack as built-in (no /sys/module
	// entry). The sysctl tree under /proc/sys/net/netfilter only exists when
	// conntrack is active.
	if _, err := os.Stat("/proc/sys/net/netfilter/nf_conntrack_max"); err == nil {
		return true
	}
	return false
}

// zramSwapActive reports whether a zram device is in use as swap.
//
// It changes what vm.swappiness should be. Compressed swap in RAM is orders
// of magnitude faster than a disk swapfile, so the usual "swap as little as
// possible" advice inverts: the kernel should prefer pushing cold anonymous
// pages into zram over evicting page cache. Recommending a low swappiness on
// a zram host quietly defeats the reason zram was set up.
func zramSwapActive() bool {
	data, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "/dev/zram")
}

// bridgeNetfilterAvailable reports whether the net.bridge.* sysctls exist.
//
// They are created by br_netfilter, which is not loaded on every host. Without
// this check the Docker category advertises two parameters that cannot be read
// or written, so they show as permanently unapplied. Mirrors the conntrack
// gate above.
func bridgeNetfilterAvailable() bool {
	_, err := os.Stat("/proc/sys/net/bridge/bridge-nf-call-iptables")
	return err == nil
}

// atLeastKeys are parameters where a bigger number is strictly better, so a
// host that already exceeds the recommendation must be left alone.
//
// Distributions raise these defaults over time — Ubuntu's vm.max_map_count is
// 1048576 on current kernels, four times what a 2020-era guide recommends —
// and a fixed "recommended" value silently becomes a downgrade. Same for the
// memory reserve, where lowering it costs OOM headroom on a host that is
// already under pressure.
var atLeastKeys = map[string]bool{
	"vm.max_map_count":               true,
	"vm.min_free_kbytes":             true,
	"kernel.pid_max":                 true,
	"fs.file-max":                    true,
	"fs.inotify.max_user_watches":    true,
	"fs.inotify.max_user_instances":  true,
	"fs.aio-max-nr":                  true,
	"net.core.somaxconn":             true,
	"net.core.netdev_max_backlog":    true,
	"net.ipv4.tcp_max_syn_backlog":   true,
	"net.netfilter.nf_conntrack_max": true,
	"net.core.rmem_max":              true,
	"net.core.wmem_max":              true,
	// Hardening levels: the kernel treats a higher value as stricter. Yama's
	// 2 is also the stopgap for a ptrace dumpability bypass, which lowering
	// to 1 would undo.
	"kernel.unprivileged_bpf_disabled": true,
	"kernel.kptr_restrict":             true,
	"net.core.bpf_jit_harden":          true,
	"fs.protected_fifos":               true,
	"fs.protected_regular":             true,
	"kernel.yama.ptrace_scope":         true,
}

// atLeastMaxKeys are min/default/max triples whose max a host already beats
// when its own is at least as big, as the kernel's tcp_rmem max is since 6.16.
var atLeastMaxKeys = map[string]bool{
	"net.ipv4.tcp_rmem": true,
	"net.ipv4.tcp_wmem": true,
}

// lockedValues are values the kernel will not change back from until a
// reboot: unprivileged_bpf_disabled=1 ("disabled without recovery"). A host
// there keeps it, as writing anything else fails, and that would fail the
// whole apply.
var lockedValues = map[string]string{
	"kernel.unprivileged_bpf_disabled": "1",
}

// keepCurrentIfBetter drops a recommendation the host already beats.
//
// Returns the value to recommend: the current one when it is at least as good,
// otherwise the proposal. Non-numeric or unreadable current values fall
// through to the proposal unchanged.
func keepCurrentIfBetter(key, current, recommended string) string {
	if locked, ok := lockedValues[key]; ok && strings.TrimSpace(current) == locked {
		return current
	}
	if atLeastMaxKeys[key] {
		cur, rec := strings.Fields(current), strings.Fields(recommended)
		if len(cur) == 3 && len(rec) == 3 {
			c, err1 := strconv.ParseInt(cur[2], 10, 64)
			r, err2 := strconv.ParseInt(rec[2], 10, 64)
			if err1 == nil && err2 == nil && c >= r {
				return current
			}
		}
		return recommended
	}
	if !atLeastKeys[key] {
		return recommended
	}
	cur, err1 := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
	rec, err2 := strconv.ParseInt(strings.TrimSpace(recommended), 10, 64)
	if err1 != nil || err2 != nil {
		return recommended
	}
	if cur >= rec {
		return current
	}
	return recommended
}

func buildRecommendations(cpuCores int, totalRAMGB float64, totalRAMBytes uint64) []TuningCategory {
	// tcp_rmem's max is what the kernel gives itself since 6.16: 1/128 of
	// memory, between 4 MiB and 32 MiB. A fixed table capped it at 16 MiB,
	// half the receive window those kernels let a fast link grow to.
	rmemAutoMax := min(max(totalRAMBytes/128, 4<<20), 32<<20)
	var rmemMax, wmemMax, tcpRmem, tcpWmem string
	switch {
	case totalRAMGB >= 16:
		rmemMax = "16777216"
		wmemMax = "16777216"
		tcpRmem = fmt.Sprintf("4096 131072 %d", rmemAutoMax)
		tcpWmem = "4096 65536 16777216"
	case totalRAMGB >= 8:
		rmemMax = "8388608"
		wmemMax = "8388608"
		tcpRmem = fmt.Sprintf("4096 131072 %d", rmemAutoMax)
		tcpWmem = "4096 65536 8388608"
	default:
		rmemMax = "4194304"
		wmemMax = "4194304"
		tcpRmem = fmt.Sprintf("4096 87380 %d", rmemAutoMax)
		tcpWmem = "4096 65536 4194304"
	}

	somaxconn := "65535"
	backlog := "65535"
	if cpuCores <= 2 {
		somaxconn = "32768"
		backlog = "32768"
	}

	// With zram, swapping is cheap and preferable to evicting page cache, so
	// the usual advice inverts. See zramSwapActive.
	swappiness := "10"
	if zramSwapActive() {
		swappiness = "100"
	} else if totalRAMGB < 2 {
		swappiness = "60"
	} else if totalRAMGB < 4 {
		swappiness = "30"
	}

	totalRAMMB := totalRAMBytes / (1024 * 1024)
	fileMax := totalRAMMB * 256
	if fileMax < 65536 {
		fileMax = 65536
	}
	if fileMax > 2097152 {
		fileMax = 2097152
	}

	minFreeKB := 65536
	if totalRAMGB >= 32 {
		minFreeKB = 262144
	} else if totalRAMGB >= 16 {
		minFreeKB = 131072
	}

	// conntrack table size scales roughly with expected concurrent
	// connections; 262144 is the modern Docker-host floor and scales by RAM.
	conntrackMax := "262144"
	if totalRAMGB >= 16 {
		conntrackMax = "524288"
	}
	if totalRAMGB >= 32 {
		conntrackMax = "1048576"
	}

	cats := []TuningCategory{
		{
			Name:    "network",
			Benefit: "benefit_network",
			Caution: "caution_network",
			Params: []TuningParam{
				// --- congestion control + buffers ---
				{Key: "net.core.default_qdisc", Recommended: "fq", Description: "Fair Queue scheduler — paces the host's own TCP flows (BBR paces by itself since Linux 4.20)"},
				{Key: "net.ipv4.tcp_congestion_control", Recommended: "bbr", Description: "BBR congestion control — higher throughput, lower latency"},
				{Key: "net.core.rmem_max", Recommended: rmemMax, Description: "Maximum receive socket buffer size"},
				{Key: "net.core.wmem_max", Recommended: wmemMax, Description: "Maximum send socket buffer size"},
				{Key: "net.ipv4.tcp_rmem", Recommended: tcpRmem, Description: "TCP receive buffer (min/default/max)"},
				{Key: "net.ipv4.tcp_wmem", Recommended: tcpWmem, Description: "TCP send buffer (min/default/max)"},
				// --- queue sizes ---
				{Key: "net.core.somaxconn", Recommended: somaxconn, Description: "Maximum connection backlog queue"},
				{Key: "net.core.netdev_max_backlog", Recommended: backlog, Description: "Maximum network device backlog"},
				{Key: "net.ipv4.tcp_max_syn_backlog", Recommended: somaxconn, Description: "Maximum SYN backlog queue"},
				// --- connection lifecycle ---
				{Key: "net.ipv4.tcp_fastopen", Recommended: "3", Description: "TCP Fast Open (client + server)"},
				{Key: "net.ipv4.tcp_tw_reuse", Recommended: "1", Description: "Reuse TIME_WAIT sockets for new connections"},
				{Key: "net.ipv4.tcp_fin_timeout", Recommended: "15", Description: "FIN-WAIT-2 timeout (seconds)"},
				{Key: "net.ipv4.tcp_keepalive_time", Recommended: "300", Description: "TCP keepalive interval (seconds)"},
				{Key: "net.ipv4.tcp_keepalive_intvl", Recommended: "15", Description: "TCP keepalive probe interval (seconds)"},
				{Key: "net.ipv4.tcp_keepalive_probes", Recommended: "5", Description: "TCP keepalive probe count before drop"},
				{Key: "net.ipv4.tcp_mtu_probing", Recommended: "1", Description: "Enable TCP MTU probing (PMTUD)"},
				// --- persistent / streaming connection tuning (WS / SSE / HTTP/2 wins) ---
				{Key: "net.ipv4.tcp_slow_start_after_idle", Recommended: "0", Description: "Don't reset CWND after an idle period — big win for persistent HTTP/2, WS, gRPC"},
				{Key: "net.ipv4.tcp_notsent_lowat", Recommended: "131072", Description: "Cap unsent bytes per socket — reduces buffer bloat for streaming / SSE"},
				{Key: "net.ipv4.tcp_no_metrics_save", Recommended: "1", Description: "Don't cache per-destination TCP metrics (can mis-tune later connections)"},
				{Key: "net.ipv4.ip_local_port_range", Recommended: "10240 65535", Description: "Expand ephemeral port range for outbound-heavy workloads, staying clear of registered service ports"},
				{Key: "net.ipv4.tcp_rfc1337", Recommended: "1", Description: "Protect against TIME-WAIT assassination hazards (RFC 1337)"},
			},
		},
		{
			Name:    "memory",
			Benefit: "benefit_memory",
			Caution: "caution_memory",
			Params: []TuningParam{
				{Key: "vm.swappiness", Recommended: swappiness, Description: "Swap usage tendency (lower = less swap)"},
				{Key: "vm.dirty_ratio", Recommended: "15", Description: "Maximum dirty page percentage before forced sync"},
				{Key: "vm.dirty_background_ratio", Recommended: "5", Description: "Background dirty page sync threshold"},
				{Key: "vm.vfs_cache_pressure", Recommended: "50", Description: "VFS cache reclaim pressure (lower = keep cache longer)"},
				{Key: "vm.min_free_kbytes", Recommended: strconv.Itoa(minFreeKB), Description: "Minimum free memory reserved (KB)"},
				// --- container-host essentials ---
				{Key: "vm.max_map_count", Recommended: "262144", Description: "Maximum VMA mappings per process — required by Elasticsearch / MongoDB / Redis / many DB containers"},
				{Key: "kernel.pid_max", Recommended: "4194304", Description: "Maximum PID — prevents PID exhaustion on container hosts running many workloads"},
			},
		},
		{
			Name:    "filesystem",
			Benefit: "benefit_filesystem",
			Caution: "caution_filesystem",
			Params: []TuningParam{
				{Key: "fs.file-max", Recommended: strconv.FormatUint(fileMax, 10), Description: "Maximum system-wide file descriptors"},
				{Key: "fs.inotify.max_user_watches", Recommended: "524288", Description: "Maximum inotify watches per user"},
				{Key: "fs.inotify.max_user_instances", Recommended: "512", Description: "Maximum inotify instances per user"},
				{Key: "fs.aio-max-nr", Recommended: "1048576", Description: "Maximum async I/O requests"},
				// --- symlink / hardlink / fifo / setuid protections (Kees Cook's suite) ---
				{Key: "fs.protected_symlinks", Recommended: "1", Description: "Block symlink-follow in world-writable sticky dirs (TOCTOU defense)"},
				{Key: "fs.protected_hardlinks", Recommended: "1", Description: "Restrict hardlink creation to owner-accessible files"},
				{Key: "fs.protected_fifos", Recommended: "2", Description: "Restrict FIFO creation in sticky directories"},
				{Key: "fs.protected_regular", Recommended: "2", Description: "Restrict regular-file creation in world-writable sticky dirs"},
				{Key: "fs.suid_dumpable", Recommended: "0", Description: "Disable core dumps from setuid programs (prevents leaking secrets)"},
			},
		},
		{
			Name:    "security",
			Benefit: "benefit_security",
			Caution: "caution_security",
			Params: []TuningParam{
				{Key: "net.ipv4.tcp_syncookies", Recommended: "1", Description: "SYN flood protection"},
				{Key: "net.ipv4.conf.all.rp_filter", Recommended: "2", Description: "Reverse path filtering, loose mode (anti-spoofing without breaking asymmetric routes)"},
				{Key: "net.ipv4.conf.default.rp_filter", Recommended: "2", Description: "Default reverse path filtering, loose mode"},
				{Key: "net.ipv4.icmp_echo_ignore_broadcasts", Recommended: "1", Description: "Ignore broadcast ICMP (Smurf attack prevention)"},
				{Key: "net.ipv4.icmp_ignore_bogus_error_responses", Recommended: "1", Description: "Ignore bogus ICMP error responses"},
				{Key: "net.ipv4.conf.all.accept_redirects", Recommended: "0", Description: "Disable ICMP redirect acceptance"},
				{Key: "net.ipv4.conf.default.accept_redirects", Recommended: "0", Description: "Disable default ICMP redirects"},
				{Key: "net.ipv4.conf.all.send_redirects", Recommended: "0", Description: "Disable sending ICMP redirects"},
				// The kernel sends them on an interface if all or the interface
				// allows it, so all=0 alone left every interface sending.
				{Key: "net.ipv4.conf.default.send_redirects", Recommended: "0", Description: "Disable sending ICMP redirects on interfaces made from now on"},
				{Key: "net.ipv4.conf.all.accept_source_route", Recommended: "0", Description: "Disable IP source routing"},
				{Key: "net.ipv4.conf.default.accept_source_route", Recommended: "0", Description: "Disable default source routing"},
				{Key: "net.ipv6.conf.all.accept_redirects", Recommended: "0", Description: "Disable IPv6 ICMP redirects"},
				{Key: "net.ipv6.conf.default.accept_redirects", Recommended: "0", Description: "Disable IPv6 default redirects"},
				// --- kernel / info-leak hardening ---
				{Key: "kernel.randomize_va_space", Recommended: "2", Description: "Full ASLR (heap + stack + mmap + VDSO)"},
				{Key: "kernel.kptr_restrict", Recommended: "2", Description: "Hide kernel pointers in /proc from unprivileged users"},
				{Key: "kernel.dmesg_restrict", Recommended: "1", Description: "Restrict dmesg to root (prevents log-based info leaks)"},
				{Key: "kernel.yama.ptrace_scope", Recommended: "1", Description: "ptrace only allowed on descendant processes (Ubuntu default)"},
				// --- eBPF hardening (matters once the workload uses eBPF-based tooling) ---
				// 2, not 1: the same block, and the one the rollback can undo —
				// the kernel keeps 1 until a reboot.
				{Key: "kernel.unprivileged_bpf_disabled", Recommended: "2", Description: "Block unprivileged BPF program loads (Spectre mitigation)"},
				{Key: "net.core.bpf_jit_harden", Recommended: "2", Description: "Harden BPF JIT against spray-style exploits"},
			},
		},
	}

	// On Proxmox VE forwarding, bridge filtering and conntrack are left to
	// Proxmox: see pveManagedKeys.
	pve := platform.Get().PVE
	for i := range cats {
		if pve || cats[i].Name != "network" {
			continue
		}
		// --- Docker / bridge networking (required on container hosts) ---
		cats[i].Params = append(cats[i].Params,
			TuningParam{Key: "net.ipv4.ip_forward", Recommended: "1", Description: "Enable IP forwarding (required by Docker; set in sysctl so it survives reboot independently of docker.service ordering)"},
		)
		// br_netfilter creates these; without it they cannot be read or written and
		// would sit permanently unapplied.
		if bridgeNetfilterAvailable() {
			cats[i].Params = append(cats[i].Params,
				TuningParam{Key: "net.bridge.bridge-nf-call-iptables", Recommended: "1", Description: "Bridged IPv4 traffic goes through iptables, so firewall rules also see traffic between containers on one bridge; dockerd turns it on itself where a network needs it (icc=false, or the userland proxy off)"},
				TuningParam{Key: "net.bridge.bridge-nf-call-ip6tables", Recommended: "1", Description: "The same for bridged IPv6 traffic and ip6tables"},
			)
		}
	}

	if pve {
		for i := range cats {
			var kept []TuningParam
			for _, p := range cats[i].Params {
				if pveSkippedKeys[p.Key] || pveManaged(p.Key) {
					continue
				}
				if p.Key == "kernel.kptr_restrict" {
					// 2 hides the addresses from root as well, and with them
					// perf and bpftrace on the hypervisor.
					p.Recommended = "1"
					p.Description = "Hide kernel pointers from users without CAP_SYSLOG; root keeps them for perf and bpftrace"
				}
				kept = append(kept, p)
			}
			cats[i].Params = kept
		}
	}

	if !pve && conntrackModuleLoaded() {
		cats = append(cats, TuningCategory{
			Name:    "conntrack",
			Benefit: "benefit_conntrack",
			Caution: "caution_conntrack",
			Params: []TuningParam{
				{Key: "net.netfilter.nf_conntrack_max", Recommended: conntrackMax, Description: "Maximum conntrack entries (Docker-heavy hosts exhaust default ~65k fast)"},
				{Key: "net.netfilter.nf_conntrack_tcp_timeout_established", Recommended: "600", Description: "TIME for established TCP conntrack entries (default is 5 days)"},
				{Key: "net.netfilter.nf_conntrack_tcp_timeout_close_wait", Recommended: "15", Description: "CLOSE_WAIT conntrack timeout (default 60s)"},
			},
		})
	}

	return cats
}
