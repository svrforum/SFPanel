// Package agentinstall installs SFPanel agents on servers over SSH from the
// cluster's leader.
//
// This file is its pre-flight check, and pure: the shell text that gathers
// facts on a target, the parser of what it prints, and the judge that turns
// the facts into one reason to stop or a pass. Nothing here opens SSH.
package agentinstall

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/release"
	"github.com/svrforum/SFPanel/internal/sshexec"
)

// What the login check says of sudo.
const (
	// SudoNone is a server without sudo.
	SudoNone = "none"
	// SudoNoPassword is sudo that lets the login run as root without asking.
	SudoNoPassword = "nopasswd"
	// SudoPassword is sudo that asks the login for a password, or that
	// refuses the login altogether: sudo -n cannot tell the two apart.
	SudoPassword = "password"
)

// How an install gets the release archive onto a target.
const (
	// ModeAuto installs online when the target has curl and reaches GitHub's
	// release downloads, and offline otherwise.
	ModeAuto = "auto"
	// ModeOnline has the target download the archive itself.
	ModeOnline = "online"
	// ModeOffline has the leader download the archive and upload it.
	ModeOffline = "offline"
)

// maxClockSkew is how far a target's clock may be from the leader's: managers
// refuse an agent's relays beyond ±30 s (internal/auth/proxyauth.go).
const maxClockSkew = 30 * time.Second

// minDiskKB is the free space the install needs on each of /tmp,
// /usr/local/bin and /var/lib, sized for all three on one filesystem: the
// release archive (about 20 MB) uploaded by the leader, install.sh's own copy
// of it and the ~40 MB binary it unpacks, all in /tmp, then the installed
// binary and the copy of the one it replaces in /usr/local/bin. That is about
// 160 MB at most; 256 MiB leaves room for the database and the logs.
const minDiskKB = 256 * 1024

// Login is what the login check found of the account the installer logs in
// with: whether it is root and how it can become root.
type Login struct {
	// UID is the login's user ID; 0 is root.
	UID int
	// Sudo is SudoNone, SudoNoPassword or SudoPassword.
	Sudo string
	// Bash is whether bash is installed: the probe and install.sh run
	// under it.
	Bash bool
}

// Root is whether the login is root and needs no sudo.
func (l Login) Root() bool { return l.UID == 0 }

// BusyPort is a TCP port something listens on.
type BusyPort struct {
	Port int
	// Owner is the listening process's name, or "?" when it cannot be told.
	Owner string
}

// Facts is what the login check and the probe found on a target.
type Facts struct {
	Login
	// OSID and OSLike are ID and ID_LIKE from /etc/os-release; Debian is
	// whether /etc/debian_version exists.
	OSID, OSLike string
	Debian       bool
	// Arch is the kernel's architecture as uname -m prints it.
	Arch string
	// Systemd is whether systemd runs the server.
	Systemd  bool
	Hostname string
	// Addrs are the addresses the server holds.
	Addrs []net.IP
	// SFPanelVersion is the installed SFPanel's version, "unknown" when its
	// binary does not say, or "" when none is installed.
	SFPanelVersion string
	// SFPanelData is whether any of /etc/sfpanel, /var/lib/sfpanel and
	// /var/log/sfpanel exists: install.sh uninstall keeps them, binary or
	// not.
	SFPanelData bool
	// Role and ClusterEnabled are cluster.role and cluster.enabled of an
	// existing config.yaml.
	Role           string
	ClusterEnabled bool
	// ManagerFiles are the files of cluster.ManagerFiles found on the server.
	ManagerFiles []string
	// OperatorTLS is an existing panel that serves the operator's own
	// certificate (server.tls.cert_file or key_file set); OperatorTLSCA
	// whether its config names a server.tls.ca_file too.
	OperatorTLS, OperatorTLSCA bool
	// SFPanelActive is whether the sfpanel service was active.
	SFPanelActive bool
	// Ports are the panel and cluster ports the agent will listen on: an
	// existing config's, else 3628 and 3629. PortsBusy are the ones something
	// already listens on.
	Ports     []int
	PortsBusy []BusyPort
	// ToolsMissing are the commands install.sh needs that the server lacks,
	// of curl, tar, sha256sum and awk.
	ToolsMissing []string
	// GitHub is whether the server downloaded the release's checksums.txt;
	// false when the probe did not try (offline).
	GitHub bool
	// Reach is, in the order the probe was given them, whether each manager
	// address took a TCP connection. The first is the leader's.
	Reach []bool
	// DiskKB is the least free space of /tmp, /usr/local/bin and /var/lib,
	// or -1 when df told nothing.
	DiskKB int64
	// OptStacks is whether /opt/stacks existed before the install: a revert
	// removes it only when this install made it.
	OptStacks bool
	// Now is the server's clock as the probe ended.
	Now time.Time
}

// SFPanelBefore is whether the server had SFPanel before the install: its
// binary, or its data without it. The operator confirms converting it as a
// standalone panel, and nothing purges it.
func (f Facts) SFPanelBefore() bool { return f.SFPanelVersion != "" || f.SFPanelData }

// ReleaseArch is the release's name for the server's CPU (amd64 or arm64),
// or "" when no release is built for it.
func (f Facts) ReleaseArch() string {
	switch f.Arch {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return ""
}

// LoginCheckScript is the first thing the installer runs on a target, as the
// login and before any sudo: it prints uid, bash (1 or 0) and sudo (none,
// nopasswd or password) as key=value lines. It is POSIX sh, so the login
// shell runs it whatever that shell is, and it changes nothing: sudo -n
// never waits for a password.
func LoginCheckScript() string {
	return `uid=$(id -u 2>/dev/null)
echo "uid=$uid"
if command -v bash >/dev/null 2>&1; then echo bash=1; else echo bash=0; fi
if ! command -v sudo >/dev/null 2>&1; then
  echo sudo=none
elif sudo -n true </dev/null >/dev/null 2>&1; then
  echo sudo=nopasswd
else
  echo sudo=password
fi
`
}

// ProbeScript is the pre-flight probe: a bash script, run as root (the
// panel's config.yaml is root's alone) under bash -c, not the login shell.
// It only reads: it prints facts as key=value lines and changes nothing on
// the server. Each of managers is a host:port the server should reach, the
// leader's join address on the server's network first; version is the
// release being installed, whose checksums.txt the probe downloads to
// nowhere when online is set, to tell whether the server reaches GitHub.
func ProbeScript(managers []string, version string, online bool) string {
	return probeScript("", managers, version, online)
}

// probeScript is ProbeScript over a server laid out under root, which a test
// fills; ProbeScript's root is "/" as the empty string.
func probeScript(root string, managers []string, version string, online bool) string {
	var reach strings.Builder
	for i, m := range managers {
		host, port, err := net.SplitHostPort(m)
		if err != nil || host == "" || port == "" {
			fmt.Fprintf(&reach, "kv reach_%d 0\n", i)
			continue
		}
		fmt.Fprintf(&reach, "reach %d %s %s\n", i, sshexec.ShellQuote(host), sshexec.ShellQuote(port))
	}
	github := ""
	if online {
		url := release.DownloadPrefix + version + "/checksums.txt"
		github = "if has curl && curl -fsSL -o /dev/null --max-time 10 " + sshexec.ShellQuote(url) + " 2>/dev/null; then kv github 1; else kv github 0; fi\n"
	}
	return strings.NewReplacer(
		"@ROOT@", sshexec.ShellQuote(root),
		"@MANAGER_PATHS@", managerPaths(),
		"@REACH@\n", reach.String(),
		"@GITHUB@\n", github,
	).Replace(probeTemplate)
}

// managerPaths is cluster.ManagerFiles as bash words under the cert_dir and
// data_dir the probe reads from config.yaml, so the probe looks for the
// files the CLI refuses a join over and no other list has to follow it.
func managerPaths() string {
	const certDir, dataDir = "/@cert_dir@/", "/@data_dir@/"
	var words []string
	for _, f := range cluster.ManagerFiles(&config.ClusterConfig{CertDir: certDir, DataDir: dataDir}) {
		if rel, ok := strings.CutPrefix(f, certDir); ok {
			words = append(words, `"$cert_dir"/`+sshexec.ShellQuote(rel))
		} else if rel, ok := strings.CutPrefix(f, dataDir); ok {
			words = append(words, `"$data_dir"/`+sshexec.ShellQuote(rel))
		}
	}
	return strings.Join(words, " ")
}

// probeTemplate is the probe with its generated parts as @NAME@. R is a root
// the server's paths are read under: empty on a real server.
const probeTemplate = `R=@ROOT@
kv() { printf '%s=%s\n' "$1" "$(printf '%s' "$2" | tr -d '\r\n')"; }
has() { command -v "$1" >/dev/null 2>&1; }
list() { tr '\n' ',' | sed 's/,*$//'; }

# A value of /etc/os-release, read rather than sourced.
osr() {
  awk -F= -v k="$1" -v sq="'" '$1 == k { v = substr($0, length(k) + 2); gsub("^[\"" sq "]|[\"" sq "]$", "", v); print v; exit }' "$R/etc/os-release" 2>/dev/null
}
kv os_id "$(osr ID)"
kv os_like "$(osr ID_LIKE)"
if [ -f "$R/etc/debian_version" ]; then kv debian 1; else kv debian 0; fi
kv arch "$(uname -m 2>/dev/null)"
if [ -d "$R/run/systemd/system" ]; then kv systemd 1; else kv systemd 0; fi
kv hostname "$(hostname 2>/dev/null || cat "$R/proc/sys/kernel/hostname" 2>/dev/null)"
addrs=$(ip -o addr show 2>/dev/null | awk '{ sub(/\/.*/, "", $4); print $4 }' | list)
[ -n "$addrs" ] || addrs=$(hostname -I 2>/dev/null | tr ' ' '\n' | sed '/^$/d' | list)
kv addrs "$addrs"

# config.yaml flattened to dotted.key=value lines, one per scalar.
flat=""
if [ -f "$R/etc/sfpanel/config.yaml" ]; then
  flat=$(awk -v sq="'" '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ || /^[[:space:]]*-/ { next }
    {
      match($0, /^ */); ind = RLENGTH
      line = substr($0, ind + 1)
      if (line !~ /^[A-Za-z0-9_.-]+[[:space:]]*:/) next
      key = line; sub(/[[:space:]]*:.*$/, "", key)
      val = line; sub(/^[^:]*:[[:space:]]*/, "", val)
      while (n > 0 && ind <= indent[n]) n--
      n++; indent[n] = ind; name[n] = key
      sub(/[[:space:]]+#.*$/, "", val); sub(/[[:space:]]+$/, "", val)
      gsub("^[\"" sq "]|[\"" sq "]$", "", val)
      if (val == "") next
      path = name[1]; for (i = 2; i <= n; i++) path = path "." name[i]
      print path "=" val
    }' "$R/etc/sfpanel/config.yaml" 2>/dev/null)
fi
cfg() { printf '%s\n' "$flat" | awk -v k="$1" 'index($0, k "=") == 1 { print substr($0, length(k) + 2); exit }'; }

installed=""
if [ -x "$R/usr/local/bin/sfpanel" ]; then
  installed=$(timeout 10 "$R/usr/local/bin/sfpanel" version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -n1)
  [ -n "$installed" ] || installed=unknown
fi
kv sfpanel_version "$installed"
if [ -e "$R/etc/sfpanel" ] || [ -e "$R/var/lib/sfpanel" ] || [ -e "$R/var/log/sfpanel" ]; then kv sfpanel_data 1; else kv sfpanel_data 0; fi
kv role "$(cfg cluster.role)"
case "$(cfg cluster.enabled | tr 'A-Z' 'a-z')" in
  true|yes|on|y) kv cluster_enabled 1 ;;
  *) kv cluster_enabled 0 ;;
esac
cert_dir=$(cfg cluster.cert_dir); cert_dir=${cert_dir:-/etc/sfpanel/cluster}
data_dir=$(cfg cluster.data_dir); data_dir=${data_dir:-/var/lib/sfpanel/cluster}
found=""
for f in @MANAGER_PATHS@; do
  if [ -e "$R$f" ] || [ -L "$R$f" ]; then found="$found${found:+,}$f"; fi
done
kv manager_files "$found"
if [ -n "$(cfg server.tls.cert_file)" ] || [ -n "$(cfg server.tls.key_file)" ]; then kv operator_tls 1; else kv operator_tls 0; fi
if [ -n "$(cfg server.tls.ca_file)" ]; then kv operator_tls_ca 1; else kv operator_tls_ca 0; fi
if has systemctl && systemctl is-active --quiet sfpanel; then kv sfpanel_active 1; else kv sfpanel_active 0; fi

panel_port=$(cfg server.port); case "$panel_port" in ''|*[!0-9]*) panel_port=3628 ;; esac
grpc_port=$(cfg cluster.grpc_port); case "$grpc_port" in ''|*[!0-9]*) grpc_port=3629 ;; esac
kv ports "$panel_port,$grpc_port"
busy=""
for p in "$panel_port" "$grpc_port"; do
  owner=""
  if has ss; then
    line=$(ss -ltnp "sport = :$p" 2>/dev/null | sed 1d | head -n1)
    [ -n "$line" ] || continue
    owner=$(printf '%s\n' "$line" | sed -n 's/.*users:(("\([^"]*\)".*/\1/p')
  elif ! timeout 2 bash -c 'exec 3<>"/dev/tcp/127.0.0.1/$1"' _ "$p" >/dev/null 2>&1; then
    continue
  fi
  busy="$busy${busy:+,}$p:${owner:-?}"
done
kv ports_busy "$busy"

missing=""
for t in curl tar sha256sum awk; do has "$t" || missing="$missing${missing:+,}$t"; done
kv tools_missing "$missing"
@GITHUB@
reach() {
  if timeout 3 bash -c 'exec 3<>"/dev/tcp/$1/$2"' _ "$2" "$3" >/dev/null 2>&1; then kv "reach_$1" 1; else kv "reach_$1" 0; fi
}
@REACH@
kv disk_kb "$(df -Pk "$R/tmp" "$R/usr/local/bin" "$R/var/lib" 2>/dev/null | awk 'NR > 1 && $4 ~ /^[0-9]+$/ { print $4 }' | sort -n | head -n1)"
if [ -e "$R/opt/stacks" ]; then kv opt_stacks 1; else kv opt_stacks 0; fi
kv now "$(date -u +%s)"
`

// ansiPattern is a terminal escape sequence, which a login's shell start-up
// may print around the facts.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

var (
	factKey  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	reachKey = regexp.MustCompile(`^reach_([0-9]+)$`)
)

var (
	loginKeys = []string{"uid", "bash", "sudo"}
	probeKeys = []string{"os_id", "os_like", "debian", "arch", "systemd", "hostname", "addrs",
		"sfpanel_version", "sfpanel_data", "role", "cluster_enabled", "manager_files", "operator_tls", "operator_tls_ca", "sfpanel_active",
		"ports", "ports_busy", "tools_missing", "disk_kb", "opt_stacks", "now"}
	optionalKeys = []string{"github"}
)

// facts reads key=value lines of the keys named, and reach_<i>. A line
// without an equals sign, or with a key it was not asked for, is someone
// else's (a banner, a shell's start-up) and is skipped; escape sequences are
// dropped first. A key it was asked for that appears twice is an error, as
// is a required one that never appears.
func facts(out []byte, required, optional []string) (map[string]string, error) {
	known := map[string]bool{}
	for _, k := range required {
		known[k] = true
	}
	for _, k := range optional {
		known[k] = true
	}
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(ansiPattern.ReplaceAllString(sc.Text(), ""), "\r")
		key, value, ok := strings.Cut(line, "=")
		if !ok || !factKey.MatchString(key) || (!known[key] && !reachKey.MatchString(key)) {
			continue
		}
		if _, dup := kv[key]; dup {
			return nil, fmt.Errorf("the probe printed %s twice", key)
		}
		kv[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the probe's output: %w", err)
	}
	for _, k := range required {
		if _, ok := kv[k]; !ok {
			return nil, fmt.Errorf("the probe printed no %s", k)
		}
	}
	return kv, nil
}

// ParseLogin reads what LoginCheckScript printed.
func ParseLogin(out []byte) (Login, error) {
	kv, err := facts(out, loginKeys, nil)
	if err != nil {
		return Login{}, err
	}
	return loginOf(kv)
}

func loginOf(kv map[string]string) (Login, error) {
	var l Login
	var err error
	if l.UID, err = strconv.Atoi(kv["uid"]); err != nil {
		return Login{}, fmt.Errorf("the probe's uid %q is not a number", kv["uid"])
	}
	switch s := kv["sudo"]; s {
	case SudoNone, SudoNoPassword, SudoPassword:
		l.Sudo = s
	default:
		return Login{}, fmt.Errorf("the probe's sudo %q is not none, nopasswd or password", s)
	}
	if l.Bash, err = flag(kv, "bash"); err != nil {
		return Login{}, err
	}
	return l, nil
}

func flag(kv map[string]string, key string) (bool, error) {
	switch kv[key] {
	case "1":
		return true, nil
	case "0", "":
		return false, nil
	}
	return false, fmt.Errorf("the probe's %s %q is not 1 or 0", key, kv[key])
}

// flagText is a flag as the probe prints it: 1 or 0.
func flagText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func items(s string) []string {
	var out []string
	for _, it := range strings.Split(s, ",") {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

// ParseFacts reads what LoginCheckScript and ProbeScript printed, the login
// check's output followed by the probe's.
func ParseFacts(out []byte) (Facts, error) {
	kv, err := facts(out, append(append([]string{}, loginKeys...), probeKeys...), optionalKeys)
	if err != nil {
		return Facts{}, err
	}
	var f Facts
	if f.Login, err = loginOf(kv); err != nil {
		return Facts{}, err
	}
	f.OSID, f.OSLike, f.Arch, f.Hostname = kv["os_id"], kv["os_like"], kv["arch"], kv["hostname"]
	f.SFPanelVersion, f.Role = kv["sfpanel_version"], kv["role"]
	for _, b := range []struct {
		key string
		to  *bool
	}{{"debian", &f.Debian}, {"systemd", &f.Systemd}, {"sfpanel_data", &f.SFPanelData}, {"cluster_enabled", &f.ClusterEnabled},
		{"operator_tls", &f.OperatorTLS}, {"operator_tls_ca", &f.OperatorTLSCA}, {"sfpanel_active", &f.SFPanelActive},
		{"github", &f.GitHub}, {"opt_stacks", &f.OptStacks}} {
		if *b.to, err = flag(kv, b.key); err != nil {
			return Facts{}, err
		}
	}
	for _, a := range items(kv["addrs"]) {
		if ip := net.ParseIP(a); ip != nil {
			f.Addrs = append(f.Addrs, ip)
		}
	}
	f.ManagerFiles = items(kv["manager_files"])
	f.ToolsMissing = items(kv["tools_missing"])
	for _, p := range items(kv["ports"]) {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Facts{}, fmt.Errorf("the probe's ports %q are not numbers", kv["ports"])
		}
		f.Ports = append(f.Ports, n)
	}
	for _, b := range items(kv["ports_busy"]) {
		port, owner, _ := strings.Cut(b, ":")
		n, err := strconv.Atoi(port)
		if err != nil {
			return Facts{}, fmt.Errorf("the probe's ports_busy %q are not port:owner", kv["ports_busy"])
		}
		f.PortsBusy = append(f.PortsBusy, BusyPort{Port: n, Owner: owner})
	}
	for i := 0; ; i++ {
		v, ok := kv["reach_"+strconv.Itoa(i)]
		if !ok {
			break
		}
		f.Reach = append(f.Reach, v == "1")
	}
	f.DiskKB = -1
	if d := kv["disk_kb"]; d != "" {
		if f.DiskKB, err = strconv.ParseInt(d, 10, 64); err != nil {
			return Facts{}, fmt.Errorf("the probe's disk_kb %q is not a number", d)
		}
	}
	sec, err := strconv.ParseInt(kv["now"], 10, 64)
	if err != nil {
		return Facts{}, fmt.Errorf("the probe's now %q is not a number", kv["now"])
	}
	f.Now = time.Unix(sec, 0)
	return f, nil
}

// Verdict is the pre-flight's answer for one server: the first reason to
// stop, or a pass (Code ""). Code is one of the AGENT_INSTALL_* row reasons
// of internal/api/response; Message is English for logs, the screen words
// the code with Details.
type Verdict struct {
	Code    string
	Message string
	Details map[string]string
	// Standalone is a pass on a server that had SFPanel before
	// (Facts.SFPanelBefore), its binary or only its data: the install
	// converts it, and the operator has to agree first.
	Standalone bool
	// Online is whether to install online: the target downloads the archive.
	Online bool
	// Name is the node name to register: the one given, else the hostname.
	Name string
}

func refuse(code, message string, details ...string) Verdict {
	v := Verdict{Code: code, Message: message}
	if len(details) > 0 {
		v.Details = map[string]string{}
		for i := 0; i+1 < len(details); i += 2 {
			v.Details[details[i]] = details[i+1]
		}
	}
	return v
}

// JudgeInput is what Judge weighs the facts against.
type JudgeInput struct {
	// IP is the address the agent registers at; the server must hold it.
	IP net.IP
	// Name is the node name asked for; "" takes the server's hostname.
	Name string
	// Version is the release being installed.
	Version string
	// Managers are the addresses given to ProbeScript, in its order.
	Managers []string
	// LeaderNow is the leader's clock when the probe's output arrived, and
	// RTT the probe command's round trip: the server read its clock at
	// most RTT/2 off the middle, so that much more skew is allowed.
	LeaderNow time.Time
	RTT       time.Duration
	// Mode is ModeAuto, ModeOnline or ModeOffline.
	Mode string
	// SudoPassword is whether a sudo password was given.
	SudoPassword bool
}

// JudgeLogin is the part of Judge the login check alone decides: whether the
// probe can run as root at all. It returns a Verdict with Code "" when it
// can.
func JudgeLogin(l Login, sudoPassword bool) Verdict {
	if !l.Root() {
		switch {
		case l.Sudo == SudoNone:
			return refuse(response.ErrAgentInstallNotRoot, "the login is not root and the server has no sudo", "sudo", SudoNone)
		case l.Sudo == SudoPassword && !sudoPassword:
			return refuse(response.ErrAgentInstallNotRoot, "sudo asks this login for a password and none was given", "sudo", SudoPassword)
		}
	}
	if !l.Bash {
		return refuse(response.ErrAgentInstallNoTools, "the server has no bash", "missing", "bash")
	}
	return Verdict{}
}

// debianFamily is whether the server is Ubuntu, Debian, or one built on
// them by its os-release.
func debianFamily(f Facts) bool {
	if f.OSID == "debian" || f.OSID == "ubuntu" {
		return true
	}
	for _, like := range strings.Fields(f.OSLike) {
		if like == "debian" || like == "ubuntu" {
			return true
		}
	}
	return f.OSID == "" && f.Debian
}

// Judge decides whether to install on a server from what the probe found:
// the first reason to stop, in the order a person would fix them, or a pass
// that says whether the server had SFPanel before (a conversion the operator
// confirms) and whether to install online. Of an SFPanel already on the
// server it refuses what install.sh or the agent join would refuse only
// after stopping the panel: an agent, a cluster manager, a manager's files
// left behind and a panel serving the operator's own certificate. It also
// refuses an SFPanel newer than the one being installed, which install.sh
// would downgrade.
func Judge(f Facts, in JudgeInput) Verdict {
	if v := JudgeLogin(f.Login, in.SudoPassword); v.Code != "" {
		return v
	}
	if !debianFamily(f) {
		return refuse(response.ErrAgentInstallOS, "SFPanel installs on Ubuntu or Debian only", "os_id", f.OSID, "os_like", f.OSLike)
	}
	if f.ReleaseArch() == "" {
		return refuse(response.ErrAgentInstallArch, "no SFPanel release is built for this CPU", "arch", f.Arch)
	}
	if !f.Systemd {
		return refuse(response.ErrAgentInstallNoSystemd, "the server does not run systemd")
	}
	var missing []string
	for _, t := range f.ToolsMissing {
		if t != "curl" {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return refuse(response.ErrAgentInstallNoTools, "the server lacks commands the install needs", "missing", strings.Join(missing, ","))
	}

	if f.Role == config.ClusterRoleAgent {
		return refuse(response.ErrAgentInstallAlreadyAgent, "the server is an agent already")
	}
	if f.ClusterEnabled {
		return refuse(response.ErrAgentInstallAlreadyManager, "the server is a cluster manager")
	}
	if len(f.ManagerFiles) > 0 {
		return refuse(response.ErrAgentInstallManagerFiles, "the server holds files a manager keeps", "files", strings.Join(f.ManagerFiles, ","))
	}
	if f.OperatorTLS {
		// The join requires the panel's certificate to verify for
		// 127.0.0.1 and the advertise address, which an operator's rarely
		// does, and refuses it after install.sh has stopped the panel.
		return refuse(response.ErrAgentInstallOperatorTLS, "the panel serves the operator's own certificate", "ca_file", flagText(f.OperatorTLSCA))
	}
	if f.SFPanelVersion != "" {
		if cmp, err := release.CompareVersions(f.SFPanelVersion, in.Version); err != nil || cmp > 0 {
			return refuse(response.ErrAgentInstallNewerInstalled, "the installed SFPanel is newer than the one being installed, or its version cannot be read",
				"installed", f.SFPanelVersion, "version", in.Version)
		}
	}

	name := in.Name
	if name == "" {
		if err := cluster.ValidNodeName(f.Hostname); err != nil {
			return refuse(response.ErrAgentInstallNameInvalid, "the server's hostname is no node name: give the server a name", "hostname", f.Hostname)
		}
		name = f.Hostname
	} else if err := cluster.ValidNodeName(name); err != nil {
		return refuse(response.ErrAgentInstallNameInvalid, "the name given is no node name", "name", name)
	}

	local := false
	for _, a := range f.Addrs {
		if a.Equal(in.IP) {
			local = true
			break
		}
	}
	if !local {
		return refuse(response.ErrAgentInstallIPNotLocal, "the server does not hold the IP given", "ip", in.IP.String())
	}
	for _, b := range f.PortsBusy {
		// A standalone panel listens on its own port; the install turns it
		// into the agent. The agent and manager cases were refused above.
		if b.Owner == "sfpanel" && f.SFPanelVersion != "" {
			continue
		}
		return refuse(response.ErrAgentInstallPortBusy, "another program listens on a port the agent needs",
			"port", strconv.Itoa(b.Port), "owner", b.Owner)
	}
	skew := f.Now.Sub(in.LeaderNow)
	if skew.Abs() > maxClockSkew+in.RTT/2 {
		return refuse(response.ErrAgentInstallClock, "the server's clock is more than 30 seconds from the leader's",
			"skew_seconds", strconv.FormatInt(int64(skew/time.Second), 10))
	}
	if len(f.Reach) == 0 || !f.Reach[0] {
		leader := ""
		if len(in.Managers) > 0 {
			leader = in.Managers[0]
		}
		return refuse(response.ErrAgentInstallCannotReachManager, "the server cannot connect to the leader's cluster port", "manager", leader)
	}

	curl := true
	for _, t := range f.ToolsMissing {
		if t == "curl" {
			curl = false
		}
	}
	var online bool
	switch in.Mode {
	case ModeOnline:
		if !curl {
			return refuse(response.ErrAgentInstallNoTools, "installing online needs curl on the server", "missing", "curl")
		}
		if !f.GitHub {
			return refuse(response.ErrAgentInstallNoInternet, "the server cannot download SFPanel's release from GitHub")
		}
		online = true
	case ModeAuto:
		online = curl && f.GitHub
	case ModeOffline:
	default:
		return refuse(response.ErrAgentInstallFailed, "the install mode is none of auto, online and offline", "mode", in.Mode)
	}
	if f.DiskKB >= 0 && f.DiskKB < minDiskKB {
		return refuse(response.ErrAgentInstallDisk, "the server is short of disk space for the install",
			"free_kb", strconv.FormatInt(f.DiskKB, 10), "need_kb", strconv.Itoa(minDiskKB))
	}
	return Verdict{Standalone: f.SFPanelBefore(), Online: online, Name: name}
}
