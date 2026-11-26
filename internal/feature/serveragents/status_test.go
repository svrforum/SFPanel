package featureserveragents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

const (
	logStart    = "2031/03/04 09:50:00.000000 Starting Zabbix Agent 2 (7.0.32)\n"
	logFail     = "2031/03/04 09:50:05.000000 [101] active check configuration update from [192.0.2.10:10051] started to fail (cannot connect to [[192.0.2.10]:10051]: dial tcp 192.0.2.10:10051: connect: connection refused)\n"
	logAgain    = "2031/03/04 09:51:00.000000 [101] active check configuration update from [192.0.2.10:10051] is working again\n"
	logNotFound = "2031/03/04 09:50:05.000000 [101] no active checks on server [192.0.2.10:10051]: host [node-a] not found\n"
)

// ossecConf is the vendor's ossec.conf pointed at a manager.
func ossecConf(t *testing.T, manager string) string {
	t.Helper()
	return strings.Replace(testdata(t, "ossec.conf"), "MANAGER_IP", manager, 1)
}

// installedHost is a noble host with both agents installed from their
// vendors' packages and running, the Zabbix agent pointed at active and
// started with nothing logged since.
func installedHost(t *testing.T, active string) (*hostFixture, *Host) {
	t.Helper()
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.pkg("zabbix-agent2", "install ok installed|1:7.0.32-2+ubuntu24.04")
	f.service("zabbix-agent2", "active")
	f.write("etc/zabbix/zabbix_agent2.conf", "# Managed elsewhere\nServer="+active+"\nServerActive="+active+"\nHostname=node-a\nLogFile=/var/log/zabbix/zabbix_agent2.log\n")
	f.write(zabbixLogRel, agentLog(t, "7.0.32-ubuntu-ok"))
	f.pkg("wazuh-agent", "install ok installed|4.14.10-1")
	f.service("wazuh-agent", "active")
	f.write("var/ossec/etc/ossec.conf", ossecConf(t, "198.51.100.5"))
	return f, h
}

func TestAnActiveAgentThatDoesNotReachItsServerIsNotConnected(t *testing.T) {
	f, h := installedHost(t, "192.0.2.19")
	f.dials["192.0.2.19:10051"] = errors.New("dial tcp 192.0.2.19:10051: i/o timeout")
	f.agentdState("pending", "", "")

	st := h.Status(context.Background())

	z := st.Zabbix
	if z.State != serveragents.StateInstalled || z.Service != "active" || z.Source != serveragents.SourceVendor || z.Version != "1:7.0.32-2+ubuntu24.04" {
		t.Fatalf("zabbix = %+v, want installed, active, vendor 1:7.0.32-2+ubuntu24.04", z)
	}
	if z.Connection == nil || z.Connection.State != serveragents.ConnUnreachable {
		t.Fatalf("zabbix connection = %+v, want unreachable although the service is active", z.Connection)
	}
	if !strings.Contains(z.Connection.Detail, "192.0.2.19:10051") {
		t.Errorf("zabbix detail = %q, want the dial error naming 192.0.2.19:10051", z.Connection.Detail)
	}
	if got := f.dialedAddrs(); len(got) != 1 || got[0] != "192.0.2.19:10051" {
		t.Errorf("dialled %q, want only ServerActive at the default port, 192.0.2.19:10051", got)
	}
	if z.Config == nil || z.Config.ServerActive != "192.0.2.19" || z.Config.Hostname != "node-a" {
		t.Errorf("zabbix config = %+v, want the file's values", z.Config)
	}

	w := st.Wazuh
	if w.State != serveragents.StateInstalled || w.Service != "active" || w.Connection == nil || w.Connection.State != serveragents.ConnPending {
		t.Fatalf("wazuh = %+v (connection %+v), want installed, active, pending", w, w.Connection)
	}
	if w.Config == nil || len(w.Config.Servers) != 1 || w.Config.Servers[0] != "198.51.100.5" {
		t.Errorf("wazuh config = %+v, want the manager from ossec.conf", w.Config)
	}
	if st.OS.Codename != "noble" || !st.CheckedAt.Equal(fixtureNow) {
		t.Errorf("os %+v, checked at %v; want noble at the host's clock %v", st.OS, st.CheckedAt, fixtureNow)
	}
}

func TestAConnectedAgentReadsConnected(t *testing.T) {
	f, h := installedHost(t, "zbx.example.net:10052")
	// A failure from the run before, then one since the start that recovered.
	f.write("var/log/zabbix/zabbix_agent2.log", logFail+logStart+logFail+logAgain)
	f.agentdState("connected", "2031-03-04 09:59:58", "2031-03-04 09:59:57")

	st := h.Status(context.Background())

	if c := st.Zabbix.Connection; c == nil || c.State != serveragents.ConnConnected {
		t.Fatalf("zabbix connection = %+v, want connected", c)
	}
	if got := f.dialedAddrs(); len(got) != 1 || got[0] != "zbx.example.net:10052" {
		t.Errorf("dialled %q, want ServerActive with its own port", got)
	}
	want := serveragents.Connection{State: serveragents.ConnConnected, LastKeepalive: "2031-03-04 09:59:58", LastAck: "2031-03-04 09:59:57"}
	if c := st.Wazuh.Connection; c == nil || *c != want {
		t.Fatalf("wazuh connection = %+v, want %+v", c, want)
	}
}

func TestAHostTheServerDoesNotKnowReadsNotRegistered(t *testing.T) {
	// Agents 7.0.22 and 7.0.32 write the refusal once, followed by "active
	// checks on server started to fail", and nothing more while it lasts.
	for _, log := range []string{"7.0.32-ubuntu-notfound", "7.0.32-debian-notfound", "7.0.22-notfound"} {
		f, h := installedHost(t, "192.0.2.10")
		f.write(zabbixLogRel, agentLog(t, log))

		c := h.Status(context.Background()).Zabbix.Connection
		if c == nil || c.State != serveragents.ConnNotRegistered {
			t.Fatalf("%s: zabbix connection = %+v, want not_registered", log, c)
		}
		if !strings.Contains(c.Detail, "no active checks on server [zbx-fake:10051]: host [") || !strings.HasSuffix(c.Detail, "] not found") {
			t.Errorf("%s: detail = %q, want the refusal's line", log, c.Detail)
		}
	}
}

func TestAHostTheServerTookReadsConnected(t *testing.T) {
	for _, log := range []string{"7.0.32-ubuntu-notfound-recovered", "7.0.32-ubuntu-autoreg", "7.0.32-debian-notfound-recovered", "7.0.22-notfound-recovered", "7.0.32-ubuntu-outage-recovered"} {
		f, h := installedHost(t, "192.0.2.10")
		f.write(zabbixLogRel, agentLog(t, log))

		if c := h.Status(context.Background()).Zabbix.Connection; c == nil || c.State != serveragents.ConnConnected {
			t.Errorf("%s: zabbix connection = %+v, want connected", log, c)
		}
	}
}

func TestAnAgentThatRepeatsItsRefusalIsReadAgainstTheHostsClock(t *testing.T) {
	// Agent 7.0.10 repeats the refusal at every refresh (5 seconds here),
	// and writes nothing once its server takes the host. Its last refusal
	// is at 12:30:34.
	at := func(clock string) time.Time {
		return time.Date(2026, 11, 26, 12, 30, 0, 0, time.UTC).Add(mustDuration(t, clock))
	}
	for _, tc := range []struct {
		name, refresh string
		now           time.Time
		want          string
	}{
		{"refused a refresh ago", "", at("40s"), serveragents.ConnNotRegistered},
		{"silent for more than three refreshes", "", at("50s"), serveragents.ConnConnected},
		{"silent for less than three of its configured refreshes", "RefreshActiveChecks=30\n", at("1m"), serveragents.ConnNotRegistered},
	} {
		f, h := installedHost(t, "192.0.2.10")
		f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nServerActive=192.0.2.10\nLogFile=/var/log/zabbix/zabbix_agent2.log\n"+tc.refresh)
		f.write(zabbixLogRel, agentLog(t, "7.0.10-notfound"))
		f.now = tc.now

		if c := h.Status(context.Background()).Zabbix.Connection; c == nil || c.State != tc.want {
			t.Errorf("%s: zabbix connection = %+v, want %s", tc.name, c, tc.want)
		}
	}
}

func mustDuration(t *testing.T, s string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTheAgentsLogIsWhereItsConfigSays(t *testing.T) {
	t.Run("LogFile", func(t *testing.T) {
		f, h := installedHost(t, "192.0.2.10")
		f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nServerActive=192.0.2.10\nLogFile=/srv/agent2/agent.log\n")
		f.write("srv/agent2/agent.log", agentLog(t, "7.0.32-ubuntu-notfound"))

		if c := h.Status(context.Background()).Zabbix.Connection; c == nil || c.State != serveragents.ConnNotRegistered {
			t.Fatalf("zabbix connection = %+v, want not_registered from the log LogFile names", c)
		}
	})
	t.Run("no LogFile", func(t *testing.T) {
		f, h := installedHost(t, "192.0.2.10")
		f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nServerActive=192.0.2.10\n")
		f.write("tmp/zabbix_agent2.log", agentLog(t, "7.0.32-ubuntu-notfound"))

		if c := h.Status(context.Background()).Zabbix.Connection; c == nil || c.State != serveragents.ConnNotRegistered {
			t.Fatalf("zabbix connection = %+v, want not_registered from the agent's default log", c)
		}
	})
	// Without a log to read, an agent that reaches its server's port is not
	// known to be connected.
	for _, tc := range []struct{ name, conf, detail string }{
		{"no such file", "LogFile=/srv/agent2/none.log\n", "/srv/agent2/none.log, cannot be read"},
		{"LogType system", "LogType=system\nLogFile=/var/log/zabbix/zabbix_agent2.log\n", "LogType=system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, h := installedHost(t, "192.0.2.10")
			f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nServerActive=192.0.2.10\n"+tc.conf)

			c := h.Status(context.Background()).Zabbix.Connection
			if c == nil || c.State != serveragents.ConnUnknown || !strings.Contains(c.Detail, tc.detail) {
				t.Fatalf("zabbix connection = %+v, want unknown saying %q", c, tc.detail)
			}
		})
	}
}

func TestAnAgentStartingOrReloadingIsNotStopped(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.service("zabbix-agent2", "activating")
	f.service("wazuh-agent", "reloading")
	f.agentdState("connected", "2031-03-04 09:59:58", "")

	st := h.Status(context.Background())

	if c := st.Zabbix.Connection; c == nil || c.State != serveragents.ConnUnknown || c.Detail != "zabbix-agent2 is activating" {
		t.Errorf("zabbix connection = %+v, want unknown while it is activating", c)
	}
	if c := st.Wazuh.Connection; c == nil || c.State != serveragents.ConnUnknown || c.Detail != "wazuh-agent is reloading" {
		t.Errorf("wazuh connection = %+v, want unknown while it is reloading", c)
	}
	if got := f.dialedAddrs(); len(got) != 0 {
		t.Errorf("dialled %q, want nothing while the service is between states", got)
	}
}

func TestAReachableServerWhoseChecksFailReadsFailing(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.write("var/log/zabbix/zabbix_agent2.log", logStart+logAgain+logFail)

	c := h.Status(context.Background()).Zabbix.Connection
	if c == nil || c.State != serveragents.ConnFailing || !strings.Contains(c.Detail, "started to fail") {
		t.Fatalf("zabbix connection = %+v, want failing with the log line", c)
	}
}

func TestOnlyTheLogsEndIsRead(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	// The failure lies more than 64 KiB before the end: the agent is read
	// from its last 64 KiB alone, which hold no verdict.
	f.write("var/log/zabbix/zabbix_agent2.log", logStart+logNotFound+strings.Repeat("2031/03/04 09:55:00.000000 [101] plugin loaded\n", 2000))

	if c := h.Status(context.Background()).Zabbix.Connection; c == nil || c.State != serveragents.ConnConnected {
		t.Fatalf("zabbix connection = %+v, want connected from the log's end", c)
	}
}

func TestAnAgentWithoutServerActiveHasNoServer(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nHostname=node-a\n")

	if c := h.Status(context.Background()).Zabbix.Connection; c == nil || c.State != serveragents.ConnNoServer {
		t.Fatalf("zabbix connection = %+v, want no_server", c)
	}
	if got := f.dialedAddrs(); len(got) != 0 {
		t.Errorf("dialled %q, want nothing", got)
	}
}

func TestSeveralActiveServersAreNotGuessedAt(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nServerActive=192.0.2.10,192.0.2.11\n")

	c := h.Status(context.Background()).Zabbix.Connection
	if c == nil || c.State != serveragents.ConnUnknown || !strings.Contains(c.Detail, "192.0.2.10,192.0.2.11") {
		t.Fatalf("zabbix connection = %+v, want unknown naming the value", c)
	}
	if got := f.dialedAddrs(); len(got) != 0 {
		t.Errorf("dialled %q, want none of several servers picked", got)
	}
}

func TestAStoppedAgentIsNotDialled(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.service("zabbix-agent2", "inactive")
	f.service("wazuh-agent", "failed")
	f.agentdState("connected", "2031-03-04 09:59:58", "")

	st := h.Status(context.Background())

	if c := st.Zabbix.Connection; c == nil || c.State != serveragents.ConnNotRunning || st.Zabbix.Service != "inactive" {
		t.Fatalf("zabbix = %+v (connection %+v), want inactive, not_running", st.Zabbix, c)
	}
	if got := f.dialedAddrs(); len(got) != 0 {
		t.Errorf("dialled %q, want nothing for a stopped agent", got)
	}
	if c := st.Wazuh.Connection; c == nil || c.State != serveragents.ConnNotRunning || st.Wazuh.Service != "failed" {
		t.Fatalf("wazuh = %+v (connection %+v), want failed, not_running whatever its state file says", st.Wazuh, c)
	}
}

func TestAStaleStateFileIsNotConnected(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.agentdState("connected", "2031-03-04 09:50:00", "")
	f.chtime("var/ossec/var/run/wazuh-agentd.state", fixtureNow.Add(-10*time.Minute))

	c := h.Status(context.Background()).Wazuh.Connection
	if c == nil || c.State != serveragents.ConnStale {
		t.Fatalf("wazuh connection = %+v, want stale for a file unchanged for 10 minutes", c)
	}
	if c.LastKeepalive != "2031-03-04 09:50:00" {
		t.Errorf("last keepalive = %q, want the file's", c.LastKeepalive)
	}

	f.chtime("var/ossec/var/run/wazuh-agentd.state", fixtureNow.Add(-90*time.Second))
	if c := h.Status(context.Background()).Wazuh.Connection; c == nil || c.State != serveragents.ConnConnected {
		t.Fatalf("wazuh connection = %+v, want connected for a file changed 90 seconds ago", c)
	}
}

func TestAWazuhThatNeverConnectedReadsNever(t *testing.T) {
	_, h := installedHost(t, "192.0.2.10")

	if c := h.Status(context.Background()).Wazuh.Connection; c == nil || c.State != serveragents.ConnNever {
		t.Fatalf("wazuh connection = %+v, want never without a state file", c)
	}
}

func TestAnUnknownAgentdStatusIsUnknown(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.agentdState("reconnecting", "", "")

	if c := h.Status(context.Background()).Wazuh.Connection; c == nil || c.State != serveragents.ConnUnknown {
		t.Fatalf("wazuh connection = %+v, want unknown", c)
	}
}

func TestAServerWithNoAgents(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.noPkg("zabbix-agent2", "zabbix-agent", "wazuh-agent")
	f.unit("not-found")

	st := h.Status(context.Background())

	if z := st.Zabbix; z.State != serveragents.StateNone || z.Version != "" || z.Source != "" || z.Service != "" || z.Config != nil || z.Connection != nil {
		t.Errorf("zabbix = %+v, want only state none", z)
	}
	if w := st.Wazuh; w.State != serveragents.StateNone || w.Version != "" || w.Service != "" || w.Config != nil || w.Connection != nil {
		t.Errorf("wazuh = %+v, want only state none", w)
	}
	if f.cmd.ran("systemctl is-active") || len(f.dialedAddrs()) != 0 {
		t.Errorf("ran %q and dialled %q; want no service read and no dial for agents that are not there", f.cmd.lines(), f.dialedAddrs())
	}
}

func TestTheOldZabbixAgentIsRecognised(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.noPkg("zabbix-agent2", "wazuh-agent")
	f.pkg("zabbix-agent", "install ok installed|1:6.0.14+dfsg-1")
	f.service("zabbix-agent2", "active")

	z := h.Status(context.Background()).Zabbix

	if z.State != serveragents.StateLegacy || z.Version != "1:6.0.14+dfsg-1" || z.Source != serveragents.SourceDistro {
		t.Fatalf("zabbix = %+v, want legacy, its package's version and source", z)
	}
	if z.Service != "" || z.Connection != nil || f.cmd.ran("systemctl is-active zabbix-agent2") {
		t.Errorf("zabbix = %+v (connection %+v), want no service or connection read for agentd", z, z.Connection)
	}

	// agentd's config file alone, its package gone, is the old agent too.
	f2, h2 := newHostFixture(t)
	f2.system(osNoble)
	f2.noPkg("zabbix-agent2", "zabbix-agent", "wazuh-agent")
	f2.write("etc/zabbix/zabbix_agentd.conf", "Server=192.0.2.10\n")
	if z := h2.Status(context.Background()).Zabbix; z.State != serveragents.StateLegacy || z.Source != serveragents.SourceOther {
		t.Fatalf("zabbix with only zabbix_agentd.conf = %+v, want legacy from no package", z)
	}
}

func TestADistroZabbixIsRecognised(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.pkg("zabbix-agent2", "install ok installed|1:7.0.22+dfsg-1")

	z := h.Status(context.Background()).Zabbix
	if z.State != serveragents.StateInstalled || z.Source != serveragents.SourceDistro || z.Version != "1:7.0.22+dfsg-1" {
		t.Fatalf("zabbix = %+v, want installed from the distro, 1:7.0.22+dfsg-1", z)
	}
}

func TestAgentsNotInstalledAsPackagesAreRecognised(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.noPkg("zabbix-agent2", "zabbix-agent", "wazuh-agent")
	f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\nServerActive=192.0.2.10\n")
	f.write("tmp/zabbix_agent2.log", agentLog(t, "7.0.32-ubuntu-ok"))
	f.service("zabbix-agent2", "active")
	f.write("var/ossec/etc/ossec.conf", ossecConf(t, "198.51.100.5"))
	f.service("wazuh-agent", "inactive")

	st := h.Status(context.Background())

	if z := st.Zabbix; z.State != serveragents.StateUnmanaged || z.Source != serveragents.SourceOther || z.Config == nil || z.Connection == nil || z.Connection.State != serveragents.ConnConnected {
		t.Errorf("zabbix = %+v (connection %+v), want unmanaged, other, its config and connection read", z, z.Connection)
	}
	if w := st.Wazuh; w.State != serveragents.StateUnmanaged || w.Config == nil || w.Connection == nil || w.Connection.State != serveragents.ConnNotRunning {
		t.Errorf("wazuh = %+v (connection %+v), want unmanaged with its config and connection read", w, w.Connection)
	}

	// A loaded unit with no package and no config file is a Zabbix agent too.
	f2, h2 := newHostFixture(t)
	f2.system(osNoble)
	f2.noPkg("zabbix-agent2", "zabbix-agent", "wazuh-agent")
	f2.unit("loaded")
	f2.service("zabbix-agent2", "active")
	if z := h2.Status(context.Background()).Zabbix; z.State != serveragents.StateUnmanaged || z.Connection == nil || z.Connection.State != serveragents.ConnNoServer {
		t.Fatalf("zabbix with only a unit = %+v, want unmanaged with no server", z)
	}
}

func TestAHeldWazuh(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.pkg("wazuh-agent", "hold ok installed|4.14.10-1")

	w := h.Status(context.Background()).Wazuh
	if w.State != serveragents.StateInstalled || !w.Held || w.Version != "4.14.10-1" {
		t.Fatalf("wazuh = %+v, want installed, held, 4.14.10-1", w)
	}
}

func TestAnUnreadableOssecConf(t *testing.T) {
	f, h := installedHost(t, "192.0.2.10")
	f.write("var/ossec/etc/ossec.conf", "<ossec_config>\n  <client>\n    <server>\n      <address>198.51.100.5</address>\n  </client>\n</ossec_config>\n")
	f.agentdState("connected", "2031-03-04 09:59:58", "")

	w := h.Status(context.Background()).Wazuh
	if w.ConfigError != "ossec.conf is not readable XML" || w.Config != nil {
		t.Fatalf("wazuh config %+v, error %q; want no config and the unreadable error", w.Config, w.ConfigError)
	}
	if w.Connection == nil || w.Connection.State != serveragents.ConnConnected {
		t.Errorf("wazuh connection = %+v, want it still read from the state file", w.Connection)
	}
}
