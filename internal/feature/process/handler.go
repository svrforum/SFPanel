package process

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/sysguard"
)

type ProcessInfo struct {
	PID     int32   `json:"pid"`
	PPID    int32   `json:"ppid"`
	Name    string  `json:"name"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	RSS     uint64  `json:"rss"` // resident set size in bytes (absolute, not %)
	Nice    int32   `json:"nice"`
	Status  string  `json:"status"`
	User    string  `json:"user"`
	Command string  `json:"command"`
}

// Handler holds the per-instance process cache. The cache used to be a
// package-level var which broke parallel tests (state leaked between them)
// and prevented two router instances from coexisting. Moving it onto the
// Handler keeps the same single-process semantics but lets tests scope it.
type Handler struct {
	cache processCache
}

type processCache struct {
	sync.RWMutex
	data      []ProcessInfo
	updatedAt time.Time
	// cpu is the CPU-time reading taken at the end of the last collection.
	// It lives beside the result cache under the same lock because it has the
	// same lifetime: one per Handler, published together with the rows it
	// will be the baseline for.
	cpu cpuSnapshot
}

const processCacheTTL = 3 * time.Second

// cpuSnapshot is the CPU time of every process at one instant, kept between
// collections so the rate can be measured over the interval the pages
// actually poll at (10-15 s) rather than inside one request. Measuring inside a
// request is what made the panel the top row of its own list: one collection
// costs sfpanel 0.16 s of CPU, so in a 200 ms window it was the one process
// guaranteed to be awake for all of it.
type cpuSnapshot struct {
	at    time.Time
	times map[int32]float64
}

const (
	// maxCPUWindow caps how old a baseline may be. Beyond it the average
	// would smear a spike across minutes, so the collection samples fresh.
	maxCPUWindow = 90 * time.Second
	// minCPUWindow is the shortest interval that yields a rate rather than an
	// accident, and doubles as the width of the fresh sample taken when no
	// usable baseline exists. A baseline younger than this (a collection right
	// after a kill invalidates the cache) is treated as unusable.
	minCPUWindow = 1 * time.Second
)

// usable reports whether the snapshot can serve as a rate baseline at now. A
// snapshot with no readings is not one however fresh it is — every rate would
// come back 0 — and the zero value is covered by that same clause.
func (s cpuSnapshot) usable(now time.Time) bool {
	if len(s.times) == 0 {
		return false
	}
	age := now.Sub(s.at)
	return age >= minCPUWindow && age <= maxCPUWindow
}

// cpuRate is the share of one core a process used between the previous
// snapshot and now, in percent. It returns 0 rather than a number it cannot
// justify: a pid the baseline never saw has no window, a pid whose CPU time
// went backwards is a reused pid, and a zero-length window is no window at
// all. Reporting a lifetime average in those cases is what made an idle
// process that burned an hour last night outrank one spiking now.
func cpuRate(prev cpuSnapshot, now time.Time, pid int32, cpuSeconds float64) float64 {
	before, ok := prev.times[pid]
	if !ok {
		return 0
	}
	window := now.Sub(prev.at).Seconds()
	if window <= 0 {
		return 0
	}
	delta := cpuSeconds - before
	if delta <= 0 {
		return 0
	}
	return delta / window * 100
}

// cachedProcesses returns the cached process list, refreshing it when stale.
func (h *Handler) cachedProcesses() ([]ProcessInfo, error) {
	h.cache.RLock()
	if time.Since(h.cache.updatedAt) < processCacheTTL && h.cache.data != nil {
		result := make([]ProcessInfo, len(h.cache.data))
		copy(result, h.cache.data)
		h.cache.RUnlock()
		return result, nil
	}
	prev := h.cache.cpu
	h.cache.RUnlock()

	// Cache miss — collect fresh data WITHOUT holding any lock. collectProcesses
	// enumerates /proc, and with no usable baseline it also sleeps a second;
	// holding the write lock across it would block every concurrent dashboard
	// reader for that whole window. Concurrent misses may each collect (bounded
	// and rare given the 3s TTL); we take the write lock only to publish.
	infos, snap, err := collectProcesses(prev)
	if err != nil {
		return nil, err
	}

	h.cache.Lock()
	h.cache.data = infos
	h.cache.updatedAt = time.Now()
	h.cache.cpu = snap
	h.cache.Unlock()

	result := make([]ProcessInfo, len(infos))
	copy(result, infos)
	return result, nil
}

// TopProcesses returns the top 10 processes by CPU usage (for dashboard).
func (h *Handler) TopProcesses(c echo.Context) error {
	infos, err := h.cachedProcesses()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrProcessError, "Failed to list processes")
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CPU > infos[j].CPU
	})

	if len(infos) > 10 {
		infos = infos[:10]
	}

	return response.OK(c, infos)
}

// ListProcesses returns all processes. Filtering and sorting is handled client-side.
// GET /system/processes/list
func (h *Handler) ListProcesses(c echo.Context) error {
	infos, err := h.cachedProcesses()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrProcessError, "Failed to list processes")
	}

	return response.OK(c, map[string]interface{}{
		"processes": infos,
		"total":     len(infos),
	})
}

// KillProcess sends a signal to a process.
// POST /system/processes/:pid/kill  body: { signal: "TERM" | "KILL" | "9" | "15" }
func (h *Handler) KillProcess(c echo.Context) error {
	pidStr := c.Param("pid")
	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPID, "Invalid PID")
	}
	if sysguard.IsProtectedPID(int(pid)) {
		return response.Fail(c, http.StatusForbidden, response.ErrInvalidPID,
			"Cannot send signal to protected PID (init, kthreadd, or sfpanel itself)")
	}
	// Refuse any subprocess the panel itself spawned (apt, docker compose,
	// terminal PTYs, etc.). They share the panel's process group by default,
	// so the pgid check catches them all in one shot. If the PID has already
	// disappeared (err != nil) we fall through to the existing process.NewProcess
	// path which returns the standard "not found" 404.
	if isChild, err := sysguard.IsPanelChildPID(int(pid)); err == nil && isChild {
		return response.Fail(c, http.StatusForbidden, response.ErrInvalidPID,
			"Refusing to kill panel-spawned subprocess")
	}

	var req struct {
		Signal string `json:"signal"`
	}
	if err := c.Bind(&req); err != nil {
		req.Signal = "TERM"
	}
	if req.Signal == "" {
		req.Signal = "TERM"
	}

	// Map signal name to syscall
	var sig syscall.Signal
	switch strings.ToUpper(req.Signal) {
	case "KILL", "9":
		sig = syscall.SIGKILL
	case "TERM", "15":
		sig = syscall.SIGTERM
	case "HUP", "1":
		sig = syscall.SIGHUP
	case "INT", "2":
		sig = syscall.SIGINT
	case "STOP", "19":
		sig = syscall.SIGSTOP
	case "CONT", "18":
		sig = syscall.SIGCONT
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSignal,
			"Supported signals: TERM, KILL, HUP, INT, STOP, CONT")
	}

	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrProcessNotFound,
			fmt.Sprintf("Process %d not found", pid))
	}

	if err := p.SendSignal(sig); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrKillFailed,
			fmt.Sprintf("Failed to send signal %s to process %d: %s", req.Signal, pid, err.Error()))
	}

	// Invalidate cache after kill so the next fetch reflects the change
	h.cache.Lock()
	h.cache.updatedAt = time.Time{}
	h.cache.Unlock()

	// Enriched audit trail: the audit middleware writes an audit_logs row
	// capturing path/method/status/user/node_id but NOT the request body,
	// so the signal that was actually sent never lands in audit_logs. Emit
	// a structured slog event so ops logs retain the full picture.
	username, _ := c.Get("username").(string)
	slog.Info("process killed via panel API",
		"component", "process",
		"pid", pid,
		"signal", strings.ToUpper(req.Signal),
		"username", username,
	)

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Signal %s sent to process %d", strings.ToUpper(req.Signal), pid),
		"pid":     pid,
		"signal":  strings.ToUpper(req.Signal),
	})
}

// ReniceProcess changes a process's scheduling priority (nice value).
// POST /system/processes/:pid/renice  body: { nice: -20..19 }
// Lowering nice (toward -20) raises priority and needs privilege; the panel
// runs as root so it's allowed, but the same protected-PID guards as kill
// apply — you can't renice init/kthreadd/the panel or a panel-spawned child.
func (h *Handler) ReniceProcess(c echo.Context) error {
	pidStr := c.Param("pid")
	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPID, "Invalid PID")
	}
	if sysguard.IsProtectedPID(int(pid)) {
		return response.Fail(c, http.StatusForbidden, response.ErrInvalidPID,
			"Cannot renice protected PID (init, kthreadd, or sfpanel itself)")
	}
	if isChild, err := sysguard.IsPanelChildPID(int(pid)); err == nil && isChild {
		return response.Fail(c, http.StatusForbidden, response.ErrInvalidPID,
			"Refusing to renice panel-spawned subprocess")
	}

	var req struct {
		Nice *int `json:"nice"`
	}
	if err := c.Bind(&req); err != nil || req.Nice == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "nice value required")
	}
	if *req.Nice < -20 || *req.Nice > 19 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
			"nice must be between -20 and 19")
	}

	// Confirm the process exists first so a stale PID yields a clean 404 rather
	// than Setpriority's bare ESRCH.
	if _, err := process.NewProcess(int32(pid)); err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrProcessNotFound,
			fmt.Sprintf("Process %d not found", pid))
	}

	if err := syscall.Setpriority(syscall.PRIO_PROCESS, int(pid), *req.Nice); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			fmt.Sprintf("Failed to renice process %d: %s", pid, err.Error()))
	}

	h.cache.Lock()
	h.cache.updatedAt = time.Time{}
	h.cache.Unlock()

	username, _ := c.Get("username").(string)
	slog.Info("process reniced via panel API",
		"component", "process",
		"pid", pid,
		"nice", *req.Nice,
		"username", username,
	)

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Process %d reniced to %d", pid, *req.Nice),
		"pid":     pid,
		"nice":    *req.Nice,
	})
}

func init() {
	// Every CreateTime re-reads /proc/stat for the boot time unless told to
	// cache it, and boot time does not change while the process is running.
	// The v0.72.0 audit turned this on because the collection called Percent
	// once per process and each of those needs a CreateTime — thousands of
	// reads of the same file per collection. The collection now reads Times()
	// directly and needs no CreateTime at all, but the setting is global to
	// gopsutil and free, so it stays for any other caller that does.
	process.EnableBootTimeCache(true)
}

// readCPUTimes takes a CPU-time reading for every process in procs. A process
// that vanishes mid-walk is simply absent from the map, which cpuRate reads as
// "no baseline" rather than as a rate.
func readCPUTimes(procs []*process.Process, at time.Time) cpuSnapshot {
	times := make(map[int32]float64, len(procs))
	for _, p := range procs {
		if t, err := p.Times(); err == nil && t != nil {
			times[p.Pid] = t.User + t.System
		}
	}
	return cpuSnapshot{at: at, times: times}
}

// collectProcesses gathers information about all running processes, reporting
// each one's CPU as the rate over the interval since the previous collection.
//
// It used to prime Percent(0) for every process, sleep 200 ms and read again.
// The delta arithmetic was right; the window was not. A process that wakes for
// nine milliseconds inside 200 ms reports 4.5%, and one process is guaranteed
// to be awake for the whole window — the panel itself, walking 514 processes
// twice. Measured on the reporting host: the panel showed sfpanel at 42% where
// fifteen seconds of direct measurement read 0.4%, and one collection costs
// sfpanel 0.16 s of CPU against 0.02 s over ten idle seconds. The pages poll
// every 10-15 s and the result is cached for 3 s, so the interval between
// collections is a window of seconds — long enough that the collection's own
// cost is a rounding error, and comparable to what top shows. It is also one
// /proc read per process instead of two, so the endpoint answers faster.
func collectProcesses(prev cpuSnapshot) ([]ProcessInfo, cpuSnapshot, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, cpuSnapshot{}, err
	}

	now := time.Now()
	if !prev.usable(now) {
		// No comparable baseline: the first collection after a restart, one
		// taken too soon after the last, or one whose baseline is old enough
		// that the average would smear a spike across minutes. Sample a
		// baseline here — over a full second, not the 200 ms this used to
		// sleep, so the first screen is not the same accident.
		prev = readCPUTimes(procs, now)
		time.Sleep(minCPUWindow)
		now = time.Now()
	}

	// Total memory once, not per process. gopsutil's MemoryPercent re-reads
	// /proc/meminfo on every call, which at 640 processes was 640 reads of the
	// same file per collection — a third of the whole walk's cost. The
	// percentage is RSS over a total that does not change between two rows.
	var totalMem uint64
	if vm, err := mem.VirtualMemory(); err == nil {
		totalMem = vm.Total
	}
	// uid → name once per uid, not per process. Username resolves through the
	// passwd database each time, and a host runs a few dozen distinct users
	// across hundreds of processes.
	usernames := map[uint32]string{}

	// The reading that becomes the next collection's baseline, built as we go.
	times := make(map[int32]float64, len(procs))

	infos := make([]ProcessInfo, 0, len(procs))
	for _, p := range procs {
		name, _ := p.Name()

		// Times() is one /proc/<pid>/stat read and needs no priming, unlike
		// Percent. User + system is the CPU the process has consumed since it
		// started; the rate is what changed since the baseline.
		var cpuSeconds float64
		if t, err := p.Times(); err == nil && t != nil {
			cpuSeconds = t.User + t.System
			times[p.Pid] = cpuSeconds
		}
		cpuPct := cpuRate(prev, now, p.Pid, cpuSeconds)

		status, _ := p.Status()
		cmdline, _ := p.Cmdline()
		ppid, _ := p.Ppid()
		nice, _ := p.Nice()

		// Absolute resident memory complements the percentage: on a 64 GB host
		// "1.2%" hides a 780 MB process. MemoryInfo can fail for a process that
		// exited mid-scan; leave RSS 0 in that case rather than dropping the row.
		var rss uint64
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			rss = mi.RSS
		}
		var memPct float64
		if totalMem > 0 {
			memPct = float64(rss) / float64(totalMem) * 100
		}

		username := ""
		if uids, err := p.Uids(); err == nil && len(uids) > 0 {
			uid := uids[0]
			if cached, ok := usernames[uid]; ok {
				username = cached
			} else {
				username, _ = p.Username()
				usernames[uid] = username
			}
		}

		statusStr := ""
		if len(status) > 0 {
			statusStr = status[0]
		}

		if cmdline == "" {
			cmdline = name
		}

		infos = append(infos, ProcessInfo{
			PID:     p.Pid,
			PPID:    ppid,
			Name:    name,
			CPU:     cpuPct,
			Memory:  memPct,
			RSS:     rss,
			Nice:    nice,
			Status:  statusStr,
			User:    username,
			Command: cmdline,
		})
	}

	return infos, cpuSnapshot{at: now, times: times}, nil
}
