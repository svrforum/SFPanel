package serveragents

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
)

// OS is what /etc/os-release says about a host.
type OS struct {
	ID        string `json:"id"`
	IDLike    string `json:"id_like,omitempty"`
	VersionID string `json:"version_id"`
	Codename  string `json:"codename"`
	Pretty    string `json:"pretty"`
}

// ParseOSRelease reads an os-release file: KEY=value lines whose values may
// be single- or double-quoted, with backslash escapes inside double quotes.
// The codename is VERSION_CODENAME, or UBUNTU_CODENAME when that is absent.
func ParseOSRelease(b []byte) OS {
	var o OS
	var ubuntuCodename string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = unquoteOSRelease(value)
		switch strings.TrimSpace(key) {
		case "ID":
			o.ID = value
		case "ID_LIKE":
			o.IDLike = value
		case "VERSION_ID":
			o.VersionID = value
		case "VERSION_CODENAME":
			o.Codename = value
		case "UBUNTU_CODENAME":
			ubuntuCodename = value
		case "PRETTY_NAME":
			o.Pretty = value
		}
	}
	if o.Codename == "" {
		o.Codename = ubuntuCodename
	}
	return o
}

func unquoteOSRelease(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(v); i++ {
			if v[i] == '\\' && i+1 < len(v) {
				i++
			}
			b.WriteByte(v[i])
		}
		return b.String()
	}
	return v
}

// Debianish reports a Debian or Ubuntu host, or one built on either — the
// hosts apt and dpkg run on.
func (o OS) Debianish() bool {
	for _, id := range append([]string{o.ID}, strings.Fields(o.IDLike)...) {
		if id == "debian" || id == "ubuntu" {
			return true
		}
	}
	return false
}

// zabbixCodenames are the releases repo.zabbix.com publishes Zabbix 7.0 for.
var zabbixCodenames = map[string][]string{
	"ubuntu": {"focal", "jammy", "noble", "resolute"},
	"debian": {"bullseye", "bookworm", "trixie"},
}

// ZabbixRepoLine is the apt source line for the vendor's Zabbix 7.0
// repository on this OS, signed by keyring. It reports false for a release
// the vendor publishes no packages for, and for every derivative such as
// Linux Mint, whose codenames are its own.
func ZabbixRepoLine(o OS, keyring string) (string, bool) {
	for _, codename := range zabbixCodenames[o.ID] {
		if codename == o.Codename {
			return "deb [signed-by=" + keyring + "] https://repo.zabbix.com/zabbix/7.0/" + o.ID + " " + o.Codename + " main", true
		}
	}
	return "", false
}

// WazuhRepoLine is the apt source line for the vendor's Wazuh 4.x
// repository, the same for every Debian and Ubuntu release.
func WazuhRepoLine(keyring string) string {
	return "deb [signed-by=" + keyring + "] https://packages.wazuh.com/4.x/apt/ stable main"
}

// Package is what dpkg says about one package on a host. Installed is a
// package that is configured and works; State is dpkg's word for where the
// package stands — installed, half-configured, unpacked, config-files … —
// or "" when dpkg knows nothing of it. Version and Held are read for a
// package that is there (Present).
type Package struct {
	Installed, Held bool
	Version         string
	State           string
}

// Present reports a package that is there, whole or part way in or out: any
// state but not-installed and config-files — removed, its config files
// left — which are not there.
func (p Package) Present() bool {
	switch p.State {
	case "", "not-installed", "config-files":
		return false
	}
	return true
}

// DpkgFormat is the dpkg-query -f format ParseDpkg reads. The \n is dpkg's
// own escape: dpkg-query gets a backslash and an n.
const DpkgFormat = "${Status}|${Version}\\n"

// ParseDpkg reads dpkg-query -W -f=DpkgFormat output. Anything else, such as
// "no packages found", reads as the zero value.
func ParseDpkg(out string) Package {
	for _, line := range strings.Split(out, "\n") {
		status, version, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			continue
		}
		f := strings.Fields(status)
		if len(f) != 3 {
			continue
		}
		p := Package{State: f[2]}
		if !p.Present() {
			return p
		}
		p.Held, p.Version = f[0] == "hold", strings.TrimSpace(version)
		switch f[2] {
		// triggers-* is a configured package waiting on another's triggers.
		case "installed", "triggers-pending", "triggers-awaited":
			p.Installed = true
		}
		return p
	}
	return Package{}
}

// Where an installed Zabbix agent came from.
const (
	SourceVendor = "vendor" // repo.zabbix.com
	SourceDistro = "distro" // the OS's own archive
	SourceOther  = "other"  // no package: a tarball, a container, a hand build
)

var vendorZabbixVersionRe = regexp.MustCompile(`\+(ubuntu|debian)[0-9]+(\.[0-9]+)?$`)

// ZabbixSource reads where a zabbix-agent2 package came from out of its
// version: the vendor's builds end in +ubuntuNN.NN or +debianNN; any other
// package is the distribution's. No version means no package.
func ZabbixSource(version string) string {
	switch {
	case version == "":
		return SourceOther
	case vendorZabbixVersionRe.MatchString(version):
		return SourceVendor
	}
	return SourceDistro
}

// UpstreamVersion strips a Debian version's epoch and revision:
// "1:7.0.22+dfsg-1" is "7.0.22+dfsg", "4.14.10-1" is "4.14.10".
func UpstreamVersion(v string) string {
	v = strings.TrimSpace(v)
	if epoch, rest, ok := strings.Cut(v, ":"); ok && epoch != "" && strings.Trim(epoch, "0123456789") == "" {
		v = rest
	}
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	return v
}

var leadingNumbersRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*`)

// CompareVersions compares the leading dotted numbers of two versions'
// UpstreamVersion — 4.14.11 against 4.14.10 — and returns -1, 0 or 1. A
// missing number counts as 0, and anything after the numbers is ignored.
func CompareVersions(a, b string) int {
	na := strings.Split(leadingNumbersRe.FindString(UpstreamVersion(a)), ".")
	nb := strings.Split(leadingNumbersRe.FindString(UpstreamVersion(b)), ".")
	for i := 0; i < len(na) || i < len(nb); i++ {
		if c := compareNumbers(at(na, i), at(nb, i)); c != 0 {
			return c
		}
	}
	return 0
}

func at(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

// compareNumbers compares two runs of digits of any length.
func compareNumbers(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// AptCandidate is the Candidate version apt-cache policy reports, or "" when
// apt has none to offer — "(none)", or a package apt does not know.
func AptCandidate(policy string) string {
	for _, line := range strings.Split(policy, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:"); ok {
			if v = strings.TrimSpace(v); v != "(none)" {
				return v
			}
			return ""
		}
	}
	return ""
}
