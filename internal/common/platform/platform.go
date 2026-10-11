// Package platform tells the panel what kind of host it runs on.
//
// Every feature that behaves differently on a Proxmox VE host — the service
// and process guards, the disk guards, the firewall and network pages, apt —
// asks here, so the answer is worked out once and the same everywhere.
//
// Proxmox VE cannot be told from Debian by /etc/os-release: a PVE 9 host says
// ID=debian, VERSION_ID=13, the same as the Debian it is installed on. The
// pve-manager package is what makes it Proxmox, so that is what is checked.
// /etc/pve is no proof either way: it exists only while pmxcfs runs.
package platform

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Network stacks, by what writes the host's network configuration.
const (
	StackNetplan        = "netplan"
	StackIfupdown2      = "ifupdown2"
	StackIfupdown       = "ifupdown"
	StackNetworkManager = "networkmanager"
	StackUnknown        = "unknown"
)

// Resolver modes, by who owns /etc/resolv.conf.
const (
	ResolverResolved   = "systemd-resolved"
	ResolverResolvconf = "resolvconf"
	ResolverFile       = "file" // a plain file nothing manages, as on Proxmox VE
)

// Info describes the host.
type Info struct {
	// From /etc/os-release.
	OSID         string `json:"distro"`
	OSVersionID  string `json:"distro_version"`
	OSCodename   string `json:"distro_codename"`
	OSPrettyName string `json:"distro_pretty"`

	// PVE is set when pve-manager is installed; PVEVersion is its version.
	PVE        bool   `json:"pve"`
	PVEVersion string `json:"pve_version,omitempty"`

	// NetworkStack is what owns the host's network configuration.
	NetworkStack string `json:"network_stack"`

	// JournalOnly is set when there is no /var/log/syslog, so the system's
	// logs are only in the journal. Debian 12 and later install no rsyslog.
	JournalOnly bool `json:"journal_only"`

	// Resolver is who owns /etc/resolv.conf.
	Resolver string `json:"resolver"`
}

// DisplayName is how the host's system is named on screen.
func (i Info) DisplayName() string {
	if i.PVE {
		name := "Proxmox VE"
		if i.PVEVersion != "" {
			name += " " + i.PVEVersion
		}
		if i.OSID == "debian" && i.OSVersionID != "" {
			name += " (Debian " + i.OSVersionID + ")"
		}
		return name
	}
	if i.OSPrettyName != "" {
		return i.OSPrettyName
	}
	return strings.TrimSpace(i.OSID + " " + i.OSVersionID)
}

// IfupdownNetworking reports whether networking.service brings the network
// up with ifupdown or ifupdown2. Stopping that unit runs ifdown -a.
func (i Info) IfupdownNetworking() bool {
	return i.NetworkStack == StackIfupdown || i.NetworkStack == StackIfupdown2
}

// Probe is what Detect reads the host through. Tests give it a fake.
type Probe interface {
	ReadFile(path string) ([]byte, error)
	Exists(path string) bool
	Glob(pattern string) []string
	// Output runs a command and returns its standard output; err is set
	// when the command is missing or exits non-zero.
	Output(name string, args ...string) (string, error)
}

// Detect works out the host's Info.
func Detect(p Probe) Info {
	var i Info
	if data, err := p.ReadFile("/etc/os-release"); err == nil {
		rel := parseOSRelease(data)
		i.OSID, i.OSVersionID, i.OSCodename, i.OSPrettyName = rel["ID"], rel["VERSION_ID"], rel["VERSION_CODENAME"], rel["PRETTY_NAME"]
	}

	// "ii  9.2.3": installed and configured. A package removed but with its
	// configuration left ("rc") is not Proxmox any more.
	if out, err := p.Output("dpkg-query", "-W", "-f=${db:Status-Abbrev} ${Version}", "pve-manager"); err == nil {
		fields := strings.Fields(out)
		if len(fields) >= 2 && fields[0] == "ii" {
			i.PVE = true
			i.PVEVersion = fields[1]
		}
	}

	switch {
	case i.PVE && p.Exists("/usr/sbin/ifreload"):
		i.NetworkStack = StackIfupdown2
	case i.PVE:
		i.NetworkStack = StackIfupdown
	case len(p.Glob("/etc/netplan/*.yaml"))+len(p.Glob("/etc/netplan/*.yml")) > 0:
		i.NetworkStack = StackNetplan
	case p.Exists("/usr/sbin/ifreload"):
		i.NetworkStack = StackIfupdown2
	case p.Exists("/usr/sbin/ifup") || p.Exists("/sbin/ifup"):
		i.NetworkStack = StackIfupdown
	case p.Exists("/usr/sbin/NetworkManager"):
		i.NetworkStack = StackNetworkManager
	default:
		i.NetworkStack = StackUnknown
	}

	i.JournalOnly = !p.Exists("/var/log/syslog")

	switch target := resolvLink(p); {
	case strings.HasPrefix(target, "/run/systemd/resolve/"):
		i.Resolver = ResolverResolved
	case p.Exists("/usr/sbin/resolvconf") || p.Exists("/sbin/resolvconf"):
		i.Resolver = ResolverResolvconf
	default:
		i.Resolver = ResolverFile
	}
	return i
}

// resolvLink is where /etc/resolv.conf points, or "" for a plain file.
func resolvLink(p Probe) string {
	if lp, ok := p.(interface{ Readlink(string) (string, error) }); ok {
		if target, err := lp.Readlink("/etc/resolv.conf"); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join("/etc", target)
			}
			return filepath.Clean(target)
		}
	}
	return ""
}

// parseOSRelease reads KEY=value lines, unquoting values.
func parseOSRelease(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[k] = strings.Trim(v, `"'`)
	}
	return out
}

// hostProbe reads the real host.
type hostProbe struct{}

func (hostProbe) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (hostProbe) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (hostProbe) Glob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}

func (hostProbe) Readlink(path string) (string, error) { return os.Readlink(path) }

func (hostProbe) Output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

var (
	detected = sync.OnceValue(func() Info { return Detect(hostProbe{}) })
	override atomic.Pointer[Info]
)

// Get returns the host's Info, worked out on first use. Installing or
// removing pve-manager under a running panel is not followed; a restart is.
func Get() Info {
	if i := override.Load(); i != nil {
		return *i
	}
	return detected()
}

// testingT is the part of testing.TB SetForTest needs, so this package does
// not link the testing package into the panel.
type testingT interface {
	Helper()
	Cleanup(func())
}

// SetForTest makes Get return i until the test ends.
func SetForTest(t testingT, i Info) {
	t.Helper()
	prev := override.Swap(&i)
	t.Cleanup(func() { override.Store(prev) })
}
