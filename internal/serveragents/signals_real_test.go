package serveragents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realLog reads the end of a Zabbix Agent 2 log taken from a real agent: the
// vendor's 7.0.32 on Ubuntu 24.04 and Debian 13, Ubuntu 26.04's own 7.0.22
// and Ubuntu 25.10's own 7.0.10, each against a server that knew the host
// (ok), did not (notfound), took it later (notfound-recovered, autoreg — a
// server that registers a host it does not know at its first request), went
// away (unreachable) or came back (outage-recovered).
func realLog(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "agent2-log", name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRealAgentLogsWithoutAClock(t *testing.T) {
	for _, tc := range []struct {
		log  string
		want LogClass
		line string // a part of the line given back
	}{
		{"7.0.32-ubuntu-ok", LogNothing, ""},
		{"7.0.32-ubuntu-notfound", LogNotFound, "host [node-u2404] not found"},
		{"7.0.32-ubuntu-notfound-recovered", LogOK, "active checks on server are active again"},
		{"7.0.32-ubuntu-autoreg", LogOK, "active checks on server are active again"},
		{"7.0.32-ubuntu-outage-recovered", LogOK, "is working again"},
		// The cause of the failure is on the line before it.
		{"7.0.32-ubuntu-unreachable", LogUnreachable, "active check configuration update from host [node-u2404] started to fail (cannot connect to [198.51.100.250:10051]: dial tcp :0->198.51.100.250:10051: i/o timeout)"},
		{"7.0.32-debian-ok", LogNothing, ""},
		{"7.0.32-debian-notfound", LogNotFound, "host [node-d13] not found"},
		{"7.0.32-debian-notfound-recovered", LogOK, "are active again"},
		{"7.0.22-ok", LogNothing, ""},
		{"7.0.22-notfound", LogNotFound, "host [keep-me] not found"},
		{"7.0.22-notfound-recovered", LogOK, "are active again"},
		{"7.0.10-ok", LogNothing, ""},
	} {
		// Long after every line: an agent that writes its refusal once is
		// still refused however long ago it wrote it.
		class, line := ClassifyZabbixLog(realLog(t, tc.log), time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), defaultRefresh)
		if class != tc.want || !strings.Contains(line, tc.line) {
			t.Errorf("%s: ClassifyZabbixLog = %q, %q; want %q and a line holding %q", tc.log, class, line, tc.want, tc.line)
		}
	}
}

func TestAnAgentThatRepeatsItsRefusalIsTakenOnceItFallsSilent(t *testing.T) {
	// Agent 7.0.10 writes "host [..] not found" at every refresh while the
	// server refuses it, and nothing once the server takes the host.
	at := func(clock string) time.Time {
		t.Helper()
		v, err := time.Parse(time.DateTime, "2026-11-26 "+clock)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		name, log string
		now       time.Time
		refresh   time.Duration
		want      LogClass
	}{
		// The last refusal is at 12:30:34.
		{"one refresh after the last refusal", "7.0.10-notfound", at("12:30:40"), 5 * time.Second, LogNotFound},
		{"three refreshes after it", "7.0.10-notfound", at("12:30:49"), 5 * time.Second, LogNotFound},
		{"more than three refreshes after it", "7.0.10-notfound", at("12:30:50"), 5 * time.Second, LogOK},
		{"a longer refresh from the config", "7.0.10-notfound", at("12:31:30"), 30 * time.Second, LogNotFound},
		// Restarted at 12:33:49, refused once at 12:33:51, then taken.
		{"autoregistration, too soon to tell", "7.0.10-autoreg", at("12:34:01"), 5 * time.Second, LogNotFound},
		{"autoregistration, taken", "7.0.10-autoreg", at("12:34:10"), 5 * time.Second, LogOK},
	} {
		class, line := ClassifyZabbixLog(realLog(t, tc.log), tc.now, tc.refresh)
		if class != tc.want {
			t.Errorf("%s: ClassifyZabbixLog = %q, %q; want %q", tc.name, class, line, tc.want)
		}
		if class == LogNotFound && !strings.Contains(line, "host [node-u2510] not found") {
			t.Errorf("%s: line %q, want the last refusal", tc.name, line)
		}
	}
}

func TestARefusalLineWithoutItsTimeIsNotAged(t *testing.T) {
	class, _ := ClassifyZabbixLog([]byte("[101] no active checks on server [192.0.2.10:10051]: host [node-a] not found\n"), time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), defaultRefresh)
	if class != LogNotFound {
		t.Fatalf("class = %q, want %q: a line whose time cannot be read never ages", class, LogNotFound)
	}
}

func TestARefusalForAnotherReasonIsRefused(t *testing.T) {
	tail := "2026/11/26 12:07:55.002395 [101] no active checks on server [192.0.2.10:10051]: host [node-a] not monitored\n" +
		"2026/11/26 12:07:55.002427 [101] active checks on server started to fail\n"
	class, line := ClassifyZabbixLog([]byte(tail), time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), defaultRefresh)
	if class != LogRefused || !strings.Contains(line, "host [node-a] not monitored") {
		t.Fatalf("ClassifyZabbixLog = %q, %q; want %q with the refusal's own line", class, line, LogRefused)
	}
	// "started to fail" alone, its refusal cut off the tail, still fails.
	class, line = ClassifyZabbixLog([]byte("2026/11/26 12:07:55.002427 [101] active checks on server started to fail\n"), time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), defaultRefresh)
	if class != LogRefused || !strings.Contains(line, "started to fail") {
		t.Fatalf("alone: ClassifyZabbixLog = %q, %q; want %q", class, line, LogRefused)
	}
}
