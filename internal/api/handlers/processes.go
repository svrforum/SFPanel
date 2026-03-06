package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/shirou/gopsutil/v4/process"
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

type ProcessesHandler struct{}

// TopProcesses returns the top 10 processes by CPU usage (for dashboard).
func (h *ProcessesHandler) TopProcesses(c echo.Context) error {
	infos, err := collectProcesses()
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

// ListProcesses returns all processes with optional search filtering.
// GET /system/processes/list?q=searchterm&sort=cpu|memory|pid|name
func (h *ProcessesHandler) ListProcesses(c echo.Context) error {
	infos, err := collectProcesses()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrProcessError, "Failed to list processes")
	}

	// Filter by search query
	query := strings.ToLower(strings.TrimSpace(c.QueryParam("q")))
	if query != "" {
		var filtered []ProcessInfo
		for _, p := range infos {
			if strings.Contains(strings.ToLower(p.Name), query) ||
				strings.Contains(strings.ToLower(p.Command), query) ||
				strings.Contains(strings.ToLower(p.User), query) ||
				fmt.Sprintf("%d", p.PID) == query {
				filtered = append(filtered, p)
			}
		}
		infos = filtered
	}

	// Sort
	sortBy := c.QueryParam("sort")
	switch sortBy {
	case "memory":
		sort.Slice(infos, func(i, j int) bool { return infos[i].Memory > infos[j].Memory })
	case "pid":
		sort.Slice(infos, func(i, j int) bool { return infos[i].PID < infos[j].PID })
	case "name":
		sort.Slice(infos, func(i, j int) bool {
			return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
		})
	default: // cpu
		sort.Slice(infos, func(i, j int) bool { return infos[i].CPU > infos[j].CPU })
	}

	return response.OK(c, map[string]interface{}{
		"processes": infos,
		"total":     len(infos),
	})
}

// KillProcess sends a signal to a process.
// POST /system/processes/:pid/kill  body: { signal: "TERM" | "KILL" | "9" | "15" }
func (h *ProcessesHandler) KillProcess(c echo.Context) error {
	pidStr := c.Param("pid")
	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPID, "Invalid PID")
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

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Signal %s sent to process %d", strings.ToUpper(req.Signal), pid),
		"pid":     pid,
		"signal":  strings.ToUpper(req.Signal),
	})
}

// collectProcesses gathers information about all running processes.
func collectProcesses() ([]ProcessInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	var infos []ProcessInfo
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
