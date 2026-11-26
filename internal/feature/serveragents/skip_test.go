package featureserveragents

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

// wazuhSettings are connection values with this Wazuh version.
func wazuhSettings(version string) *serveragents.Settings {
	return &serveragents.Settings{
		Zabbix: serveragents.ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10"},
		Wazuh:  serveragents.WazuhSettings{Manager: "198.51.100.5", Version: version},
	}
}

// wantSkip fails unless got is a skip for why with exactly these details and
// a message for the run record.
func wantSkip(t *testing.T, got *Skip, why string, details map[string]string) {
	t.Helper()
	if got == nil {
		t.Fatalf("skip = nil, want %s", why)
	}
	if got.Why != why || !maps.Equal(got.Details, details) {
		t.Fatalf("skip = %+v, want why %s, details %v", got, why, details)
	}
	if !strings.HasPrefix(got.Message, "skipped: ") || len(got.Message) <= len("skipped: ") {
		t.Errorf("message = %q, want \"skipped: <why>\"", got.Message)
	}
}

func TestSkipTheOldZabbixAgent(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.noPkg("zabbix-agent2")
	f.pkg("zabbix-agent", "install ok installed|1:6.0.14+dfsg-1")

	wantSkip(t, h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyLegacy, map[string]string{"agent": "zabbix", "why": "legacy"})

	f2, h2 := newHostFixture(t)
	f2.system(osNoble)
	f2.noPkg("zabbix-agent2", "zabbix-agent")
	f2.write("etc/zabbix/zabbix_agentd.conf", "Server=192.0.2.10\n")
	wantSkip(t, h2.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyLegacy, map[string]string{"agent": "zabbix", "why": "legacy"})
}

func TestSkipAnOSWithoutAZabbixPackage(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osQuesting)
	f.noPkg("zabbix-agent2", "zabbix-agent")
	f.unit("not-found")
	f.cmd.on("apt-cache policy zabbix-agent2", "zabbix-agent2:\n  Installed: (none)\n  Candidate: (none)\n  Version table:\n", nil)

	wantSkip(t, h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyNoPackage, map[string]string{"agent": "zabbix", "why": "no_package", "os": "Ubuntu 25.10"})
	if env := f.cmd.env("apt-cache policy zabbix-agent2"); !slices.Contains(env, "LC_ALL=C") {
		t.Errorf("apt-cache ran with %q, want LC_ALL=C so Candidate: is read in English", env)
	}
}

func TestAnOSWithoutTheVendorRepoUsesItsOwnPackage(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osQuesting)
	f.noPkg("zabbix-agent2", "zabbix-agent")
	f.unit("not-found")
	f.cmd.on("apt-cache policy zabbix-agent2", "zabbix-agent2:\n  Installed: (none)\n  Candidate: 1:7.0.27+dfsg-1\n  Version table:\n     1:7.0.27+dfsg-1 500\n", nil)

	if s := h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")); s != nil {
		t.Fatalf("skip = %+v, want nil: the distro has zabbix-agent2", s)
	}
	if !f.cmd.ran("apt-cache policy zabbix-agent2") {
		t.Errorf("ran %q, want the distro asked for its zabbix-agent2", f.cmd.lines())
	}
}

func TestAVendorOSAsksNoAptCache(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.noPkg("zabbix-agent2", "zabbix-agent")
	f.unit("not-found")

	if s := h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")); s != nil {
		t.Fatalf("skip = %+v, want nil on a release the vendor builds for", s)
	}
	if f.cmd.ran("apt-cache") {
		t.Errorf("ran %q, want no apt-cache on a release the vendor builds for", f.cmd.lines())
	}
}

func TestAnInstalledZabbixAgent2IsNotSkipped(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osQuesting)
	f.pkg("zabbix-agent2", "install ok installed|1:7.0.27+dfsg-1")
	f.write("etc/zabbix/zabbix_agent2.conf", testdata(t, "zabbix_agent2.conf"))

	if s := h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")); s != nil {
		t.Fatalf("skip = %+v, want nil for an installed agent with its config", s)
	}
}

func TestSkipAWazuhNewerThanTheManager(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.pkg("wazuh-agent", "install ok installed|4.14.11-1")
	f.write("var/ossec/etc/ossec.conf", ossecConf(t, "198.51.100.5"))

	wantSkip(t, h.Skip(serveragents.Wazuh, wazuhSettings("4.14.10")), WhyNewer, map[string]string{"agent": "wazuh", "why": "newer", "installed": "4.14.11-1", "version": "4.14.10"})
}

func TestAnOlderWazuhIsNotSkipped(t *testing.T) {
	for _, installed := range []string{"4.13.1-1", "4.14.10-1"} {
		f, h := newHostFixture(t)
		f.system(osNoble)
		f.pkg("wazuh-agent", "install ok installed|"+installed)
		f.write("var/ossec/etc/ossec.conf", ossecConf(t, "198.51.100.5"))

		if s := h.Skip(serveragents.Wazuh, wazuhSettings("4.14.10")); s != nil {
			t.Errorf("installed %s: skip = %+v, want nil", installed, s)
		}
	}
}

func TestSkipAgentsNotInstalledAsPackages(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osNoble)
	f.noPkg("wazuh-agent", "zabbix-agent2", "zabbix-agent")
	f.write("var/ossec/etc/ossec.conf", ossecConf(t, "198.51.100.5"))
	f.write("etc/zabbix/zabbix_agent2.conf", testdata(t, "zabbix_agent2.conf"))

	wantSkip(t, h.Skip(serveragents.Wazuh, wazuhSettings("4.14.10")), WhyUnmanaged, map[string]string{"agent": "wazuh", "why": "unmanaged"})
	wantSkip(t, h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyUnmanaged, map[string]string{"agent": "zabbix", "why": "unmanaged"})

	// A unit systemd loads with no package and no config file.
	f2, h2 := newHostFixture(t)
	f2.system(osNoble)
	f2.noPkg("zabbix-agent2", "zabbix-agent")
	f2.unit("loaded")
	wantSkip(t, h2.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyUnmanaged, map[string]string{"agent": "zabbix", "why": "unmanaged"})

	// The package without its config file: nothing to set the values in.
	f3, h3 := newHostFixture(t)
	f3.system(osNoble)
	f3.pkg("zabbix-agent2", "install ok installed|1:7.0.32-2+ubuntu24.04")
	wantSkip(t, h3.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyUnmanaged, map[string]string{"agent": "zabbix", "why": "unmanaged"})
}

func TestSkipANonDebianHost(t *testing.T) {
	f, h := newHostFixture(t)
	f.system(osFedora)

	for _, agent := range serveragents.Names {
		wantSkip(t, h.Skip(agent, wazuhSettings("4.14.10")), WhyOS, map[string]string{"agent": agent, "why": "os", "os": "Fedora Linux 40 (Server Edition)"})
	}
	if got := f.cmd.lines(); len(got) != 0 {
		t.Errorf("ran %q, want nothing run on a host apt does not run on", got)
	}
}

func TestAnAgentRemovedButNotPurgedIsNotThere(t *testing.T) {
	// apt remove leaves the config files and dpkg's "config-files": the
	// agent is gone, and a deploy installs it again over its own config.
	f, h := newHostFixture(t)
	f.system(osQuesting)
	f.pkg("zabbix-agent2", "deinstall ok config-files|1:7.0.10+dfsg-2build1")
	f.noPkg("zabbix-agent")
	f.unit("not-found")
	f.write("etc/zabbix/zabbix_agent2.conf", testdata(t, "zabbix_agent2.conf"))
	f.cmd.on("apt-cache policy zabbix-agent2", "zabbix-agent2:\n  Installed: (none)\n  Candidate: 1:7.0.10+dfsg-2build1\n", nil)
	f.pkg("wazuh-agent", "deinstall ok config-files|4.14.10-1")
	f.write("var/ossec/etc/ossec.conf", ossecConf(t, "198.51.100.5"))

	for _, agent := range serveragents.Names {
		if s := h.Skip(agent, wazuhSettings("4.14.10")); s != nil {
			t.Errorf("%s: skip = %+v, want none for an agent apt removed", agent, s)
		}
	}
	st := h.Status(context.Background())
	if st.Zabbix.State != serveragents.StateNone || st.Wazuh.State != serveragents.StateNone {
		t.Errorf("states zabbix %q, wazuh %q; want none for both", st.Zabbix.State, st.Wazuh.State)
	}

	// Its config file beside a unit systemd still loads is someone's agent.
	f.unit("loaded")
	wantSkip(t, h.Skip(serveragents.Zabbix, wazuhSettings("4.14.10")), WhyUnmanaged, map[string]string{"agent": "zabbix", "why": "unmanaged"})
}
