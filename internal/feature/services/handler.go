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
	"github.com/svrforum/SFPanel/internal/common/sysguard"
)

var validServiceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9@._:-]*\.service$`)

// isProtectedServiceUnit delegates to sysguard so the deny-list lives in
// one place. The local function name is kept so existing call sites stay
// untouched; new modules should call sysguard.IsProtectedSystemdUnit
// directly. See internal/common/sysguard/sysguard.go for the canonical
// list (currently sfpanel/dbus/systemd-journald).
func isProtectedServiceUnit(name string) bool {
	return sysguard.IsProtectedSystemdUnit(name)
}

// refuseProtectedUnit returns a 403 response if the given unit is protected
// from the given operation. Returns nil if the operation may proceed.
func refuseProtectedUnit(c echo.Context, name, op string) error {
	if isProtectedServiceUnit(name) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied,
			fmt.Sprintf("Refusing to %s protected unit %q via the panel API", op, name))
	}
	return nil
}

type Handler struct {
	Cmd exec.Commander
	// cache holds the per-Handler service list. Per-Handler (not package
	// global) so parallel tests and two router instances don't share state —
	// mirrors the process module's cache placement.
	cache serviceCacheData
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

type serviceCacheData struct {
	sync.RWMutex
	services []ServiceInfo
	fetched  time.Time
}

const serviceCacheTTL = 3 * time.Second

// ListServices returns all systemd services.
// GET /system/services
//
// Returns an empty list (not 500) when systemctl is missing — common on
// minimal container hosts and on cluster follower nodes the operator
// reaches via ?node=. The frontend renders "no services found" rather
// than a stack trace.
func (h *Handler) ListServices(c echo.Context) error {
	if !h.Cmd.Exists("systemctl") {
		return response.OK(c, map[string]interface{}{
			"services": []interface{}{},
			"total":    0,
		})
	}
	services, err := h.getCachedServices()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrServiceError, "Failed to list services")
	}

	// ?state=failed narrows the answer to units that need attention. The
	// dashboard polls this every thirty seconds for a number that is almost
	// always zero, and the unfiltered list is 229 units on an ordinary host —
	// paying for that payload to render "0" is the wrong trade.
	if c.QueryParam("state") == "failed" {
		services = failedOnly(services)
	}

	return response.OK(c, map[string]interface{}{
		"services": services,
		"total":    len(services),
	})
}

// failedOnly keeps the units systemd considers failed.
//
// Matched on the state fields only. Substring-matching the whole record would
// hand back every unit whose *description* happens to contain the word, which
// is a filter that looks like it works right up until it doesn't.
func failedOnly(services []ServiceInfo) []ServiceInfo {
	out := make([]ServiceInfo, 0, 4)
	for _, s := range services {
		if s.ActiveState == "failed" || s.SubState == "failed" {
			out = append(out, s)
		}
	}
	return out
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
			fmt.Sprintf("Failed to start %s: %s", name, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	h.invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s started", name)})
}

// StopService stops a systemd service.
// POST /system/services/:name/stop
func (h *Handler) StopService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}
	if err := refuseProtectedUnit(c, name, "stop"); err != nil {
		return err
	}

	if out, err := h.Cmd.Run("systemctl", "stop", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrStopFailed,
			fmt.Sprintf("Failed to stop %s: %s", name, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	h.invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s stopped", name)})
}

// RestartService restarts a systemd service.
// POST /system/services/:name/restart
func (h *Handler) RestartService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}
	if err := refuseProtectedUnit(c, name, "restart"); err != nil {
		return err
	}

	if out, err := h.Cmd.Run("systemctl", "restart", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRestartFailed,
			fmt.Sprintf("Failed to restart %s: %s", name, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	h.invalidateServiceCache()
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
			fmt.Sprintf("Failed to enable %s: %s", name, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	h.invalidateServiceCache()
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Service %s enabled", name)})
}

// DisableService disables a systemd service from starting at boot.
// POST /system/services/:name/disable
func (h *Handler) DisableService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}
	if err := refuseProtectedUnit(c, name, "disable"); err != nil {
		return err
	}

	if out, err := h.Cmd.Run("systemctl", "disable", name); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDisableFailed,
			fmt.Sprintf("Failed to disable %s: %s", name, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	h.invalidateServiceCache()
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

// GetServiceUnit returns the rendered unit file (`systemctl cat`) so operators
// can inspect ExecStart/Restart/Environment without shelling in. Read-only.
func (h *Handler) GetServiceUnit(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}

	out, err := h.Cmd.Run("systemctl", "cat", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrServiceError,
			fmt.Sprintf("Failed to read unit file for %s", name))
	}

	return response.OK(c, map[string]string{"unit": out})
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

func (h *Handler) getCachedServices() ([]ServiceInfo, error) {
	h.cache.RLock()
	if time.Since(h.cache.fetched) < serviceCacheTTL && h.cache.services != nil {
		result := make([]ServiceInfo, len(h.cache.services))
		copy(result, h.cache.services)
		h.cache.RUnlock()
		return result, nil
	}
	h.cache.RUnlock()

	// Cache miss — fetch WITHOUT holding the lock. fetchAllServices runs two
	// systemctl execs; holding the write lock across them would serialize every
	// concurrent list caller behind that multi-hundred-ms pair. Concurrent
	// misses may each fetch (bounded by the 3s TTL); we lock only to publish.
	svcs, err := fetchAllServices(h.Cmd)
	if err != nil {
		return nil, err
	}

	h.cache.Lock()
	h.cache.services = svcs
	h.cache.fetched = time.Now()
	h.cache.Unlock()

	result := make([]ServiceInfo, len(svcs))
	copy(result, svcs)
	return result, nil
}

func (h *Handler) invalidateServiceCache() {
	h.cache.Lock()
	h.cache.fetched = time.Time{}
	h.cache.Unlock()
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
