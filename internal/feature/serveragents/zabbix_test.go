package featureserveragents

import (
	"context"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	zabbixVendorVersion = "1:7.0.32-2+ubuntu24.04"
	zabbixDistroVersion = "1:7.0.22+dfsg-1"
	zabbixConfRel       = "etc/zabbix/zabbix_agent2.conf"
	zabbixKeyringRel    = "usr/share/keyrings/sfpanel-zabbix.gpg"
	zabbixSourcesRel    = "etc/apt/sources.list.d/sfpanel-zabbix.list"
	zabbixPrefRel       = "etc/apt/preferences.d/sfpanel-zabbix"
)

// zabbixConf is the vendor's config with its active Server, ServerActive and
// Hostname lines set to these values, and a HostMetadata line below its
// example when metadata is not empty.
func zabbixConf(t *testing.T, server, active, hostname, metadata string) string {
	t.Helper()
	meta := "\n# HostMetadata=\n"
	if metadata != "" {
		meta += "HostMetadata=" + metadata + "\n"
	}
	return strings.NewReplacer(
		"\nServer=127.0.0.1\n", "\nServer="+server+"\n",
		"\nServerActive=127.0.0.1\n", "\nServerActive="+active+"\n",
		"\nHostname=Zabbix server\n", "\nHostname="+hostname+"\n",
		"\n# HostMetadata=\n", meta,
	).Replace(testdata(t, "zabbix_agent2.conf"))
}

// restartable scripts systemctl enable and restart of the Zabbix agent; a
// restarted agent logs its start, as the real one does, and nothing else
// yet.
func (f *deployFixture) restartable() {
	f.cmd.on("systemctl enable zabbix-agent2", "", nil)
	f.cmd.on("systemctl restart zabbix-agent2", "Created symlink …\n", nil)
	f.restartLogs("7.0.32-ubuntu-ok")
}

// restartLogs makes a restart of the Zabbix agent leave this real log.
func (f *deployFixture) restartLogs(log string) {
	content := agentLog(f.t, log)
	f.cmd.does("systemctl restart zabbix-agent2", func() { f.write(zabbixLogRel, content) })
}

// freshZabbixHost is a host without any Zabbix agent, whose apt installs
// agent2 at version with the vendor's config file.
func freshZabbixHost(t *testing.T, osRelease, version string) *deployFixture {
	t.Helper()
	f := newDeployFixture(t)
	f.system(osRelease)
	f.noPkg("zabbix-agent2", "zabbix-agent")
	f.unit("not-found")
	f.service("zabbix-agent2", "active")
	f.restartable()
	f.io.onApt = func(aptCall) error {
		f.pkg("zabbix-agent2", "install ok installed|"+version)
		f.write(zabbixConfRel, testdata(t, "zabbix_agent2.conf"))
		return nil
	}
	return f
}

// installedZabbixHost is a noble host with agent2 installed at version,
// running since it last started with nothing logged since, with this
// config file.
func installedZabbixHost(t *testing.T, version, conf string) *deployFixture {
	t.Helper()
	f := newDeployFixture(t)
	f.system(osNoble)
	f.pkg("zabbix-agent2", "install ok installed|"+version)
	f.service("zabbix-agent2", "active")
	f.restartable()
	f.write(zabbixConfRel, conf)
	f.write(zabbixLogRel, agentLog(t, "7.0.32-ubuntu-ok"))
	return f
}

// settledZabbixHost is an installed agent whose config already holds Server
// server, ServerActive 192.0.2.10 and Hostname node-a, and no HostMetadata.
func settledZabbixHost(t *testing.T, server string) *deployFixture {
	t.Helper()
	return installedZabbixHost(t, zabbixDistroVersion, zabbixConf(t, server, "192.0.2.10", "node-a", ""))
}

func (f *deployFixture) deploy(ctx context.Context, step tasks.AgentStep) Outcome {
	return f.d.Deploy(ctx, step, f.io)
}

// mustSucceed fails the test, with the deploy's log, unless it succeeded.
func (f *deployFixture) mustSucceed(out Outcome) {
	f.t.Helper()
	if out.Status != tasks.JobSucceeded {
		f.t.Fatalf("outcome %+v, want succeeded; log:\n%s", out, strings.Join(f.io.logs, "\n"))
	}
}

func (f *deployFixture) conf() serveragents.ZabbixConf {
	return serveragents.ReadZabbixConf([]byte(f.read(zabbixConfRel)))
}

// allows are the ufw allow command lines run.
func (f *deployFixture) allows() []string {
	var got []string
	for _, l := range f.cmd.lines() {
		if strings.HasPrefix(l, "ufw allow") {
			got = append(got, l)
		}
	}
	return got
}

func TestAFreshUbuntuGetsTheVendorRepositoryAndAgent2(t *testing.T) {
	f := freshZabbixHost(t, osNoble, zabbixVendorVersion)
	// A commented-out line naming the vendor is not a source.
	f.write("etc/apt/sources.list", "# deb https://repo.zabbix.com/zabbix/6.0/ubuntu noble main\ndeb http://archive.ubuntu.com/ubuntu noble main\n")

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", "linux-fleet-auto"))

	f.mustSucceed(out)
	if !out.Changed {
		t.Error("Changed = false, want true after an install")
	}
	want, err := serveragents.SelectKey([]byte(testdata(t, "zabbix-official-repo.key")), serveragents.ZabbixKeyFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.read(zabbixKeyringRel); got != string(want) {
		t.Errorf("keyring holds %d bytes, want the %d bytes of the pinned key alone", len(got), len(want))
	}
	if got, want := f.read(zabbixSourcesRel), "deb [signed-by=/usr/share/keyrings/sfpanel-zabbix.gpg] https://repo.zabbix.com/zabbix/7.0/ubuntu noble main\n"; got != want {
		t.Errorf("sources = %q, want %q", got, want)
	}
	if got, want := f.read(zabbixPrefRel), "Package: zabbix-agent2 zabbix-agent2-plugin-*\nPin: origin repo.zabbix.com\nPin-Priority: 1001\n"; got != want {
		t.Errorf("pin = %q, want %q", got, want)
	}
	if want := []aptCall{{Kind: "install", Names: []string{"zabbix-agent2"}, Update: true}}; !slices.EqualFunc(f.io.apts, want, aptEqual) {
		t.Errorf("apt = %+v, want %+v", f.io.apts, want)
	}
	c := f.conf()
	if c.Server != "192.0.2.10" || c.ServerActive != "192.0.2.10" || c.HostMetadata != "linux-fleet-auto" || c.Hostname != "node-a" {
		t.Errorf("config = %+v, want the cluster's values and Hostname node-a in place of the vendor's %q", c, serveragents.ZabbixVendorHostname)
	}
	lines := f.cmd.lines()
	enable, restart := slices.Index(lines, "systemctl enable zabbix-agent2"), slices.Index(lines, "systemctl restart zabbix-agent2")
	if enable < 0 || restart < 0 || enable > restart {
		t.Errorf("commands %q, want systemctl enable then restart zabbix-agent2", lines)
	}
	for _, s := range []string{"installed " + zabbixVendorVersion, "connected to 192.0.2.10:10051"} {
		if !strings.Contains(out.Summary, s) {
			t.Errorf("summary %q, want it to say %q", out.Summary, s)
		}
	}
}

func aptEqual(a, b aptCall) bool {
	return a.Kind == b.Kind && a.Update == b.Update && slices.Equal(a.Names, b.Names)
}

func TestAKeyWithAnotherFingerprintIsNotTrusted(t *testing.T) {
	f := freshZabbixHost(t, osNoble, zabbixVendorVersion)
	f.serve(zabbixKeyPath, []byte(testdata(t, "GPG-KEY-WAZUH")))

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobFailed {
		t.Errorf("status %q, want failed", out.Status)
	}
	if want := "does not have fingerprint " + serveragents.ZabbixKeyFingerprint; !strings.Contains(out.Summary, want) || !strings.Contains(out.Summary, f.h.KeyURLs[serveragents.Zabbix]) {
		t.Errorf("summary %q, want it to name the key's URL and say it %s", out.Summary, want)
	}
	for _, rel := range []string{zabbixKeyringRel, zabbixSourcesRel, zabbixPrefRel} {
		if f.has(rel) {
			t.Errorf("%s written, want nothing from an untrusted key", rel)
		}
	}
	if len(f.io.apts) != 0 {
		t.Errorf("apt = %+v, want nothing installed", f.io.apts)
	}
}

func TestAnExistingZabbixSourceIsUsedAsItIs(t *testing.T) {
	f := freshZabbixHost(t, osNoble, zabbixVendorVersion)
	const own = "deb [signed-by=/usr/share/keyrings/zabbix-official-repo.gpg] https://repo.zabbix.com/zabbix/7.0/ubuntu noble main\n"
	f.write("etc/apt/sources.list.d/zabbix.list", own)

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	for _, rel := range []string{zabbixKeyringRel, zabbixSourcesRel, zabbixPrefRel} {
		if f.has(rel) {
			t.Errorf("%s written, want the host's own Zabbix source used as it is", rel)
		}
	}
	if got := f.read("etc/apt/sources.list.d/zabbix.list"); got != own {
		t.Errorf("zabbix.list = %q, want it unchanged", got)
	}
	if n := f.hits(); n != 0 {
		t.Errorf("key fetched %d times, want none", n)
	}
	if want := []aptCall{{Kind: "install", Names: []string{"zabbix-agent2"}, Update: true}}; !slices.EqualFunc(f.io.apts, want, aptEqual) {
		t.Errorf("apt = %+v, want %+v", f.io.apts, want)
	}
}

func TestAnExistingDeb822ZabbixSourceIsUsedAsItIs(t *testing.T) {
	f := freshZabbixHost(t, osNoble, zabbixVendorVersion)
	const own = "Types: deb\nURIs: https://repo.zabbix.com/zabbix/7.0/ubuntu\nSuites: noble\nComponents: main\nSigned-By: /usr/share/keyrings/zabbix-official-repo.gpg\n"
	f.write("etc/apt/sources.list.d/zabbix.sources", own)

	f.mustSucceed(f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", "")))

	if f.has(zabbixSourcesRel) || f.hits() != 0 {
		t.Errorf("%s written or the key fetched, want the host's own 7.0 source used", zabbixSourcesRel)
	}
}

func TestAZabbixSourceThatIsNotAnEnabled70IsNotUsed(t *testing.T) {
	for _, c := range []struct{ name, file, content, why string }{
		{"another series", "zabbix.list", "deb https://repo.zabbix.com/zabbix/6.0/ubuntu noble main\n", "not for zabbix/7.0"},
		{"the tools alone", "zabbix-tools.list", "deb [signed-by=/etc/apt/keyrings/zabbix-tools.gpg] https://repo.zabbix.com/zabbix-tools/debian-ubuntu noble main\n", "not for zabbix/7.0"},
		{"turned off", "zabbix.sources", "Types: deb\nURIs: https://repo.zabbix.com/zabbix/7.0/ubuntu\nSuites: noble\nComponents: main\nEnabled: no\n", "turned off"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := freshZabbixHost(t, osNoble, zabbixVendorVersion)
			f.write("etc/apt/sources.list.d/"+c.file, c.content)

			f.mustSucceed(f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", "")))

			if got, want := f.read(zabbixSourcesRel), "deb [signed-by=/usr/share/keyrings/sfpanel-zabbix.gpg] https://repo.zabbix.com/zabbix/7.0/ubuntu noble main\n"; got != want {
				t.Errorf("sources = %q, want the 7.0 source added: %q", got, want)
			}
			if f.read("etc/apt/sources.list.d/"+c.file) != c.content {
				t.Errorf("%s changed, want the host's own source left alone", c.file)
			}
			if !f.io.logged("/etc/apt/sources.list.d/"+c.file) || !f.io.logged(c.why) {
				t.Errorf("log %q, want it to name %s and why it is not used (%s)", f.io.logs, c.file, c.why)
			}
		})
	}
}

func TestAnInstalledDistroAgentIsKeptAndOnlyItsValuesChange(t *testing.T) {
	old := zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", "")
	f := installedZabbixHost(t, zabbixDistroVersion, old)

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	if !out.Changed || !strings.Contains(out.Summary, "configured") {
		t.Errorf("outcome %+v, want changed and configured", out)
	}
	for _, rel := range []string{zabbixKeyringRel, zabbixSourcesRel, zabbixPrefRel} {
		if f.has(rel) {
			t.Errorf("%s written, want no repository for an agent that is already installed", rel)
		}
	}
	if len(f.io.apts) != 0 {
		t.Errorf("apt = %+v, want nothing installed over the distro's agent", f.io.apts)
	}
	c := f.conf()
	if c.Hostname != "web-host" {
		t.Errorf("Hostname = %q, want the agent's own web-host kept", c.Hostname)
	}
	if c.Server != "192.0.2.10" || c.ServerActive != "192.0.2.10" {
		t.Errorf("config = %+v, want Server and ServerActive 192.0.2.10", c)
	}
	if strings.Contains(f.read(zabbixConfRel), "\nHostMetadata=") {
		t.Error("a HostMetadata line was written, want none while the cluster sets no metadata")
	}
	backups := f.backups(zabbixConfRel)
	if len(backups) != 1 {
		t.Fatalf("backups %q, want one", backups)
	}
	if got := f.read(strings.TrimPrefix(backups[0], f.root)); got != old {
		t.Error("the backup does not hold the old config")
	}
	if !f.cmd.ran("systemctl restart zabbix-agent2") {
		t.Error("not restarted after its config changed")
	}
}

func TestADuplicatedServerLineEndsAsOneLine(t *testing.T) {
	f := installedZabbixHost(t, zabbixDistroVersion, zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", "")+"Server=192.0.2.99\n")

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	c := f.conf()
	if len(c.Duplicates) != 0 || c.Server != "192.0.2.10" {
		t.Errorf("config = %+v, want one Server line, 192.0.2.10", c)
	}
	if after := f.read(zabbixConfRel); !strings.HasSuffix(after, "\n# Server=192.0.2.99\n") {
		t.Errorf("config ends %q, want the second Server line commented out", after[max(len(after)-40, 0):])
	}
}

func TestNothingToChangeRestartsNothing(t *testing.T) {
	conf := zabbixConf(t, "192.0.2.10", "192.0.2.10", "node-a", "linux-fleet-auto")
	f := installedZabbixHost(t, zabbixDistroVersion, conf)
	f.write("var/log/zabbix/zabbix_agent2.log", logStart+logAgain)

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", "linux-fleet-auto"))

	f.mustSucceed(out)
	if out.Changed {
		t.Error("Changed = true, want false when everything was already set")
	}
	if !strings.Contains(out.Summary, "already set") || !strings.Contains(out.Summary, "connected to 192.0.2.10:10051") {
		t.Errorf("summary %q, want already set and connected", out.Summary)
	}
	if f.cmd.ran("systemctl restart") || f.cmd.ran("systemctl enable") {
		t.Errorf("commands %q, want no restart of an active agent with nothing changed", f.cmd.lines())
	}
	if f.read(zabbixConfRel) != conf || len(f.backups(zabbixConfRel)) != 0 {
		t.Error("the config was rewritten, want it left alone")
	}
	if f.sleeps != 0 {
		t.Errorf("waited %d times, want the connection judged at once", f.sleeps)
	}
}

func TestUbuntu2510UsesTheDistroPackageWithoutARepository(t *testing.T) {
	f := freshZabbixHost(t, osQuesting, "1:7.0.27+dfsg-1")
	f.cmd.on("apt-cache policy zabbix-agent2", "zabbix-agent2:\n  Installed: (none)\n  Candidate: 1:7.0.27+dfsg-1\n  Version table:\n     1:7.0.27+dfsg-1 500\n", nil)

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	if want := []aptCall{{Kind: "install", Names: []string{"zabbix-agent2"}, Update: true}}; !slices.EqualFunc(f.io.apts, want, aptEqual) {
		t.Errorf("apt = %+v, want %+v", f.io.apts, want)
	}
	for _, dir := range []string{"etc/apt/sources.list.d", "etc/apt/preferences.d", "usr/share/keyrings"} {
		for rel := range f.tree() {
			if strings.HasPrefix(rel, dir+"/") {
				t.Errorf("%s written, want no repository on a release the vendor does not build for", rel)
			}
		}
	}
	if n := f.hits(); n != 0 {
		t.Errorf("key fetched %d times, want none", n)
	}
}

func TestAManagedKeyInAnIncludedFileIsRefused(t *testing.T) {
	conf := zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", "")
	f := installedZabbixHost(t, zabbixDistroVersion, conf)
	f.write("etc/zabbix/zabbix_agent2.d/override.conf", "Server=192.0.2.30\n")

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobFailed {
		t.Errorf("status %q, want failed", out.Status)
	}
	if want := "Server is also set in /etc/zabbix/zabbix_agent2.d/override.conf; change it there"; !strings.Contains(out.Summary, want) {
		t.Errorf("summary %q, want %q", out.Summary, want)
	}
	if f.read(zabbixConfRel) != conf {
		t.Error("the config was changed, want it left alone")
	}
	if f.cmd.ran("systemctl restart") {
		t.Error("restarted, want nothing done")
	}
}

func TestAManagedKeyInALinkedIncludedFileIsRefused(t *testing.T) {
	// A directory included whole, holding a link to the file that sets it.
	conf := zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", "") + "Include=/etc/zabbix/extra/\n"
	f := installedZabbixHost(t, zabbixDistroVersion, conf)
	f.write("etc/zabbix/shared/override.conf", "Server=192.0.2.30\n")
	f.symlink("../shared/override.conf", "etc/zabbix/extra/override.conf")

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if want := "Server is also set in /etc/zabbix/extra/override.conf; change it there"; out.Status != tasks.JobFailed || !strings.Contains(out.Summary, want) {
		t.Errorf("outcome %+v, want failed: %q", out, want)
	}
	if f.read(zabbixConfRel) != conf {
		t.Error("the config was changed, want it left alone")
	}
}

func TestTheAgentMustReallyConnect(t *testing.T) {
	f := installedZabbixHost(t, zabbixDistroVersion, zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", ""))
	f.dials["192.0.2.10:10051"] = errors.New("dial tcp 192.0.2.10:10051: connect: connection refused")

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobFailed || !strings.HasPrefix(out.Summary, "not connected after 1m0s: unreachable") {
		t.Fatalf("outcome %+v, want failed, not connected after 1m0s: unreachable", out)
	}
	if !strings.Contains(out.Summary, "connection refused") {
		t.Errorf("summary %q, want the dial error", out.Summary)
	}
	if waited := f.now.Sub(fixtureNow); waited != time.Minute {
		t.Errorf("waited %v, want the whole minute", waited)
	}
	if !out.Changed {
		t.Error("Changed = false, want true: the config was written")
	}
}

func TestAHostTheServerRegistersAtItsFirstRequestConnects(t *testing.T) {
	// A server that registers a host it does not know (autoregistration)
	// refuses its first request; agent 7.0.32 logs the refusal and "started
	// to fail", then "are active again" once a later request is taken.
	f := installedZabbixHost(t, zabbixVendorVersion, zabbixConf(t, "192.0.2.19", "192.0.2.19", "node-a", ""))
	f.restartLogs("7.0.32-ubuntu-notfound")
	f.onSleep = func(n int) {
		if n == 2 {
			f.write(zabbixLogRel, agentLog(t, "7.0.32-ubuntu-notfound-recovered"))
		}
	}

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	if f.sleeps != 2 {
		t.Errorf("succeeded after %d waits, want 2: not while the server did not know the host", f.sleeps)
	}
	if !f.io.logged("5s: not_registered") {
		t.Errorf("log %q, want the first wait to say not_registered", f.io.logs)
	}
}

// at10 is when the 7.0.10 agent of testdata/agent2-log/7.0.10-autoreg.log
// started; it was refused at 12:33:51 and taken at its next request.
var at10 = time.Date(2026, 11, 26, 12, 33, 49, 0, time.UTC)

func TestAnAgentThatSaysNothingOnceTakenConnectsWhenItFallsSilent(t *testing.T) {
	f := installedZabbixHost(t, "1:7.0.10+dfsg-2build1", zabbixConf(t, "192.0.2.19", "192.0.2.19", "node-a", ""))
	f.restartLogs("7.0.10-autoreg")
	f.now = at10

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	// Refused at 12:33:51, silent since: three refreshes of 5s later it is
	// taken, read at the poll of 12:34:09.
	if waited := f.now.Sub(at10); waited != 20*time.Second {
		t.Errorf("succeeded %v after the restart, want 20s", waited)
	}
	if !f.io.logged("15s: not_registered") {
		t.Errorf("log %q, want not_registered until it had been silent long enough", f.io.logs)
	}
}

func TestAnAgentThatKeepsBeingRefusedNeverConnects(t *testing.T) {
	f := installedZabbixHost(t, "1:7.0.10+dfsg-2build1", zabbixConf(t, "192.0.2.19", "192.0.2.19", "node-a", ""))
	f.restartLogs("7.0.10-autoreg")
	f.now = at10
	refused := "[101] no active checks on server [zbx-fake:10051]: host [node-u2510] not found\n"
	f.onSleep = func(int) {
		// The refusal again, a second ago, as at every refresh.
		f.write(zabbixLogRel, f.read(zabbixLogRel)+f.now.Add(-time.Second).Format("2006/01/02 15:04:05.000000 ")+refused)
	}

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobFailed || !strings.HasPrefix(out.Summary, "not connected after 1m0s: not_registered") {
		t.Fatalf("outcome %+v, want failed, not connected after 1m0s: not_registered", out)
	}
}

func TestAnActiveAgentThatIsNotConnectedIsRestarted(t *testing.T) {
	// Values already set, service active — but its config could have been
	// written by a run that died before the restart, and it still talks to
	// the old server, or as here, the server refused it.
	f := settledZabbixHost(t, "192.0.2.10")
	f.write(zabbixLogRel, agentLog(t, "7.0.32-ubuntu-notfound"))

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	if !f.cmd.ran("systemctl restart zabbix-agent2") {
		t.Fatalf("commands %q, want the agent restarted", f.cmd.lines())
	}
	if !f.io.logged("zabbix-agent2 is active but not connected (not_registered") {
		t.Errorf("log %q, want it to say why it restarts", f.io.logs)
	}
	if !out.Changed || !strings.Contains(out.Summary, "already set; started") {
		t.Errorf("outcome %+v, want changed and already set; started", out)
	}
}

func TestAConnectionIsJudgedOnlyAfterTheSettleTime(t *testing.T) {
	f := installedZabbixHost(t, zabbixDistroVersion, zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", ""))
	var dialledAt []time.Duration
	dial := f.h.Dial
	f.h.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialledAt = append(dialledAt, f.now.Sub(fixtureNow))
		return dial(ctx, network, addr)
	}

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	f.mustSucceed(out)
	if !slices.Contains(dialledAt, 5*time.Second) {
		t.Errorf("dialled at %v, want a check at the first poll, 5s", dialledAt)
	}
	if waited := f.now.Sub(fixtureNow); waited < 10*time.Second {
		t.Errorf("succeeded %v after the restart, want at least 10s: the dial reached at 5s, but the agent had not run long enough", waited)
	}
}

func TestUfwIsOpenedForTheServersOnly(t *testing.T) {
	const servers = "192.0.2.10,198.51.100.0/24,zbx.example.net"
	allow := func(from string) string {
		return "ufw allow from " + from + " to any port 10050 proto tcp comment sfpanel-zabbix-agent"
	}

	t.Run("active", func(t *testing.T) {
		f := settledZabbixHost(t, servers)
		f.cmd.on("ufw status", "Status: active\n\nTo                         Action      From\n--                         ------      ----\n22/tcp                     ALLOW       Anywhere\n", nil)
		f.cmd.on(allow("192.0.2.10"), "Rule added\n", nil)
		f.cmd.on(allow("198.51.100.0/24"), "Rule added\n", nil)

		out := f.deploy(context.Background(), zabbixStep(servers, "192.0.2.10", ""))

		f.mustSucceed(out)
		if got, want := f.allows(), []string{allow("192.0.2.10"), allow("198.51.100.0/24")}; !slices.Equal(got, want) {
			t.Errorf("ufw allows %q, want %q: the addresses only, not the name", got, want)
		}
		if env := f.cmd.env("ufw status"); !slices.Contains(env, "LC_ALL=C") {
			t.Errorf("ufw status ran with %q, want LC_ALL=C", env)
		}
		if !f.io.logged("zbx.example.net") {
			t.Errorf("log %q, want it to name zbx.example.net, left out of ufw", f.io.logs)
		}
		if !out.Changed {
			t.Error("Changed = false, want true: ufw rules were added")
		}
	})

	t.Run("inactive", func(t *testing.T) {
		f := settledZabbixHost(t, servers)
		f.cmd.on("ufw status", "Status: inactive\n", nil)

		f.mustSucceed(f.deploy(context.Background(), zabbixStep(servers, "192.0.2.10", "")))

		if !f.cmd.ran("ufw status") {
			t.Error("ufw status not read")
		}
		if got := f.allows(); len(got) != 0 {
			t.Errorf("ufw allows %q, want none while ufw is inactive", got)
		}
	})

	t.Run("proxmox", func(t *testing.T) {
		f := settledZabbixHost(t, servers)
		f.h.Platform = func() platform.Info { return platform.Info{PVE: true} }
		f.cmd.on("ufw status", "Status: active\n", nil)

		f.mustSucceed(f.deploy(context.Background(), zabbixStep(servers, "192.0.2.10", "")))

		if f.cmd.ran("ufw") {
			t.Errorf("commands %q, want no ufw call on Proxmox VE", f.cmd.lines())
		}
	})
}

func TestACancelledDeployWritesNothing(t *testing.T) {
	f := freshZabbixHost(t, osNoble, zabbixVendorVersion)
	before := f.tree()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := f.deploy(ctx, zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobCancelled {
		t.Fatalf("status %q, want cancelled", out.Status)
	}
	if after := f.tree(); !maps.Equal(before, after) {
		t.Errorf("root changed: %q → %q", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	}
	if lines := f.cmd.lines(); len(lines) != 0 || len(f.io.apts) != 0 || f.hits() != 0 {
		t.Errorf("ran %q, apt %+v, %d key fetches; want nothing", lines, f.io.apts, f.hits())
	}
}

func TestACancelAfterTheConfigIsWrittenStillRestarts(t *testing.T) {
	f := installedZabbixHost(t, zabbixDistroVersion, zabbixConf(t, "192.0.2.19", "192.0.2.19", "web-host", ""))
	ctx, cancel := context.WithCancel(context.Background())
	// Platform is asked for at the firewall, after the config is written and
	// before the restart.
	f.h.Platform = func() platform.Info { cancel(); return platform.Info{} }

	out := f.deploy(ctx, zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if f.conf().Server != "192.0.2.10" {
		t.Fatal("config not written; the cancel came too early to test this")
	}
	if !f.cmd.ran("systemctl restart zabbix-agent2") {
		t.Error("not restarted: a running agent would keep its old values, and a run again finds nothing to change")
	}
	if out.Status != tasks.JobCancelled || !out.Changed {
		t.Errorf("outcome %+v, want cancelled, changed", out)
	}
}

func TestAnAptStoppedByACancelIsCancelled(t *testing.T) {
	f := freshZabbixHost(t, osQuesting, "1:7.0.27+dfsg-1")
	f.cmd.on("apt-cache policy zabbix-agent2", "zabbix-agent2:\n  Installed: (none)\n  Candidate: 1:7.0.27+dfsg-1\n", nil)
	ctx, cancel := context.WithCancel(context.Background())
	f.io.onApt = func(aptCall) error {
		cancel()
		return errors.New("install failed with exit status 100")
	}

	out := f.deploy(ctx, zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobCancelled || !strings.Contains(out.Summary, "cancelled") {
		t.Errorf("outcome %+v, want cancelled", out)
	}
}

func TestASkipFoundAtDeployIsAFailure(t *testing.T) {
	f := newDeployFixture(t)
	f.system(osFedora)

	out := f.deploy(context.Background(), zabbixStep("192.0.2.10", "192.0.2.10", ""))

	if out.Status != tasks.JobFailed || !strings.HasPrefix(out.Summary, "skipped: ") || !strings.Contains(out.Summary, "not Debian or Ubuntu") {
		t.Errorf("outcome %+v, want failed with the skip's message", out)
	}
	if lines := f.cmd.lines(); len(lines) != 0 {
		t.Errorf("ran %q, want nothing", lines)
	}
}

func TestTheServerNameIsWrittenOnlyWhereTheAgentHasNone(t *testing.T) {
	withItem := func(conf string) string {
		return strings.Replace(conf, "# HostnameItem=system.hostname", "HostnameItem=system.hostname", 1)
	}
	cases := []struct {
		name, conf, nodeName, want string
	}{
		{"empty Hostname", zabbixConf(t, "192.0.2.10", "192.0.2.10", "", ""), "node-a", "node-a"},
		{"named by HostnameItem", withItem(zabbixConf(t, "192.0.2.10", "192.0.2.10", "", "")), "node-a", ""},
		// A Hostname, even the package's, wins over HostnameItem: left as it
		// is, every such server would be "Zabbix server".
		{"the package's name beside HostnameItem", withItem(testdata(t, "zabbix_agent2.conf")), "node-a", "node-a"},
		{"no name, and one Zabbix refuses", zabbixConf(t, "192.0.2.10", "192.0.2.10", "", ""), "node/a", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := installedZabbixHost(t, zabbixDistroVersion, c.conf)
			step := zabbixStep("192.0.2.10", "192.0.2.10", "")
			step.NodeName = c.nodeName

			f.mustSucceed(f.deploy(context.Background(), step))

			if got := f.conf().Hostname; got != c.want {
				t.Errorf("Hostname = %q, want %q", got, c.want)
			}
		})
	}
}

func TestThePackagesNameIsNotKeptWhenTheServersIsRefused(t *testing.T) {
	conf := testdata(t, "zabbix_agent2.conf")
	f := installedZabbixHost(t, zabbixVendorVersion, conf)
	step := zabbixStep("192.0.2.10", "192.0.2.10", "")
	step.NodeName = "node/a"

	out := f.deploy(context.Background(), step)

	if out.Status != tasks.JobFailed {
		t.Fatalf("outcome %+v, want failed", out)
	}
	if want := `Hostname is still the package's "Zabbix server", and this server's name "node/a" is not one Zabbix accepts`; !strings.Contains(out.Summary, want) {
		t.Errorf("summary %q, want it to say %q", out.Summary, want)
	}
	if f.read(zabbixConfRel) != conf || f.cmd.ran("systemctl restart") {
		t.Error("the config was changed or the agent restarted, want nothing done")
	}
}
