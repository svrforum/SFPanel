package serveragents

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const managedBanner = "# Managed by Semaphore / Ansible."

func zabbixFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/zabbix_agent2.conf")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func splitLines(b []byte) []string { return strings.Split(string(b), "\n") }

// managedLineRe is written apart from the code under test, so the tests do
// not check the matcher against itself.
var managedLineRe = regexp.MustCompile(`^[ \t]*(Server|ServerActive|Hostname|HostMetadata)[ \t]*=`)

func TestReadZabbixConfTakesActiveLinesOnly(t *testing.T) {
	c := ReadZabbixConf(zabbixFixture(t))
	if c.Server != "127.0.0.1" || c.ServerActive != "127.0.0.1" {
		t.Fatalf("Server = %q, ServerActive = %q, want the active lines' 127.0.0.1", c.Server, c.ServerActive)
	}
	if c.Hostname != ZabbixVendorHostname || !c.HostnameSet {
		t.Fatalf("Hostname = %q (set %v), want the vendor's %q", c.Hostname, c.HostnameSet, ZabbixVendorHostname)
	}
	if c.HostMetadata != "" {
		t.Fatalf("HostMetadata = %q, want empty: \"# HostMetadata=\" is the vendor's example", c.HostMetadata)
	}
	if c.HostnameItemSet {
		t.Fatal("HostnameItemSet, but HostnameItem is only commented out")
	}
	want := []string{"/etc/zabbix/zabbix_agent2.d/*.conf", "./zabbix_agent2.d/plugins.d/*.conf"}
	if !slices.Equal(c.Includes, want) {
		t.Fatalf("Includes = %q, want %q", c.Includes, want)
	}
	if c.Duplicates != nil {
		t.Fatalf("Duplicates = %v, want none", c.Duplicates)
	}
}

func TestReadZabbixConfTrimsTheValueAsTheAgentDoes(t *testing.T) {
	c := ReadZabbixConf([]byte("  Server = 192.0.2.19 \r\n\tHostnameItem\t=\tsystem.hostname\nHostname=\n"))
	if c.Server != "192.0.2.19" {
		t.Fatalf("Server = %q, want 192.0.2.19 without the spaces and the carriage return", c.Server)
	}
	if !c.HostnameItemSet || !c.HostnameSet || c.Hostname != "" {
		t.Fatalf("HostnameItemSet %v, HostnameSet %v, Hostname %q; want an active HostnameItem and an empty Hostname",
			c.HostnameItemSet, c.HostnameSet, c.Hostname)
	}
}

func TestEditReplacesTheActiveLineInPlace(t *testing.T) {
	in := zabbixFixture(t)
	out, changed, err := EditZabbixConf(in, []KeyValue{{"Server", "192.0.2.10"}})
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	before, after := splitLines(in), splitLines(out)
	if len(after) != len(before) {
		t.Fatalf("%d lines became %d", len(before), len(after))
	}
	old := slices.Index(before, "Server=127.0.0.1")
	var differ []int
	for i := range before {
		if before[i] != after[i] {
			differ = append(differ, i)
		}
	}
	if !slices.Equal(differ, []int{old}) || after[old] != "Server=192.0.2.10" {
		t.Fatalf("lines %v differ (line %d is now %q), want only line %d as Server=192.0.2.10", differ, old, after[old], old)
	}
}

func TestEditKeepsEveryOtherLineByteForByte(t *testing.T) {
	in := zabbixFixture(t)
	out, changed, err := EditZabbixConf(in, []KeyValue{
		{"Server", "192.0.2.10"},
		{"ServerActive", "192.0.2.10"},
		{"HostMetadata", "linux-fleet-auto"},
		{"Hostname", "node-a"},
	})
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	others := func(b []byte) []string {
		var keep []string
		for _, l := range splitLines(b) {
			if !managedLineRe.MatchString(l) {
				keep = append(keep, l)
			}
		}
		return keep
	}
	if b, a := others(in), others(out); !slices.Equal(b, a) {
		t.Fatalf("lines other than the four keys changed:\nbefore %q\nafter  %q", b, a)
	}
	if first := splitLines(out)[0]; first != managedBanner {
		t.Fatalf("line 1 = %q, want %q", first, managedBanner)
	}
	got := ReadZabbixConf(out)
	if got.Server != "192.0.2.10" || got.ServerActive != "192.0.2.10" || got.HostMetadata != "linux-fleet-auto" || got.Hostname != "node-a" {
		t.Fatalf("read back %+v", got)
	}
}

func TestACommentedOutKeyIsWrittenBelowItsExample(t *testing.T) {
	in := zabbixFixture(t)
	out, _, err := EditZabbixConf(in, []KeyValue{{"HostMetadata", "linux-fleet-auto"}})
	if err != nil {
		t.Fatal(err)
	}
	l := splitLines(out)
	i := slices.Index(l, "# HostMetadata=")
	if i < 0 || i+1 >= len(l) || l[i+1] != "HostMetadata=linux-fleet-auto" {
		t.Fatalf("example at line %d, the line below it %q; want the example kept and the key right below it", i, l[min(i+1, len(l)-1)])
	}
	if len(l) != len(splitLines(in))+1 {
		t.Fatalf("%d lines, want one more than %d", len(l), len(splitLines(in)))
	}
}

func TestADuplicatedKeyIsWrittenOnceAndTheRestCommentedOut(t *testing.T) {
	in := []byte("Server=192.0.2.19\nLogType=file\nServer=192.0.2.20\n")
	if got := ReadZabbixConf(in).Duplicates; !slices.Equal(got, []string{"Server"}) {
		t.Fatalf("Duplicates = %v", got)
	}
	out, changed, err := EditZabbixConf(in, []KeyValue{{"Server", "192.0.2.10"}})
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if want := "Server=192.0.2.10\nLogType=file\n# Server=192.0.2.20\n"; string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	if got := ReadZabbixConf(out).Duplicates; got != nil {
		t.Fatalf("Duplicates after the edit = %v, want none", got)
	}
}

func TestADuplicateIsCommentedOutEvenWhenTheFirstLineHoldsTheValue(t *testing.T) {
	in := []byte("Server=192.0.2.10\nServer=192.0.2.20\n")
	out, changed, err := EditZabbixConf(in, []KeyValue{{"Server", "192.0.2.10"}})
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if want := "Server=192.0.2.10\n# Server=192.0.2.20\n"; string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestAnAbsentKeyIsAppended(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"LogType=file", "LogType=file\nHostMetadata=x\n"},
		{"LogType=file\n", "LogType=file\nHostMetadata=x\n"},
		{"", "HostMetadata=x\n"},
		{"# HostMetadata=", "# HostMetadata=\nHostMetadata=x\n"},
	} {
		out, changed, err := EditZabbixConf([]byte(c.in), []KeyValue{{"HostMetadata", "x"}})
		if err != nil || !changed || string(out) != c.want {
			t.Fatalf("%q: out %q, changed %v, err %v; want %q", c.in, out, changed, err, c.want)
		}
	}
}

func TestACRLFFileKeepsItsLineEnds(t *testing.T) {
	in := []byte("# HostMetadata=\r\nServer=192.0.2.19\r\nServer=192.0.2.20\r\n")
	out, _, err := EditZabbixConf(in, []KeyValue{{"Server", "192.0.2.10"}, {"HostMetadata", "x"}, {"Hostname", "node-a"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "# HostMetadata=\r\nHostMetadata=x\r\nServer=192.0.2.10\r\n# Server=192.0.2.20\r\nHostname=node-a\r\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestEditingToTheSameValueChangesNothing(t *testing.T) {
	in := zabbixFixture(t)
	out, changed, err := EditZabbixConf(in, []KeyValue{
		{"Server", "127.0.0.1"},
		{"ServerActive", "127.0.0.1"},
		{"Hostname", ZabbixVendorHostname},
	})
	if err != nil || changed || string(out) != string(in) {
		t.Fatalf("changed %v, err %v, out equal %v; want nothing changed", changed, err, string(out) == string(in))
	}
	// Spaces and the carriage return are not part of the value.
	spaced := []byte("Server = 192.0.2.10 \r\n")
	out, changed, err = EditZabbixConf(spaced, []KeyValue{{"Server", "192.0.2.10"}})
	if err != nil || changed || string(out) != string(spaced) {
		t.Fatalf("out %q, changed %v, err %v; want %q unchanged", out, changed, err, spaced)
	}
}

func TestANewlineInAValueIsRefused(t *testing.T) {
	in := zabbixFixture(t)
	for _, v := range []string{"192.0.2.10\nUserParameter=x,id", "192.0.2.10\rUserParameter=x,id"} {
		out, changed, err := EditZabbixConf(in, []KeyValue{{"Hostname", "node-a"}, {"Server", v}})
		if !errors.Is(err, ErrValueHasNewline) {
			t.Fatalf("%q: err = %v, want ErrValueHasNewline", v, err)
		}
		if out != nil || changed {
			t.Fatalf("%q: out %q, changed %v; want the whole edit refused", v, out, changed)
		}
	}
}

func TestTheKeyIsMatchedWholeAndCaseSensitively(t *testing.T) {
	in := []byte("# ServerActive=\nServerActive=192.0.2.19\nserver=192.0.2.20\nLogType=file\n")
	if c := ReadZabbixConf(in); c.Server != "" || c.ServerActive != "192.0.2.19" {
		t.Fatalf("Server = %q, ServerActive = %q; want ServerActive and server not read as Server", c.Server, c.ServerActive)
	}
	out, _, err := EditZabbixConf(in, []KeyValue{{"Server", "192.0.2.10"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := string(in) + "Server=192.0.2.10\n"; string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestSetsManagedKey(t *testing.T) {
	if got := SetsManagedKey([]byte("Server=1\n# Hostname=x\n")); !slices.Equal(got, []string{"Server"}) {
		t.Fatalf("got %v, want [Server]", got)
	}
	if got := SetsManagedKey([]byte("HostMetadata=a\nServerActive=b\n")); !slices.Equal(got, []string{"ServerActive", "HostMetadata"}) {
		t.Fatalf("got %v, want [ServerActive HostMetadata]", got)
	}
	if got := SetsManagedKey([]byte("Plugins.Docker.Endpoint=unix:///var/run/docker.sock\n")); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestWhereTheAgentLogsAndHowOftenItAsks(t *testing.T) {
	vendor := ReadZabbixConf(zabbixFixture(t))
	if got := vendor.LogPath(); got != "/var/log/zabbix/zabbix_agent2.log" {
		t.Errorf("vendor LogPath = %q, want its LogFile, not LogFileSize's line", got)
	}
	if got := vendor.Refresh(); got != ZabbixDefaultRefresh {
		t.Errorf("vendor Refresh = %v, want the agent's default %v", got, ZabbixDefaultRefresh)
	}
	for _, tc := range []struct {
		conf, path string
		refresh    time.Duration
	}{
		{"", ZabbixDefaultLogFile, 5 * time.Second},
		{"LogType=file\nLogFile=/srv/log/agent2.log\nRefreshActiveChecks=60\n", "/srv/log/agent2.log", time.Minute},
		{"LogType=system\nLogFile=/srv/log/agent2.log\n", "", 5 * time.Second},
		{"LogType=console\n", "", 5 * time.Second},
		{"# LogFile=/srv/log/agent2.log\nRefreshActiveChecks=0\n", ZabbixDefaultLogFile, 5 * time.Second},
		{"RefreshActiveChecks=86401\n", ZabbixDefaultLogFile, 5 * time.Second},
		{"RefreshActiveChecks=often\n", ZabbixDefaultLogFile, 5 * time.Second},
	} {
		c := ReadZabbixConf([]byte(tc.conf))
		if got := c.LogPath(); got != tc.path {
			t.Errorf("%q: LogPath = %q, want %q", tc.conf, got, tc.path)
		}
		if got := c.Refresh(); got != tc.refresh {
			t.Errorf("%q: Refresh = %v, want %v", tc.conf, got, tc.refresh)
		}
	}
}
