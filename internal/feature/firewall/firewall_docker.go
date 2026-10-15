package firewall

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
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
	// Stale marks a rule pinned to a container address that no published
	// port leads to any more, which therefore matches nothing. Port is then
	// the container's port, the host port being unknown.
	Stale bool `json:"stale,omitempty"`
}

// AddDockerRuleRequest is the request body for adding a DOCKER-USER rule.
type AddDockerRuleRequest struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
	Action   string `json:"action"`
}

// dockerUserRulesFile is the path for persisting DOCKER-USER rules.
// Lives under /var/lib (mutable runtime state) rather than /etc/sfpanel
// (config) because it's updated by every iptables add/delete and the
// /etc tree should stay quiescent — operators back up /etc with the
// expectation that it doesn't change under their feet. 0600 perms
// match the rest of the per-node state under /var/lib/sfpanel.
//
// Vars rather than consts so tests can move them.
var dockerUserRulesFile = "/var/lib/sfpanel/docker-user.rules"
var dockerUserRulesFileLegacy = "/etc/sfpanel/docker-user.rules"

// validDockerAction matches allowed DOCKER-USER rule actions.
var validDockerAction = regexp.MustCompile(`^(drop|accept)$`)

// panelRuleTag marks the DOCKER-USER rules the panel writes, so that it saves
// and puts back its own and nothing else. It goes after the port match:
// `iptables -L` prints a comment where it stands, and the listing is parsed
// up to the match.
var panelRuleTag = []string{"-m", "comment", "--comment", "sfpanel"}

// GetDockerFirewall returns Docker published ports and DOCKER-USER chain rules.
// GET /firewall/docker
func (h *Handler) GetDockerFirewall(c echo.Context) error {
	if !h.Cmd.Exists("iptables") {
		return response.OK(c, map[string]interface{}{
			"ports": []DockerPublishedPort{},
			"rules": []DockerUserRule{},
		})
	}
	// Fetch the NAT DOCKER chain once and derive both the published-ports view
	// and the reverse-DNAT map from it (previously each parser ran iptables
	// itself, so a single GET hit `iptables -t nat -L DOCKER` twice).
	ports := []DockerPublishedPort{}
	reverseMap := map[string]int{}
	natOutput, portsErr := h.getDockerNATChain()
	if portsErr == nil {
		ports = h.parseDockerPublishedPorts(natOutput)
		reverseMap = parseReverseDNATMap(natOutput)
	}
	rules, rulesErr := h.getDockerUserRulesWith(reverseMap)

	if portsErr != nil && rulesErr != nil {
		// Neither chain exists where Docker was never installed — a Proxmox
		// VE host, say — and that is nothing to show, not an error.
		if !h.Cmd.Exists("docker") {
			return response.OK(c, map[string]interface{}{
				"ports":            []DockerPublishedPort{},
				"rules":            []DockerUserRule{},
				"docker_installed": false,
			})
		}
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

// getDockerNATChain fetches the `nat` table's DOCKER chain once. GetDockerFirewall
// needs this output for BOTH the published-ports view and the reverse-DNAT map;
// fetching here and passing the string to the parsers collapses what used to be
// two `iptables -t nat -L DOCKER` invocations per request into one.
func (h *Handler) getDockerNATChain() (string, error) {
	return h.Cmd.Run("iptables", "-t", "nat", "-L", "DOCKER", "-n", "--line-numbers")
}

// getDockerPublishedPorts fetches and parses the NAT DOCKER chain. Kept as a
// standalone fetch for callers (and tests) that only want the ports; the hot
// path in GetDockerFirewall uses parseDockerPublishedPorts with a shared fetch.
func (h *Handler) getDockerPublishedPorts() ([]DockerPublishedPort, error) {
	output, err := h.getDockerNATChain()
	if err != nil {
		return nil, fmt.Errorf("iptables nat DOCKER: %w", err)
	}
	return h.parseDockerPublishedPorts(output), nil
}

// parseDockerPublishedPorts parses a pre-fetched NAT DOCKER chain listing.
func (h *Handler) parseDockerPublishedPorts(output string) []DockerPublishedPort {
	// Build container IP → name mapping from docker ps
	ipToName := h.buildContainerIPMap()

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

	return ports
}

// buildContainerIPMap builds a map of container IP → container name using docker inspect.
func (h *Handler) buildContainerIPMap() map[string]string {
	ipMap := make(map[string]string)

	output, err := h.Cmd.Run("docker", "ps", "-q")
	if err != nil || strings.TrimSpace(output) == "" {
		return ipMap
	}

	ids := strings.Fields(strings.TrimSpace(output))
	if len(ids) == 0 {
		return ipMap
	}
	// Inspect all containers in a single docker call (it accepts multiple IDs
	// and emits one formatted line each) rather than spawning one subprocess
	// per container.
	args := append([]string{"inspect",
		"--format", "{{.Name}} {{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}"}, ids...)
	inspectOut, err := h.Cmd.Run("docker", args...)
	if err != nil {
		return ipMap
	}
	for _, line := range strings.Split(strings.TrimSpace(inspectOut), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
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

// dockerPortMatch matches connections Docker forwarded from a host port, by
// the port conntrack recorded before Docker's DNAT rewrote the packet to the
// container's address. The rules used to match that address, and a container
// gets a new one when it restarts: after a reboot a port blocked this way was
// open again, and nothing said so (#112).
//
// ctstate DNAT keeps the match to connections made to a published port, so
// a container's own connections out to the same port number elsewhere are
// left alone; ctdir ORIGINAL keeps it to the client's side of them, so the
// replies to a client an ACCEPT let through do not fall to the DROP below it.
// Without either, one of those was cut off in a namespace emulating Docker.
func dockerPortMatch(protocol string, hostPort int) []string {
	return []string{"-p", protocol, "-m", "conntrack", "--ctstate", "DNAT",
		"--ctorigdstport", strconv.Itoa(hostPort), "--ctdir", "ORIGINAL"}
}

// buildReverseDNATMap builds a mapping from "containerIP:containerPort/protocol" → hostPort
// so that DOCKER-USER rules (which match on post-DNAT destination) can be displayed with host ports.
func (h *Handler) buildReverseDNATMap() map[string]int {
	output, err := h.Cmd.Run("iptables", "-t", "nat", "-L", "DOCKER", "-n")
	if err != nil {
		return make(map[string]int)
	}
	return parseReverseDNATMap(output)
}

// parseReverseDNATMap parses a pre-fetched NAT DOCKER listing into a
// "containerIP:containerPort/proto" → hostPort map. The regex matches DNAT
// anywhere on the line, so it works with or without iptables --line-numbers.
func parseReverseDNATMap(output string) map[string]int {
	result := make(map[string]int)
	dnatRe := regexp.MustCompile(`DNAT\s+(?:tcp|udp|6|17)\s+--\s+\S+\s+\S+\s+(tcp|udp)\s+dpt:(\d+)\s+to:(\d+\.\d+\.\d+\.\d+):(\d+)`)
	for _, line := range strings.Split(output, "\n") {
		matches := dnatRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		proto := matches[1]
		hostPort, _ := strconv.Atoi(matches[2])
		containerIP := matches[3]
		containerPort := matches[4]
		key := containerIP + ":" + containerPort + "/" + proto
		result[key] = hostPort
	}
	return result
}

// getDockerUserRules parses the DOCKER-USER chain, fetching its own reverse
// DNAT map. Standalone entry point for callers/tests that want just the rules.
func (h *Handler) getDockerUserRules() ([]DockerUserRule, error) {
	return h.getDockerUserRulesWith(h.buildReverseDNATMap())
}

// getDockerUserRulesWith parses the DOCKER-USER chain using a caller-supplied
// reverse DNAT map, so GetDockerFirewall can reuse the map it already built from
// a single NAT DOCKER fetch instead of triggering another iptables call.
func (h *Handler) getDockerUserRulesWith(reverseMap map[string]int) ([]DockerUserRule, error) {
	output, err := h.Cmd.Run("iptables", "-L", "DOCKER-USER", "-n", "--line-numbers")
	if err != nil {
		return nil, fmt.Errorf("iptables DOCKER-USER: %w", err)
	}

	var rules []DockerUserRule

	// Parse rules like:
	// 1    ACCEPT     tcp  --  192.168.1.0/24  0.0.0.0/0   ctstate DNAT ctorigdstport 3310 ctdir ORIGINAL
	// 2    LOG        tcp  --  0.0.0.0/0       0.0.0.0/0   ctstate DNAT ctorigdstport 3310 ctdir ORIGINAL LOG ...  (skip)
	// 3    DROP       tcp  --  0.0.0.0/0       0.0.0.0/0   ctstate DNAT ctorigdstport 3310 ctdir ORIGINAL
	// 4    DROP       tcp  --  192.168.1.0/24  172.18.0.5  tcp dpt:3306   (pinned to a container, before #112)
	// 5    DROP       6    --  0.0.0.0/0       0.0.0.0/0   tcp dpt:3310
	// 6    RETURN     all  --  0.0.0.0/0       0.0.0.0/0
	ruleRe := regexp.MustCompile(`^\s*(\d+)\s+(DROP|ACCEPT)\s+(\S+)\s+--\s+(\S+)\s+(\S+)\s+(?:ctstate DNAT ctorigdstport (\d+)|(?:tcp|udp)\s+dpt:(\d+))`)

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

		if source == "0.0.0.0/0" {
			source = ""
		}

		rule := DockerUserRule{
			Number:   number,
			Protocol: protocol,
			Source:   source,
			Action:   action,
		}
		if matches[6] != "" {
			// Matched by host port already.
			rule.Port, _ = strconv.Atoi(matches[6])
		} else {
			// Pinned to a container address: shown by the host port that
			// leads there now, or as stale when none does.
			rule.Port, _ = strconv.Atoi(matches[7])
			if dest != "0.0.0.0/0" {
				key := dest + ":" + matches[7] + "/" + protocol
				if hp, ok := reverseMap[key]; ok {
					rule.Port = hp
				} else {
					rule.Stale = true
				}
			}
		}
		rules = append(rules, rule)
	}

	if rules == nil {
		rules = []DockerUserRule{}
	}

	return rules, nil
}

// AddDockerUserRule adds a rule to the DOCKER-USER iptables chain.
// POST /firewall/docker/rules
func (h *Handler) AddDockerUserRule(c echo.Context) error {
	if ok, err := requireTool(c, h.Cmd, "iptables"); !ok {
		return err
	}
	// Serialize chain mutations so line-number addressing stays stable.
	h.dockerRuleMu.Lock()
	defer h.dockerRuleMu.Unlock()
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
	match := append(dockerPortMatch(req.Protocol, req.Port), panelRuleTag...)
	args = append(args, match...)
	args = append(args, "-j", iptablesAction)

	_, err := h.Cmd.Run("iptables", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrIPTablesError,
			"Failed to add DOCKER-USER rule: "+response.SanitizeOutput(err.Error()))
	}

	// Add LOG rule before DROP so blocked traffic is recorded in kern.log
	if iptablesAction == "DROP" {
		logArgs := []string{"-I", "DOCKER-USER"}
		if req.Source != "" {
			logArgs = append(logArgs, "-s", req.Source)
		}
		logPrefix := fmt.Sprintf("[DOCKER-USER DROP] HPORT=%d ", req.Port)
		logArgs = append(logArgs, match...)
		logArgs = append(logArgs, "-j", "LOG", "--log-prefix", logPrefix, "--log-level", "4")
		if _, logErr := h.Cmd.Run("iptables", logArgs...); logErr != nil {
			slog.Warn("failed to add LOG rule for DOCKER-USER DROP", "component", "firewall", "error", logErr)
		}
	}

	// Persist rules
	if saveErr := h.saveDockerUserRules(); saveErr != nil {
		slog.Warn("failed to persist DOCKER-USER rules", "component", "firewall", "error", saveErr)
	}

	return response.OK(c, map[string]interface{}{
		"message": "DOCKER-USER rule added successfully",
	})
}

// DeleteDockerUserRule removes a rule from the DOCKER-USER chain by number.
// DELETE /firewall/docker/rules/:number
func (h *Handler) DeleteDockerUserRule(c echo.Context) error {
	if ok, err := requireTool(c, h.Cmd, "iptables"); !ok {
		return err
	}
	// Serialize chain mutations so line-number addressing stays stable.
	h.dockerRuleMu.Lock()
	defer h.dockerRuleMu.Unlock()
	numberStr := c.Param("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil || number < 1 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRuleNumber,
			"Rule number must be a positive integer")
	}

	// Check if the rule above (number-1) is a companion LOG rule for this DROP.
	// Pattern: LOG rule at N-1 has same match criteria as DROP rule at N.
	if number >= 2 {
		output, listErr := h.Cmd.Run("iptables", "-S", "DOCKER-USER", strconv.Itoa(number))
		if listErr == nil && strings.Contains(output, "-j DROP") {
			logOutput, logErr := h.Cmd.Run("iptables", "-S", "DOCKER-USER", strconv.Itoa(number-1))
			if logErr == nil && strings.Contains(logOutput, "-j LOG") && strings.Contains(logOutput, "DOCKER-USER DROP]") {
				// Delete the LOG rule first (same number since after deletion indices shift)
				if _, delErr := h.Cmd.Run("iptables", "-D", "DOCKER-USER", strconv.Itoa(number-1)); delErr != nil {
					slog.Warn("failed to delete companion LOG rule", "component", "firewall", "error", delErr)
				}
				// After deleting the LOG rule, the DROP rule shifts up by 1
				number = number - 1
			}
		}
	}

	_, err = h.Cmd.Run("iptables", "-D", "DOCKER-USER", strconv.Itoa(number))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrIPTablesError,
			"Failed to delete DOCKER-USER rule: "+response.SanitizeOutput(err.Error()))
	}

	// Persist rules
	if saveErr := h.saveDockerUserRules(); saveErr != nil {
		slog.Warn("failed to persist DOCKER-USER rules", "component", "firewall", "error", saveErr)
	}

	return response.OK(c, map[string]interface{}{
		"message": "DOCKER-USER rule deleted successfully",
	})
}

// saveDockerUserRules keeps the panel's own DOCKER-USER rules, in chain
// order, for RestoreDockerUserRules to put back at the next start.
//
// It used to keep the whole chain, its declaration included, and the restore
// replayed that through iptables-restore, which empties a chain it declares:
// at every start a rule added by hand since the last save was deleted, and one
// deleted by hand came back. On a Proxmox host running Docker, the rule lost
// is typically the ACCEPT that lets the guests' bridge through.
func (h *Handler) saveDockerUserRules() error {
	output, err := h.Cmd.Run("iptables-save", "-t", "filter")
	if err != nil {
		return fmt.Errorf("iptables-save: %w", err)
	}

	lines := []string{"*filter"}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if isPanelDockerRule(line) {
			lines = append(lines, line)
		}
	}

	// Only save if the panel has rules in the chain
	if len(lines) == 1 {
		// Remove file if no rules exist
		os.Remove(dockerUserRulesFile)
		os.Remove(dockerUserRulesFileLegacy)
		return nil
	}

	lines = append(lines, "COMMIT")
	content := strings.Join(lines, "\n") + "\n"

	// Ensure directory exists
	dir := filepath.Dir(dockerUserRulesFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// 0600 — rule list isn't a secret per se, but it's per-node mutable
	// runtime state with no need for other users on the box to read.
	if err := os.WriteFile(dockerUserRulesFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("write %s: %w", dockerUserRulesFile, err)
	}
	// Clean up the legacy /etc/sfpanel copy so a subsequent
	// RestoreDockerUserRules doesn't have two sources of truth.
	_ = os.Remove(dockerUserRulesFileLegacy)
	return nil
}

// RestoreDockerUserRules puts the panel's saved DOCKER-USER rules back.
// Called at SFPanel startup. Silently skips if no file exists.
//
// Each saved rule is looked for with iptables -C and inserted, in the saved
// order, only where it is missing. The chain is never declared, flushed or
// replaced, so the rules the panel did not write stay as they are; it used to
// replay the file through iptables-restore, which emptied the chain first.
//
// Tries the new /var/lib/sfpanel path first; falls back to the legacy
// /etc/sfpanel location so installations that haven't yet hit
// saveDockerUserRules (which migrates the path) still restore on boot.
func RestoreDockerUserRules(cmd exec.Commander) {
	source := dockerUserRulesFile
	if _, err := os.Stat(source); os.IsNotExist(err) {
		if _, err := os.Stat(dockerUserRulesFileLegacy); os.IsNotExist(err) {
			return
		}
		source = dockerUserRulesFileLegacy
	}

	data, err := os.ReadFile(source)
	if err != nil {
		slog.Warn("failed to read the saved DOCKER-USER rules", "component", "firewall", "file", source, "error", err)
		return
	}
	saved := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	// Rules saved before #112 match the container's address, which may not
	// be the container's any more. Moved to the host port first, so this
	// restore does not put back a rule that lets the port through.
	lines, moved := migrateDockerUserRules(saved)
	if moved > 0 {
		if err := os.WriteFile(source, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			slog.Warn("failed to save DOCKER-USER rules moved to host ports", "component", "firewall", "error", err)
		} else {
			slog.Info("moved DOCKER-USER rules from container addresses to host ports", "component", "firewall", "rules", moved)
		}
	}

	// Docker makes the chain when it starts, and the panel can start first.
	// A missing chain is made here, empty, as Docker would make it; Docker
	// then takes it as it finds it.
	listing, err := cmd.Run("iptables", "-S", "DOCKER-USER")
	if err != nil {
		if out, err := cmd.Run("iptables", "-N", "DOCKER-USER"); err != nil {
			slog.Warn("failed to restore DOCKER-USER rules: no chain", "component", "firewall", "error", err, "output", out)
			return
		}
		listing = ""
	}
	var live []string
	for _, l := range strings.Split(listing, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "-A DOCKER-USER ") {
			live = append(live, l)
		}
	}

	// next is where a missing rule goes: after the saved rule before it,
	// wherever that stands in the chain, so the panel's rules keep their
	// order — the ACCEPT for one source above the DROP for everyone — with
	// other rules above or between them.
	next, inserted := 1, 0
	for _, line := range lines {
		spec, ok := dockerUserRuleSpec(line)
		if !ok {
			continue
		}
		if _, err := cmd.Run("iptables", append([]string{"-C", "DOCKER-USER"}, spec...)...); err == nil {
			if i := slices.Index(live, line); i+2 > next {
				next = i + 2
			}
			continue
		}
		if out, err := cmd.Run("iptables", append([]string{"-I", "DOCKER-USER", strconv.Itoa(next)}, spec...)...); err != nil {
			slog.Warn("failed to restore a DOCKER-USER rule", "component", "firewall", "rule", line, "error", err, "output", out)
			continue
		}
		live = slices.Insert(live, min(next-1, len(live)), line)
		next++
		inserted++
	}

	// The container-pinned rules the move replaced are still in the chain
	// when the panel restarts without a reboot, as an update does. They are
	// the panel's own — the LOG beside them names their host port — and with
	// their replacements in place they go.
	for i, line := range saved {
		if line == lines[i] {
			continue
		}
		spec, ok := dockerUserRuleSpec(line)
		if !ok {
			continue
		}
		if _, err := cmd.Run("iptables", append([]string{"-C", "DOCKER-USER"}, spec...)...); err != nil {
			continue
		}
		if out, err := cmd.Run("iptables", append([]string{"-D", "DOCKER-USER"}, spec...)...); err != nil {
			slog.Warn("failed to remove a DOCKER-USER rule moved to its host port", "component", "firewall", "rule", line, "error", err, "output", out)
		}
	}

	slog.Info("restored DOCKER-USER firewall rules", "component", "firewall", "file", source, "inserted", inserted)
}

var (
	// panelTagRe finds the panel's tag in an iptables-save line.
	panelTagRe = regexp.MustCompile(`(?:^| )-m comment --comment sfpanel(?: |$)`)
	// panelConntrackRuleRe is a rule in the form only the panel writes, as it
	// wrote them before it tagged them: a host port matched through conntrack.
	panelConntrackRuleRe = regexp.MustCompile(`^-A DOCKER-USER (?:-s \S+ )?-p (?:tcp|udp) -m conntrack --ctstate DNAT --ctorigdstport \d+ --ctdir ORIGINAL -j (?:ACCEPT|DROP|LOG)(?: |$)`)
)

// isPanelDockerRule reports whether an iptables-save line is a DOCKER-USER
// rule the panel wrote: tagged; or, from before the tag, the LOG naming a host
// port that goes in front of each DROP, or a rule in the conntrack form.
func isPanelDockerRule(line string) bool {
	if !strings.HasPrefix(line, "-A DOCKER-USER ") {
		return false
	}
	return panelTagRe.MatchString(line) || hostPortPrefixRe.MatchString(line) || panelConntrackRuleRe.MatchString(line)
}

// dockerUserRuleSpec turns a saved "-A DOCKER-USER …" line into the rule's
// arguments, for iptables -C, -I and -D.
func dockerUserRuleSpec(line string) ([]string, bool) {
	rest, ok := strings.CutPrefix(line, "-A DOCKER-USER ")
	if !ok {
		return nil, false
	}
	spec := splitSaveArgs(rest)
	return spec, len(spec) > 0
}

// splitSaveArgs splits a rule as iptables-save prints it into arguments:
// fields separated by spaces, where a value with other characters than
// letters, digits, - and _ is in double quotes with \", \\ and \' escaped.
func splitSaveArgs(s string) []string {
	var args []string
	var cur strings.Builder
	quoted, escaped, have := false, false, false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
			have = true
		case r == ' ' && !quoted:
			if have {
				args = append(args, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		args = append(args, cur.String())
	}
	return args
}

// legacyDockerRuleRe matches, in iptables-save form, a rule AddDockerUserRule
// wrote before #112: pinned to a container's address and port.
var legacyDockerRuleRe = regexp.MustCompile(`^-A DOCKER-USER (?:-s (\S+) )?(?:-d (\S+) )?-p (tcp|udp) -m (?:tcp|udp) --dport (\d+) -j (ACCEPT|DROP|LOG)(.*)$`)

// hostPortPrefixRe reads the host port from the LOG rule the panel puts in
// front of each DROP.
var hostPortPrefixRe = regexp.MustCompile(`--log-prefix "\[DOCKER-USER DROP\] HPORT=(\d+) "`)

// migrateDockerUserRules rewrites the panel's container-pinned rules in an
// iptables-save listing to match the host port. The host port comes from the
// LOG rule beside each DROP, so this works after the container's address has
// changed, when the address in the rule no longer says which port it was.
// An ACCEPT takes the host port of the DROP on the same container port. A
// rule with nothing naming its host port, an operator's own among them, is
// left as it is and shows as stale while it matches nothing.
func migrateDockerUserRules(lines []string) ([]string, int) {
	type target struct{ dest, proto, port string }
	hostPorts := map[target]string{}
	for _, line := range lines {
		m := legacyDockerRuleRe.FindStringSubmatch(line)
		if m == nil || m[5] != "LOG" {
			continue
		}
		if hp := hostPortPrefixRe.FindStringSubmatch(m[6]); hp != nil {
			hostPorts[target{m[2], m[3], m[4]}] = hp[1]
		}
	}

	out := make([]string, 0, len(lines))
	moved := 0
	for _, line := range lines {
		m := legacyDockerRuleRe.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		hostPort, ok := hostPorts[target{m[2], m[3], m[4]}]
		if !ok {
			out = append(out, line)
			continue
		}
		port, _ := strconv.Atoi(hostPort)
		rule := []string{"-A", "DOCKER-USER"}
		if m[1] != "" {
			rule = append(rule, "-s", m[1])
		}
		rule = append(rule, dockerPortMatch(m[3], port)...)
		out = append(out, strings.Join(append(rule, "-j", m[5]), " ")+m[6])
		moved++
	}
	return out, moved
}
