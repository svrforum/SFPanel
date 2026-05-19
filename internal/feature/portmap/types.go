package portmap

// PortMapRow is the canonical row returned by GET /api/v1/system/portmap.
// Firewall + Process stay singular (at most one rule, at most one host
// listener per port/proto). Containers is a slice so a stack that publishes
// the same host port from multiple replicas — or two stacks colliding on
// the same port — surfaces every owner instead of last-write-winning.
type PortMapRow struct {
	Port       int             `json:"port"`
	Proto      string          `json:"proto"` // "tcp" | "udp"
	State      string          `json:"state"` // "listening" | "bound"
	Firewall   *FirewallInfo   `json:"firewall"`
	Containers []ContainerInfo `json:"containers"`
	Process    *ProcessInfo    `json:"process"`
}

// FirewallInfo captures the UFW rule that affects this port.
type FirewallInfo struct {
	Action string `json:"action"`  // "ALLOW" | "DENY" | "REJECT" | "LIMIT"
	Scope  string `json:"scope"`   // "Anywhere" | "192.168.1.0/24" | …
	RuleID int    `json:"rule_id"` // UFW rule number
	Source string `json:"source"`  // "ufw"
}

// ContainerInfo captures the Docker DNAT mapping for this port.
type ContainerInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Stack string `json:"stack"` // "" if not part of a compose stack
}

// ProcessInfo captures the bare host process listening on this port.
type ProcessInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// SsEntry is one parsed entry from `ss -tlnp -H` / `ss -ulnp -H`.
// Multiple users on one socket emit one entry each.
type SsEntry struct {
	Port  int
	Proto string // "tcp" | "udp"
	PID   int
	Name  string
}

// PortBinding is the simplified Docker DNAT mapping passed to Aggregate.
// Proto is "tcp" or "udp"; before plumbing it through, every binding was
// silently aggregated as tcp, so UDP-only services (DNS, WireGuard, syslog)
// either dropped off the table entirely or collided with an unrelated tcp
// row that happened to share the same host port.
type PortBinding struct {
	HostPort      int
	Proto         string
	ContainerID   string
	ContainerName string
	Stack         string
}
