package handlers

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// FirewallHandler exposes REST handlers for UFW firewall and Fail2ban management.
type FirewallHandler struct{}

// ---------- Types ----------

// UFWStatus represents the current state of UFW.
type UFWStatus struct {
	Active        bool   `json:"active"`
	DefaultIncome string `json:"default_income"`
	DefaultOut    string `json:"default_out"`
}

// UFWRule represents a single UFW firewall rule.
type UFWRule struct {
	Number  int    `json:"number"`
	To      string `json:"to"`
	Action  string `json:"action"`
	From    string `json:"from"`
	Comment string `json:"comment,omitempty"`
	V6      bool   `json:"v6"`
}

// AddRuleRequest is the request body for adding a new UFW rule.
type AddRuleRequest struct {
	Action   string `json:"action"`
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
	From     string `json:"from"`
	To       string `json:"to"`
	Comment  string `json:"comment"`
}

// ListeningPort represents a listening network port from ss output.
type ListeningPort struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	PID      int    `json:"pid"`
	Process  string `json:"process"`
}

// Fail2banStatus represents the installation and running state of fail2ban.
type Fail2banStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
}

// Fail2banJail represents a fail2ban jail with its configuration and state.
type Fail2banJail struct {
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Filter       string   `json:"filter"`
	BannedCount  int      `json:"banned_count"`
	TotalBanned  int      `json:"total_banned"`
	MaxRetry     int      `json:"max_retry"`
	BanTime      string   `json:"ban_time"`
	FindTime     string   `json:"find_time"`
	BannedIPs    []string `json:"banned_ips"`
}

// ---------- Validation ----------

// validPort matches port numbers, ranges (e.g., "8000:8080"), and service names.
var validPort = regexp.MustCompile(`^[a-zA-Z0-9_\-]+(:[a-zA-Z0-9_\-]+)?$`)

// validIP matches IPv4, IPv6, and CIDR notation addresses.
var validIP = regexp.MustCompile(`^[a-fA-F0-9.:\/]+$`)

// validProtocol matches allowed protocol values.
var validProtocol = regexp.MustCompile(`^(tcp|udp|any)$`)

// validAction matches allowed UFW rule actions.
var validAction = regexp.MustCompile(`^(allow|deny|reject|limit)$`)

// validJailName matches safe fail2ban jail names.
var validJailName = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// validComment matches safe comment text (alphanumeric, spaces, basic punctuation).
var validComment = regexp.MustCompile(`^[a-zA-Z0-9 _\-.,()/#:]+$`)

// ---------- UFW Handlers ----------

// GetUFWStatus returns the current UFW status including active state and default policies.
// GET /firewall/status
func (h *FirewallHandler) GetUFWStatus(c echo.Context) error {
	output, err := runCommand("ufw", "status", "verbose")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UFW_ERROR",
			"Failed to get UFW status: "+err.Error())
	}

	status := parseUFWStatus(output)

	return response.OK(c, status)
}

// parseUFWStatus parses the output of `ufw status verbose`.
// Example output:
//
//	Status: active
//	Default: deny (incoming), allow (outgoing), disabled (routed)
func parseUFWStatus(output string) UFWStatus {
	status := UFWStatus{
		DefaultIncome: "deny",
		DefaultOut:    "allow",
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Status:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
			status.Active = val == "active"
		}

		if strings.HasPrefix(line, "Default:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Default:"))
			// Parse "deny (incoming), allow (outgoing), disabled (routed)"
			parts := strings.Split(val, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.Contains(part, "(incoming)") {
					status.DefaultIncome = strings.TrimSpace(strings.Split(part, "(")[0])
				} else if strings.Contains(part, "(outgoing)") {
					status.DefaultOut = strings.TrimSpace(strings.Split(part, "(")[0])
				}
			}
		}
	}

	return status
}

// EnableUFW enables the UFW firewall.
// POST /firewall/enable
func (h *FirewallHandler) EnableUFW(c echo.Context) error {
	output, err := runCommand("ufw", "--force", "enable")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UFW_ENABLE_ERROR",
			"Failed to enable UFW: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": "UFW enabled successfully",
		"output":  strings.TrimSpace(output),
	})
}

// DisableUFW disables the UFW firewall.
// POST /firewall/disable
func (h *FirewallHandler) DisableUFW(c echo.Context) error {
	output, err := runCommand("ufw", "disable")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UFW_DISABLE_ERROR",
			"Failed to disable UFW: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": "UFW disabled successfully",
		"output":  strings.TrimSpace(output),
	})
}

// ListRules returns all current UFW rules with their numbers.
// GET /firewall/rules
func (h *FirewallHandler) ListRules(c echo.Context) error {
	output, err := runCommand("ufw", "status", "numbered")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UFW_ERROR",
			"Failed to list UFW rules: "+err.Error())
	}

	rules := parseUFWRules(output)

	return response.OK(c, map[string]interface{}{
		"rules": rules,
		"total": len(rules),
	})
}

// parseUFWRules parses the output of `ufw status numbered`.
// Example output:
//
//	Status: active
//
//	     To                         Action      From
//	     --                         ------      ----
//	[ 1] 22/tcp                     ALLOW IN    Anywhere                   # SSH
//	[ 2] 80/tcp                     ALLOW IN    Anywhere
//	[ 3] 22/tcp (v6)                ALLOW IN    Anywhere (v6)              # SSH
func parseUFWRules(output string) []UFWRule {
	var rules []UFWRule

	// Match lines like: [ 1] 22/tcp   ALLOW IN   Anywhere   # SSH
	// The number is in brackets, followed by the rule details.
	ruleRe := regexp.MustCompile(`\[\s*(\d+)\]\s+(.+)`)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		matches := ruleRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		number, _ := strconv.Atoi(matches[1])
		rest := matches[2]

		rule := UFWRule{Number: number}

		// Extract comment (after #)
		if commentIdx := strings.LastIndex(rest, "#"); commentIdx >= 0 {
			rule.Comment = strings.TrimSpace(rest[commentIdx+1:])
			rest = strings.TrimSpace(rest[:commentIdx])
		}

		// Check for (v6) marker
		if strings.Contains(rest, "(v6)") {
			rule.V6 = true
		}

		// Parse: To   Action   From
		// Action keywords: ALLOW IN, DENY IN, REJECT IN, LIMIT IN, ALLOW OUT, etc.
		actionRe := regexp.MustCompile(`\s+(ALLOW|DENY|REJECT|LIMIT)\s+(IN|OUT|FWD)\s+`)
		actionMatches := actionRe.FindStringSubmatchIndex(rest)

		if actionMatches != nil {
			rule.To = strings.TrimSpace(rest[:actionMatches[0]])
			rule.Action = strings.TrimSpace(rest[actionMatches[2]:actionMatches[5]])
			rule.From = strings.TrimSpace(rest[actionMatches[1]:])
		} else {
			// Fallback: try simpler pattern without direction
			simpleActionRe := regexp.MustCompile(`\s+(ALLOW|DENY|REJECT|LIMIT)\s+`)
			simpleMatches := simpleActionRe.FindStringSubmatchIndex(rest)
			if simpleMatches != nil {
				rule.To = strings.TrimSpace(rest[:simpleMatches[0]])
				rule.Action = strings.TrimSpace(rest[simpleMatches[2]:simpleMatches[3]])
				rule.From = strings.TrimSpace(rest[simpleMatches[1]:])
			} else {
				// Cannot parse, store the whole line in To
				rule.To = rest
				rule.Action = "UNKNOWN"
				rule.From = ""
			}
		}

		// Clean up (v6) from To and From fields
		rule.To = strings.TrimSpace(strings.ReplaceAll(rule.To, "(v6)", ""))
		rule.From = strings.TrimSpace(strings.ReplaceAll(rule.From, "(v6)", ""))

		rules = append(rules, rule)
	}

	return rules
}

// AddRule adds a new UFW firewall rule.
// POST /firewall/rules
// JSON body: { "action": "allow", "port": "22", "protocol": "tcp", "from": "any", "to": "", "comment": "SSH" }
func (h *FirewallHandler) AddRule(c echo.Context) error {
	var req AddRuleRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	// Validate action
	if req.Action == "" {
		req.Action = "allow"
	}
	req.Action = strings.ToLower(req.Action)
	if !validAction.MatchString(req.Action) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_ACTION",
			"Action must be one of: allow, deny, reject, limit")
	}

	// Validate port (required)
	if req.Port == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "Port is required")
	}
	if !validPort.MatchString(req.Port) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PORT",
			"Port must be a number, range (e.g., 8000:8080), or service name")
	}

	// Validate protocol
	if req.Protocol == "" {
		req.Protocol = "any"
	}
	req.Protocol = strings.ToLower(req.Protocol)
	if !validProtocol.MatchString(req.Protocol) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PROTOCOL",
			"Protocol must be one of: tcp, udp, any")
	}

	// Validate from address if provided
	if req.From != "" && req.From != "any" {
		if !validIP.MatchString(req.From) {
			return response.Fail(c, http.StatusBadRequest, "INVALID_FROM_ADDRESS",
				"From address must be a valid IP or CIDR notation")
		}
	}

	// Validate to address if provided
	if req.To != "" && req.To != "any" {
		if !validIP.MatchString(req.To) {
			return response.Fail(c, http.StatusBadRequest, "INVALID_TO_ADDRESS",
				"To address must be a valid IP or CIDR notation")
		}
	}

	// Validate comment if provided
	if req.Comment != "" && !validComment.MatchString(req.Comment) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_COMMENT",
			"Comment contains invalid characters")
	}

	// Build the UFW command
	args := buildUFWAddArgs(req)

	output, err := runCommand("ufw", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UFW_ADD_RULE_ERROR",
			"Failed to add rule: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": "Rule added successfully",
		"output":  strings.TrimSpace(output),
	})
}

// buildUFWAddArgs constructs the argument list for a ufw add rule command.
func buildUFWAddArgs(req AddRuleRequest) []string {
	var args []string

	// ufw [action] [from <addr>] [to <addr>] [port <port>[/<proto>]] [comment <comment>]
	args = append(args, req.Action)

	// Add from clause
	if req.From != "" && req.From != "any" {
		args = append(args, "from", req.From)
	}

	// Add to clause
	if req.To != "" && req.To != "any" {
		args = append(args, "to", req.To)
	}

	// Add port with optional protocol
	if req.Protocol != "any" {
		args = append(args, "proto", req.Protocol)
	}

	// Determine if we need 'to any port' or just the port
	if req.From != "" && req.From != "any" || req.To != "" && req.To != "any" {
		args = append(args, "port", req.Port)
	} else if req.Protocol != "any" {
		args = append(args, "to", "any", "port", req.Port)
	} else {
		args = append(args, req.Port)
	}

	// Add comment if provided
	if req.Comment != "" {
		args = append(args, "comment", req.Comment)
	}

	return args
}

// DeleteRule deletes a UFW rule by its number.
// DELETE /firewall/rules/:number
func (h *FirewallHandler) DeleteRule(c echo.Context) error {
	numberStr := c.Param("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil || number < 1 {
		return response.Fail(c, http.StatusBadRequest, "INVALID_RULE_NUMBER",
			"Rule number must be a positive integer")
	}

	output, err := runCommand("ufw", "--force", "delete", strconv.Itoa(number))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UFW_DELETE_ERROR",
			"Failed to delete rule: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Rule %d deleted successfully", number),
		"output":  strings.TrimSpace(output),
	})
}

// ---------- Listening Ports ----------

// ListPorts returns all listening TCP and UDP ports on the system.
// GET /firewall/ports
func (h *FirewallHandler) ListPorts(c echo.Context) error {
	// Get TCP listening ports
	tcpOutput, err := runCommand("ss", "-tlnp")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "SS_ERROR",
			"Failed to list TCP ports: "+err.Error())
	}

	// Get UDP listening ports
	udpOutput, err := runCommand("ss", "-ulnp")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "SS_ERROR",
			"Failed to list UDP ports: "+err.Error())
	}

	ports := parseSSOutput(tcpOutput, "tcp")
	ports = append(ports, parseSSOutput(udpOutput, "udp")...)

	return response.OK(c, map[string]interface{}{
		"ports": ports,
		"total": len(ports),
	})
}

// parseSSOutput parses the output of `ss -tlnp` or `ss -ulnp`.
// Example output:
//
//	State   Recv-Q  Send-Q   Local Address:Port    Peer Address:Port  Process
//	LISTEN  0       128      0.0.0.0:22            0.0.0.0:*          users:(("sshd",pid=1234,fd=3))
//	LISTEN  0       128      [::]:22               [::]:*             users:(("sshd",pid=1234,fd=4))
func parseSSOutput(output string, protocol string) []ListeningPort {
	var ports []ListeningPort

	// Regex to extract PID and process name from users:(("name",pid=123,fd=4))
	processRe := regexp.MustCompile(`"([^"]+)",pid=(\d+)`)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip header and empty lines
		if line == "" || strings.HasPrefix(line, "State") || strings.HasPrefix(line, "Netid") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		// Local Address:Port is typically the 4th field (index 3) for ss -tlnp
		// but can vary. Find the field containing the local address.
		localAddr := fields[3]
		if len(fields) > 4 && !strings.Contains(fields[3], ":") {
			localAddr = fields[4]
		}

		address, port := parseAddressPort(localAddr)

		lp := ListeningPort{
			Protocol: protocol,
			Address:  address,
			Port:     port,
		}

		// Extract process info from the line
		processMatches := processRe.FindStringSubmatch(line)
		if len(processMatches) >= 3 {
			lp.Process = processMatches[1]
			lp.PID, _ = strconv.Atoi(processMatches[2])
		}

		// Skip entries where port couldn't be parsed
		if lp.Port > 0 {
			ports = append(ports, lp)
		}
	}

	return ports
}

// parseAddressPort splits a "address:port" string, handling IPv6 bracket notation.
func parseAddressPort(addrPort string) (string, int) {
	// Handle IPv6 format: [::]:22 or [::1]:22
	if strings.HasPrefix(addrPort, "[") {
		closeBracket := strings.LastIndex(addrPort, "]")
		if closeBracket >= 0 {
			addr := addrPort[1:closeBracket]
			portStr := ""
			if closeBracket+2 < len(addrPort) {
				portStr = addrPort[closeBracket+2:]
			}
			port, _ := strconv.Atoi(portStr)
			return addr, port
		}
	}

	// Handle IPv4 format: 0.0.0.0:22 or *:22
	lastColon := strings.LastIndex(addrPort, ":")
	if lastColon >= 0 {
		addr := addrPort[:lastColon]
		portStr := addrPort[lastColon+1:]
		port, _ := strconv.Atoi(portStr)
		return addr, port
	}

	return addrPort, 0
}

// ---------- Fail2ban Handlers ----------

// GetFail2banStatus checks if fail2ban is installed and running.
// GET /fail2ban/status
func (h *FirewallHandler) GetFail2banStatus(c echo.Context) error {
	status := Fail2banStatus{}

	// Check if fail2ban-client exists
	f2bPath, err := exec.LookPath("fail2ban-client")
	if err != nil || f2bPath == "" {
		return response.OK(c, status)
	}
	status.Installed = true

	// Get version
	versionOutput, err := runCommand("fail2ban-client", "version")
	if err == nil {
		status.Version = strings.TrimSpace(versionOutput)
	}

	// Check if running via ping/pong
	pingOutput, err := runCommand("fail2ban-client", "ping")
	if err == nil && strings.Contains(pingOutput, "pong") {
		status.Running = true
	}

	return response.OK(c, status)
}

// InstallFail2ban installs fail2ban via apt and enables the service.
// POST /fail2ban/install
func (h *FirewallHandler) InstallFail2ban(c echo.Context) error {
	// Step 1: apt-get update
	_, err := runCommandEnv(aptEnv(), "apt-get", "update")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "APT_UPDATE_ERROR",
			"Failed to update package lists: "+err.Error())
	}

	// Step 2: install fail2ban
	output, err := runCommandEnv(aptEnv(), "apt-get", "install", "-y", "fail2ban")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_INSTALL_ERROR",
			"Failed to install fail2ban: "+err.Error())
	}

	// Step 3: enable and start fail2ban
	_, _ = runCommand("systemctl", "enable", "fail2ban")
	startOutput, err := runCommand("systemctl", "start", "fail2ban")
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
func (h *FirewallHandler) ListJails(c echo.Context) error {
	output, err := runCommand("fail2ban-client", "status")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_ERROR",
			"Failed to get fail2ban status: "+err.Error())
	}

	jailNames := parseFail2banJailList(output)
	jails := make([]Fail2banJail, 0, len(jailNames))

	for _, name := range jailNames {
		jail := getJailInfo(name)
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
func getJailInfo(name string) Fail2banJail {
	jail := Fail2banJail{
		Name:      name,
		Enabled:   true, // If it's in the jail list, it's enabled
		BannedIPs: []string{},
	}

	output, err := runCommand("fail2ban-client", "status", name)
	if err != nil {
		return jail
	}

	jail = parseFail2banJailStatus(output, jail)

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
func parseFail2banJailStatus(output string, jail Fail2banJail) Fail2banJail {
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
		out, err := runCommand("fail2ban-client", "get", jail, key)
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

	return jail
}

// GetJailDetail returns detailed information for a specific fail2ban jail.
// GET /fail2ban/jails/:name
func (h *FirewallHandler) GetJailDetail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_JAIL_NAME", "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_JAIL_NAME",
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	output, err := runCommand("fail2ban-client", "status", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_JAIL_ERROR",
			"Failed to get jail status: "+err.Error())
	}

	jail := Fail2banJail{
		Name:      name,
		Enabled:   true,
		BannedIPs: []string{},
	}
	jail = parseFail2banJailStatus(output, jail)

	return response.OK(c, jail)
}

// EnableJail starts a fail2ban jail.
// POST /fail2ban/jails/:name/enable
func (h *FirewallHandler) EnableJail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_JAIL_NAME", "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_JAIL_NAME",
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	output, err := runCommand("fail2ban-client", "start", name)
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
func (h *FirewallHandler) DisableJail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_JAIL_NAME", "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_JAIL_NAME",
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	output, err := runCommand("fail2ban-client", "stop", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_DISABLE_ERROR",
			"Failed to disable jail: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Jail %s disabled successfully", name),
		"output":  strings.TrimSpace(output),
	})
}

// UnbanIP removes a banned IP from a specific fail2ban jail.
// POST /fail2ban/jails/:name/unban
// JSON body: { "ip": "192.168.1.100" }
func (h *FirewallHandler) UnbanIP(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_JAIL_NAME", "Jail name is required")
	}
	if !validJailName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_JAIL_NAME",
			"Jail name contains invalid characters (allowed: a-zA-Z0-9_-)")
	}

	var req struct {
		IP string `json:"ip"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}
	if req.IP == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "IP address is required")
	}
	if !validIP.MatchString(req.IP) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_IP",
			"IP address must be a valid IPv4 or IPv6 address")
	}

	output, err := runCommand("fail2ban-client", "set", name, "unbanip", req.IP)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FAIL2BAN_UNBAN_ERROR",
			"Failed to unban IP: "+err.Error())
	}

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("IP %s unbanned from jail %s", req.IP, name),
		"output":  strings.TrimSpace(output),
	})
}
