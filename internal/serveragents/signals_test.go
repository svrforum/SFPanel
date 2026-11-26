package serveragents

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseAgentdState(t *testing.T) {
	b := []byte("# State file for wazuh-agentd\n# - pending: waiting to get connected.\nstatus='pending'\nlast_keepalive='2026-11-26 10:00:00'\nlast_ack=''\nmsg_count='4'\n")
	want := AgentdState{Status: "pending", LastKeepalive: "2026-11-26 10:00:00", LastAck: ""}
	if got := ParseAgentdState(b); got != want {
		t.Fatalf("ParseAgentdState = %+v, want %+v", got, want)
	}
	if got := ParseAgentdState(nil); got != (AgentdState{}) {
		t.Fatalf("ParseAgentdState(empty) = %+v, want the zero value", got)
	}
}

const (
	zabbixStart    = "2026/11/26 10:00:00.000000 Starting Zabbix Agent 2 (7.0.32)\n"
	zabbixFail     = "2026/11/26 10:00:05.000000 [101] active check configuration update from [192.0.2.19:10051] started to fail (cannot connect to [[192.0.2.19]:10051]: dial tcp 192.0.2.19:10051: i/o timeout)\n"
	zabbixAgain    = "2026/11/26 10:01:00.000000 [101] active check configuration update from [192.0.2.10:10051] is working again\n"
	zabbixNotFound = "2026/11/26 10:00:05.000000 [101] no active checks on server [192.0.2.10:10051]: host [node-a] not found\n"
	zabbixOther    = "2026/11/26 10:02:00.000000 [101] plugin Docker loaded\n"
)

func TestTheLastLineSinceTheAgentStartedDecides(t *testing.T) {
	for _, tc := range []struct {
		name, tail string
		want       LogClass
		line       string
	}{
		{"a failure after the start", zabbixStart + zabbixFail, LogUnreachable, zabbixFail},
		{"a recovery after the failure", zabbixStart + zabbixFail + zabbixAgain, LogOK, zabbixAgain},
		{"a host the server does not know", zabbixStart + zabbixNotFound, LogNotFound, zabbixNotFound},
		{"unclassified lines after the failure", zabbixStart + zabbixFail + zabbixOther, LogUnreachable, zabbixFail},
		{"a failure before the last start", zabbixFail + zabbixStart, LogNothing, ""},
		{"a failure before a restart, nothing since", zabbixStart + zabbixFail + zabbixStart + zabbixOther, LogNothing, ""},
		{"an empty log", "", LogNothing, ""},
	} {
		class, line := ClassifyZabbixLog([]byte(tc.tail))
		if class != tc.want || line != strings.TrimSpace(tc.line) {
			t.Errorf("%s: ClassifyZabbixLog = %q, %q; want %q, %q", tc.name, class, line, tc.want, strings.TrimSpace(tc.line))
		}
	}
}

func TestWithoutAStartMarkerTheWholeTailDecides(t *testing.T) {
	// A tail cut from a long-running agent's log no longer holds the line it
	// started with; every line in it came after that start.
	class, line := ClassifyZabbixLog([]byte(zabbixOther + zabbixFail + zabbixOther))
	if class != LogUnreachable || line != strings.TrimSpace(zabbixFail) {
		t.Fatalf("ClassifyZabbixLog = %q, %q; want %q and the failing line", class, line, LogUnreachable)
	}
}

func TestEveryFailureWordingDecides(t *testing.T) {
	for _, tc := range []struct {
		line string
		want LogClass
	}{
		{"[101] active check configuration update from [192.0.2.10:10051] STARTED TO FAIL", LogUnreachable},
		{"[101] Cannot connect to [192.0.2.10:10051]", LogUnreachable},
		{"[101] dial tcp 192.0.2.10:10051: connect: connection refused", LogUnreachable},
		{"[101] dial tcp 192.0.2.10:10051: i/o timeout", LogUnreachable},
		{"[101] dial tcp 192.0.2.10:10051: connect: no route to host", LogUnreachable},
		{"[101] read tcp 192.0.2.10:10051: connection reset by peer", LogUnreachable},
		{"[101] no active checks on server [192.0.2.10:10051]: Host [node-a] Not Found", LogNotFound},
		{"[101] active check configuration update from [192.0.2.10:10051] Is Working Again", LogOK},
		{"[101] host [node-a] is up", LogNothing},
		{"[101] item not found", LogNothing},
	} {
		class, _ := ClassifyZabbixLog([]byte(zabbixStart + tc.line + "\n"))
		if class != tc.want {
			t.Errorf("ClassifyZabbixLog(%q) = %q, want %q", tc.line, class, tc.want)
		}
	}
}

func TestALongLineIsCut(t *testing.T) {
	// The failure wording sits past the cut, so the line is classified whole
	// before it is shortened.
	long := "2026/11/26 10:00:05.000000 [101] active check configuration update from [" +
		strings.Repeat("é", 480) + ":10051] started to fail (cannot connect: i/o timeout)"
	if len(long) < 1000 {
		t.Fatalf("fixture is %d bytes, want at least 1000", len(long))
	}
	class, line := ClassifyZabbixLog([]byte(zabbixStart + long + "\n"))
	if class != LogUnreachable {
		t.Fatalf("class = %q, want %q", class, LogUnreachable)
	}
	if len(line) > 300 || len(line) < 290 {
		t.Fatalf("line is %d bytes, want a cut at no more than 300", len(line))
	}
	if !strings.HasPrefix(long, line) {
		t.Fatalf("line %q is not the start of the logged line", line)
	}
	if !utf8.ValidString(line) {
		t.Fatalf("line ends inside a character: %q", line[len(line)-4:])
	}
}
