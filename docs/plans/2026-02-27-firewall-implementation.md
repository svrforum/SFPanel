# Firewall Management Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add UFW firewall rules management, open ports viewer, and Fail2ban status management to SFPanel as a single tabbed page.

**Architecture:** New `FirewallHandler` struct in `internal/api/handlers/firewall.go` wrapping UFW and Fail2ban CLI commands. Frontend as tabbed page (`Firewall.tsx` + 3 sub-components in `web/src/pages/firewall/`). Follows existing Disk page pattern exactly.

**Tech Stack:** Go (Echo v4, exec.Command for ufw/fail2ban-client/ss), React 18, TypeScript, Tailwind CSS, shadcn/ui Tabs/Table/Dialog/Input/Button.

---

### Task 1: Backend — UFW Handler (`firewall.go`)

**Files:**
- Create: `internal/api/handlers/firewall.go`

**Step 1: Create the FirewallHandler with all UFW + Fail2ban endpoints**

The handler uses `runCommand()` (already defined in `packages.go`, accessible package-wide).

```go
package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// ---------- Types ----------

type UFWStatus struct {
	Active        bool   `json:"active"`
	DefaultIncome string `json:"default_incoming"`
	DefaultOut    string `json:"default_outgoing"`
}

type UFWRule struct {
	Number    int    `json:"number"`
	To        string `json:"to"`
	Action    string `json:"action"`
	From      string `json:"from"`
	Comment   string `json:"comment,omitempty"`
	V6        bool   `json:"v6"`
}

type AddRuleRequest struct {
	Action   string `json:"action"`   // allow, deny
	Port     string `json:"port"`     // e.g. "80", "80:90", "" (for IP-only rules)
	Protocol string `json:"protocol"` // tcp, udp, "" (both)
	From     string `json:"from"`     // IP or "any"
	To       string `json:"to"`       // IP or "any"
	Comment  string `json:"comment"`
}

type ListeningPort struct {
	Protocol string `json:"protocol"` // tcp, tcp6, udp, udp6
	Address  string `json:"address"`
	Port     int    `json:"port"`
	PID      int    `json:"pid"`
	Process  string `json:"process"`
}

type Fail2banStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

type Fail2banJail struct {
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	Filter       string   `json:"filter"`
	BannedCount  int      `json:"banned_count"`
	TotalBanned  int      `json:"total_banned"`
	BannedIPs    []string `json:"banned_ips,omitempty"`
	MaxRetry     int      `json:"max_retry"`
	BanTime      string   `json:"ban_time"`
	FindTime     string   `json:"find_time"`
}

type FirewallHandler struct{}

// ---------- UFW Status ----------

// GetUFWStatus returns UFW active state and default policies.
// GET /firewall/status
func (h *FirewallHandler) GetUFWStatus(c echo.Context) error {
	out, err := runCommand("ufw", "status", "verbose")
	if err != nil {
		// ufw not installed
		if strings.Contains(err.Error(), "not found") || strings.Contains(out, "not found") {
			return response.Fail(c, http.StatusNotFound, "UFW_NOT_FOUND", "UFW is not installed")
		}
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}

	status := UFWStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			status.Active = strings.Contains(line, "active") && !strings.Contains(line, "inactive")
		}
		if strings.HasPrefix(line, "Default:") {
			parts := strings.Split(line, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if strings.Contains(p, "incoming") {
					// "deny (incoming)" -> extract action
					status.DefaultIncome = extractPolicyAction(p)
				}
				if strings.Contains(p, "outgoing") {
					status.DefaultOut = extractPolicyAction(p)
				}
			}
		}
	}

	return response.OK(c, status)
}

func extractPolicyAction(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "default:")
	s = strings.TrimSpace(s)
	// "deny (incoming)" -> "deny"
	parts := strings.Fields(s)
	if len(parts) > 0 {
		return parts[0]
	}
	return "unknown"
}

// EnableUFW activates the firewall.
// POST /firewall/enable
func (h *FirewallHandler) EnableUFW(c echo.Context) error {
	out, err := runCommand("ufw", "--force", "enable")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": "Firewall enabled"})
}

// DisableUFW deactivates the firewall.
// POST /firewall/disable
func (h *FirewallHandler) DisableUFW(c echo.Context) error {
	out, err := runCommand("ufw", "disable")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": "Firewall disabled"})
}

// ---------- UFW Rules ----------

var ufwRuleLineRegex = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s+(ALLOW|DENY|REJECT|LIMIT)\s+(IN|OUT|FWD)?\s*(.*)$`)

// ListRules returns all UFW rules parsed from `ufw status numbered`.
// GET /firewall/rules
func (h *FirewallHandler) ListRules(c echo.Context) error {
	out, err := runCommand("ufw", "status", "numbered")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}

	var rules []UFWRule
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		rule := parseUFWRuleLine(line)
		if rule != nil {
			rules = append(rules, *rule)
		}
	}

	if rules == nil {
		rules = []UFWRule{}
	}
	return response.OK(c, map[string]interface{}{"rules": rules, "total": len(rules)})
}

func parseUFWRuleLine(line string) *UFWRule {
	// Example lines:
	// [ 1] 22/tcp                     ALLOW IN    Anywhere
	// [ 2] 80                         DENY IN     192.168.1.0/24
	// [ 3] Anywhere                   DENY IN     10.0.0.1                   # blocked
	// [ 4] 22/tcp (v6)                ALLOW IN    Anywhere (v6)

	// Remove (v6) markers and track them
	v6 := strings.Contains(line, "(v6)")
	cleaned := strings.ReplaceAll(line, "(v6)", "")
	cleaned = strings.TrimSpace(cleaned)

	matches := ufwRuleLineRegex.FindStringSubmatch(cleaned)
	if matches == nil {
		return nil
	}

	num, _ := strconv.Atoi(matches[1])

	// Extract comment if present
	comment := ""
	fromPart := matches[5]
	if idx := strings.Index(fromPart, "#"); idx >= 0 {
		comment = strings.TrimSpace(fromPart[idx+1:])
		fromPart = strings.TrimSpace(fromPart[:idx])
	}

	return &UFWRule{
		Number:  num,
		To:      strings.TrimSpace(matches[2]),
		Action:  strings.TrimSpace(matches[3]),
		From:    strings.TrimSpace(fromPart),
		Comment: comment,
		V6:      v6,
	}
}

// AddRule creates a new UFW rule.
// POST /firewall/rules
func (h *FirewallHandler) AddRule(c echo.Context) error {
	var req AddRuleRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if req.Action != "allow" && req.Action != "deny" && req.Action != "reject" && req.Action != "limit" {
		return response.Fail(c, http.StatusBadRequest, "INVALID_ACTION", "Action must be allow, deny, reject, or limit")
	}

	// Validate port format if provided
	if req.Port != "" {
		portRegex := regexp.MustCompile(`^(\d{1,5})(:\d{1,5})?(/[a-z]+)?$`)
		if !portRegex.MatchString(req.Port) {
			return response.Fail(c, http.StatusBadRequest, "INVALID_PORT", "Invalid port format")
		}
	}

	// Validate IP if provided and not "any"
	if req.From != "" && req.From != "any" {
		ipRegex := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}(/\d{1,2})?$`)
		if !ipRegex.MatchString(req.From) {
			return response.Fail(c, http.StatusBadRequest, "INVALID_IP", "Invalid IP address format")
		}
	}

	args := buildUFWArgs(req)
	out, err := runCommand("ufw", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": "Rule added", "output": strings.TrimSpace(out)})
}

func buildUFWArgs(req AddRuleRequest) []string {
	args := []string{}

	// IP-only blocking: ufw deny from <ip>
	if req.Port == "" && req.From != "" && req.From != "any" {
		args = append(args, req.Action, "from", req.From)
		if req.Comment != "" {
			args = append(args, "comment", req.Comment)
		}
		return args
	}

	// Port rule: ufw allow [proto <protocol>] [from <ip>] to any port <port>
	if req.From != "" && req.From != "any" {
		args = append(args, req.Action)
		if req.Protocol != "" {
			args = append(args, "proto", req.Protocol)
		}
		args = append(args, "from", req.From, "to", "any", "port", req.Port)
	} else {
		// Simple: ufw allow <port>[/<protocol>]
		port := req.Port
		if req.Protocol != "" {
			port = port + "/" + req.Protocol
		}
		args = append(args, req.Action, port)
	}

	if req.Comment != "" {
		args = append(args, "comment", req.Comment)
	}
	return args
}

// DeleteRule removes a UFW rule by its number.
// DELETE /firewall/rules/:number
func (h *FirewallHandler) DeleteRule(c echo.Context) error {
	numStr := c.Param("number")
	num, err := strconv.Atoi(numStr)
	if err != nil || num < 1 {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NUMBER", "Invalid rule number")
	}
	out, err := runCommand("ufw", "--force", "delete", numStr)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Rule %d deleted", num)})
}

// ---------- Listening Ports ----------

// ListPorts returns currently listening ports from `ss -tlnp`.
// GET /firewall/ports
func (h *FirewallHandler) ListPorts(c echo.Context) error {
	// TCP
	tcpOut, err := runCommand("ss", "-tlnp")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", tcpOut)
	}
	// UDP
	udpOut, _ := runCommand("ss", "-ulnp")

	var ports []ListeningPort
	ports = append(ports, parseSSOutput(tcpOut, "tcp")...)
	ports = append(ports, parseSSOutput(udpOut, "udp")...)

	if ports == nil {
		ports = []ListeningPort{}
	}
	return response.OK(c, map[string]interface{}{"ports": ports, "total": len(ports)})
}

func parseSSOutput(out string, proto string) []ListeningPort {
	var ports []ListeningPort
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 { // skip header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		localAddr := fields[3]
		// Extract address and port: "0.0.0.0:22" or "[::]:22" or "*:22"
		addr, portStr := splitAddrPort(localAddr)
		port, _ := strconv.Atoi(portStr)
		if port == 0 {
			continue
		}

		protocol := proto
		if strings.Contains(addr, ":") || addr == "*" && strings.Contains(localAddr, "]:") {
			protocol = proto + "6"
		}

		pid, process := parseSSProcess(fields[len(fields)-1])

		ports = append(ports, ListeningPort{
			Protocol: protocol,
			Address:  addr,
			Port:     port,
			PID:      pid,
			Process:  process,
		})
	}
	return ports
}

func splitAddrPort(s string) (string, string) {
	// Handle [::]:port format
	if strings.HasPrefix(s, "[") {
		if idx := strings.LastIndex(s, "]:"); idx >= 0 {
			return s[:idx+1], s[idx+2:]
		}
	}
	// Handle addr:port
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

func parseSSProcess(s string) (int, string) {
	// users:(("sshd",pid=1234,fd=3))
	pidRegex := regexp.MustCompile(`pid=(\d+)`)
	nameRegex := regexp.MustCompile(`\("([^"]+)"`)
	pid := 0
	name := ""
	if m := pidRegex.FindStringSubmatch(s); len(m) > 1 {
		pid, _ = strconv.Atoi(m[1])
	}
	if m := nameRegex.FindStringSubmatch(s); len(m) > 1 {
		name = m[1]
	}
	return pid, name
}

// ---------- Fail2ban ----------

// GetFail2banStatus checks if fail2ban is installed and running.
// GET /fail2ban/status
func (h *FirewallHandler) GetFail2banStatus(c echo.Context) error {
	status := Fail2banStatus{}

	// Check installed
	_, err := runCommand("which", "fail2ban-client")
	status.Installed = err == nil

	if !status.Installed {
		return response.OK(c, status)
	}

	// Check version
	vOut, err := runCommand("fail2ban-client", "version")
	if err == nil {
		status.Version = strings.TrimSpace(vOut)
	}

	// Check running
	out, err := runCommand("fail2ban-client", "ping")
	status.Running = err == nil && strings.Contains(out, "pong")

	return response.OK(c, status)
}

// InstallFail2ban installs fail2ban via apt.
// POST /fail2ban/install
func (h *FirewallHandler) InstallFail2ban(c echo.Context) error {
	out, err := runCommandEnv(aptEnv(), "apt-get", "install", "-y", "fail2ban")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "INSTALL_FAILED", out)
	}
	// Enable and start
	runCommand("systemctl", "enable", "fail2ban")
	runCommand("systemctl", "start", "fail2ban")
	return response.OK(c, map[string]string{"message": "Fail2ban installed and started"})
}

// ListJails returns all fail2ban jails with status.
// GET /fail2ban/jails
func (h *FirewallHandler) ListJails(c echo.Context) error {
	out, err := runCommand("fail2ban-client", "status")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}

	// Parse jail list from "Jail list:\tname1, name2"
	jailNames := parseJailList(out)
	var jails []Fail2banJail
	for _, name := range jailNames {
		jail := getJailInfo(name)
		jails = append(jails, jail)
	}

	if jails == nil {
		jails = []Fail2banJail{}
	}
	return response.OK(c, map[string]interface{}{"jails": jails, "total": len(jails)})
}

func parseJailList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Jail list:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 1 {
				for _, n := range strings.Split(parts[1], ",") {
					n = strings.TrimSpace(n)
					if n != "" {
						names = append(names, n)
					}
				}
			}
		}
	}
	return names
}

func getJailInfo(name string) Fail2banJail {
	jail := Fail2banJail{Name: name, Enabled: true}
	out, err := runCommand("fail2ban-client", "status", name)
	if err != nil {
		jail.Enabled = false
		return jail
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Currently banned:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 1 {
				jail.BannedCount, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			}
		}
		if strings.Contains(line, "Total banned:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 1 {
				jail.TotalBanned, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			}
		}
		if strings.Contains(line, "Banned IP list:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) > 1 {
				for _, ip := range strings.Fields(parts[1]) {
					ip = strings.TrimSpace(ip)
					if ip != "" {
						jail.BannedIPs = append(jail.BannedIPs, ip)
					}
				}
			}
		}
	}

	if jail.BannedIPs == nil {
		jail.BannedIPs = []string{}
	}
	return jail
}

// GetJailDetail returns detailed info about a specific jail.
// GET /fail2ban/jails/:name
func (h *FirewallHandler) GetJailDetail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "Jail name is required")
	}

	// Validate jail name (alphanumeric + hyphen + underscore)
	jailNameRegex := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !jailNameRegex.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", "Invalid jail name")
	}

	jail := getJailInfo(name)
	return response.OK(c, jail)
}

// EnableJail enables (starts) a fail2ban jail.
// POST /fail2ban/jails/:name/enable
func (h *FirewallHandler) EnableJail(c echo.Context) error {
	name := c.Param("name")
	// Create jail.local override if not exists, or use fail2ban-client
	out, err := runCommand("fail2ban-client", "start", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Jail %s enabled", name)})
}

// DisableJail disables (stops) a fail2ban jail.
// POST /fail2ban/jails/:name/disable
func (h *FirewallHandler) DisableJail(c echo.Context) error {
	name := c.Param("name")
	out, err := runCommand("fail2ban-client", "stop", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": fmt.Sprintf("Jail %s disabled", name)})
}

// UnbanIP unbans an IP from a specific jail.
// POST /fail2ban/jails/:name/unban
func (h *FirewallHandler) UnbanIP(c echo.Context) error {
	name := c.Param("name")
	var req struct {
		IP string `json:"ip"`
	}
	if err := c.Bind(&req); err != nil || req.IP == "" {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS", "IP address is required")
	}

	// Validate IP format
	ipRegex := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	if !ipRegex.MatchString(req.IP) {
		return response.Fail(c, http.StatusBadRequest, "INVALID_IP", "Invalid IP address")
	}

	out, err := runCommand("fail2ban-client", "set", name, "unbanip", req.IP)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "COMMAND_FAILED", out)
	}
	return response.OK(c, map[string]string{"message": fmt.Sprintf("IP %s unbanned from %s", req.IP, name)})
}
```

**Step 2: Verify compilation**

Run: `go vet ./internal/api/handlers/`
Expected: No errors

**Step 3: Commit**

```bash
git add internal/api/handlers/firewall.go
git commit -m "feat: 방화벽 핸들러 추가 (UFW + Fail2ban)"
```

---

### Task 2: Backend — Router Registration

**Files:**
- Modify: `internal/api/router.go`

**Step 1: Add firewall and fail2ban routes after the disk management section**

Insert after the `swap.PUT("/resize", diskHandler.ResizeSwap)` line, before the packages section:

```go
	// Firewall management (UFW)
	firewallHandler := &handlers.FirewallHandler{}
	fw := authorized.Group("/firewall")
	fw.GET("/status", firewallHandler.GetUFWStatus)
	fw.POST("/enable", firewallHandler.EnableUFW)
	fw.POST("/disable", firewallHandler.DisableUFW)
	fw.GET("/rules", firewallHandler.ListRules)
	fw.POST("/rules", firewallHandler.AddRule)
	fw.DELETE("/rules/:number", firewallHandler.DeleteRule)
	fw.GET("/ports", firewallHandler.ListPorts)

	// Fail2ban
	f2b := authorized.Group("/fail2ban")
	f2b.GET("/status", firewallHandler.GetFail2banStatus)
	f2b.POST("/install", firewallHandler.InstallFail2ban)
	f2b.GET("/jails", firewallHandler.ListJails)
	f2b.GET("/jails/:name", firewallHandler.GetJailDetail)
	f2b.POST("/jails/:name/enable", firewallHandler.EnableJail)
	f2b.POST("/jails/:name/disable", firewallHandler.DisableJail)
	f2b.POST("/jails/:name/unban", firewallHandler.UnbanIP)
```

**Step 2: Verify compilation**

Run: `go vet ./...`
Expected: No errors

**Step 3: Commit**

```bash
git add internal/api/router.go
git commit -m "feat: 방화벽 라우트 등록"
```

---

### Task 3: Frontend — TypeScript Types

**Files:**
- Modify: `web/src/types/api.ts`

**Step 1: Add firewall types at the end of the file (before the closing)**

```typescript
// Firewall (UFW)
export interface UFWStatus {
  active: boolean
  default_incoming: string
  default_outgoing: string
}

export interface UFWRule {
  number: number
  to: string
  action: string
  from: string
  comment: string
  v6: boolean
}

export interface AddRuleRequest {
  action: string
  port: string
  protocol: string
  from: string
  to: string
  comment: string
}

export interface ListeningPort {
  protocol: string
  address: string
  port: number
  pid: number
  process: string
}

// Fail2ban
export interface Fail2banStatus {
  installed: boolean
  running: boolean
  version: string
}

export interface Fail2banJail {
  name: string
  enabled: boolean
  filter: string
  banned_count: number
  total_banned: number
  banned_ips: string[]
  max_retry: number
  ban_time: string
  find_time: string
}
```

**Step 2: Commit**

```bash
git add web/src/types/api.ts
git commit -m "feat: 방화벽 TypeScript 타입 추가"
```

---

### Task 4: Frontend — API Client Methods

**Files:**
- Modify: `web/src/lib/api.ts`

**Step 1: Add firewall and fail2ban methods before the closing `}` of ApiClient**

```typescript
  // Firewall (UFW)
  getFirewallStatus() {
    return this.request<{ active: boolean; default_incoming: string; default_outgoing: string }>('/firewall/status')
  }

  enableFirewall() {
    return this.request<{ message: string }>('/firewall/enable', { method: 'POST' })
  }

  disableFirewall() {
    return this.request<{ message: string }>('/firewall/disable', { method: 'POST' })
  }

  getFirewallRules() {
    return this.request<{ rules: Array<{ number: number; to: string; action: string; from: string; comment: string; v6: boolean }>; total: number }>('/firewall/rules')
  }

  addFirewallRule(data: { action: string; port: string; protocol: string; from: string; to: string; comment: string }) {
    return this.request<{ message: string; output: string }>('/firewall/rules', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  deleteFirewallRule(number: number) {
    return this.request<{ message: string }>(`/firewall/rules/${number}`, { method: 'DELETE' })
  }

  getListeningPorts() {
    return this.request<{ ports: Array<{ protocol: string; address: string; port: number; pid: number; process: string }>; total: number }>('/firewall/ports')
  }

  // Fail2ban
  getFail2banStatus() {
    return this.request<{ installed: boolean; running: boolean; version: string }>('/fail2ban/status')
  }

  installFail2ban() {
    return this.request<{ message: string }>('/fail2ban/install', { method: 'POST' })
  }

  getFail2banJails() {
    return this.request<{ jails: Array<{ name: string; enabled: boolean; banned_count: number; total_banned: number; banned_ips: string[] }>; total: number }>('/fail2ban/jails')
  }

  getFail2banJailDetail(name: string) {
    return this.request<{ name: string; enabled: boolean; banned_count: number; total_banned: number; banned_ips: string[] }>(`/fail2ban/jails/${encodeURIComponent(name)}`)
  }

  enableFail2banJail(name: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(name)}/enable`, { method: 'POST' })
  }

  disableFail2banJail(name: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(name)}/disable`, { method: 'POST' })
  }

  unbanFail2banIP(jail: string, ip: string) {
    return this.request<{ message: string }>(`/fail2ban/jails/${encodeURIComponent(jail)}/unban`, {
      method: 'POST',
      body: JSON.stringify({ ip }),
    })
  }
```

**Step 2: Commit**

```bash
git add web/src/lib/api.ts
git commit -m "feat: 방화벽 API 클라이언트 메서드 추가"
```

---

### Task 5: Frontend — i18n (ko.json + en.json)

**Files:**
- Modify: `web/src/i18n/locales/ko.json`
- Modify: `web/src/i18n/locales/en.json`

**Step 1: Add firewall i18n keys to both files**

Add to `ko.json` top-level object:
```json
"firewall": {
  "title": "방화벽",
  "tabs": {
    "rules": "UFW 규칙",
    "ports": "열린 포트",
    "fail2ban": "Fail2ban"
  },
  "status": {
    "title": "방화벽 상태",
    "active": "활성",
    "inactive": "비활성",
    "defaultIncoming": "기본 수신 정책",
    "defaultOutgoing": "기본 발신 정책",
    "enable": "활성화",
    "disable": "비활성화",
    "enableConfirm": "방화벽을 활성화하시겠습니까?",
    "disableConfirm": "방화벽을 비활성화하시겠습니까? 서버가 보호되지 않습니다."
  },
  "rules": {
    "title": "방화벽 규칙",
    "count": "{{count}}개 규칙",
    "number": "번호",
    "to": "대상",
    "action": "동작",
    "from": "출발지",
    "comment": "설명",
    "addRule": "규칙 추가",
    "deleteRule": "규칙 삭제",
    "deleteConfirm": "규칙 #{{number}}을(를) 삭제하시겠습니까?",
    "noRules": "방화벽 규칙이 없습니다",
    "allow": "허용",
    "deny": "차단",
    "reject": "거부",
    "limit": "제한",
    "port": "포트",
    "protocol": "프로토콜",
    "fromIP": "출발지 IP",
    "any": "전체",
    "tcp": "TCP",
    "udp": "UDP",
    "both": "TCP/UDP"
  },
  "ports": {
    "title": "열린 포트",
    "count": "{{count}}개 포트",
    "protocol": "프로토콜",
    "address": "주소",
    "port": "포트",
    "process": "프로세스",
    "pid": "PID",
    "addToUFW": "UFW 규칙 추가",
    "noPorts": "열린 포트가 없습니다"
  },
  "fail2ban": {
    "title": "Fail2ban",
    "notInstalled": "Fail2ban이 설치되지 않았습니다",
    "install": "설치",
    "installing": "설치 중...",
    "running": "실행 중",
    "stopped": "중지됨",
    "version": "버전",
    "jails": "Jail 목록",
    "jailCount": "{{count}}개 Jail",
    "name": "이름",
    "status": "상태",
    "enabled": "활성",
    "disabled": "비활성",
    "bannedCount": "차단 IP",
    "totalBanned": "누적 차단",
    "bannedIPs": "차단된 IP 목록",
    "unban": "차단 해제",
    "unbanConfirm": "{{ip}}의 차단을 해제하시겠습니까?",
    "noJails": "Jail이 없습니다",
    "noBannedIPs": "차단된 IP가 없습니다",
    "enable": "활성화",
    "disable": "비활성화"
  }
}
```

Add `layout.nav.firewall` key:
```json
"layout": {
  "nav": {
    "firewall": "방화벽"
  }
}
```

Add equivalent English translations to `en.json`:
```json
"firewall": {
  "title": "Firewall",
  "tabs": {
    "rules": "UFW Rules",
    "ports": "Open Ports",
    "fail2ban": "Fail2ban"
  },
  "status": {
    "title": "Firewall Status",
    "active": "Active",
    "inactive": "Inactive",
    "defaultIncoming": "Default Incoming",
    "defaultOutgoing": "Default Outgoing",
    "enable": "Enable",
    "disable": "Disable",
    "enableConfirm": "Are you sure you want to enable the firewall?",
    "disableConfirm": "Are you sure you want to disable the firewall? Your server will be unprotected."
  },
  "rules": {
    "title": "Firewall Rules",
    "count": "{{count}} rules",
    "number": "#",
    "to": "To",
    "action": "Action",
    "from": "From",
    "comment": "Comment",
    "addRule": "Add Rule",
    "deleteRule": "Delete Rule",
    "deleteConfirm": "Are you sure you want to delete rule #{{number}}?",
    "noRules": "No firewall rules",
    "allow": "Allow",
    "deny": "Deny",
    "reject": "Reject",
    "limit": "Limit",
    "port": "Port",
    "protocol": "Protocol",
    "fromIP": "Source IP",
    "any": "Any",
    "tcp": "TCP",
    "udp": "UDP",
    "both": "TCP/UDP"
  },
  "ports": {
    "title": "Open Ports",
    "count": "{{count}} ports",
    "protocol": "Protocol",
    "address": "Address",
    "port": "Port",
    "process": "Process",
    "pid": "PID",
    "addToUFW": "Add UFW Rule",
    "noPorts": "No open ports"
  },
  "fail2ban": {
    "title": "Fail2ban",
    "notInstalled": "Fail2ban is not installed",
    "install": "Install",
    "installing": "Installing...",
    "running": "Running",
    "stopped": "Stopped",
    "version": "Version",
    "jails": "Jails",
    "jailCount": "{{count}} Jails",
    "name": "Name",
    "status": "Status",
    "enabled": "Enabled",
    "disabled": "Disabled",
    "bannedCount": "Banned IPs",
    "totalBanned": "Total Banned",
    "bannedIPs": "Banned IP List",
    "unban": "Unban",
    "unbanConfirm": "Are you sure you want to unban {{ip}}?",
    "noJails": "No jails",
    "noBannedIPs": "No banned IPs",
    "enable": "Enable",
    "disable": "Disable"
  }
}
```

And `layout.nav.firewall` in en.json:
```json
"layout": {
  "nav": {
    "firewall": "Firewall"
  }
}
```

**Step 2: Commit**

```bash
git add web/src/i18n/locales/ko.json web/src/i18n/locales/en.json
git commit -m "feat: 방화벽 다국어 번역 추가 (ko/en)"
```

---

### Task 6: Frontend — Page Components

**Files:**
- Create: `web/src/pages/Firewall.tsx`
- Create: `web/src/pages/firewall/FirewallRules.tsx`
- Create: `web/src/pages/firewall/FirewallPorts.tsx`
- Create: `web/src/pages/firewall/FirewallFail2ban.tsx`
- Modify: `web/src/App.tsx` (add route)
- Modify: `web/src/components/Layout.tsx` (add nav item with `Shield` icon from lucide-react)

**Step 1: Create Firewall.tsx tab container**

Follow exact pattern from `Disk.tsx`:
- Import all 3 sub-components
- Use `<Tabs>` with `defaultValue="rules"`
- 3 `<TabsTrigger>` with `className="rounded-lg text-[13px]"`

**Step 2: Create FirewallRules.tsx**

Main functionality:
- Status card at top showing active/inactive, default policies, enable/disable toggle
- "Add Rule" dialog with fields: action (allow/deny/reject/limit), port, protocol (tcp/udp/both), from IP, comment
- Rules table wrapped in card div: number, to, action (with color-coded pill), from, comment, delete button
- Confirmation dialog for enable/disable and delete

UI patterns (from CLAUDE.md design system):
- Cards: `bg-card rounded-2xl p-5 card-shadow`
- Status pills: `bg-[#00c471]/10 text-[#00c471]` for active, `bg-[#f04452]/10 text-[#f04452]` for inactive
- Action pills: `bg-[#00c471]/10 text-[#00c471]` for ALLOW, `bg-[#f04452]/10 text-[#f04452]` for DENY/REJECT
- Table wrapped in `bg-card rounded-2xl card-shadow overflow-hidden`
- Count pill: `inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary`

**Step 3: Create FirewallPorts.tsx**

- Table of listening ports: protocol, address, port, PID, process
- "Add to UFW" button per row that opens the AddRule dialog pre-filled with port/protocol
- Table wrapped in card div

**Step 4: Create FirewallFail2ban.tsx**

- Status card: installed (yes/no), running/stopped, version. Install button if not installed.
- Jail list table: name, enabled status pill, banned count, total banned, enable/disable toggle
- Expandable jail detail: banned IP list with unban buttons
- Confirmation dialog for unban

**Step 5: Register in App.tsx and Layout.tsx**

App.tsx — add after `<Route path="disk" ...>`:
```tsx
import Firewall from '@/pages/Firewall'
// ...
<Route path="firewall" element={<Firewall />} />
```

Layout.tsx — add nav item after disk, before packages:
```tsx
import { Shield } from 'lucide-react'
// In navItems array:
{ to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield },
```

**Step 6: Commit**

```bash
git add web/src/pages/Firewall.tsx web/src/pages/firewall/ web/src/App.tsx web/src/components/Layout.tsx
git commit -m "feat: 방화벽 프론트엔드 페이지 구현"
```

---

### Task 7: Specs Update + Version Bump + Release

**Files:**
- Modify: `docs/specs/api-spec.md` — add firewall + fail2ban API section
- Modify: `docs/specs/frontend-spec.md` — add Firewall page section
- Modify: `cmd/sfpanel/main.go` — version 0.2.0 → 0.3.0
- Modify: `web/package.json` — version 0.2.0 → 0.3.0

**Step 1: Update specs**

**Step 2: Bump version to 0.3.0**

**Step 3: Commit, tag, and push**

```bash
git add -A
git commit -m "feat: 방화벽 관리 기능 v0.3.0"
git tag -a v0.3.0 -m "v0.3.0: 방화벽 관리 (UFW + Fail2ban)"
git push origin main --tags
```
