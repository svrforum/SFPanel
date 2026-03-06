package handlers

import (
	"regexp"
)

// FirewallHandler exposes REST handlers for UFW firewall and Fail2ban management.
type FirewallHandler struct{}

// ---------- Types ----------

// UFWStatus represents the current state of UFW.
type UFWStatus struct {
	Active          bool   `json:"active"`
	DefaultIncoming string `json:"default_incoming"`
	DefaultOutgoing string `json:"default_outgoing"`
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

// validBanTime matches fail2ban time values: plain seconds or expressions like 10m, 1h, 1d, -1.
var validBanTime = regexp.MustCompile(`^-?\d+[smhdw]?$`)
