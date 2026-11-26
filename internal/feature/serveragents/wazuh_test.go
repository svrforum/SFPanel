package featureserveragents

import (
	"context"
	"errors"
	"net"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	ossecConfRel    = "var/ossec/etc/ossec.conf"
	clientKeysRel   = "var/ossec/etc/client.keys"
	agentdStateRel  = "var/ossec/var/run/wazuh-agentd.state"
	wazuhKeyringRel = "usr/share/keyrings/sfpanel-wazuh.gpg"
	wazuhSourcesRel = "etc/apt/sources.list.d/sfpanel-wazuh.list"
	// enrolledKeys is client.keys of an agent enrolled with its manager.
	enrolledKeys = "001 node-a any KEY\n"
	// wazuhNewest is the repository's newest agent, ahead of the manager.
	wazuhNewest = "4.14.11-1"
)

// wazuhStep is an agent step for Wazuh with these values, for node-a.
func wazuhStep(manager, groups, version string) tasks.AgentStep {
	return tasks.AgentStep{
		Name:     serveragents.Wazuh,
		NodeName: "node-a",
		Settings: &serveragents.Settings{Wazuh: serveragents.WazuhSettings{Manager: manager, Groups: groups, Version: version}},
	}
}

// ossecWith is the vendor's ossec.conf with its server's address set to
// address, and — when groups is not empty — an enrollment that names the
// same manager and groups.
func ossecWith(t *testing.T, address, groups string) string {
	t.Helper()
	enrollment := "<enabled>yes</enabled>\n    </enrollment>"
	withGroups := enrollment
	if groups != "" {
		withGroups = "<enabled>yes</enabled>\n      <manager_address>" + address + "</manager_address>\n      <groups>" + groups + "</groups>\n    </enrollment>"
	}
	return strings.NewReplacer(
		"<address>MANAGER_IP</address>", "<address>"+address+"</address>",
		enrollment, withGroups,
	).Replace(testdata(t, "ossec.conf"))
}

// wazuhHost is a noble host whose wazuh-agent unit is active and enables,
// restarts and holds when asked, and whose agent, once the deploy waits for
// it, rewrites its state file at every poll saying it is connected.
func wazuhHost(t *testing.T) *deployFixture {
	t.Helper()
	f := newDeployFixture(t)
	f.system(osNoble)
	f.service("wazuh-agent", "active")
	f.cmd.on("systemctl enable wazuh-agent", "", nil)
	f.cmd.on("systemctl restart wazuh-agent", "", nil)
	f.cmd.on("apt-mark hold wazuh-agent", "wazuh-agent set on hold.\n", nil)
	f.agentReports("connected")
	return f
}

// agentReports makes the agent rewrite its state file with status at every
// poll.
func (f *deployFixture) agentReports(status string) {
	f.onSleep = func(int) { f.agentdState(status, "2031-03-04 10:00:05", "2031-03-04 10:00:05") }
}

// freshWazuhHost is a host without a Wazuh agent, whose apt installs the
// version asked for — or, asked for none, the repository's newest — with
// the vendor's ossec.conf.
func freshWazuhHost(t *testing.T) *deployFixture {
	t.Helper()
	f := wazuhHost(t)
	f.noPkg("wazuh-agent")
	f.io.onApt = func(c aptCall) error {
		version := wazuhNewest
		if _, v, ok := strings.Cut(c.Names[0], "="); ok {
			version = v
		}
		f.pkg("wazuh-agent", "install ok installed|"+version)
		f.write(ossecConfRel, testdata(t, "ossec.conf"))
		return nil
	}
	return f
}

// installedWazuhHost is a host with wazuh-agent installed, dpkg saying
// statusVersion of it, with this ossec.conf and, when keys is not empty,
// this client.keys.
func installedWazuhHost(t *testing.T, statusVersion, conf, keys string) *deployFixture {
	t.Helper()
	f := wazuhHost(t)
	f.pkg("wazuh-agent", statusVersion)
	f.write(ossecConfRel, conf)
	if keys != "" {
		f.write(clientKeysRel, keys)
	}
	return f
}

func (f *deployFixture) ossec() serveragents.OssecConf {
	f.t.Helper()
	c, err := serveragents.ReadOssecConf([]byte(f.read(ossecConfRel)))
	if err != nil {
		f.t.Fatalf("ossec.conf after the deploy: %v", err)
	}
	return c
}

func TestAFreshServerGetsThePinnedWazuhAndHoldsIt(t *testing.T) {
	f := freshWazuhHost(t)
	linesBeforeApt := -1
	install := f.io.onApt
	f.io.onApt = func(c aptCall) error {
		linesBeforeApt = len(f.cmd.lines())
		return install(c)
	}

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	f.mustSucceed(out)
	if !out.Changed {
		t.Error("Changed = false, want true after an install")
	}
	want, err := serveragents.SelectKey([]byte(testdata(t, "GPG-KEY-WAZUH")), serveragents.WazuhKeyFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.read(wazuhKeyringRel); got != string(want) {
		t.Errorf("keyring holds %d bytes, want the %d bytes of the pinned key alone", len(got), len(want))
	}
	if got, want := f.read(wazuhSourcesRel), "deb [signed-by=/usr/share/keyrings/sfpanel-wazuh.gpg] https://packages.wazuh.com/4.x/apt/ stable main\n"; got != want {
		t.Errorf("sources = %q, want %q", got, want)
	}
	for rel := range f.tree() {
		if strings.HasPrefix(rel, "etc/apt/preferences.d/") {
			t.Errorf("%s written, want no pin: the version installed is named exactly", rel)
		}
	}
	if want := []aptCall{{Kind: "install", Names: []string{"wazuh-agent=4.14.10-1"}, Update: true}}; !slices.EqualFunc(f.io.apts, want, aptEqual) {
		t.Errorf("apt = %+v, want %+v", f.io.apts, want)
	}
	lines := f.cmd.lines()
	if hold := slices.Index(lines, "apt-mark hold wazuh-agent"); hold < 0 || hold < linesBeforeApt {
		t.Errorf("commands %q (apt ran after the first %d), want apt-mark hold wazuh-agent after the install", lines, linesBeforeApt)
	}
	c := f.ossec()
	if !slices.Equal(c.Servers, []string{"198.51.100.5"}) || c.EnrollmentManager != "198.51.100.5" || c.Groups != "default" {
		t.Errorf("ossec.conf = %+v, want server and enrollment manager 198.51.100.5, groups default", c)
	}
	enable, restart := slices.Index(lines, "systemctl enable wazuh-agent"), slices.Index(lines, "systemctl restart wazuh-agent")
	if enable < 0 || restart < 0 || enable > restart {
		t.Errorf("commands %q, want systemctl enable then restart wazuh-agent", lines)
	}
	for _, s := range []string{"installed 4.14.10-1", "connected to 198.51.100.5"} {
		if !strings.Contains(out.Summary, s) {
			t.Errorf("summary %q, want it to say %q", out.Summary, s)
		}
	}
}

func TestTheRepositorysNewestWazuhIsNeverInstalled(t *testing.T) {
	for _, c := range []struct{ version, want string }{
		{"4.14.10", "wazuh-agent=4.14.10-1"},
		{"4.13.1", "wazuh-agent=4.13.1-1"},
		{"", "wazuh-agent=" + serveragents.DefaultWazuhVersion + "-1"},
	} {
		t.Run("version "+c.version, func(t *testing.T) {
			f := freshWazuhHost(t)

			f.mustSucceed(f.deploy(context.Background(), wazuhStep("198.51.100.5", "", c.version)))

			if len(f.io.apts) == 0 {
				t.Fatal("nothing installed")
			}
			for _, a := range f.io.apts {
				for _, name := range a.Names {
					if name != c.want {
						t.Errorf("apt %s %q, want exactly %q: the repository's newest can be ahead of the manager", a.Kind, name, c.want)
					}
				}
			}
		})
	}
}

func TestReenrollOnlyWhenTheManagerChanged(t *testing.T) {
	const manager = "198.51.100.5"
	t.Run("another manager", func(t *testing.T) {
		f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, "192.0.2.19", "default"), enrolledKeys)

		out := f.deploy(context.Background(), wazuhStep(manager, "", "4.14.10"))

		f.mustSucceed(out)
		if got := f.read(clientKeysRel); got != "" {
			t.Errorf("client.keys = %q, want it emptied so the agent enrolls with the new manager", got)
		}
		backups := f.backups(clientKeysRel)
		if len(backups) != 1 {
			t.Fatalf("client.keys backups %q, want one", backups)
		}
		if got := f.read(strings.TrimPrefix(backups[0], f.root)); got != enrolledKeys {
			t.Errorf("backup holds %q, want the old key %q", got, enrolledKeys)
		}
		if !f.io.logged("re-enroll: manager changed from 192.0.2.19 to 198.51.100.5") {
			t.Errorf("log %q, want it to say the agent re-enrolls because the manager changed", f.io.logs)
		}
		if c := f.ossec(); !slices.Equal(c.Servers, []string{manager}) || c.EnrollmentManager != manager {
			t.Errorf("ossec.conf = %+v, want the new manager", c)
		}
		if !strings.Contains(out.Summary, "re-enrolled") {
			t.Errorf("summary %q, want it to say re-enrolled", out.Summary)
		}
	})

	// Each of these leaves an enrolled agent's key alone.
	for _, c := range []struct{ name, address, groups, to, setGroups, wantGroups string }{
		{"the same manager, other groups", manager, "default", manager, "linux,web", "linux,web"},
		{"the same manager spelled another way", "ZBX.example.net", "default", "zbx.example.net", "", "default"},
		{"the package's placeholder", serveragents.OssecPlaceholder, "", manager, "", "default"},
		{"no address", "", "", manager, "", "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, c.address, c.groups), enrolledKeys)

			f.mustSucceed(f.deploy(context.Background(), wazuhStep(c.to, c.setGroups, "4.14.10")))

			if got := f.read(clientKeysRel); got != enrolledKeys {
				t.Errorf("client.keys = %q, want %q kept", got, enrolledKeys)
			}
			if b := f.backups(clientKeysRel); len(b) != 0 {
				t.Errorf("client.keys backups %q, want none", b)
			}
			if f.io.logged("re-enroll") {
				t.Errorf("log %q, want no re-enroll", f.io.logs)
			}
			if got := f.ossec(); got.Groups != c.wantGroups || got.EnrollmentManager != c.to {
				t.Errorf("ossec.conf = %+v, want enrollment manager %s and groups %q", got, c.to, c.wantGroups)
			}
		})
	}
}

func TestSeveralManagersInOssecConfAreLeftForTheOperator(t *testing.T) {
	two := strings.Replace(ossecWith(t, "192.0.2.10", "default"),
		"    <server>\n      <address>192.0.2.10</address>",
		"    <server>\n      <address>192.0.2.11</address>\n    </server>\n    <server>\n      <address>192.0.2.10</address>", 1)
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", two, enrolledKeys)
	if c, err := serveragents.ReadOssecConf([]byte(two)); err != nil || len(c.Servers) != 2 {
		t.Fatalf("fixture: %+v, %v; want two servers", c, err)
	}

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	if out.Status != tasks.JobFailed {
		t.Errorf("status %q, want failed", out.Status)
	}
	if !strings.Contains(out.Summary, "several managers") {
		t.Errorf("summary %q, want it to say ossec.conf names several managers", out.Summary)
	}
	if f.read(ossecConfRel) != two || len(f.backups(ossecConfRel)) != 0 {
		t.Error("ossec.conf was changed, want it left for the operator")
	}
	if f.read(clientKeysRel) != enrolledKeys {
		t.Error("client.keys was changed, want it left alone")
	}
	if f.cmd.ran("systemctl restart") || f.cmd.ran("systemctl enable") {
		t.Errorf("commands %q, want no restart", f.cmd.lines())
	}
}

func TestAnInstalledOlderWazuhIsConfiguredNotUpgraded(t *testing.T) {
	f := installedWazuhHost(t, "install ok installed|4.13.1-1", testdata(t, "ossec.conf"), "")

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	f.mustSucceed(out)
	if len(f.io.apts) != 0 {
		t.Errorf("apt = %+v, want nothing installed over the agent that is there", f.io.apts)
	}
	if !f.io.logged("does not upgrade") {
		t.Errorf("log %q, want it to say the deploy does not upgrade an older agent", f.io.logs)
	}
	if !f.cmd.ran("apt-mark hold wazuh-agent") {
		t.Errorf("commands %q, want the older agent held too", f.cmd.lines())
	}
	if c := f.ossec(); !slices.Equal(c.Servers, []string{"198.51.100.5"}) {
		t.Errorf("ossec.conf = %+v, want it pointed at 198.51.100.5", c)
	}
}

func TestAHeldWazuhIsNotHeldAgain(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, "198.51.100.5", "default"), enrolledKeys)
	f.agentdState("connected", "2031-03-04 09:59:58", "2031-03-04 09:59:58")

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	f.mustSucceed(out)
	if f.cmd.ran("apt-mark") {
		t.Errorf("commands %q, want no apt-mark for an agent dpkg says is held", f.cmd.lines())
	}
	if out.Changed || f.cmd.ran("systemctl restart") || !strings.Contains(out.Summary, "already set") {
		t.Errorf("outcome %+v, commands %q; want nothing changed or restarted", out, f.cmd.lines())
	}
}

func TestWazuhThatStaysPendingFails(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", testdata(t, "ossec.conf"), "")
	f.agentReports("pending")

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	if out.Status != tasks.JobFailed || !strings.HasPrefix(out.Summary, "not connected after 1m0s: pending") {
		t.Fatalf("outcome %+v, want failed, not connected after 1m0s: pending", out)
	}
	if waited := f.now.Sub(fixtureNow); waited != time.Minute {
		t.Errorf("waited %v, want the whole minute", waited)
	}
	if !out.Changed {
		t.Error("Changed = false, want true: ossec.conf was written")
	}
}

func TestAStateFileFromBeforeTheRestartDoesNotCount(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", testdata(t, "ossec.conf"), "")
	// The agent before the restart was connected — to its old manager — and
	// the restarted one never writes the file again.
	f.agentdState("connected", "2031-03-04 09:59:30", "2031-03-04 09:59:30")
	f.chtime(agentdStateRel, fixtureNow.Add(-30*time.Second))
	f.onSleep = nil

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	if out.Status != tasks.JobFailed {
		t.Fatalf("outcome %+v, want failed", out)
	}
	if !strings.HasPrefix(out.Summary, "not connected after 1m0s: stale") || !strings.Contains(out.Summary, "since the restart") {
		t.Errorf("summary %q, want it to say the state file has not changed since the restart", out.Summary)
	}
}

func TestAWazuhAptStoppedByACancelIsStillHeld(t *testing.T) {
	f := freshWazuhHost(t)
	install := f.io.onApt
	ctx, cancel := context.WithCancel(context.Background())
	f.io.onApt = func(c aptCall) error {
		_ = install(c)
		cancel()
		return errors.New("install failed: the job was cancelled")
	}

	out := f.deploy(ctx, wazuhStep("198.51.100.5", "", "4.14.10"))

	if out.Status != tasks.JobCancelled {
		t.Errorf("outcome %+v, want cancelled", out)
	}
	if !f.cmd.ran("apt-mark hold wazuh-agent") {
		t.Errorf("commands %q, want the agent apt left installed held all the same", f.cmd.lines())
	}
	if f.cmd.ran("systemctl restart") {
		t.Error("restarted, want nothing past the cancel")
	}
}

func TestAnActiveWazuhThatIsNotConnectedIsRestarted(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, "198.51.100.5", "default"), enrolledKeys)
	f.agentdState("disconnected", "2031-03-04 09:50:00", "2031-03-04 09:50:00")

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	f.mustSucceed(out)
	if !f.cmd.ran("systemctl restart wazuh-agent") {
		t.Fatalf("commands %q, want the agent restarted", f.cmd.lines())
	}
	if !f.io.logged("wazuh-agent is active but not connected (disconnected); it is restarted") {
		t.Errorf("log %q, want it to say why it restarts", f.io.logs)
	}
	if got := f.read(clientKeysRel); got != enrolledKeys {
		t.Errorf("client.keys = %q, want the key kept: the manager did not change", got)
	}
}

func TestAStateFileTheStoppingAgentWroteDoesNotCount(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", testdata(t, "ossec.conf"), "")
	// While it stops, the old agent writes that it is connected — to the
	// manager it had — and the restart takes two seconds. The restarted
	// agent never writes the file.
	f.cmd.does("systemctl restart wazuh-agent", func() {
		f.now = f.now.Add(time.Second)
		f.agentdState("connected", "2031-03-04 10:00:01", "2031-03-04 10:00:01")
		f.now = f.now.Add(time.Second)
	})
	f.onSleep = nil

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	if out.Status != tasks.JobFailed || !strings.HasPrefix(out.Summary, "not connected after 1m0s: stale") {
		t.Fatalf("outcome %+v, want failed as stale: the only state file is the stopped agent's", out)
	}
}

func TestAWazuhReloadingWhenTheWaitEndsIsWaitedFor(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", testdata(t, "ossec.conf"), "")
	f.onSleep = func(n int) {
		switch {
		case n < 12:
			f.agentdState("pending", "", "")
		case n == 12:
			// A minute after the restart, it reloads for its manager's
			// shared config.
			f.service("wazuh-agent", "reloading")
		default:
			f.service("wazuh-agent", "active")
			f.agentdState("connected", "2031-03-04 10:01:05", "2031-03-04 10:01:05")
		}
	}

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	f.mustSucceed(out)
	if !f.io.logged("1m0s: unknown (wazuh-agent is reloading)") {
		t.Errorf("log %q, want the reload at the end of the wait logged", f.io.logs)
	}
	if waited := f.now.Sub(fixtureNow); waited != 65*time.Second {
		t.Errorf("waited %v, want one more poll past the minute", waited)
	}
}

func TestAWazuhStillReloadingIsNotWaitedForEver(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", testdata(t, "ossec.conf"), "")
	f.onSleep = func(int) { f.service("wazuh-agent", "reloading") }

	out := f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10"))

	if out.Status != tasks.JobFailed || !strings.HasPrefix(out.Summary, "not connected after 1m0s: unknown (wazuh-agent is reloading)") {
		t.Fatalf("outcome %+v, want failed while it reloads", out)
	}
	if waited := f.now.Sub(fixtureNow); waited != 90*time.Second {
		t.Errorf("waited %v, want the minute and 30 seconds more", waited)
	}
}

func TestTwoSpellingsOfOneManagerDoNotReenroll(t *testing.T) {
	for _, c := range []struct{ name, address, to string }{
		{"an address, then its name", "198.51.100.5", "wazuh.example.net"},
		{"a name, then its address", "wazuh.example.net", "198.51.100.5"},
		{"two names of one host", "wazuh.example.net", "manager.example.net"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, c.address, "default"), enrolledKeys)
			f.resolves("wazuh.example.net", "2001:db8::5", "198.51.100.5")
			f.resolves("manager.example.net", "198.51.100.5")

			f.mustSucceed(f.deploy(context.Background(), wazuhStep(c.to, "", "4.14.10")))

			if got := f.read(clientKeysRel); got != enrolledKeys {
				t.Errorf("client.keys = %q, want the key kept for the same manager", got)
			}
			if f.io.logged("re-enroll") {
				t.Errorf("log %q, want no re-enroll", f.io.logs)
			}
			if !f.io.logged("share the address 198.51.100.5") {
				t.Errorf("log %q, want it to say why they are one manager", f.io.logs)
			}
			if got := f.ossec(); !slices.Equal(got.Servers, []string{c.to}) || got.EnrollmentManager != c.to {
				t.Errorf("ossec.conf = %+v, want the cluster's spelling %s written", got, c.to)
			}
			for _, l := range f.lookedUp() {
				if l.left <= 0 || l.left > 2*time.Second {
					t.Errorf("looked up %s with %v left, want at most 2s", l.name, l.left)
				}
			}
		})
	}
}

func TestTwoNamesOfDifferentManagersReenroll(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, "wazuh-a.example.net", "default"), enrolledKeys)
	f.resolves("wazuh-a.example.net", "198.51.100.5")
	f.resolves("wazuh-b.example.net", "198.51.100.6")

	f.mustSucceed(f.deploy(context.Background(), wazuhStep("wazuh-b.example.net", "", "4.14.10")))

	if got := f.read(clientKeysRel); got != "" {
		t.Errorf("client.keys = %q, want it emptied for another manager", got)
	}
	if !f.io.logged("re-enroll: manager changed from wazuh-a.example.net to wazuh-b.example.net") {
		t.Errorf("log %q, want the re-enroll", f.io.logs)
	}
}

func TestAManagerNameThatNoLongerExistsIsAnotherManager(t *testing.T) {
	f := installedWazuhHost(t, "hold ok installed|4.14.10-1", ossecWith(t, "wazuh-old.example.net", "default"), enrolledKeys)
	f.nameErrs["wazuh-old.example.net"] = &net.DNSError{Err: "no such host", Name: "wazuh-old.example.net", IsNotFound: true}

	f.mustSucceed(f.deploy(context.Background(), wazuhStep("198.51.100.6", "", "4.14.10")))

	if !f.io.logged("re-enroll: manager changed from wazuh-old.example.net to 198.51.100.6") {
		t.Errorf("log %q, want the re-enroll", f.io.logs)
	}
}

func TestWhenOneManagerCannotBeToldFromAnotherNothingChanges(t *testing.T) {
	for _, c := range []struct {
		name, address, to string
		err               error
		why               string
	}{
		{"a lookup that fails", "198.51.100.5", "wazuh.example.net",
			&net.DNSError{Err: "i/o timeout", Name: "wazuh.example.net", IsTimeout: true}, "i/o timeout"},
		{"the cluster's manager does not exist", "198.51.100.5", "wazuh.example.net",
			&net.DNSError{Err: "no such host", Name: "wazuh.example.net", IsNotFound: true}, "wazuh.example.net does not resolve"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conf := ossecWith(t, c.address, "default")
			f := installedWazuhHost(t, "hold ok installed|4.14.10-1", conf, enrolledKeys)
			f.nameErrs[c.to] = c.err

			out := f.deploy(context.Background(), wazuhStep(c.to, "", "4.14.10"))

			if out.Status != tasks.JobFailed {
				t.Fatalf("outcome %+v, want failed", out)
			}
			if !strings.Contains(out.Summary, "whether "+c.address+" and "+c.to+" are one manager cannot be told") || !strings.Contains(out.Summary, c.why) {
				t.Errorf("summary %q, want it to say why the managers cannot be told apart: %s", out.Summary, c.why)
			}
			if f.read(clientKeysRel) != enrolledKeys || f.read(ossecConfRel) != conf {
				t.Error("client.keys or ossec.conf changed, want both left alone")
			}
			if f.cmd.ran("systemctl restart") {
				t.Error("restarted, want nothing done")
			}
		})
	}
}

func TestReenrollTurnsEnrollmentOn(t *testing.T) {
	off := strings.Replace(ossecWith(t, "192.0.2.19", "default"), "<enabled>yes</enabled>", "<enabled>no</enabled>", 1)

	t.Run("another manager", func(t *testing.T) {
		f := installedWazuhHost(t, "hold ok installed|4.14.10-1", off, enrolledKeys)

		f.mustSucceed(f.deploy(context.Background(), wazuhStep("198.51.100.5", "", "4.14.10")))

		if got := enrollmentEnabled(t, f.read(ossecConfRel)); got != "yes" {
			t.Errorf("<enrollment><enabled> = %q, want yes: its key is gone", got)
		}
		if !f.io.logged("enrollment was off in /var/ossec/etc/ossec.conf (<enabled>no</enabled>); it is turned on so the agent can enroll with 198.51.100.5") {
			t.Errorf("log %q, want it to say enrollment was turned on", f.io.logs)
		}
	})
	t.Run("the same manager", func(t *testing.T) {
		f := installedWazuhHost(t, "hold ok installed|4.14.10-1", off, enrolledKeys)

		f.mustSucceed(f.deploy(context.Background(), wazuhStep("192.0.2.19", "", "4.14.10")))

		if f.read(ossecConfRel) != off {
			t.Error("ossec.conf changed, want an agent keeping its key left with enrollment off")
		}
	})
}

// enrollmentEnabled is what <enabled> says in <client><enrollment>, read
// apart from the code under test.
func enrollmentEnabled(t *testing.T, conf string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<client>.*?<enrollment>\s*<enabled>([^<]*)</enabled>`).FindStringSubmatch(conf)
	if m == nil {
		t.Fatalf("no <enrollment><enabled> in:\n%s", conf)
	}
	return m[1]
}
