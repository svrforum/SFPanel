package firewall

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// Handler exposes REST handlers for UFW firewall and Fail2ban management.
type Handler struct {
	Cmd exec.Commander
	// PanelPort is the HTTP port sfpanel listens on (cfg.Server.Port).
	// Used by the SSH-lockout guard so deleting an ALLOW rule for this
	// port — or enabling UFW with no allow rule for either SSH or this
	// port — requires explicit force=true confirmation.
	PanelPort int
	// dockerRuleMu serializes DOCKER-USER chain mutations. iptables rules are
	// addressed by line number, so a concurrent add/delete that renumbers the
	// chain mid-operation can delete the wrong rule (the companion-LOG cleanup
	// in DeleteDockerUserRule adjusts indices by hand). Serializing add/delete
	// keeps the numbering stable for the duration of each operation.
	dockerRuleMu sync.Mutex
}

// requireTool returns a structured "not installed" response when the named
// tool is missing from PATH. Mutation handlers call this at entry to keep
// remote cluster nodes (which may legitimately lack ufw / fail2ban /
// iptables) from returning a raw 500 — the proxied UI relies on
// ErrToolNotInstalled being recognizable so it can show an actionable hint
// instead of a stack trace.
//
// ok is false when it has answered, and the handler returns err. It used to
// return only response.Fail's error, which is nil once the answer is
// written, so every handler carried on past it: with ufw missing, Enable
// wrote the 501, ran ufw anyway and appended a second body to the response.
func requireTool(c echo.Context, cmd exec.Commander, tool string) (ok bool, err error) {
	if cmd.Exists(tool) {
		return true, nil
	}
	return false, response.Fail(c, http.StatusNotImplemented, response.ErrToolNotInstalled,
		fmt.Sprintf("%s is not installed on this node", tool))
}

// ---------- Types ----------

// UFWStatus represents the current state of UFW.
type UFWStatus struct {
	Active          bool   `json:"active"`
	DefaultIncoming string `json:"default_incoming"`
	DefaultOutgoing string `json:"default_outgoing"`
	// Installed is whether ufw is on this host at all. Debian and Proxmox VE
	// install none, which is not the same as a firewall turned off.
	Installed bool `json:"installed"`
	// Manager is what the page manages this host's firewall with: "ufw";
	// "pve-firewall" on a Proxmox VE host not already running ufw, where the
	// ufw changes are refused; or "none" when there is no ufw to manage.
	Manager string `json:"manager"`
	// PVE is pve-firewall's own status, on a Proxmox VE host.
	PVE *PVEFirewallStatus `json:"pve,omitempty"`
}

// Firewall managers, as GET /firewall/status names them.
const (
	managerUFW  = "ufw"
	managerPVE  = "pve-firewall"
	managerNone = "none"
)

// PVEFirewallStatus is `pve-firewall status`: whether the datacenter
// firewall is enabled, and whether the pve-firewall daemon runs.
type PVEFirewallStatus struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Raw     string `json:"raw"`
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
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Filter      string   `json:"filter"`
	BannedCount int      `json:"banned_count"`
	TotalBanned int      `json:"total_banned"`
	MaxRetry    int      `json:"max_retry"`
	BanTime     string   `json:"ban_time"`
	FindTime    string   `json:"find_time"`
	IgnoreIP    string   `json:"ignoreip"`
	BannedIPs   []string `json:"banned_ips"`
}

// ---------- Validation ----------

// validPort matches port numbers, ranges (e.g., "8000:8080"), and service names.
var validPort = regexp.MustCompile(`^(?:[a-zA-Z0-9_\-]+(?::[a-zA-Z0-9_\-]+)?|\d+(?::\d+)?(?:,\d+(?::\d+)?)+)$`)

// isValidIPOrCIDR validates an IP address or CIDR notation using net.ParseIP and net.ParseCIDR.
func isValidIPOrCIDR(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

// validIP is kept for backward compatibility but delegates to isValidIPOrCIDR.
// It is used as a simple MatchString check, so we wrap it.
type ipValidator struct{}

func (ipValidator) MatchString(s string) bool {
	return isValidIPOrCIDR(s)
}

var validIP = ipValidator{}

// validProtocol matches allowed protocol values.
var validProtocol = regexp.MustCompile(`^(tcp|udp|any)$`)

// validAction matches allowed UFW rule actions.
var validAction = regexp.MustCompile(`^(allow|deny|reject|limit)$`)

// validJailName matches safe fail2ban jail names.
var validJailName = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// validComment matches safe comment text. `#` is intentionally excluded:
// `ufw status numbered` prints the rule as `TO FROM ACTION # COMMENT`, and
// parseUFWRules splits on the last `#` to extract the comment, so a `#`
// inside the comment text would corrupt the parsed To/From/Action fields.
var validComment = regexp.MustCompile(`^[\p{L}\p{N} _\-.,()/:@!]+$`)

// validBanTime matches fail2ban time values: plain seconds or expressions like 10m, 1h, 1d, -1.
var validBanTime = regexp.MustCompile(`^-?\d+[smhdw]?$`)
