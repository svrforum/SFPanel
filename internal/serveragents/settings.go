// Package serveragents is the I/O-free part of deploying the monitoring and
// security agents — Zabbix Agent 2 and the Wazuh agent — to cluster servers:
// the central connection values and their checks, and reading what a host's
// files and commands say. Whatever reads the files and runs the commands
// hands the bytes here.
package serveragents

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The agents this package knows, by the name a task step and the API use.
const (
	Zabbix = "zabbix"
	Wazuh  = "wazuh"
)

// Names lists the agents in the order the panel shows them.
var Names = []string{Zabbix, Wazuh}

const (
	// DefaultWazuhVersion is the agent version installed when none is chosen.
	// An agent newer than its manager is refused by the manager, and the
	// vendor repository's newest is often ahead of it, so the version is
	// fixed rather than "latest".
	DefaultWazuhVersion = "4.14.10"
	// ZabbixActivePort is where an agent reaches its server when ServerActive
	// names no port.
	ZabbixActivePort = 10051
)

// Settings are the central connection values every server's agent is set to.
// They are replicated across the cluster; nothing here is per node.
type Settings struct {
	Zabbix    ZabbixSettings `json:"zabbix"`
	Wazuh     WazuhSettings  `json:"wazuh"`
	UpdatedAt time.Time      `json:"updated_at,omitzero"`
	UpdatedBy string         `json:"updated_by,omitempty"`
}

// ZabbixSettings are the values written into zabbix_agent2.conf.
type ZabbixSettings struct {
	Server       string `json:"server"`        // passive: comma list of hosts or CIDRs
	ServerActive string `json:"server_active"` // host[:port]
	HostMetadata string `json:"host_metadata"`
}

// WazuhSettings are the values written into ossec.conf, and the agent
// version to install.
type WazuhSettings struct {
	Manager string `json:"manager"`
	Groups  string `json:"groups"`  // comma list; "" = default
	Version string `json:"version"` // x.y.z
}

// FieldError names the first value Validate refused. Field is the value's
// JSON path: zabbix.server, zabbix.server_active, zabbix.host_metadata,
// wazuh.manager, wazuh.groups or wazuh.version.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

const (
	maxServerEntries = 16
	maxHostMetadata  = 255
	maxGroups        = 255
	maxHostName      = 253
)

var (
	hostNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	groupsRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+(,[A-Za-z0-9_.-]+)*$`)
	versionRe  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	portRe     = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// Normalize trims every value, drops the empty entries of the comma lists,
// and puts in DefaultWazuhVersion when no version is chosen. Validate checks
// the normalized values; it does not normalize them itself.
func (s Settings) Normalize() Settings {
	s.Zabbix.Server = normalizeList(s.Zabbix.Server)
	s.Zabbix.ServerActive = strings.TrimSpace(s.Zabbix.ServerActive)
	s.Zabbix.HostMetadata = strings.TrimSpace(s.Zabbix.HostMetadata)
	s.Wazuh.Manager = strings.TrimSpace(s.Wazuh.Manager)
	s.Wazuh.Groups = normalizeList(s.Wazuh.Groups)
	s.Wazuh.Version = strings.TrimSpace(s.Wazuh.Version)
	if s.Wazuh.Version == "" {
		s.Wazuh.Version = DefaultWazuhVersion
	}
	return s
}

func normalizeList(v string) string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return strings.Join(out, ",")
}

// Validate refuses any value that is not what its field holds — above all
// anything that could add a line or an element to the agent's config file.
// Nothing set at all is valid: an agent without values is simply not
// configured. Zabbix needs both addresses once any of its values is set;
// Wazuh needs a manager once groups are set, and a version once a manager is.
func (s Settings) Validate() *FieldError {
	z, w := s.Zabbix, s.Wazuh

	zabbixUsed := z.Server != "" || z.ServerActive != "" || z.HostMetadata != ""
	if z.Server == "" && zabbixUsed {
		return fieldError("zabbix.server", "required when any Zabbix value is set")
	}
	if z.Server != "" {
		if msg := checkServerList(z.Server); msg != "" {
			return fieldError("zabbix.server", msg)
		}
	}
	if z.ServerActive == "" && zabbixUsed {
		return fieldError("zabbix.server_active", "required when any Zabbix value is set")
	}
	if z.ServerActive != "" {
		if _, _, msg := parseActive(z.ServerActive); msg != "" {
			return fieldError("zabbix.server_active", msg)
		}
	}
	if msg := checkHostMetadata(z.HostMetadata); msg != "" {
		return fieldError("zabbix.host_metadata", msg)
	}

	if w.Manager == "" && w.Groups != "" {
		return fieldError("wazuh.manager", "required when groups are set")
	}
	if w.Manager != "" {
		if msg := checkHost(w.Manager); msg != "" {
			return fieldError("wazuh.manager", msg)
		}
	}
	if w.Groups != "" && (len(w.Groups) > maxGroups || !groupsRe.MatchString(w.Groups)) {
		return fieldError("wazuh.groups", fmt.Sprintf("must be comma-separated names of letters, digits, _ . - (%d characters at most)", maxGroups))
	}
	if (w.Version != "" || w.Manager != "") && !versionRe.MatchString(w.Version) {
		return fieldError("wazuh.version", "must be x.y.z, like "+DefaultWazuhVersion)
	}
	return nil
}

func fieldError(field, msg string) *FieldError { return &FieldError{Field: field, Message: msg} }

func checkServerList(v string) string {
	items := strings.Split(v, ",")
	if len(items) > maxServerEntries {
		return fmt.Sprintf("at most %d entries", maxServerEntries)
	}
	for _, item := range items {
		if checkHost(item) == "" {
			continue
		}
		if _, err := netip.ParsePrefix(item); err == nil {
			continue
		}
		if _, _, msg := parseActive(item); msg == "" {
			return fmt.Sprintf("%q: Server takes no port, only an address, a CIDR range or a host name", item)
		}
		if hasZone(item) {
			return zoneMessage(item)
		}
		return fmt.Sprintf("%q is not an IP address, a CIDR range or a host name", item)
	}
	return ""
}

func checkHostMetadata(v string) string {
	if len(v) > maxHostMetadata {
		return fmt.Sprintf("longer than %d bytes", maxHostMetadata)
	}
	if !utf8.ValidString(v) {
		return "not valid UTF-8"
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return "has a control character such as a line break"
		}
	}
	return ""
}

// checkHost accepts an IPv4 or IPv6 address or a DNS host name and returns
// why anything else is not one.
func checkHost(v string) string {
	if hasZone(v) {
		return zoneMessage(v)
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return ""
	}
	if len(v) <= maxHostName && hostNameRe.MatchString(v) {
		return ""
	}
	return fmt.Sprintf("%q is not an IP address or a host name", v)
}

// hasZone reports an IPv6 address with a zone. netip takes any bytes after
// the % as the zone, line breaks included, so a zone is never accepted.
func hasZone(v string) bool {
	a, err := netip.ParseAddr(v)
	return err == nil && a.Zone() != ""
}

func zoneMessage(v string) string {
	return fmt.Sprintf("%q: an IPv6 zone is not allowed", v)
}

// parseActive reads one ServerActive address — host, host:port or
// [IPv6]:port — and returns why it is not one when it is not.
func parseActive(v string) (host string, port int, msg string) {
	bad := fmt.Sprintf("%q is not one host, host:port or [IPv6]:port", v)
	if strings.HasPrefix(v, "[") {
		h, p, err := net.SplitHostPort(v)
		if err != nil {
			return "", 0, bad
		}
		if a, err := netip.ParseAddr(h); err != nil || !a.Is6() || a.Zone() != "" {
			return "", 0, bad
		}
		n, msg := parsePort(p)
		if msg != "" {
			return "", 0, msg
		}
		return h, n, ""
	}
	if checkHost(v) == "" {
		return v, ZabbixActivePort, ""
	}
	i := strings.LastIndexByte(v, ':')
	if i < 0 {
		return "", 0, bad
	}
	h, p := v[:i], v[i+1:]
	if strings.Contains(h, ":") || checkHost(h) != "" {
		return "", 0, bad
	}
	n, msg := parsePort(p)
	if msg != "" {
		return "", 0, msg
	}
	return h, n, ""
}

func parsePort(p string) (int, string) {
	if portRe.MatchString(p) {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 65535 {
			return n, ""
		}
	}
	return 0, fmt.Sprintf("port %q must be 1-65535", p)
}

// Configured reports whether the agent has the values a deployment needs:
// both Zabbix addresses, or the Wazuh manager. An unknown agent is never
// configured.
func (s Settings) Configured(agent string) bool {
	switch agent {
	case Zabbix:
		return strings.TrimSpace(s.Zabbix.Server) != "" && strings.TrimSpace(s.Zabbix.ServerActive) != ""
	case Wazuh:
		return strings.TrimSpace(s.Wazuh.Manager) != ""
	}
	return false
}

// ActiveAddress is the host and port an agent with this ServerActive
// connects to, with ZabbixActivePort when the value names no port. A value
// that is not one address, or none, gives ("", 0).
func (z ZabbixSettings) ActiveAddress() (host string, port int) {
	v := strings.TrimSpace(z.ServerActive)
	if v == "" {
		return "", 0
	}
	h, p, msg := parseActive(v)
	if msg != "" {
		return "", 0
	}
	return h, p
}

// SameHost reports whether two manager or server addresses name the same
// host: spaces around them and letter case do not count, and two spellings
// of one IP address are the same address.
func SameHost(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.EqualFold(a, b) {
		return true
	}
	ia, errA := netip.ParseAddr(a)
	ib, errB := netip.ParseAddr(b)
	return errA == nil && errB == nil && ia == ib
}
