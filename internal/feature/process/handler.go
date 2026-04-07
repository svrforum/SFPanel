package process

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/svrforum/SFPanel/internal/api/response"
)

type ProcessInfo struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	Status  string  `json:"status"`
	User    string  `json:"user"`
	Command string  `json:"command"`
}

type Handler struct{}

// processCache holds a cached snapshot of process data so that the expensive
// 200ms CPU measurement is not repeated on every HTTP request.
var processCache struct {
	sync.RWMutex
	data      []ProcessInfo
	updatedAt time.Time
}

const processCacheTTL = 3 * time.Second

// cachedProcesses returns the cached process list, refreshing it when stale.
func cachedProcesses() ([]ProcessInfo, error) {
	processCache.RLock()
	if time.Since(processCache.updatedAt) < processCacheTTL && processCache.data != nil {
		result := make([]ProcessInfo, len(processCache.data))
		copy(result, processCache.data)
		processCache.RUnlock()
		return result, nil
	}
	processCache.RUnlock()

	// Cache miss — collect fresh data
	processCache.Lock()
	defer processCache.Unlock()

	// Double-check after acquiring write lock (another goroutine may have refreshed)
	if time.Since(processCache.updatedAt) < processCacheTTL && processCache.data != nil {
		result := make([]ProcessInfo, len(processCache.data))
		copy(result, processCache.data)
		return result, nil
	}

	infos, err := collectProcesses()
	if err != nil {
		return nil, err
	}
	processCache.data = infos
	processCache.updatedAt = time.Now()

	result := make([]ProcessInfo, len(infos))
	copy(result, infos)
	return result, nil
}

// TopProcesses returns the top 10 processes by CPU usage (for dashboard).
func (h *Handler) TopProcesses(c echo.Context) error {
	infos, err := cachedProcesses()
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
	infos, err := cachedProcesses()
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
	if pid <= 1 {
		return response.Fail(c, http.StatusForbidden, response.ErrInvalidPID, "Cannot send signal to PID 0 or 1")
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
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSignal,
			"Supported signals: TERM, KILL, HUP, INT")
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
	processCache.Lock()
	processCache.updatedAt = time.Time{}
	processCache.Unlock()

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Signal %s sent to process %d", strings.ToUpper(req.Signal), pid),
		"pid":     pid,
		"signal":  strings.ToUpper(req.Signal),
	})
}

// collectProcesses gathers information about all running processes.
// It calls CPUPercent() twice with a 200ms interval to get accurate CPU usage,
// since the first call to gopsutil's CPUPercent() returns a value over the
// process lifetime rather than the current rate.
func collectProcesses() ([]ProcessInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	// First pass: prime CPU measurement for all processes
	for _, p := range procs {
		p.CPUPercent()
	}

	// Wait for a short interval to measure actual CPU rate
	time.Sleep(200 * time.Millisecond)

	// Second pass: collect actual data
	infos := make([]ProcessInfo, 0, len(procs))
	for _, p := range procs {
		name, _ := p.Name()
		cpuPct, _ := p.CPUPercent()
		memPct, _ := p.MemoryPercent()
		status, _ := p.Status()
		username, _ := p.Username()
		cmdline, _ := p.Cmdline()

		statusStr := ""
		if len(status) > 0 {
			statusStr = status[0]
		}

		if cmdline == "" {
			cmdline = name
		}

		infos = append(infos, ProcessInfo{
			PID:     p.Pid,
			Name:    name,
			CPU:     cpuPct,
			Memory:  float64(memPct),
			Status:  statusStr,
			User:    username,
			Command: cmdline,
		})
	}

	return infos, nil
}
