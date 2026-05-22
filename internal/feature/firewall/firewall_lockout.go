package firewall

import (
	"regexp"
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

// wouldLockOutOnAdd reports whether adding the proposed rule would block
// remote admin access to SSH (port 22) or the panel port. Mirrors
// ruleAllowsPort's reverse: if the rule is deny/reject/limit AND its
// destination port matches SSH or panelPort, the answer is yes.
//
// Used by AddRule with the same ?force=true override pattern as
// EnableUFW and DeleteRule — the guard is opt-out, not absolute.
func wouldLockOutOnAdd(req AddRuleRequest, panelPort int) bool {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "deny", "reject", "limit":
	default:
		return false
	}
	port := strings.TrimSpace(req.Port)
	if port == "" {
		return false
	}
	// Strip optional /proto (UFW accepts both "22" and "22/tcp" here, but
	// our request model splits port and protocol — defensive trim only).
	if i := strings.Index(port, "/"); i >= 0 {
		port = port[:i]
	}
	// App profile names that map to SSH.
	switch port {
	case "OpenSSH", "SSH":
		return true
	}
	// Port range like "20:25".
	if i := strings.Index(port, ":"); i >= 0 {
		lo, errLo := strconv.Atoi(port[:i])
		hi, errHi := strconv.Atoi(port[i+1:])
		if errLo != nil || errHi != nil {
			return false
		}
		return (SSHPort >= lo && SSHPort <= hi) || (panelPort >= lo && panelPort <= hi)
	}
	// Bare port number.
	n, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	return n == SSHPort || n == panelPort
}

// loadUFWRulesForLockoutCheck returns the rule set that would apply if UFW
// were enabled. `ufw status numbered` only lists the live ruleset; when UFW
// is inactive that output is empty even if the operator has pre-staged
// `ufw allow 22` (those rules live in /etc/ufw/user.rules regardless of
// UFW's enabled state). For the lockout precheck on EnableUFW we need to
// see those staged rules, so we fall back to `ufw show added` — which
// lists pre-staged ALLOW/DENY rules as `ufw allow …` lines — and parse
// that instead.
func (h *Handler) loadUFWRulesForLockoutCheck() ([]UFWRule, error) {
	out, err := h.Cmd.RunWithEnv([]string{"LANG=C"}, "ufw", "status", "numbered")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(out), "status: inactive") {
		return parseUFWRules(out), nil
	}
	// Inactive: try `ufw show added` for pre-staged rules. If it fails for
	// any reason, fall back to the (empty) parsed status output — the
	// caller will refuse to enable, which is the safer default; the
	// operator can still pass force=true.
	addedOut, addedErr := h.Cmd.RunWithEnv([]string{"LANG=C"}, "ufw", "show", "added")
	if addedErr != nil {
		return parseUFWRules(out), nil
	}
	return parseUFWAddedOutput(addedOut), nil
}

// ufwAddedLineRe matches lines like "ufw allow 22/tcp" or
// "ufw allow from 192.168.1.0/24" emitted by `ufw show added`.
var ufwAddedLineRe = regexp.MustCompile(`^ufw allow\s+(.+)$`)

// ufwAddedPortRe matches a bare port token, optionally suffixed with
// /tcp or /udp — the only forms the lockout check needs to recognize.
var ufwAddedPortRe = regexp.MustCompile(`^(\d+)(?:/(?:tcp|udp))?$`)

// parseUFWAddedOutput parses `ufw show added` into UFWRule entries
// sufficient for hasAccessRule. Only ALLOW rules with a numeric port are
// returned — DENY rules don't help against lockout, and rules without a
// port (e.g. "allow from <CIDR>") are skipped because hasAccessRule
// matches on the .To port. App-profile names like "OpenSSH" are also
// preserved verbatim in .To so ruleAllowsPort's profile branch fires.
func parseUFWAddedOutput(out string) []UFWRule {
	rules := make([]UFWRule, 0)
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Added user rules") {
			continue
		}
		m := ufwAddedLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		spec := strings.TrimSpace(m[1])
		tokens := strings.Fields(spec)
		if len(tokens) == 0 {
			continue
		}
		first := tokens[0]
		// Skip clauses that don't lead with a port — `from <addr>`,
		// `to <addr>`, `in on <iface>`, etc. ruleAllowsPort can't match
		// these against a port number anyway.
		if first == "from" || first == "to" || first == "in" || first == "out" || first == "on" {
			continue
		}
		// App-profile name (e.g. "OpenSSH") — pass through so
		// ruleAllowsPort's profile mapping handles it.
		if portMatch := ufwAddedPortRe.FindStringSubmatch(first); portMatch != nil {
			port, err := strconv.Atoi(portMatch[1])
			if err != nil {
				continue
			}
			rules = append(rules, UFWRule{
				Action: "ALLOW",
				To:     strconv.Itoa(port),
			})
			continue
		}
		// Non-numeric leading token: treat as an app-profile name and
		// hand it to ruleAllowsPort verbatim.
		rules = append(rules, UFWRule{
			Action: "ALLOW",
			To:     first,
		})
	}
	return rules
}
