package services

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

var validServiceName = regexp.MustCompile(`^[a-zA-Z0-9@._:-]+\.service$`)

type Handler struct {
	Cmd exec.Commander
}

type ServiceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
	Enabled     string `json:"enabled"`
}

type ServiceDeps struct {
	Requires   []string `json:"requires,omitempty"`
	RequiredBy []string `json:"required_by,omitempty"`
	WantedBy   []string `json:"wanted_by,omitempty"`
}

var serviceCache struct {
	sync.RWMutex
	services []ServiceInfo
	fetched  time.Time
}

const serviceCacheTTL = 3 * time.Second

// ListServices returns all systemd services.
// GET /system/services
func (h *Handler) ListServices(c echo.Context) error {
	services, err := getCachedServices(h.Cmd)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrServiceError, "Failed to list services")
	}

	return response.OK(c, map[string]interface{}{
		"services": services,
		"total":    len(services),
	})
}

// StartService starts a systemd service.
// POST /system/services/:name/start
func (h *Handler) StartService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if out, err := h.Cmd.Run("systemctl", "start", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrStartFailed,
			fmt.Sprintf("Failed to start %s: %s", name, strings.TrimSpace(out)))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s started", name)})
}

// StopService stops a systemd service.
// POST /system/services/:name/stop
func (h *Handler) StopService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if out, err := h.Cmd.Run("systemctl", "stop", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrStopFailed,
			fmt.Sprintf("Failed to stop %s: %s", name, strings.TrimSpace(out)))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s stopped", name)})
}

// RestartService restarts a systemd service.
// POST /system/services/:name/restart
func (h *Handler) RestartService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if out, err := h.Cmd.Run("systemctl", "restart", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestartFailed,
			fmt.Sprintf("Failed to restart %s: %s", name, strings.TrimSpace(out)))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s restarted", name)})
}

// EnableService enables a systemd service to start at boot.
// POST /system/services/:name/enable
func (h *Handler) EnableService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if out, err := h.Cmd.Run("systemctl", "enable", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrEnableFailed,
			fmt.Sprintf("Failed to enable %s: %s", name, strings.TrimSpace(out)))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s enabled", name)})
}

// DisableService disables a systemd service from starting at boot.
// POST /system/services/:name/disable
func (h *Handler) DisableService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if out, err := h.Cmd.Run("systemctl", "disable", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDisableFailed,
			fmt.Sprintf("Failed to disable %s: %s", name, strings.TrimSpace(out)))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s disabled", name)})
}

// ServiceLogs returns journalctl logs for a service.
// GET /system/services/:name/logs?lines=100
func (h *Handler) ServiceLogs(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	lines := 100
	if l := c.QueryParam("lines"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			lines = n
		}
	}

	out, err := h.Cmd.Run("journalctl", "-u", name, "--no-pager", "-n", strconv.Itoa(lines), "--output=short-iso")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLogError,
			fmt.Sprintf("Failed to read logs for %s", name))
	}

	return response.OK(c, map[string]string{"logs": out})
}

// GetServiceDeps returns dependency information for a systemd service.
// GET /system/services/:name/deps
func (h *Handler) GetServiceDeps(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	out, err := h.Cmd.Run("systemctl", "show", name, "--property=Requires,RequiredBy,WantedBy")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrServiceError,
			fmt.Sprintf("Failed to get dependencies for %s", name))
	}

	deps := ServiceDeps{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if val == "" {
			continue
		}
		items := filterDeps(strings.Fields(val))
		if len(items) == 0 {
			continue
		}
		switch key {
		case "Requires":
			deps.Requires = items
		case "RequiredBy":
			deps.RequiredBy = items
		case "WantedBy":
			deps.WantedBy = items
		}
	}

	return response.OK(c, deps)
}

func filterDeps(items []string) []string {
	noise := map[string]bool{
		"-.mount":      true,
		"init.scope":   true,
		"system.slice": true,
	}
	var result []string
	for _, item := range items {
		if item != "" && !noise[item] {
			result = append(result, item)
		}
	}
	return result
}

func getCachedServices(cmd exec.Commander) ([]ServiceInfo, error) {
	serviceCache.RLock()
	if time.Since(serviceCache.fetched) < serviceCacheTTL && serviceCache.services != nil {
		result := make([]ServiceInfo, len(serviceCache.services))
		copy(result, serviceCache.services)
		serviceCache.RUnlock()
		return result, nil
	}
	serviceCache.RUnlock()

	serviceCache.Lock()
	defer serviceCache.Unlock()

	if time.Since(serviceCache.fetched) < serviceCacheTTL && serviceCache.services != nil {
		result := make([]ServiceInfo, len(serviceCache.services))
		copy(result, serviceCache.services)
		return result, nil
	}

	svcs, err := fetchAllServices(cmd)
	if err != nil {
		return nil, err
	}

	serviceCache.services = svcs
	serviceCache.fetched = time.Now()

	result := make([]ServiceInfo, len(svcs))
	copy(result, svcs)
	return result, nil
}

func invalidateServiceCache() {
	serviceCache.Lock()
	serviceCache.fetched = time.Time{}
	serviceCache.Unlock()
}

func fetchAllServices(cmd exec.Commander) ([]ServiceInfo, error) {
	out, err := cmd.Run("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--plain", "--no-legend")
	if err != nil {
		return nil, err
	}

	enabledMap := getEnabledStates(cmd)

	svcs := make([]ServiceInfo, 0)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		name := fields[0]
		loadState := fields[1]
		activeState := fields[2]
		subState := fields[3]
		description := ""
		if len(fields) > 4 {
			description = strings.Join(fields[4:], " ")
		}

		enabled := enabledMap[name]
		if enabled == "" {
			enabled = "unknown"
		}

		svcs = append(svcs, ServiceInfo{
			Name:        name,
			Description: description,
			LoadState:   loadState,
			ActiveState: activeState,
			SubState:    subState,
			Enabled:     enabled,
		})
	}

	return svcs, nil
}

func getEnabledStates(cmd exec.Commander) map[string]string {
	out, err := cmd.Run("systemctl", "list-unit-files", "--type=service", "--no-pager", "--no-legend")
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			result[fields[0]] = fields[1]
		}
	}
	return result
}
