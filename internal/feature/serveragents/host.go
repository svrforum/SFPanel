// Package featureserveragents reads and sets up the monitoring and security
// agents — Zabbix Agent 2 and the Wazuh agent — on the node it runs on. Each
// node reads its own agents; nothing here reaches another node.
package featureserveragents

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/serveragents"
)

// The agents' files, each under Host.Root.
const (
	osReleasePath        = "/etc/os-release"
	zabbixConfPath       = "/etc/zabbix/zabbix_agent2.conf"
	zabbixAgentdConfPath = "/etc/zabbix/zabbix_agentd.conf"
	ossecConfPath        = "/var/ossec/etc/ossec.conf"
	clientKeysPath       = "/var/ossec/etc/client.keys"
	agentdStatePath      = "/var/ossec/var/run/wazuh-agentd.state"
)

// Packages and the systemd units they ship.
const (
	zabbixPackage       = "zabbix-agent2"
	zabbixLegacyPackage = "zabbix-agent"
	wazuhPackage        = "wazuh-agent"
	zabbixUnit          = "zabbix-agent2"
	wazuhUnit           = "wazuh-agent"
)

const (
	dpkgTimeout      = 10 * time.Second
	systemctlTimeout = 10 * time.Second
	aptCacheTimeout  = 20 * time.Second
	dialTimeout      = 3 * time.Second
	// logTail is how much of the end of the Zabbix agent's log is read.
	logTail = 64 << 10
)

// Host is the node's view of its agents: its files, under Root, and its
// commands, network and clock.
type Host struct {
	Root     string // "/" in production
	Cmd      exec.Commander
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	Resolve  func(ctx context.Context, name string) ([]netip.Addr, error)
	HTTP     *http.Client
	KeyURLs  map[string]string // agent → the URL of its vendor's signing key
	Platform func() platform.Info
	Now      func() time.Time
}

// NewHost is the Host of the machine the panel runs on.
func NewHost(cmd exec.Commander) *Host {
	var d net.Dialer
	return &Host{
		Root: "/",
		Cmd:  cmd,
		Dial: d.DialContext,
		Resolve: func(ctx context.Context, name string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", name)
		},
		HTTP: &http.Client{Timeout: 30 * time.Second},
		KeyURLs: map[string]string{
			serveragents.Zabbix: serveragents.ZabbixKeyURL,
			serveragents.Wazuh:  serveragents.WazuhKeyURL,
		},
		Platform: platform.Get,
		Now:      time.Now,
	}
}

// path is p under the host's root.
func (h *Host) path(p string) string { return filepath.Join(h.Root, p) }

// readFile reads a file under the root; ok is false when it cannot be read,
// which for these files means it is not there.
func (h *Host) readFile(p string) (b []byte, ok bool) {
	b, err := os.ReadFile(h.path(p))
	return b, err == nil
}

// exists reports whether a file is under the root.
func (h *Host) exists(p string) bool {
	_, err := os.Stat(h.path(p))
	return err == nil
}

// readTail reads at most n bytes from the end of a file under the root,
// starting at a whole line when it does not start at the beginning; ok is
// false when the file cannot be read.
func (h *Host) readTail(p string, n int64) (b []byte, ok bool) {
	f, err := os.Open(h.path(p))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	off := max(fi.Size()-n, 0)
	b, err = io.ReadAll(io.NewSectionReader(f, off, fi.Size()-off))
	if err != nil {
		return nil, false
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, true
}

// osRelease reads the host's os-release.
func (h *Host) osRelease() serveragents.OS {
	b, _ := h.readFile(osReleasePath)
	return serveragents.ParseOSRelease(b)
}

// dpkg is what dpkg says about a package. A failing dpkg-query — the
// package is unknown, or there is no dpkg — reads as not installed.
func (h *Host) dpkg(pkg string) serveragents.Package {
	out, _ := h.Cmd.RunWithTimeout(dpkgTimeout, "dpkg-query", "-W", "-f="+serveragents.DpkgFormat, pkg)
	return serveragents.ParseDpkg(out)
}

// service is the word systemctl is-active answers for a unit — active,
// inactive, failed … — or unknown when it answers nothing.
func (h *Host) service(unit string) string {
	out, _ := h.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "is-active", unit)
	word, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if word = strings.TrimSpace(word); word == "" {
		return "unknown"
	}
	return word
}

// zabbixUnitLoaded reports whether systemd has a zabbix-agent2 unit.
func (h *Host) zabbixUnitLoaded() bool {
	out, _ := h.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "show", "-p", "LoadState", "--value", zabbixUnit+".service")
	return strings.TrimSpace(out) == "loaded"
}

// aptCandidate is the version of pkg apt would install from the sources the
// host has, or "" when it has none.
func (h *Host) aptCandidate(pkg string) string {
	ctx, cancel := context.WithTimeout(context.Background(), aptCacheTimeout)
	defer cancel()
	out, _ := h.Cmd.RunWithEnvCtx(ctx, []string{"LC_ALL=C"}, "apt-cache", "policy", pkg)
	return serveragents.AptCandidate(out)
}

// dial opens a TCP connection to addr within dialTimeout and closes it.
func (h *Host) dial(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := h.Dial(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// zabbixFacts is what the host says about its Zabbix agent.
type zabbixFacts struct {
	state   string
	agent2  serveragents.Package
	legacy  serveragents.Package // read only when agent2 is not installed
	conf    []byte
	hasConf bool // zabbix_agent2.conf is there
}

// readZabbix reads the Zabbix agent's package and config file. Agent 2's
// package makes it installed; without it, the old agent's package or config
// file makes it legacy, and agent 2's config file — unless it is what a
// removed agent 2 package left — or a loaded unit makes it unmanaged.
func (h *Host) readZabbix() zabbixFacts {
	f := zabbixFacts{agent2: h.dpkg(zabbixPackage)}
	f.conf, f.hasConf = h.readFile(zabbixConfPath)
	switch {
	case f.agent2.Installed:
		f.state = serveragents.StateInstalled
	default:
		f.legacy = h.dpkg(zabbixLegacyPackage)
		switch {
		case f.legacy.Installed || h.exists(zabbixAgentdConfPath):
			f.state = serveragents.StateLegacy
		case f.hasConf && !removedLeavingConfig(f.agent2) || h.zabbixUnitLoaded():
			f.state = serveragents.StateUnmanaged
		default:
			f.state = serveragents.StateNone
		}
	}
	return f
}

// wazuhFacts is what the host says about its Wazuh agent.
type wazuhFacts struct {
	state   string
	pkg     serveragents.Package
	conf    []byte
	hasConf bool // ossec.conf is there
}

// removedLeavingConfig reports a package apt removed and did not purge: its
// config files are still on disk.
func removedLeavingConfig(p serveragents.Package) bool { return p.State == "config-files" }

// readWazuh reads the Wazuh agent's package and ossec.conf: the package
// makes it installed, ossec.conf without it — unless a removed package left
// it — unmanaged.
func (h *Host) readWazuh() wazuhFacts {
	f := wazuhFacts{pkg: h.dpkg(wazuhPackage)}
	f.conf, f.hasConf = h.readFile(ossecConfPath)
	switch {
	case f.pkg.Installed:
		f.state = serveragents.StateInstalled
	case f.hasConf && !removedLeavingConfig(f.pkg):
		f.state = serveragents.StateUnmanaged
	default:
		f.state = serveragents.StateNone
	}
	return f
}
