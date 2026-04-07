package system

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
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
)

const (
	sysctlConfPath  = "/etc/sysctl.d/99-sfpanel-tuning.conf"
	rollbackTimeout = 60 // seconds
	commandTimeout  = 5 * time.Minute
)

// TuningHandler exposes REST handlers for system kernel parameter tuning.
type TuningHandler struct{}

// rollbackState holds the state for auto-rollback on unconfirmed changes.
var (
	rollbackMu       sync.Mutex
	rollbackTimer    *time.Timer
	rollbackValues   map[string]string
	rollbackConfFile []byte
	rollbackHadFile  bool
	rollbackDeadline time.Time
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

func runTuningCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", commandTimeout)
	}
	return string(out), err
}

func readSysctl(key string) string {
	out, err := runTuningCommand("sysctl", "-n", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func normalizeSysctl(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
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
			current := readSysctl(p.Key)
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
			Kernel:   readSysctl("kernel.osrelease"),
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
			preApplyValues[p.Key] = readSysctl(p.Key)
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

	output, err := runTuningCommand("sysctl", "-p", sysctlConfPath)
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

	log.Println("[tuning] Auto-rollback: no confirmation received, reverting changes")

	for key, val := range rollbackValues {
		if _, err := runTuningCommand("sysctl", "-w", key+"="+val); err != nil {
			log.Printf("[tuning] Rollback failed for %s: %v", key, err)
		}
	}

	if rollbackHadFile && rollbackConfFile != nil {
		if err := os.WriteFile(sysctlConfPath, rollbackConfFile, 0644); err != nil {
			log.Printf("[tuning] Failed to restore config file: %v", err)
		}
	} else if !rollbackHadFile {
		if err := os.Remove(sysctlConfPath); err != nil && !os.IsNotExist(err) {
			log.Printf("[tuning] Failed to remove config file: %v", err)
		}
	}

	rollbackValues = nil
	rollbackConfFile = nil
	rollbackTimer = nil
	log.Println("[tuning] Auto-rollback completed")
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

	_, _ = runTuningCommand("sysctl", "--system")

	return response.OK(c, map[string]interface{}{
		"message": "Tuning reset to system defaults",
	})
}

// ---------- Recommendations ----------

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

	return []TuningCategory{
		{
			Name:    "network",
			Benefit: "benefit_network",
			Caution: "caution_network",
			Params: []TuningParam{
				{Key: "net.core.default_qdisc", Recommended: "fq", Description: "Fair Queue scheduler (required for BBR)"},
				{Key: "net.ipv4.tcp_congestion_control", Recommended: "bbr", Description: "BBR congestion control — higher throughput, lower latency"},
				{Key: "net.core.rmem_max", Recommended: rmemMax, Description: "Maximum receive socket buffer size"},
				{Key: "net.core.wmem_max", Recommended: wmemMax, Description: "Maximum send socket buffer size"},
				{Key: "net.ipv4.tcp_rmem", Recommended: tcpRmem, Description: "TCP receive buffer (min/default/max)"},
				{Key: "net.ipv4.tcp_wmem", Recommended: tcpWmem, Description: "TCP send buffer (min/default/max)"},
				{Key: "net.core.somaxconn", Recommended: somaxconn, Description: "Maximum connection backlog queue"},
				{Key: "net.core.netdev_max_backlog", Recommended: backlog, Description: "Maximum network device backlog"},
				{Key: "net.ipv4.tcp_max_syn_backlog", Recommended: somaxconn, Description: "Maximum SYN backlog queue"},
				{Key: "net.ipv4.tcp_fastopen", Recommended: "3", Description: "TCP Fast Open (client + server)"},
				{Key: "net.ipv4.tcp_tw_reuse", Recommended: "1", Description: "Reuse TIME_WAIT sockets for new connections"},
				{Key: "net.ipv4.tcp_fin_timeout", Recommended: "15", Description: "FIN-WAIT-2 timeout (seconds)"},
				{Key: "net.ipv4.tcp_keepalive_time", Recommended: "300", Description: "TCP keepalive interval (seconds)"},
				{Key: "net.ipv4.tcp_keepalive_intvl", Recommended: "15", Description: "TCP keepalive probe interval (seconds)"},
				{Key: "net.ipv4.tcp_keepalive_probes", Recommended: "5", Description: "TCP keepalive probe count before drop"},
				{Key: "net.ipv4.tcp_mtu_probing", Recommended: "1", Description: "Enable TCP MTU probing (PMTUD)"},
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
			},
		},
	}
}
