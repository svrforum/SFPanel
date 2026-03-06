package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ---------- UFW Handlers ----------

// GetUFWStatus returns the current UFW status including active state and default policies.
// GET /firewall/status
func (h *FirewallHandler) GetUFWStatus(c echo.Context) error {
	output, err := runCommandEnv([]string{"LANG=C"}, "ufw", "status", "verbose")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUFWError,
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
		DefaultIncoming: "deny",
		DefaultOutgoing: "allow",
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
					status.DefaultIncoming = strings.TrimSpace(strings.Split(part, "(")[0])
				} else if strings.Contains(part, "(outgoing)") {
					status.DefaultOutgoing = strings.TrimSpace(strings.Split(part, "(")[0])
				}
			}
		}
	}

	return status
}

// EnableUFW enables the UFW firewall.
// POST /firewall/enable
func (h *FirewallHandler) EnableUFW(c echo.Context) error {
	output, err := runCommandEnv([]string{"LANG=C"}, "ufw", "--force", "enable")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUFWEnableError,
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
	output, err := runCommandEnv([]string{"LANG=C"}, "ufw", "disable")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUFWDisableError,
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
	output, err := runCommandEnv([]string{"LANG=C"}, "ufw", "status", "numbered")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUFWError,
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// Validate action
	if req.Action == "" {
		req.Action = "allow"
	}
	req.Action = strings.ToLower(req.Action)
	if !validAction.MatchString(req.Action) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAction,
			"Action must be one of: allow, deny, reject, limit")
	}

	// Validate port (required)
	if req.Port == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Port is required")
	}
	if !validPort.MatchString(req.Port) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPort,
			"Port must be a number, range (e.g., 8000:8080), or service name")
	}

	// Validate protocol
	if req.Protocol == "" {
		req.Protocol = "any"
	}
	req.Protocol = strings.ToLower(req.Protocol)
	if !validProtocol.MatchString(req.Protocol) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidProtocol,
			"Protocol must be one of: tcp, udp, any")
	}

	// Validate from address if provided
	if req.From != "" && req.From != "any" {
		if !validIP.MatchString(req.From) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFromAddress,
				"From address must be a valid IP or CIDR notation")
		}
	}

	// Validate to address if provided
	if req.To != "" && req.To != "any" {
		if !validIP.MatchString(req.To) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidToAddress,
				"To address must be a valid IP or CIDR notation")
		}
	}

	// Validate comment if provided
	if req.Comment != "" && !validComment.MatchString(req.Comment) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidComment,
			"Comment contains invalid characters")
	}

	// Build the UFW command
	args := buildUFWAddArgs(req)

	output, err := runCommandEnv([]string{"LANG=C"}, "ufw", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUFWAddRuleError,
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRuleNumber,
			"Rule number must be a positive integer")
	}

	output, err := runCommandEnv([]string{"LANG=C"}, "ufw", "--force", "delete", strconv.Itoa(number))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUFWDeleteError,
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
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSError,
			"Failed to list TCP ports: "+err.Error())
	}

	// Get UDP listening ports
	udpOutput, err := runCommand("ss", "-ulnp")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSSError,
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
