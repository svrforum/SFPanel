package firewall

import (
	"strconv"
	"strings"
)

// SSHPort is the standard ssh port. Hardcoded because the lockout guard's
// whole point is preventing an operator from losing the well-known remote
// admin channel; if they're running sshd elsewhere they can pass force=true.
const SSHPort = 22

// ruleAllowsPort reports whether the rule is an ALLOW that grants access
// to the given numeric port. UFW's "To" field can be:
//   - a bare port ("22")
//   - port/proto ("22/tcp")
//   - a port range ("20:25/tcp")
//   - an app profile name ("OpenSSH", "Nginx Full") that resolves to ports
//
// We handle numbers and ranges directly, plus a small set of well-known app
// profiles. Unknown app profiles fall through to "doesn't match" — operators
// using app profiles can still pass force=true.
func ruleAllowsPort(rule UFWRule, port int) bool {
	if !strings.HasPrefix(strings.ToUpper(rule.Action), "ALLOW") {
		return false
	}
	to := strings.TrimSpace(rule.To)
	if to == "" {
		return false
	}
	// Strip optional /proto.
	if i := strings.Index(to, "/"); i >= 0 {
		to = to[:i]
	}
	// App profile names map to one or more ports.
	switch to {
	case "OpenSSH", "SSH":
		return port == SSHPort
	}
	// Port range like "20:25".
	if i := strings.Index(to, ":"); i >= 0 {
		lo, errLo := strconv.Atoi(to[:i])
		hi, errHi := strconv.Atoi(to[i+1:])
		if errLo != nil || errHi != nil {
			return false
		}
		return port >= lo && port <= hi
	}
	// Bare port number.
	n, err := strconv.Atoi(to)
	if err != nil {
		return false
	}
	return n == port
}

// hasAccessRule returns true when the rule set contains an ALLOW for SSH
// (port 22) OR for the panel port. Either is enough to prevent the operator
// from being locked out by an EnableUFW that flips default-deny.
func hasAccessRule(rules []UFWRule, panelPort int) bool {
	for _, r := range rules {
		if ruleAllowsPort(r, SSHPort) || ruleAllowsPort(r, panelPort) {
			return true
		}
	}
	return false
}
