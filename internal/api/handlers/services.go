package handlers

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

var validServiceName = regexp.MustCompile(`^[a-zA-Z0-9@._:-]+\.service$`)

type ServicesHandler struct{}

type ServiceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadState   string `json:"load_state"`
	ActiveState string `json:"active_state"`
	SubState    string `json:"sub_state"`
	Enabled     string `json:"enabled"`
}

// serviceCache caches the full service list to avoid spawning systemctl
// processes on every request. TTL is 3 seconds — enough to absorb rapid
// polling while still reflecting state changes promptly.
var serviceCache struct {
	sync.Mutex
	services []ServiceInfo
	fetched  time.Time
}

const serviceCacheTTL = 3 * time.Second

// ListServices returns all systemd services with optional search, filter, and sort.
// GET /system/services?q=search&sort=name|active|enabled&type=all|running|failed|inactive
func (h *ServicesHandler) ListServices(c echo.Context) error {
	all, err := getCachedServices()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrServiceError, "Failed to list services")
	}

	// Work on a copy so filters don't mutate the cache
	services := make([]ServiceInfo, len(all))
	copy(services, all)

	// Filter by type
	filterType := c.QueryParam("type")
	if filterType != "" && filterType != "all" {
		var filtered []ServiceInfo
		for _, s := range services {
			switch filterType {
			case "running":
				if s.ActiveState == "active" && s.SubState == "running" {
					filtered = append(filtered, s)
				}
			case "failed":
				if s.ActiveState == "failed" {
					filtered = append(filtered, s)
				}
			case "inactive":
				if s.ActiveState == "inactive" {
					filtered = append(filtered, s)
				}
			}
		}
		services = filtered
	}

	// Filter by search query
	query := strings.ToLower(strings.TrimSpace(c.QueryParam("q")))
	if query != "" {
		var filtered []ServiceInfo
		for _, s := range services {
			if strings.Contains(strings.ToLower(s.Name), query) ||
				strings.Contains(strings.ToLower(s.Description), query) {
				filtered = append(filtered, s)
			}
		}
		services = filtered
	}

	// Sort
	sortBy := c.QueryParam("sort")
	switch sortBy {
	case "active":
		sort.Slice(services, func(i, j int) bool {
			return services[i].ActiveState < services[j].ActiveState
		})
	case "enabled":
		sort.Slice(services, func(i, j int) bool {
			return services[i].Enabled < services[j].Enabled
		})
	default: // name
		sort.Slice(services, func(i, j int) bool {
			return strings.ToLower(services[i].Name) < strings.ToLower(services[j].Name)
		})
	}

	return response.OK(c, map[string]interface{}{
		"services": services,
		"total":    len(services),
	})
}

// StartService starts a systemd service.
// POST /system/services/:name/start
func (h *ServicesHandler) StartService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if err := exec.Command("systemctl", "start", name).Run(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrStartFailed,
			fmt.Sprintf("Failed to start %s: %s", name, err.Error()))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s started", name)})
}

// StopService stops a systemd service.
// POST /system/services/:name/stop
func (h *ServicesHandler) StopService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if err := exec.Command("systemctl", "stop", name).Run(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrStopFailed,
			fmt.Sprintf("Failed to stop %s: %s", name, err.Error()))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s stopped", name)})
}

// RestartService restarts a systemd service.
// POST /system/services/:name/restart
func (h *ServicesHandler) RestartService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if err := exec.Command("systemctl", "restart", name).Run(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestartFailed,
			fmt.Sprintf("Failed to restart %s: %s", name, err.Error()))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s restarted", name)})
}

// EnableService enables a systemd service to start at boot.
// POST /system/services/:name/enable
func (h *ServicesHandler) EnableService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if err := exec.Command("systemctl", "enable", name).Run(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrEnableFailed,
			fmt.Sprintf("Failed to enable %s: %s", name, err.Error()))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s enabled", name)})
}

// DisableService disables a systemd service from starting at boot.
// POST /system/services/:name/disable
func (h *ServicesHandler) DisableService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	if err := exec.Command("systemctl", "disable", name).Run(); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDisableFailed,
			fmt.Sprintf("Failed to disable %s: %s", name, err.Error()))
	}

	invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s disabled", name)})
}

// ServiceLogs returns journalctl logs for a service.
// GET /system/services/:name/logs?lines=100
func (h *ServicesHandler) ServiceLogs(c echo.Context) error {
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

	out, err := exec.Command("journalctl", "-u", name, "--no-pager", "-n", strconv.Itoa(lines), "--output=short-iso").Output()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLogError,
			fmt.Sprintf("Failed to read logs for %s", name))
	}

	return response.OK(c, map[string]string{"logs": string(out)})
}

// getCachedServices returns the cached service list, refreshing if stale.
func getCachedServices() ([]ServiceInfo, error) {
	serviceCache.Lock()
	defer serviceCache.Unlock()

	if time.Since(serviceCache.fetched) < serviceCacheTTL && serviceCache.services != nil {
		return serviceCache.services, nil
	}

	services, err := fetchAllServices()
	if err != nil {
		return nil, err
	}

	serviceCache.services = services
	serviceCache.fetched = time.Now()
	return services, nil
}

// invalidateServiceCache forces the next list request to re-fetch.
func invalidateServiceCache() {
	serviceCache.Lock()
	serviceCache.fetched = time.Time{}
	serviceCache.Unlock()
}

// fetchAllServices runs systemctl commands and parses the output.
func fetchAllServices() ([]ServiceInfo, error) {
	out, err := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--plain", "--no-legend").Output()
	if err != nil {
		return nil, err
	}

	enabledMap := getEnabledStates()

	var services []ServiceInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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

		services = append(services, ServiceInfo{
			Name:        name,
			Description: description,
			LoadState:   loadState,
			ActiveState: activeState,
			SubState:    subState,
			Enabled:     enabled,
		})
	}

	return services, nil
}

// getEnabledStates returns a map of service name -> enabled state.
func getEnabledStates() map[string]string {
	out, err := exec.Command("systemctl", "list-unit-files", "--type=service", "--no-pager", "--no-legend").Output()
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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
