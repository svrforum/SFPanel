package serveragents

import (
	"bufio"
	"bytes"
	"strings"
	"unicode/utf8"
)

// AgentdState is what the Wazuh agent writes about its own connection to the
// manager in wazuh-agentd.state, which it rewrites every few seconds while it
// runs.
type AgentdState struct{ Status, LastKeepalive, LastAck string }

// ParseAgentdState reads a wazuh-agentd.state file: key='value' lines under a
// comment block that explains them.
func ParseAgentdState(b []byte) AgentdState {
	var s AgentdState
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = unquoteOSRelease(value)
		switch strings.TrimSpace(key) {
		case "status":
			s.Status = value
		case "last_keepalive":
			s.LastKeepalive = value
		case "last_ack":
			s.LastAck = value
		}
	}
	return s
}

// LogClass is what a Zabbix Agent 2 log line says about the agent's active
// checks reaching its server.
type LogClass string

const (
	LogNothing     LogClass = ""
	LogOK          LogClass = "ok"
	LogUnreachable LogClass = "unreachable"
	LogNotFound    LogClass = "not_found"
)

// zabbixStartMarker opens every run of the agent in its log.
const zabbixStartMarker = "starting zabbix agent 2"

// zabbixUnreachable are the wordings of a failed connection to the server.
var zabbixUnreachable = []string{
	"cannot connect",
	"started to fail",
	"connection refused",
	"i/o timeout",
	"no route to host",
	"connection reset",
}

// maxLogLine bounds the log line handed back with its class.
const maxLogLine = 300

// ClassifyZabbixLog reads the end of zabbix_agent2.log: the last classified
// line after the last "starting zabbix agent 2", and that line (trimmed, at
// most 300 bytes). A tail without the start line is taken whole, since the
// agent started before the oldest line in it. Lines are compared lowercased.
func ClassifyZabbixLog(tail []byte) (LogClass, string) {
	class, last := LogNothing, ""
	for raw := range strings.Lines(string(tail)) {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if strings.Contains(lower, zabbixStartMarker) {
			class, last = LogNothing, ""
			continue
		}
		if c := classifyZabbixLine(lower); c != LogNothing {
			class, last = c, line
		}
	}
	return class, cutLogLine(last)
}

func classifyZabbixLine(lower string) LogClass {
	if strings.Contains(lower, "host [") && strings.Contains(lower, "not found") {
		return LogNotFound
	}
	if strings.Contains(lower, "is working again") {
		return LogOK
	}
	for _, w := range zabbixUnreachable {
		if strings.Contains(lower, w) {
			return LogUnreachable
		}
	}
	return LogNothing
}

// cutLogLine shortens a line to maxLogLine bytes without splitting a
// character.
func cutLogLine(line string) string {
	if len(line) <= maxLogLine {
		return line
	}
	n := maxLogLine
	for n > 0 && !utf8.RuneStart(line[n]) {
		n--
	}
	return line[:n]
}
