package serveragents

import (
	"bufio"
	"bytes"
	"strings"
	"time"
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

// LogClass is what Zabbix Agent 2's log says about its active checks
// reaching its server.
type LogClass string

const (
	LogNothing LogClass = ""
	LogOK      LogClass = "ok"
	// LogUnreachable: the agent could not connect to its server.
	LogUnreachable LogClass = "unreachable"
	// LogNotFound: the server answered that it does not know the host.
	LogNotFound LogClass = "not_found"
	// LogRefused: the server answered and gave the host no active checks,
	// for a reason other than not knowing it.
	LogRefused LogClass = "refused"
)

// What agent 2 7.0 writes about its active checks, lowercased. It writes a
// line when their state changes, not at every request: a refusal is "no
// active checks on server [s]: <why>" followed by "active checks on server
// started to fail", and its end "active checks on server are active again";
// a server it cannot reach is "cannot connect to [s]: <dial error>"
// followed by "<what> started to fail", and its end "<what> … is working
// again". Agent 7.0.10 is the exception: it repeats the refusal at every
// refresh and writes nothing when the server takes the host.
const (
	zabbixStartMarker   = "starting zabbix agent 2"
	zabbixNoChecks      = "no active checks on server"
	zabbixChecksFailing = "active checks on server started to fail"
	zabbixChecksAgain   = "active checks on server are active again"
	zabbixWorkingAgain  = "is working again"
	zabbixStartedToFail = "started to fail"
)

// zabbixUnreachable are the wordings of a failed connection to the server.
var zabbixUnreachable = []string{
	"cannot connect",
	"connection refused",
	"i/o timeout",
	"no route to host",
	"connection reset",
}

// zabbixLogTime is how agent 2 stamps its log lines, in the host's time.
const zabbixLogTime = "2006/01/02 15:04:05.000000"

// zabbixQuietRefreshes is how many refreshes an agent that repeats its
// refusals (7.0.10) stays silent before its server counts as having taken
// the host.
const zabbixQuietRefreshes = 3

// maxLogLine bounds the log line handed back with its class.
const maxLogLine = 300

// ClassifyZabbixLog reads the end of an agent 2 log — the lines after its
// last "starting zabbix agent 2", or all of a tail without one, since the
// agent started before the oldest line in it — and returns its last word on
// the active checks and the line that says it (trimmed, at most 300 bytes).
// Lines are compared lowercased.
//
//   - A refusal's "started to fail" keeps the refusal's own line; a "started
//     to fail" right after "cannot connect" gets the cause in brackets.
//   - An agent that repeats its "host [..] not found" — one that does not
//     follow it with "started to fail" — and whose last one is more than
//     zabbixQuietRefreshes refreshes old by now has been taken by its
//     server: LogOK. The line's time is read in now's location; a line
//     whose time cannot be read stays LogNotFound.
func ClassifyZabbixLog(tail []byte, now time.Time, refresh time.Duration) (LogClass, string) {
	var l zabbixLog
	for raw := range strings.Lines(string(tail)) {
		l.read(strings.TrimSpace(raw), now.Location())
	}
	if l.class == LogNotFound && !l.followed && !l.notFoundAt.IsZero() && refresh > 0 &&
		now.Sub(l.notFoundAt) > zabbixQuietRefreshes*refresh {
		return LogOK, ""
	}
	return l.class, cutLogLine(l.line)
}

// ZabbixLogHasStart reports whether a tail of an agent 2 log holds the line
// a run of the agent starts with.
func ZabbixLogHasStart(tail []byte) bool {
	return bytes.Contains(bytes.ToLower(tail), []byte(zabbixStartMarker))
}

// zabbixLogLine is the kind of an agent 2 log line about its active checks.
type zabbixLogLine int

const (
	lineOther    zabbixLogLine = iota
	lineRefusal                // no active checks on server …
	lineCause                  // cannot connect …
	lineFailing                // … started to fail
	lineRecovery               // … again
)

// zabbixLog is what the lines read so far say.
type zabbixLog struct {
	class LogClass
	line  string
	last  zabbixLogLine // the last line that said anything
	// notFoundAt is when the last "host [..] not found" was written, and
	// followed whether a "started to fail" came right after it.
	notFoundAt time.Time
	followed   bool
}

func (l *zabbixLog) read(line string, loc *time.Location) {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, zabbixStartMarker):
		*l = zabbixLog{}
		return
	case strings.Contains(lower, "host [") && strings.Contains(lower, "not found"):
		l.set(LogNotFound, line, lineRefusal)
		l.notFoundAt, l.followed = logLineTime(line, loc), false
	case strings.Contains(lower, zabbixNoChecks):
		l.set(LogRefused, line, lineRefusal)
	case strings.Contains(lower, zabbixChecksFailing):
		if l.last == lineRefusal {
			// The refusal said why; this only says it began.
			l.followed = true
			l.last = lineFailing
			return
		}
		l.set(LogRefused, line, lineFailing)
	case strings.Contains(lower, zabbixChecksAgain), strings.Contains(lower, zabbixWorkingAgain):
		l.set(LogOK, line, lineRecovery)
	case strings.Contains(lower, zabbixStartedToFail):
		if l.last == lineCause {
			line += " (" + logMessage(l.line) + ")"
		}
		l.set(LogUnreachable, line, lineFailing)
	case containsAny(lower, zabbixUnreachable):
		l.set(LogUnreachable, line, lineCause)
	}
}

func (l *zabbixLog) set(class LogClass, line string, kind zabbixLogLine) {
	l.class, l.line, l.last = class, line, kind
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// logLineTime is when agent 2 wrote a line, read in loc; zero when the line
// does not start with its time.
func logLineTime(line string, loc *time.Location) time.Time {
	if len(line) < len(zabbixLogTime) {
		return time.Time{}
	}
	t, err := time.ParseInLocation(zabbixLogTime, line[:len(zabbixLogTime)], loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

// logMessage is a log line without its time and its "[101]" prefix.
func logMessage(line string) string {
	msg := line
	if !logLineTime(line, time.UTC).IsZero() {
		msg = strings.TrimSpace(line[len(zabbixLogTime):])
	}
	if rest, ok := strings.CutPrefix(msg, "["); ok {
		if id, after, ok := strings.Cut(rest, "] "); ok && id != "" && strings.Trim(id, "0123456789") == "" {
			msg = after
		}
	}
	return msg
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
