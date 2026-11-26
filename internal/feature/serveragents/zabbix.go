package featureserveragents

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// The files that give apt the vendor's Zabbix repository.
const (
	zabbixRepoHost = "repo.zabbix.com"
	zabbixKeyring  = "/usr/share/keyrings/sfpanel-zabbix.gpg"
	zabbixSources  = "/etc/apt/sources.list.d/sfpanel-zabbix.list"
	zabbixPrefFile = "/etc/apt/preferences.d/sfpanel-zabbix"
	// zabbixPref makes the vendor's 7.0 win over the OS's own agent,
	// whatever either version's epoch.
	zabbixPref = "Package: zabbix-agent2 zabbix-agent2-plugin-*\nPin: origin " + zabbixRepoHost + "\nPin-Priority: 1001"
)

const (
	// zabbixPassivePort is where the agent takes the server's passive checks.
	zabbixPassivePort = "10050"
	// ufwComment marks the ufw rules a deploy adds.
	ufwComment = "sfpanel-zabbix-agent"
	ufwTimeout = 30 * time.Second
)

// zabbixHostname is a host name Zabbix accepts.
var zabbixHostname = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,128}$`)

// deployZabbix installs Zabbix Agent 2 when it is not there — from the
// vendor's repository where the vendor builds for the release, else from the
// OS's own — sets the cluster's values in its config, opens ufw to the
// servers, restarts it when anything changed, and waits until it connects.
func (d *Deployer) deployZabbix(ctx context.Context, step tasks.AgentStep, io JobIO) Outcome {
	s := step.Settings.Zabbix
	var changed, installed bool

	o := d.osRelease()
	io.Logf("system: %s", osName(o))
	if ctx.Err() != nil {
		return cancelled(changed, "reading the agent")
	}
	f := d.readZabbix()
	if f.agent2.Installed {
		io.Logf("%s %s is installed (%s); it is kept", zabbixPackage, f.agent2.Version, serveragents.ZabbixSource(f.agent2.Version))
	} else {
		io.Logf("%s is not installed", zabbixPackage)
		if line, vendor := serveragents.ZabbixRepoLine(o, zabbixKeyring); vendor {
			if ctx.Err() != nil {
				return cancelled(changed, "adding the Zabbix repository")
			}
			added, err := d.addRepo(ctx, vendorRepo{
				Agent:       serveragents.Zabbix,
				Host:        zabbixRepoHost,
				KeyURL:      d.KeyURLs[serveragents.Zabbix],
				Fingerprint: serveragents.ZabbixKeyFingerprint,
				Keyring:     zabbixKeyring,
				SourcesFile: zabbixSources,
				Line:        line,
				PrefFile:    zabbixPrefFile,
				Pref:        zabbixPref,
			}, io)
			changed = changed || added
			if err != nil {
				if ctx.Err() != nil {
					return cancelledWhile(changed, "adding the Zabbix repository")
				}
				return failed(changed, err.Error())
			}
		} else {
			io.Logf("the Zabbix repository has no packages for %s; %s comes from its own archive", osName(o), zabbixPackage)
		}
		if ctx.Err() != nil {
			return cancelled(changed, "installing "+zabbixPackage)
		}
		if err := io.Apt(ctx, "install", []string{zabbixPackage}, true); err != nil {
			if ctx.Err() != nil {
				return cancelledWhile(changed, "installing "+zabbixPackage)
			}
			return failed(changed, "installing "+zabbixPackage+" failed: "+response.SanitizeOutput(err.Error()))
		}
		changed = true
		f = d.readZabbix()
		if !f.agent2.Installed {
			return failed(changed, "apt finished, but "+zabbixPackage+" is not installed")
		}
		if !f.hasConf {
			return failed(changed, zabbixPackage+" is installed without "+zabbixConfPath)
		}
		installed = true
		io.Logf("installed %s %s", zabbixPackage, f.agent2.Version)
	}

	conf := serveragents.ReadZabbixConf(f.conf)
	if ctx.Err() != nil {
		return cancelled(changed, "checking the included config files")
	}
	if key, file := d.zabbixIncludeSets(conf.Includes); key != "" {
		return failed(changed, fmt.Sprintf("%s is also set in %s; change it there", key, file))
	}

	edits := []serveragents.KeyValue{{Key: "Server", Value: s.Server}, {Key: "ServerActive", Value: s.ServerActive}}
	if s.HostMetadata != "" {
		edits = append(edits, serveragents.KeyValue{Key: "HostMetadata", Value: s.HostMetadata})
	}
	switch {
	case conf.Hostname != "" && conf.Hostname != serveragents.ZabbixVendorHostname:
		io.Logf("the agent's own name is kept: Hostname=%s", conf.Hostname)
	case conf.HostnameItemSet:
		io.Logf("the agent's own name is kept: it takes it from HostnameItem")
	case zabbixHostname.MatchString(step.NodeName):
		edits = append(edits, serveragents.KeyValue{Key: "Hostname", Value: step.NodeName})
	default:
		io.Logf("Hostname is left as it is: the server's name %q is not one Zabbix accepts (letters, digits, spaces, '.', '_' and '-', up to 128)", step.NodeName)
	}
	out, edited, err := serveragents.EditZabbixConf(f.conf, edits)
	if err != nil {
		return failed(changed, zabbixConfPath+" is not changed: "+err.Error())
	}
	if ctx.Err() != nil {
		return cancelled(changed, "writing "+zabbixConfPath)
	}
	// From here to the restart, a cancel is not heeded: a config written and
	// not loaded would look set to the next run, which then restarts nothing.
	if edited {
		backup, err := d.writeConf(zabbixConfPath, out)
		if err != nil {
			return failed(changed, "writing "+zabbixConfPath+" failed: "+response.SanitizeOutput(err.Error()))
		}
		changed = true
		io.Logf("wrote %s; what it held before is in %s", zabbixConfPath, backup)
	} else {
		io.Logf("%s already holds these values", zabbixConfPath)
	}

	ufwChanged := d.openZabbixPort(s.Server, io)

	written := serveragents.ReadZabbixConf(out)
	restarted := d.mustRestart(zabbixUnit, changed, io, func(service string) *serveragents.Connection {
		return d.zabbixConnection(ctx, service, written)
	})
	if restarted {
		if err := d.restart(zabbixUnit, io); err != nil {
			return failed(changed || ufwChanged, err.Error())
		}
	}
	changed = changed || ufwChanged || restarted

	if ctx.Err() != nil {
		return cancelled(changed, "checking the connection")
	}
	host, port := s.ActiveAddress()
	target := net.JoinHostPort(host, strconv.Itoa(port))
	io.Logf("waiting up to %s for the agent to reach %s", d.Wait, target)
	c, ok, err := d.waitConnected(ctx, io, restarted, d.Settle, func(ctx context.Context) *serveragents.Connection {
		return d.zabbixConnection(ctx, d.service(zabbixUnit), written)
	})
	if err != nil {
		return cancelledWhile(changed, "waiting for the agent to connect")
	}
	if !ok {
		return failed(changed, fmt.Sprintf("not connected after %s: %s", d.Wait, describeConnection(c)))
	}

	var summary strings.Builder
	if installed {
		summary.WriteString("installed " + f.agent2.Version + ", ")
	}
	if edited {
		summary.WriteString("configured, ")
	}
	if !installed && !edited {
		summary.WriteString("already set; ")
		if restarted {
			summary.WriteString("started, ")
		}
	}
	summary.WriteString("connected to " + target)
	return Outcome{Status: tasks.JobSucceeded, Changed: changed, Summary: summary.String()}
}

// zabbixIncludeSets finds a file the agent's config includes that sets a key
// a deploy writes: the first such key and the file's path, or "" when none
// does. An Include is a file, a directory (every file in it) or a glob, a
// relative one against the config file's directory.
func (h *Host) zabbixIncludeSets(includes []string) (key, file string) {
	for _, inc := range includes {
		if !filepath.IsAbs(inc) {
			inc = filepath.Join(filepath.Dir(zabbixConfPath), inc)
		}
		matches, _ := filepath.Glob(h.path(inc))
		for _, m := range matches {
			for _, p := range filesAt(m) {
				b, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				if keys := serveragents.SetsManagedKey(b); len(keys) > 0 {
					rel, _ := filepath.Rel(h.path("/"), p)
					return keys[0], "/" + rel
				}
			}
		}
	}
	return "", ""
}

// filesAt is the file at p, or the files in the directory at p.
func filesAt(p string) []string {
	fi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if !fi.IsDir() {
		return []string{p}
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			files = append(files, filepath.Join(p, e.Name()))
		}
	}
	return files
}

// openZabbixPort lets the Zabbix servers reach the agent's passive port
// through ufw, when ufw is active and the host is not Proxmox VE, whose own
// firewall is left alone. Only addresses and networks get a rule: ufw takes
// no host names. It reports whether ufw added or changed a rule. Like the
// restart it follows, it does not heed a cancel.
func (d *Deployer) openZabbixPort(servers string, io JobIO) (changed bool) {
	if d.Platform().PVE {
		io.Logf("ufw: not used on Proxmox VE; its firewall is left as it is")
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), ufwTimeout)
	defer cancel()
	env := []string{"LC_ALL=C"}
	if out, err := d.Cmd.RunWithEnvCtx(ctx, env, "ufw", "status"); err != nil || !ufwActive(out) {
		io.Logf("ufw: not active; no rule added")
		return false
	}
	for _, from := range strings.Split(servers, ",") {
		from = strings.TrimSpace(from)
		if from == "" {
			continue
		}
		if !isAddressOrNetwork(from) {
			io.Logf("ufw: %s is a name, which ufw does not take; allow %s/tcp from its address yourself", from, zabbixPassivePort)
			continue
		}
		out, err := d.Cmd.RunWithEnvCtx(ctx, env, "ufw", "allow", "from", from, "to", "any", "port", zabbixPassivePort, "proto", "tcp", "comment", ufwComment)
		if err != nil {
			detail := response.SanitizeOutput(out)
			if detail == "" {
				detail = response.SanitizeOutput(err.Error())
			}
			io.Logf("ufw: allowing %s/tcp from %s failed: %s", zabbixPassivePort, from, detail)
			continue
		}
		io.Logf("ufw: %s/tcp from %s: %s", zabbixPassivePort, from, response.SanitizeOutput(out))
		if strings.Contains(out, "Rule added") || strings.Contains(out, "Rule updated") {
			changed = true
		}
	}
	return changed
}

// ufwActive reports whether ufw status says it is active.
func ufwActive(status string) bool {
	for line := range strings.Lines(status) {
		if strings.TrimSpace(line) == "Status: active" {
			return true
		}
	}
	return false
}

func isAddressOrNetwork(v string) bool {
	if _, err := netip.ParseAddr(v); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(v)
	return err == nil
}
