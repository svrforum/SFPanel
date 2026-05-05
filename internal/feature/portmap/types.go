package portmap

// PortMapRow is the canonical row returned by GET /api/v1/system/portmap.
// Each non-nil pointer reflects one of the three data sources.
type PortMapRow struct {
	Port      int            `json:"port"`
	Proto     string         `json:"proto"` // "tcp" | "udp"
	State     string         `json:"state"` // "listening" | "bound"
	Firewall  *FirewallInfo  `json:"firewall"`
	Container *ContainerInfo `json:"container"`
	Process   *ProcessInfo   `json:"process"`
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
type PortBinding struct {
	HostPort      int
	ContainerID   string
	ContainerName string
	Stack         string
}
