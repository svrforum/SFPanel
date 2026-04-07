package firewall

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ---------- Docker Firewall (DOCKER-USER chain) ----------

// DockerPublishedPort represents a port published by a Docker container via NAT.
type DockerPublishedPort struct {
	ContainerName string `json:"container_name"`
	ContainerIP   string `json:"container_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip"`
}

// DockerUserRule represents a rule in the DOCKER-USER iptables chain.
type DockerUserRule struct {
	Number   int    `json:"number"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
	Action   string `json:"action"`
}

// AddDockerRuleRequest is the request body for adding a DOCKER-USER rule.
type AddDockerRuleRequest struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
	Action   string `json:"action"`
}

// dockerUserRulesFile is the path for persisting DOCKER-USER rules.
const dockerUserRulesFile = "/etc/sfpanel/docker-user.rules"

// validDockerAction matches allowed DOCKER-USER rule actions.
var validDockerAction = regexp.MustCompile(`^(drop|accept)$`)

// GetDockerFirewall returns Docker published ports and DOCKER-USER chain rules.
// GET /firewall/docker
func (h *Handler) GetDockerFirewall(c echo.Context) error {
	if !commandExists("iptables") {
		return response.OK(c, map[string]interface{}{
			"ports": []DockerPublishedPort{},
			"rules": []DockerUserRule{},
		})
	}
	ports, portsErr := getDockerPublishedPorts()
	rules, rulesErr := getDockerUserRules()

	if portsErr != nil && rulesErr != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDockerFirewallError,
			"Failed to get Docker firewall info: "+portsErr.Error())
	}

	return response.OK(c, map[string]interface{}{
		"ports": ports,
		"rules": rules,
	})
}

// normalizeProtocol converts numeric protocol values to names.
// iptables may output protocol as "6" (TCP) or "17" (UDP) instead of names.
func normalizeProtocol(proto string) string {
	switch proto {
	case "6":
		return "tcp"
	case "17":
		return "udp"
	default:
		return strings.ToLower(proto)
	}
}

// getDockerPublishedPorts parses iptables NAT DOCKER chain to find published ports.
func getDockerPublishedPorts() ([]DockerPublishedPort, error) {
	output, err := runCommand("iptables", "-t", "nat", "-L", "DOCKER", "-n", "--line-numbers")
	if err != nil {
		return nil, fmt.Errorf("iptables nat DOCKER: %w", err)
	}

	// Build container IP → name mapping from docker ps
	ipToName := buildContainerIPMap()

	var ports []DockerPublishedPort

	// Parse DNAT rules like:
	// 1    DNAT       tcp  --  0.0.0.0/0   0.0.0.0/0   tcp dpt:80 to:172.17.0.2:80
	// 1    DNAT       6    --  0.0.0.0/0   0.0.0.0/0   tcp dpt:80 to:172.18.0.3:80
	dnatRe := regexp.MustCompile(`^\s*(\d+)\s+DNAT\s+(\S+)\s+--\s+\S+\s+(\S+)\s+(?:tcp|udp)\s+dpt:(\d+)\s+to:(\d+\.\d+\.\d+\.\d+):(\d+)`)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		matches := dnatRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		hostPort, _ := strconv.Atoi(matches[4])
		containerIP := matches[5]
		containerPort, _ := strconv.Atoi(matches[6])
		protocol := normalizeProtocol(matches[2])
		hostIP := matches[3]

		if hostIP == "0.0.0.0/0" {
			hostIP = "0.0.0.0"
		}

		containerName := ipToName[containerIP]
		if containerName == "" {
			containerName = containerIP
		}

		ports = append(ports, DockerPublishedPort{
			ContainerName: containerName,
			ContainerIP:   containerIP,
			HostPort:      hostPort,
			ContainerPort: containerPort,
			Protocol:      protocol,
			HostIP:        hostIP,
		})
	}

	if ports == nil {
		ports = []DockerPublishedPort{}
	}

	return ports, nil
}

// buildContainerIPMap builds a map of container IP → container name using docker inspect.
func buildContainerIPMap() map[string]string {
	ipMap := make(map[string]string)

	output, err := runCommand("docker", "ps", "-q")
	if err != nil || strings.TrimSpace(output) == "" {
		return ipMap
	}

	ids := strings.Fields(strings.TrimSpace(output))
	for _, id := range ids {
		inspectOut, err := runCommand("docker", "inspect",
			"--format", "{{.Name}} {{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", id)
		if err != nil {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(inspectOut))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[0], "/")
		for _, ip := range fields[1:] {
			if ip != "" {
				ipMap[ip] = name
			}
		}
	}

	return ipMap
}

// lookupDNATMapping finds the container IP and container port for a given host port
// by inspecting Docker's NAT DNAT rules. After Docker DNAT in PREROUTING, packets in
// FORWARD/DOCKER-USER have the container IP:port as destination, not the host port.
func lookupDNATMapping(hostPort int, protocol string) (containerIP string, containerPort int, found bool) {
	output, err := runCommand("iptables", "-t", "nat", "-L", "DOCKER", "-n")
	if err != nil {
		return "", 0, false
	}

	// Match: DNAT  tcp  --  0.0.0.0/0  0.0.0.0/0  tcp dpt:3310 to:172.18.0.5:3306
	dnatRe := regexp.MustCompile(`DNAT\s+(?:tcp|udp|6|17)\s+--\s+\S+\s+\S+\s+(?:tcp|udp)\s+dpt:(\d+)\s+to:(\d+\.\d+\.\d+\.\d+):(\d+)`)
	for _, line := range strings.Split(output, "\n") {
		matches := dnatRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		hp, _ := strconv.Atoi(matches[1])
		if hp == hostPort {
			lineProto := "tcp"
			if strings.Contains(line, "udp") {
				lineProto = "udp"
			}
			if lineProto == protocol {
				cp, _ := strconv.Atoi(matches[3])
				return matches[2], cp, true
			}
		}
	}
	return "", 0, false
}

// buildReverseDNATMap builds a mapping from "containerIP:containerPort/protocol" → hostPort
// so that DOCKER-USER rules (which match on post-DNAT destination) can be displayed with host ports.
func buildReverseDNATMap() map[string]int {
	result := make(map[string]int)
	output, err := runCommand("iptables", "-t", "nat", "-L", "DOCKER", "-n")
	if err != nil {
		return result
	}

	dnatRe := regexp.MustCompile(`DNAT\s+(?:tcp|udp|6|17)\s+--\s+\S+\s+\S+\s+(?:tcp|udp)\s+dpt:(\d+)\s+to:(\d+\.\d+\.\d+\.\d+):(\d+)`)
	for _, line := range strings.Split(output, "\n") {
		matches := dnatRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		hostPort, _ := strconv.Atoi(matches[1])
		containerIP := matches[2]
		containerPort := matches[3]
		proto := "tcp"
		if strings.Contains(line, "udp") {
			proto = "udp"
		}
		key := containerIP + ":" + containerPort + "/" + proto
		result[key] = hostPort
	}
	return result
}

// getDockerUserRules parses the DOCKER-USER iptables chain.
func getDockerUserRules() ([]DockerUserRule, error) {
	output, err := runCommand("iptables", "-L", "DOCKER-USER", "-n", "--line-numbers")
	if err != nil {
		return nil, fmt.Errorf("iptables DOCKER-USER: %w", err)
	}

	// Build reverse DNAT map to translate container IP:port back to host port
	reverseMap := buildReverseDNATMap()

	var rules []DockerUserRule

	// Parse rules like:
	// 1    LOG        tcp  --  0.0.0.0/0       172.18.0.5  tcp dpt:3306 LOG ...  (skip)
	// 2    DROP       tcp  --  192.168.1.0/24  172.18.0.5  tcp dpt:3306
	// 2    DROP       6    --  0.0.0.0/0       0.0.0.0/0   tcp dpt:3310
	// 3    RETURN     all  --  0.0.0.0/0       0.0.0.0/0
	ruleRe := regexp.MustCompile(`^\s*(\d+)\s+(DROP|ACCEPT)\s+(\S+)\s+--\s+(\S+)\s+(\S+)\s+(?:tcp|udp)\s+dpt:(\d+)`)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		matches := ruleRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		number, _ := strconv.Atoi(matches[1])
		action := strings.ToLower(matches[2])
		protocol := normalizeProtocol(matches[3])
		source := matches[4]
		dest := matches[5]
		port, _ := strconv.Atoi(matches[6])

		if source == "0.0.0.0/0" {
			source = ""
		}

		// Reverse-map container IP:port to host port for display
		displayPort := port
		if dest != "0.0.0.0/0" {
			key := dest + ":" + strconv.Itoa(port) + "/" + protocol
			if hp, ok := reverseMap[key]; ok {
				displayPort = hp
			}
		}

		rules = append(rules, DockerUserRule{
			Number:   number,
			Port:     displayPort,
			Protocol: protocol,
			Source:   source,
			Action:   action,
		})
	}

	if rules == nil {
		rules = []DockerUserRule{}
	}

	return rules, nil
}

// AddDockerUserRule adds a rule to the DOCKER-USER iptables chain.
// POST /firewall/docker/rules
func (h *Handler) AddDockerUserRule(c echo.Context) error {
	var req AddDockerRuleRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	// Validate port
	if req.Port < 1 || req.Port > 65535 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPort,
			"Port must be between 1 and 65535")
	}

	// Validate protocol
	if req.Protocol == "" {
		req.Protocol = "tcp"
	}
	req.Protocol = strings.ToLower(req.Protocol)
	if req.Protocol != "tcp" && req.Protocol != "udp" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidProtocol,
			"Protocol must be tcp or udp")
	}

	// Validate action
	if req.Action == "" {
		req.Action = "drop"
	}
	req.Action = strings.ToLower(req.Action)
	if !validDockerAction.MatchString(req.Action) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAction,
			"Action must be drop or accept")
	}

	// Validate source IP if provided
	if req.Source != "" {
		if !validIP.MatchString(req.Source) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSource,
				"Source must be a valid IP or CIDR notation")
		}
	}

	// Build iptables command
	iptablesAction := strings.ToUpper(req.Action)
	args := []string{"-I", "DOCKER-USER"}

	if req.Source != "" {
		args = append(args, "-s", req.Source)
	}

	// Look up DNAT mapping: Docker rewrites destination to container IP:port in PREROUTING,
	// so DOCKER-USER (in FORWARD chain) sees the container IP:port, not the host port.
	matchPort := req.Port
	containerIP, containerPort, dnatFound := lookupDNATMapping(req.Port, req.Protocol)
	if dnatFound {
		args = append(args, "-d", containerIP)
		matchPort = containerPort
	}

	args = append(args, "-p", req.Protocol, "--dport", strconv.Itoa(matchPort), "-j", iptablesAction)

	_, err := runCommand("iptables", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrIPTablesError,
			"Failed to add DOCKER-USER rule: "+err.Error())
	}

	// Add LOG rule before DROP so blocked traffic is recorded in kern.log
	if iptablesAction == "DROP" {
		logArgs := []string{"-I", "DOCKER-USER"}
		if req.Source != "" {
			logArgs = append(logArgs, "-s", req.Source)
		}
		if dnatFound {
			logArgs = append(logArgs, "-d", containerIP)
		}
		logPrefix := fmt.Sprintf("[DOCKER-USER DROP] HPORT=%d ", req.Port)
		logArgs = append(logArgs, "-p", req.Protocol, "--dport", strconv.Itoa(matchPort),
			"-j", "LOG", "--log-prefix", logPrefix, "--log-level", "4")
		if _, logErr := runCommand("iptables", logArgs...); logErr != nil {
			log.Printf("Warning: failed to add LOG rule for DOCKER-USER DROP: %v", logErr)
		}
	}

	// Persist rules
	if saveErr := saveDockerUserRules(); saveErr != nil {
		log.Printf("Warning: failed to persist DOCKER-USER rules: %v", saveErr)
	}

	return response.OK(c, map[string]interface{}{
		"message": "DOCKER-USER rule added successfully",
	})
}

// DeleteDockerUserRule removes a rule from the DOCKER-USER chain by number.
// DELETE /firewall/docker/rules/:number
func (h *Handler) DeleteDockerUserRule(c echo.Context) error {
	numberStr := c.Param("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil || number < 1 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRuleNumber,
			"Rule number must be a positive integer")
	}

	// Check if the rule above (number-1) is a companion LOG rule for this DROP.
	// Pattern: LOG rule at N-1 has same match criteria as DROP rule at N.
	if number >= 2 {
		output, listErr := runCommand("iptables", "-S", "DOCKER-USER", strconv.Itoa(number))
		if listErr == nil && strings.Contains(output, "-j DROP") {
			logOutput, logErr := runCommand("iptables", "-S", "DOCKER-USER", strconv.Itoa(number-1))
			if logErr == nil && strings.Contains(logOutput, "-j LOG") && strings.Contains(logOutput, "DOCKER-USER DROP]") {
				// Delete the LOG rule first (same number since after deletion indices shift)
				if _, delErr := runCommand("iptables", "-D", "DOCKER-USER", strconv.Itoa(number-1)); delErr != nil {
					log.Printf("Warning: failed to delete companion LOG rule: %v", delErr)
				}
				// After deleting the LOG rule, the DROP rule shifts up by 1
				number = number - 1
			}
		}
	}

	_, err = runCommand("iptables", "-D", "DOCKER-USER", strconv.Itoa(number))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrIPTablesError,
			"Failed to delete DOCKER-USER rule: "+err.Error())
	}

	// Persist rules
	if saveErr := saveDockerUserRules(); saveErr != nil {
		log.Printf("Warning: failed to persist DOCKER-USER rules: %v", saveErr)
	}

	return response.OK(c, map[string]interface{}{
		"message": "DOCKER-USER rule deleted successfully",
	})
}

// saveDockerUserRules extracts DOCKER-USER rules from iptables-save and persists to file.
func saveDockerUserRules() error {
	output, err := runCommand("iptables-save", "-t", "filter")
	if err != nil {
		return fmt.Errorf("iptables-save: %w", err)
	}

	// Extract DOCKER-USER chain rules
	var lines []string
	lines = append(lines, "*filter")

	inDockerUser := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == ":DOCKER-USER - [0:0]" {
			lines = append(lines, line)
			continue
		}
		if strings.HasPrefix(line, "-A DOCKER-USER") {
			// Skip RETURN rules — Docker will re-add them
			if strings.Contains(line, "-j RETURN") {
				continue
			}
			lines = append(lines, line)
			inDockerUser = true
		}
	}

	// Only save if we found user rules
	if !inDockerUser {
		// Remove file if no rules exist
		os.Remove(dockerUserRulesFile)
		return nil
	}

	lines = append(lines, "COMMIT")
	content := strings.Join(lines, "\n") + "\n"

	// Ensure directory exists
	if err := os.MkdirAll("/etc/sfpanel", 0755); err != nil {
		return fmt.Errorf("mkdir /etc/sfpanel: %w", err)
	}

	return writeFile(dockerUserRulesFile, content)
}

// RestoreDockerUserRules restores DOCKER-USER rules from the persisted file.
// Called at SFPanel startup. Silently skips if no file exists.
func RestoreDockerUserRules() {
	if _, err := os.Stat(dockerUserRulesFile); os.IsNotExist(err) {
		return
	}

	output, err := runCommand("iptables-restore", "-n", "--", dockerUserRulesFile)
	if err != nil {
		log.Printf("Warning: failed to restore DOCKER-USER rules: %v (output: %s)", err, output)
		return
	}

	log.Printf("Restored DOCKER-USER firewall rules from %s", dockerUserRulesFile)
}
