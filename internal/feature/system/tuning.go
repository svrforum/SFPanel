package system

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
)

const (
	sysctlConfPath  = "/etc/sysctl.d/99-sfpanel-tuning.conf"
	rollbackTimeout = 60 // seconds
	commandTimeout  = 5 * time.Minute
)

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

func readSysctl(cmd commonExec.Commander, key string) string {
	out, err := runTuningCommand(cmd, "sysctl", "-n", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
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

	categories := buildRecommendations(cpuCores, totalRAMGB, totalRAM)

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

	return response.OK(c, map[string]interface{}{
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

	categories := buildRecommendations(cpuCores, totalRAMGB, totalRAM)

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

	keys := make([]string, 0, len(existingParams))
	for k := range existingParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	lines = append(lines, "# SFPanel System Tuning")
	lines = append(lines, "# Auto-generated — do not edit manually")
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

	if _, err := os.Stat(sysctlConfPath); os.IsNotExist(err) {
		return response.OK(c, map[string]interface{}{
			"message": "No tuning configuration to reset",
		})
	}

	if err := os.Remove(sysctlConfPath); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrTuningError,
			"Failed to remove config: "+err.Error())
	}

	_, _ = runTuningCommand(h.Cmd, "sysctl", "--system")

	return response.OK(c, map[string]interface{}{
		"message": "Tuning reset to system defaults",
	})
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

func buildRecommendations(cpuCores int, totalRAMGB float64, totalRAMBytes uint64) []TuningCategory {
	var rmemMax, wmemMax, tcpRmem, tcpWmem string
	switch {
	case totalRAMGB >= 16:
		rmemMax = "16777216"
		wmemMax = "16777216"
		tcpRmem = "4096 131072 16777216"
		tcpWmem = "4096 65536 16777216"
	case totalRAMGB >= 8:
		rmemMax = "8388608"
		wmemMax = "8388608"
		tcpRmem = "4096 131072 8388608"
		tcpWmem = "4096 65536 8388608"
	default:
		rmemMax = "4194304"
		wmemMax = "4194304"
		tcpRmem = "4096 87380 4194304"
		tcpWmem = "4096 65536 4194304"
	}

	somaxconn := "65535"
	backlog := "65535"
	if cpuCores <= 2 {
		somaxconn = "32768"
		backlog = "32768"
	}

	swappiness := "10"
	if totalRAMGB < 2 {
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
				{Key: "net.core.default_qdisc", Recommended: "fq", Description: "Fair Queue scheduler (required for BBR)"},
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
				{Key: "net.ipv4.ip_local_port_range", Recommended: "1024 65535", Description: "Expand ephemeral port range for outbound-heavy workloads"},
				{Key: "net.ipv4.tcp_rfc1337", Recommended: "1", Description: "Protect against TIME-WAIT assassination hazards (RFC 1337)"},
				// --- Docker / bridge networking (required on container hosts) ---
				{Key: "net.ipv4.ip_forward", Recommended: "1", Description: "Enable IP forwarding (required by Docker; set in sysctl so it survives reboot independently of docker.service ordering)"},
				{Key: "net.bridge.bridge-nf-call-iptables", Recommended: "1", Description: "Bridged traffic traverses iptables (Docker bridge networks depend on this)"},
				{Key: "net.bridge.bridge-nf-call-ip6tables", Recommended: "1", Description: "Bridged IPv6 traffic traverses ip6tables (same rationale as the IPv4 variant)"},
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
				{Key: "net.ipv4.conf.all.rp_filter", Recommended: "1", Description: "Reverse path filtering (anti-spoofing)"},
				{Key: "net.ipv4.conf.default.rp_filter", Recommended: "1", Description: "Default reverse path filtering"},
				{Key: "net.ipv4.icmp_echo_ignore_broadcasts", Recommended: "1", Description: "Ignore broadcast ICMP (Smurf attack prevention)"},
				{Key: "net.ipv4.icmp_ignore_bogus_error_responses", Recommended: "1", Description: "Ignore bogus ICMP error responses"},
				{Key: "net.ipv4.conf.all.accept_redirects", Recommended: "0", Description: "Disable ICMP redirect acceptance"},
				{Key: "net.ipv4.conf.default.accept_redirects", Recommended: "0", Description: "Disable default ICMP redirects"},
				{Key: "net.ipv4.conf.all.send_redirects", Recommended: "0", Description: "Disable sending ICMP redirects"},
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
				{Key: "kernel.unprivileged_bpf_disabled", Recommended: "1", Description: "Block unprivileged BPF program loads (Spectre mitigation)"},
				{Key: "net.core.bpf_jit_harden", Recommended: "2", Description: "Harden BPF JIT against spray-style exploits"},
			},
		},
	}

	if conntrackModuleLoaded() {
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
