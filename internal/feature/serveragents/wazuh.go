package featureserveragents

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// The files that give apt the vendor's Wazuh repository. It has one source
// for every Debian and Ubuntu release and needs no pin: the agent is
// installed by its exact version.
const (
	wazuhRepoHost = "packages.wazuh.com"
	wazuhKeyring  = "/usr/share/keyrings/sfpanel-wazuh.gpg"
	wazuhSources  = "/etc/apt/sources.list.d/sfpanel-wazuh.list"
)

// aptMarkTimeout bounds apt-mark hold.
const aptMarkTimeout = 30 * time.Second

// wazuhVersion is the Wazuh agent version s sets for the cluster.
func wazuhVersion(s *serveragents.Settings) string {
	if s != nil && s.Wazuh.Version != "" {
		return s.Wazuh.Version
	}
	return serveragents.DefaultWazuhVersion
}

// deployWazuh installs the Wazuh agent at the cluster's version when it is
// not there — never the repository's newest, which can be ahead of the
// manager, and a manager refuses an agent newer than itself — and holds it
// there. An older agent is kept as it is. It then points the agent at the
// manager, has it enroll again when the manager changed, restarts it when
// anything changed, and waits until the agent itself says it is connected.
func (d *Deployer) deployWazuh(ctx context.Context, step tasks.AgentStep, io JobIO) Outcome {
	s := step.Settings.Wazuh
	manager := strings.TrimSpace(s.Manager)
	version := wazuhVersion(step.Settings)
	var changed, installed, held bool

	o := d.osRelease()
	io.Logf("system: %s", osName(o))
	if ctx.Err() != nil {
		return cancelled(changed, "reading the agent")
	}
	f := d.readWazuh()
	if f.pkg.Installed {
		if serveragents.CompareVersions(f.pkg.Version, version) < 0 {
			io.Logf("%s %s is installed, older than %s, the version set for the cluster; a deploy does not upgrade it, it only sets its values", wazuhPackage, f.pkg.Version, version)
		} else {
			io.Logf("%s %s is installed; it is kept", wazuhPackage, f.pkg.Version)
		}
	} else {
		io.Logf("%s is not installed", wazuhPackage)
		if ctx.Err() != nil {
			return cancelled(changed, "adding the Wazuh repository")
		}
		added, err := d.addRepo(ctx, vendorRepo{
			Agent:       serveragents.Wazuh,
			Host:        wazuhRepoHost,
			KeyURL:      d.KeyURLs[serveragents.Wazuh],
			Fingerprint: serveragents.WazuhKeyFingerprint,
			Keyring:     wazuhKeyring,
			SourcesFile: wazuhSources,
			Line:        serveragents.WazuhRepoLine(wazuhKeyring),
		}, io)
		changed = changed || added
		if err != nil {
			if ctx.Err() != nil {
				return cancelledWhile(changed, "adding the Wazuh repository")
			}
			return failed(changed, err.Error())
		}
		if ctx.Err() != nil {
			return cancelled(changed, "installing "+wazuhPackage)
		}
		name := wazuhPackage + "=" + version + "-1"
		if err := io.Apt(ctx, "install", []string{name}, true); err != nil {
			// apt can have installed the agent, or part of it, before it
			// failed or was stopped; what this deploy leaves there is held
			// all the same.
			if p := d.dpkg(wazuhPackage); p.Present() && !p.Held {
				changed = true
				if herr := d.holdWazuh(io); herr != nil {
					io.Logf("%s", herr)
				}
			}
			if ctx.Err() != nil {
				return cancelledWhile(changed, "installing "+name)
			}
			return failed(changed, "installing "+name+" failed: "+response.SanitizeOutput(err.Error()))
		}
		changed = true
		f = d.readWazuh()
		if !f.pkg.Installed {
			return failed(changed, "apt finished, but "+wazuhPackage+" is not installed")
		}
		installed = true
		io.Logf("installed %s %s", wazuhPackage, f.pkg.Version)
	}

	// The hold does not wait for a cancel: an agent left installed and not
	// held is one upgrade away from being newer than its manager.
	if f.pkg.Held {
		io.Logf("%s is held at %s", wazuhPackage, f.pkg.Version)
	} else {
		if err := d.holdWazuh(io); err != nil {
			return failed(changed, err.Error())
		}
		changed, held = true, true
	}

	if !f.hasConf {
		return failed(changed, wazuhPackage+" is installed without "+ossecConfPath)
	}
	out, edited, err := serveragents.EditOssecConf(f.conf, manager, s.Groups)
	if err != nil {
		msg := ossecConfPath + " is not changed: " + response.SanitizeOutput(err.Error())
		if errors.Is(err, serveragents.ErrOssecManyServers) {
			msg += "; failing over between managers is left to you"
		}
		return failed(changed, msg)
	}
	// It parsed for the edit, so it reads; an edit refuses several servers.
	before, _ := serveragents.ReadOssecConf(f.conf)
	var from string
	if len(before.Servers) == 1 {
		from = before.Servers[0]
	}
	var reenroll bool
	if pointedAtManager(from) && !serveragents.SameHost(from, manager) {
		same, shared, err := d.sameManager(ctx, from, manager)
		if ctx.Err() != nil {
			return cancelled(changed, "writing "+ossecConfPath)
		}
		if err != nil {
			return failed(changed, fmt.Sprintf("the agent's manager is %s and the cluster's %s, and whether %s and %s are one manager cannot be told: %s; %s and %s are not changed",
				response.SanitizeOutput(from), manager, response.SanitizeOutput(from), manager, response.SanitizeOutput(err.Error()), ossecConfPath, clientKeysPath))
		}
		if same {
			io.Logf("%s and %s share the address %s: they are one manager, and the agent keeps its key", response.SanitizeOutput(from), manager, shared)
		}
		reenroll = !same
	}
	if reenroll {
		// An agent whose key is gone enrolls again only if enrollment is on.
		on, was, err := serveragents.EnableOssecEnrollment(out)
		if err != nil {
			return failed(changed, ossecConfPath+" is not changed: "+response.SanitizeOutput(err.Error()))
		}
		if was != "" {
			io.Logf("enrollment was off in %s (<enabled>%s</enabled>); it is turned on so the agent can enroll with %s", ossecConfPath, response.SanitizeOutput(was), manager)
			out, edited = on, true
		}
	}
	if pointedAtManager(from) && !reenroll {
		if after, _ := serveragents.ReadOssecConf(out); after.Groups != before.Groups {
			io.Logf("groups are %s now in %s; an agent takes its groups when it enrolls, so this one keeps its own until they change on the manager", response.SanitizeOutput(after.Groups), ossecConfPath)
		}
	}

	if ctx.Err() != nil {
		return cancelled(changed, "writing "+ossecConfPath)
	}
	// From here to the restart, a cancel is not heeded: values written and
	// not loaded would look set to the next run, which then restarts nothing.
	// client.keys is emptied before ossec.conf is written: a run that dies
	// between the two leaves the old manager in ossec.conf, so the next run
	// sees the change again.
	if reenroll {
		io.Logf("re-enroll: manager changed from %s to %s", response.SanitizeOutput(from), manager)
		if keys, ok := d.readFile(clientKeysPath); ok && len(keys) > 0 {
			backup, err := d.writeConf(clientKeysPath, nil)
			if err != nil {
				return failed(changed, "emptying "+clientKeysPath+" failed: "+response.SanitizeOutput(err.Error()))
			}
			changed = true
			io.Logf("emptied %s, so the agent enrolls with %s when it starts; its old key is in %s", clientKeysPath, manager, backup)
		} else {
			io.Logf("%s is empty; the agent enrolls with %s when it starts", clientKeysPath, manager)
		}
	}
	if edited {
		backup, err := d.writeConf(ossecConfPath, out)
		if err != nil {
			return failed(changed, "writing "+ossecConfPath+" failed: "+response.SanitizeOutput(err.Error()))
		}
		changed = true
		io.Logf("wrote %s; what it held before is in %s", ossecConfPath, backup)
	} else {
		io.Logf("%s already holds these values", ossecConfPath)
	}

	restarted := d.mustRestart(wazuhUnit, changed, io, d.wazuhConnection)
	// A state file the agent wrote before the restart ended speaks for the
	// agent that ran then — while it stopped, it could still write that it
	// was connected to the manager it had — not the one restarted.
	var restartedAt time.Time
	if restarted {
		if err := d.restart(wazuhUnit, io); err != nil {
			return failed(changed, err.Error())
		}
		restartedAt = d.Now()
	}
	changed = changed || restarted

	if ctx.Err() != nil {
		return cancelled(changed, "checking the connection")
	}
	io.Logf("waiting up to %s for the agent to connect to %s", d.Wait, manager)
	c, waited, ok, err := d.waitConnected(ctx, io, restarted, 0, func(context.Context) *serveragents.Connection {
		if restarted {
			return d.wazuhConnectionAfter(restartedAt)
		}
		return d.wazuhConnection(d.service(wazuhUnit))
	})
	if err != nil {
		return cancelledWhile(changed, "waiting for the agent to connect")
	}
	if !ok {
		return failed(changed, notConnected(waited, c))
	}

	var summary strings.Builder
	if installed {
		summary.WriteString("installed " + f.pkg.Version + ", ")
	}
	if held {
		summary.WriteString("held, ")
	}
	if edited {
		summary.WriteString("configured, ")
	}
	if reenroll {
		summary.WriteString("re-enrolled, ")
	}
	if !installed && !held && !edited && !reenroll {
		summary.WriteString("already set; ")
		if restarted {
			summary.WriteString("started, ")
		}
	}
	summary.WriteString("connected to " + manager)
	return Outcome{Status: tasks.JobSucceeded, Changed: changed, Summary: summary.String()}
}

// aptMarkRetry is how long a hold met by dpkg's lock waits before it is
// tried once more. The hold runs outside the package transaction, so an
// apt another process started in between can hold the lock.
const aptMarkRetry = 10 * time.Second

// holdWazuh keeps wazuh-agent at the version installed, so that upgrading
// every package does not take it past the manager. Like the restart, it does
// not heed a cancel.
func (d *Deployer) holdWazuh(io JobIO) error {
	for retried := false; ; retried = true {
		io.Logf("apt-mark hold %s", wazuhPackage)
		out, err := d.Cmd.RunWithTimeout(aptMarkTimeout, "apt-mark", "hold", wazuhPackage)
		if err == nil {
			if msg := response.SanitizeOutput(out); msg != "" {
				io.Logf("%s", msg)
			}
			return nil
		}
		if !retried && dpkgLockHeld(out) {
			io.Logf("dpkg's lock is held by another process; apt-mark is tried again in %s", aptMarkRetry)
			_ = d.Sleep(context.Background(), aptMarkRetry)
			continue
		}
		detail := response.SanitizeOutput(out)
		if detail == "" {
			detail = response.SanitizeOutput(err.Error())
		}
		return fmt.Errorf("apt-mark hold %s failed: %s", wazuhPackage, detail)
	}
}

// dpkgLockHeld reports apt or dpkg saying another process holds dpkg's lock.
func dpkgLockHeld(out string) bool {
	lower := strings.ToLower(out)
	for _, w := range []string{"could not get lock", "frontend lock", "status database is locked", "lock was locked by another process"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// resolveTimeout bounds each lookup of a manager's name.
const resolveTimeout = 2 * time.Second

// sameManager reports whether two manager addresses spelled differently name
// one manager: they share an address, the first one found in shared. A name
// is looked up within resolveTimeout; an old manager's name that no longer
// exists shares nothing. err is a lookup that failed otherwise, or the
// cluster's manager's name not existing: then it cannot be told, and
// nothing is to be done on a guess — wrongly re-enrolled, an agent loses a
// key the manager will not hand out again under the same name; wrongly
// kept, the change of manager is no longer seen once ossec.conf is
// rewritten.
func (h *Host) sameManager(ctx context.Context, old, cur string) (same bool, shared netip.Addr, err error) {
	oldAddrs, err := h.managerAddrs(ctx, old, true)
	if err != nil {
		return false, netip.Addr{}, err
	}
	curAddrs, err := h.managerAddrs(ctx, cur, false)
	if err != nil {
		return false, netip.Addr{}, err
	}
	for _, a := range curAddrs {
		if slices.Contains(oldAddrs, a) {
			return true, a, nil
		}
	}
	return false, netip.Addr{}, nil
}

// managerAddrs are the addresses a manager's address stands for: itself for
// an IP address, else what the name resolves to. A name that does not exist
// stands for none when gone is allowed, and is an error when not.
func (h *Host) managerAddrs(ctx context.Context, host string, gone bool) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, err := h.Resolve(ctx, host)
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound && gone:
		return nil, nil
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return nil, fmt.Errorf("%s does not resolve", host)
	case err != nil:
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Unmap())
	}
	return out, nil
}

// pointedAtManager reports whether an ossec.conf server address names a
// manager: it is neither empty nor the package's placeholder.
func pointedAtManager(address string) bool {
	return address != "" && !strings.EqualFold(address, serveragents.OssecPlaceholder)
}

// wazuhConnectionAfter is wazuhConnection for an agent restarted at since:
// until the restarted agent rewrites its state file, the file says how the
// agent before it was, so it reads as stale.
func (h *Host) wazuhConnectionAfter(since time.Time) *serveragents.Connection {
	c := h.wazuhConnection(h.service(wazuhUnit))
	if c.Transitional {
		return c
	}
	switch c.State {
	case serveragents.ConnNotRunning, serveragents.ConnNever:
		return c
	}
	if fi, err := os.Stat(h.path(agentdStatePath)); err == nil && !fi.ModTime().After(since) {
		return &serveragents.Connection{State: serveragents.ConnStale, Detail: "wazuh-agentd.state has not changed since the restart"}
	}
	return c
}
