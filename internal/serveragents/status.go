package serveragents

import "time"

// How an agent is installed on a host.
const (
	StateNone      = "none"      // not there
	StateInstalled = "installed" // its package is installed
	StateLegacy    = "legacy"    // only the old Zabbix agent (agentd), which is left alone
	StateUnmanaged = "unmanaged" // there, but not from its package; looked at, not changed
)

// Whether an agent reaches its server, as the host sees it.
const (
	ConnConnected     = "connected"
	ConnPending       = "pending"      // Wazuh: waiting for the manager
	ConnDisconnected  = "disconnected" // Wazuh: lost the manager
	ConnNever         = "never"        // Wazuh: no state file, never connected
	ConnStale         = "stale"        // Wazuh: the state file stopped changing
	ConnUnreachable   = "unreachable"  // Zabbix: no TCP connection to ServerActive
	ConnNotRegistered = "not_registered"
	ConnFailing       = "failing" // Zabbix: TCP connects, the active checks fail
	ConnNotRunning    = "not_running"
	ConnNoServer      = "no_server" // Zabbix: no ServerActive to reach
	ConnUnknown       = "unknown"   // cannot be told: the service is starting or reloading, Zabbix has no log to read or several servers, Wazuh a status it does not know
)

// Connection is whether an agent reaches its server, judged from the agent's
// side.
type Connection struct {
	State string `json:"state"`
	// Detail is why, in the words of the dial error or the log line, with
	// anything that looks like a secret taken out.
	Detail        string `json:"detail,omitempty"`
	LastKeepalive string `json:"last_keepalive,omitempty"`
	LastAck       string `json:"last_ack,omitempty"`
	// Transitional reports an agent whose service is starting or reloading:
	// how it connects is about to be known, so a wait for it goes on.
	Transitional bool `json:"-"`
}

// ZabbixStatus is a host's Zabbix agent.
type ZabbixStatus struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
	// Service is what systemctl is-active says about zabbix-agent2.
	Service    string      `json:"service,omitempty"`
	Config     *ZabbixConf `json:"config,omitempty"`
	Connection *Connection `json:"connection,omitempty"`
}

// WazuhStatus is a host's Wazuh agent.
type WazuhStatus struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Held    bool   `json:"held,omitempty"`
	// Service is what systemctl is-active says about wazuh-agent.
	Service string     `json:"service,omitempty"`
	Config  *OssecConf `json:"config,omitempty"`
	// ConfigError is why ossec.conf could not be read, when it could not.
	ConfigError string      `json:"config_error,omitempty"`
	Connection  *Connection `json:"connection,omitempty"`
}

// Status is what one host says about its agents.
type Status struct {
	OS        OS           `json:"os"`
	Zabbix    ZabbixStatus `json:"zabbix"`
	Wazuh     WazuhStatus  `json:"wazuh"`
	CheckedAt time.Time    `json:"checked_at"`
}
