package firewall

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ---------- Fail2ban Handlers ----------

// GetFail2banStatus checks if fail2ban is installed and running.
// GET /fail2ban/status
func (h *Handler) GetFail2banStatus(c echo.Context) error {
	status := Fail2banStatus{}

	// Check if fail2ban-client exists
	if !h.Cmd.Exists("fail2ban-client") {
		return response.OK(c, status)
	}
	status.Installed = true

	// Get version
	versionOutput, err := h.Cmd.Run("fail2ban-client", "version")
	if err == nil {
		status.Version = strings.TrimSpace(versionOutput)
	}

	// Check if running via ping/pong
	pingOutput, err := h.Cmd.Run("fail2ban-client", "ping")
	if err == nil && strings.Contains(pingOutput, "pong") {
		status.Running = true
	}

	return response.OK(c, status)
}

// InstallFail2ban installs fail2ban via apt and enables the service.
// POST /fail2ban/install
func (h *Handler) InstallFail2ban(c echo.Context) error {
	// Step 1: apt-get update
	_, err := h.Cmd.RunWithEnv(aptEnv(), "apt-get", "update")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAPTUpdateError,
			"Failed to update package lists: "+err.Error())
	}

	// Step 2: install fail2ban
	output, err := h.Cmd.RunWithEnv(aptEnv(), "apt-get", "install", "-y", "fail2ban")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_INSTALL_ERROR",
			"Failed to install fail2ban: "+err.Error())
	}

	// Step 3: enable and start fail2ban
	_, _ = h.Cmd.Run("systemctl", "enable", "fail2ban")
	startOutput, err := h.Cmd.Run("systemctl", "start", "fail2ban")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_START_ERROR",
			"Fail2ban installed but failed to start: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message":        "Fail2ban installed and started successfully",
		"install_output": strings.TrimSpace(output),
		"start_output":   strings.TrimSpace(startOutput),
	})
}

// ListJails returns all fail2ban jails with their status.
// GET /fail2ban/jails
func (h *Handler) ListJails(c echo.Context) error {
	output, err := h.Cmd.Run("fail2ban-client", "status")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFail2banError,
			"Failed to get fail2ban status: "+err.Error())
	}

	jailNames := parseFail2banJailList(output)
	jails := make([]Fail2banJail, 0, len(jailNames))

	for _, name := range jailNames {
		jail := h.getJailInfo(name)
		jails = append(jails, jail)
	}

	return response.OK(c, map[string]interface{}{
		"jails": jails,
		"total": len(jails),
	})
}

// parseFail2banJailList parses the output of `fail2ban-client status` to extract jail names.
// Example output:
//
//	Status
//	|- Number of jail:	2
//	`- Jail list:	sshd, apache-auth
func parseFail2banJailList(output string) []string {
	var jails []string

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Jail list:") {
			// Extract everything after "Jail list:"
			parts := strings.SplitN(line, "Jail list:", 2)
			if len(parts) < 2 {
				continue
			}
			// Remove tree drawing characters
			list := strings.TrimSpace(parts[1])
			if list == "" {
				continue
			}
			// Split by comma and trim each jail name
			for _, name := range strings.Split(list, ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					jails = append(jails, name)
				}
			}
		}
	}

	return jails
}

// getJailInfo retrieves detailed information for a single fail2ban jail.
func (h *Handler) getJailInfo(name string) Fail2banJail {
	jail := Fail2banJail{
		Name:      name,
		Enabled:   true, // If it's in the jail list, it's enabled
		BannedIPs: []string{},
	}

	output, err := h.Cmd.Run("fail2ban-client", "status", name)
	if err != nil {
		return jail
	}

	jail = h.parseFail2banJailStatus(output, jail)

	return jail
}

// parseFail2banJailStatus parses the output of `fail2ban-client status <jail>`.
// Example output:
//
//	Status for the jail: sshd
//	|- Filter
//	|  |- Currently failed:	3
//	|  |- Total failed:	15
//	|  `- File list:	/var/log/auth.log
//	`- Actions
//	   |- Currently banned:	1
//	   |- Total banned:	5
//	   `- Banned IP list:	192.168.1.100
func (h *Handler) parseFail2banJailStatus(output string, jail Fail2banJail) Fail2banJail {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Remove tree drawing characters
		line = strings.TrimLeft(line, "|- `")
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Currently banned:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Currently banned:"))
			jail.BannedCount, _ = strconv.Atoi(val)
		} else if strings.HasPrefix(line, "Total banned:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Total banned:"))
			jail.TotalBanned, _ = strconv.Atoi(val)
		} else if strings.HasPrefix(line, "Banned IP list:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Banned IP list:"))
			if val != "" {
				for _, ip := range strings.Fields(val) {
					ip = strings.TrimSpace(ip)
					if ip != "" {
						jail.BannedIPs = append(jail.BannedIPs, ip)
					}
				}
			}
		} else if strings.HasPrefix(line, "File list:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "File list:"))
			jail.Filter = val
		}
	}

	// Get jail configuration for maxretry, bantime, findtime
	getConfVal := func(jail string, key string) string {
		out, err := h.Cmd.Run("fail2ban-client", "get", jail, key)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}

	if val := getConfVal(jail.Name, "maxretry"); val != "" {
		jail.MaxRetry, _ = strconv.Atoi(val)
	}
	jail.BanTime = getConfVal(jail.Name, "bantime")
	jail.FindTime = getConfVal(jail.Name, "findtime")
	jail.IgnoreIP = getConfVal(jail.Name, "ignoreip")

	return jail
}

// GetJailDetail returns detailed information for a specific fail2ban jail.
// GET /fail2ban/jails/:name
func (h *Handler) GetJailDetail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingJailName, "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	output, err := h.Cmd.Run("fail2ban-client", "status", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_JAIL_ERROR",
			"Failed to get jail status: "+err.Error())
	}

	jail := Fail2banJail{
		Name:      name,
		Enabled:   true,
		BannedIPs: []string{},
	}
	jail = h.parseFail2banJailStatus(output, jail)

	return response.OK(c, jail)
}

// EnableJail starts a fail2ban jail.
// POST /fail2ban/jails/:name/enable
func (h *Handler) EnableJail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingJailName, "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	output, err := h.Cmd.Run("fail2ban-client", "start", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_ENABLE_ERROR",
			"Failed to enable jail: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Jail %s enabled successfully", name),
		"output":  strings.TrimSpace(output),
	})
}

// DisableJail stops a fail2ban jail.
// POST /fail2ban/jails/:name/disable
func (h *Handler) DisableJail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingJailName, "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	output, err := h.Cmd.Run("fail2ban-client", "stop", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_DISABLE_ERROR",
			"Failed to disable jail: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Jail %s disabled successfully", name),
		"output":  strings.TrimSpace(output),
	})
}

// UpdateJailConfig updates the configuration (maxretry, bantime, findtime) for a fail2ban jail.
// PUT /fail2ban/jails/:name/config
// JSON body: { "max_retry": 5, "ban_time": "600", "find_time": "600" }
func (h *Handler) UpdateJailConfig(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingJailName, "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	var req struct {
		MaxRetry *int    `json:"max_retry"`
		BanTime  *string `json:"ban_time"`
		FindTime *string `json:"find_time"`
		IgnoreIP *string `json:"ignoreip"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// Validate and apply maxretry
	if req.MaxRetry != nil {
		if *req.MaxRetry < 1 || *req.MaxRetry > 100 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidMaxRetry,
				"max_retry must be between 1 and 100")
		}
		_, err := h.Cmd.Run("fail2ban-client", "set", name, "maxretry", strconv.Itoa(*req.MaxRetry))
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_CONFIG_ERROR",
				"Failed to set maxretry: "+err.Error())
		}
	}

	// Validate and apply bantime
	if req.BanTime != nil {
		if !validBanTime.MatchString(*req.BanTime) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBanTime,
				"ban_time must be a number (seconds) or a time expression like 10m, 1h, 1d")
		}
		_, err := h.Cmd.Run("fail2ban-client", "set", name, "bantime", *req.BanTime)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_CONFIG_ERROR",
				"Failed to set bantime: "+err.Error())
		}
	}

	// Validate and apply findtime
	if req.FindTime != nil {
		if !validBanTime.MatchString(*req.FindTime) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFindTime,
				"find_time must be a number (seconds) or a time expression like 10m, 1h, 1d")
		}
		_, err := h.Cmd.Run("fail2ban-client", "set", name, "findtime", *req.FindTime)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_CONFIG_ERROR",
				"Failed to set findtime: "+err.Error())
		}
	}

	// Validate and apply ignoreip
	if req.IgnoreIP != nil {
		if *req.IgnoreIP != "" && !validIgnoreIP.MatchString(*req.IgnoreIP) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidIP,
				"ignoreip contains invalid characters (allowed: IPs, CIDRs, space-separated)")
		}
		// First, remove all existing ignoreip entries
		existingOutput, _ := h.Cmd.Run("fail2ban-client", "get", name, "ignoreip")
		existingIgnoreIP := strings.TrimSpace(existingOutput)
		if existingIgnoreIP != "" {
			for _, ip := range strings.Fields(existingIgnoreIP) {
				ip = strings.TrimSpace(ip)
				if ip != "" {
					_, _ = h.Cmd.Run("fail2ban-client", "set", name, "delignoreip", ip)
				}
			}
		}
		// Then add the new IPs
		if *req.IgnoreIP != "" {
			for _, ip := range strings.Fields(*req.IgnoreIP) {
				ip = strings.TrimSpace(ip)
				if ip != "" {
					_, err := h.Cmd.Run("fail2ban-client", "set", name, "addignoreip", ip)
					if err != nil {
						return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_CONFIG_ERROR",
							"Failed to set ignoreip: "+err.Error())
					}
				}
			}
		}
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Jail %s configuration updated", name),
	})
}

// UnbanIP removes a banned IP from a specific fail2ban jail.
// POST /fail2ban/jails/:name/unban
// JSON body: { "ip": "192.168.1.100" }
func (h *Handler) UnbanIP(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingJailName, "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	var req struct {
		IP string `json:"ip"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.IP == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "IP address is required")
	}
	if !validIP.MatchString(req.IP) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidIP,
			"IP address must be a valid IPv4 or IPv6 address")
	}

	output, err := h.Cmd.Run("fail2ban-client", "set", name, "unbanip", req.IP)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_UNBAN_ERROR",
			"Failed to unban IP: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("IP %s unbanned from jail %s", req.IP, name),
		"output":  strings.TrimSpace(output),
	})
}

// ---------- Jail Templates ----------

// JailTemplate represents a pre-configured jail template.
type JailTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Filter      string `json:"filter"`
	LogPath     string `json:"log_path"`
	MaxRetry    int    `json:"max_retry"`
	BanTime     int    `json:"ban_time"`
	FindTime    int    `json:"find_time"`
	Available   bool   `json:"available"`
}

// jailTemplates defines the built-in jail templates.
var jailTemplates = []JailTemplate{
	{
		ID: "sshd", Name: "SSH (sshd)", Description: "SSH brute-force protection",
		Filter: "sshd", LogPath: "/var/log/auth.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "nginx-http-auth", Name: "Nginx HTTP Auth", Description: "Nginx basic auth failure protection",
		Filter: "nginx-http-auth", LogPath: "/var/log/nginx/error.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "nginx-botsearch", Name: "Nginx Bot Search", Description: "Block bots scanning for vulnerable URLs",
		Filter: "nginx-botsearch", LogPath: "/var/log/nginx/access.log",
		MaxRetry: 10, BanTime: 600, FindTime: 600,
	},
	{
		ID: "nginx-limit-req", Name: "Nginx Rate Limit", Description: "Ban IPs exceeding Nginx rate limits",
		Filter: "nginx-limit-req", LogPath: "/var/log/nginx/error.log",
		MaxRetry: 10, BanTime: 600, FindTime: 600,
	},
	{
		ID: "apache-auth", Name: "Apache Auth", Description: "Apache basic auth failure protection",
		Filter: "apache-auth", LogPath: "/var/log/apache2/*error.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "postfix", Name: "Postfix SMTP", Description: "Postfix SMTP auth failure protection",
		Filter: "postfix", LogPath: "/var/log/mail.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "dovecot", Name: "Dovecot IMAP/POP3", Description: "Dovecot mail auth failure protection",
		Filter: "dovecot", LogPath: "/var/log/mail.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "grafana", Name: "Grafana", Description: "Grafana login failure protection",
		Filter: "grafana", LogPath: "/var/log/grafana/grafana.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "traefik-auth", Name: "Traefik Auth", Description: "Traefik auth failure protection",
		Filter: "traefik-auth", LogPath: "/var/log/traefik/access.log",
		MaxRetry: 5, BanTime: 600, FindTime: 600,
	},
	{
		ID: "recidive", Name: "Recidive", Description: "Long-term ban for repeat offenders across all jails",
		Filter: "recidive", LogPath: "/var/log/fail2ban.log",
		MaxRetry: 5, BanTime: 604800, FindTime: 86400,
	},
}

// GetJailTemplates returns available jail templates with availability status.
// GET /fail2ban/templates
func (h *Handler) GetJailTemplates(c echo.Context) error {
	// Get currently active jails
	output, _ := h.Cmd.Run("fail2ban-client", "status")
	activeJails := make(map[string]bool)
	for _, name := range parseFail2banJailList(output) {
		activeJails[name] = true
	}

	templates := make([]JailTemplate, len(jailTemplates))
	for i, tmpl := range jailTemplates {
		templates[i] = tmpl
		// Check if filter file exists
		filterPath := fmt.Sprintf("/etc/fail2ban/filter.d/%s.conf", tmpl.Filter)
		if h.Cmd.Exists("test") {
			_, err := h.Cmd.Run("test", "-f", filterPath)
			templates[i].Available = err == nil
		}
		// Mark as unavailable if already active
		if activeJails[tmpl.ID] {
			templates[i].Available = false
		}
	}

	return response.OK(c, map[string]interface{}{
		"templates": templates,
	})
}

// CreateJailRequest is the request body for creating a new jail.
type CreateJailRequest struct {
	ID       string `json:"id"`
	MaxRetry int    `json:"max_retry"`
	BanTime  int    `json:"ban_time"`
	FindTime int    `json:"find_time"`
	LogPath  string `json:"log_path"`
	IgnoreIP string `json:"ignoreip"`
	// Custom jail fields (used when id == "custom")
	Name   string `json:"name,omitempty"`
	Filter string `json:"filter,omitempty"`
}

// validLogPath matches safe file paths for log files (no path traversal).
var validLogPath = regexp.MustCompile(`^[a-zA-Z0-9/_\-.*]+\.log[a-zA-Z0-9/_\-.*]*$`)

// validIgnoreIP matches space-separated IPs, CIDRs, and common fail2ban ignoreip values.
var validIgnoreIP = regexp.MustCompile(`^[a-fA-F0-9.:/ ]+$`)

// CreateJail creates a new fail2ban jail from a template or custom config.
// POST /fail2ban/jails
func (h *Handler) CreateJail(c echo.Context) error {
	var req CreateJailRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.ID == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Template ID is required")
	}

	var jailName, filterName, logPath string
	var maxRetry, banTime, findTime int

	if req.ID == "custom" {
		// Custom jail creation
		if req.Name == "" || req.Filter == "" || req.LogPath == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields,
				"Custom jail requires name, filter, and log_path")
		}
		if !validJailName.MatchString(req.Name) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
				"Jail name contains invalid characters (use letters, numbers, hyphens, underscores)")
		}
		if !validJailName.MatchString(req.Filter) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFilterName,
				"Filter name contains invalid characters")
		}
		if !validLogPath.MatchString(req.LogPath) || strings.Contains(req.LogPath, "..") {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidLogPath,
				"Log path contains invalid characters")
		}
		jailName = req.Name
		filterName = req.Filter
		logPath = req.LogPath
		maxRetry = 5
		banTime = 600
		findTime = 600
		if req.MaxRetry > 0 && req.MaxRetry <= 100 {
			maxRetry = req.MaxRetry
		}
		if req.BanTime != 0 {
			banTime = req.BanTime
		}
		if req.FindTime > 0 {
			findTime = req.FindTime
		}
	} else {
		// Template-based jail creation
		if !validJailName.MatchString(req.ID) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
				"Jail name contains invalid characters")
		}

		var tmpl *JailTemplate
		for _, t := range jailTemplates {
			if t.ID == req.ID {
				tmpl = &t
				break
			}
		}
		if tmpl == nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrUnknownTemplate,
				"Unknown jail template: "+req.ID)
		}

		jailName = req.ID
		filterName = tmpl.Filter
		logPath = tmpl.LogPath
		maxRetry = tmpl.MaxRetry
		banTime = tmpl.BanTime
		findTime = tmpl.FindTime

		if req.MaxRetry > 0 && req.MaxRetry <= 100 {
			maxRetry = req.MaxRetry
		}
		if req.BanTime != 0 {
			banTime = req.BanTime
		}
		if req.FindTime > 0 {
			findTime = req.FindTime
		}
		if req.LogPath != "" {
			if !validLogPath.MatchString(req.LogPath) {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidLogPath,
					"Log path contains invalid characters")
			}
			logPath = req.LogPath
		}
	}

	// Check if jail already exists
	existingOutput, _ := h.Cmd.Run("fail2ban-client", "status")
	for _, name := range parseFail2banJailList(existingOutput) {
		if name == jailName {
			return response.Fail(c, http.StatusConflict, response.ErrJailExists,
				fmt.Sprintf("Jail %s already exists", jailName))
		}
	}

	// Write jail config file
	configContent := fmt.Sprintf(`[%s]
enabled = true
filter = %s
logpath = %s
maxretry = %d
bantime = %d
findtime = %d
`, jailName, filterName, logPath, maxRetry, banTime, findTime)

	// Add ignoreip if provided
	if req.IgnoreIP != "" {
		if !validIgnoreIP.MatchString(req.IgnoreIP) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidIP,
				"ignoreip contains invalid characters (allowed: IPs, CIDRs, space-separated)")
		}
		configContent += fmt.Sprintf("ignoreip = %s\n", req.IgnoreIP)
	}

	configPath := fmt.Sprintf("/etc/fail2ban/jail.d/%s.local", jailName)
	if err := writeFile(configPath, configContent); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileWriteError,
			"Failed to write jail config: "+err.Error())
	}

	// Reload fail2ban
	_, err := h.Cmd.Run("fail2ban-client", "reload")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_RELOAD_ERROR",
			"Failed to reload fail2ban: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Jail %s created successfully", jailName),
	})
}

// DeleteJail removes a fail2ban jail by deleting its config file and reloading.
// DELETE /fail2ban/jails/:name
func (h *Handler) DeleteJail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingJailName, "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidJailName,
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	// Check if this jail has a .local file we can delete
	configPath := fmt.Sprintf("/etc/fail2ban/jail.d/%s.local", name)
	if _, err := h.Cmd.Run("test", "-f", configPath); err != nil {
		// Also check defaults-debian.conf — cannot delete system jails directly
		// Instead, create a .local file that disables it
		configContent := fmt.Sprintf(`[%s]
enabled = false
`, name)
		if writeErr := writeFile(configPath, configContent); writeErr != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrFileWriteError,
				"Failed to disable jail: "+writeErr.Error())
		}
	} else {
		// Remove the .local file
		if _, err := h.Cmd.Run("rm", configPath); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrFileDeleteError,
				"Failed to remove jail config: "+err.Error())
		}
	}

	// Reload fail2ban
	_, err := h.Cmd.Run("fail2ban-client", "reload")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_RELOAD_ERROR",
			"Failed to reload fail2ban: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Jail %s removed successfully", name),
	})
}

// writeFile writes content to a file path with 0644 permissions.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
