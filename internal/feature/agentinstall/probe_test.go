package agentinstall

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/release"
	"github.com/svrforum/SFPanel/internal/sshexec"
)

var probeLeaderNow = time.Unix(1_800_000_000, 0)

// passingFacts is a server every check passes: Ubuntu on x86_64 with
// systemd, a login with passwordless sudo, nothing of SFPanel on it, the
// leader reached, the release downloads reached and room on the disk.
func passingFacts() Facts {
	return Facts{
		Login:    Login{UID: 1000, Sudo: SudoNoPassword, Bash: true},
		OSID:     "ubuntu",
		OSLike:   "debian",
		Debian:   true,
		Arch:     "x86_64",
		Systemd:  true,
		Hostname: "web-01",
		Addrs:    []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("100.64.0.10")},
		Ports:    []int{3628, 3629},
		GitHub:   true,
		Reach:    []bool{true, false},
		DiskKB:   10 << 20,
		Now:      probeLeaderNow,
	}
}

func passingInput() JudgeInput {
	return JudgeInput{
		IP:        net.ParseIP("192.0.2.10"),
		Version:   "0.81.0",
		Managers:  []string{"192.0.2.1:3629", "192.0.2.2:3629"},
		LeaderNow: probeLeaderNow,
		Mode:      ModeAuto,
	}
}

func TestJudgeRefusesEachReasonWithItsCode(t *testing.T) {
	cases := []struct {
		name    string
		facts   func(*Facts)
		input   func(*JudgeInput)
		code    string
		details map[string]string
	}{
		{name: "not debian", facts: func(f *Facts) { f.OSID, f.OSLike, f.Debian = "fedora", "", false },
			code: response.ErrAgentInstallOS, details: map[string]string{"os_id": "fedora"}},
		{name: "32-bit arm", facts: func(f *Facts) { f.Arch = "armv7l" },
			code: response.ErrAgentInstallArch, details: map[string]string{"arch": "armv7l"}},
		{name: "no systemd", facts: func(f *Facts) { f.Systemd = false },
			code: response.ErrAgentInstallNoSystemd},
		{name: "no sudo", facts: func(f *Facts) { f.Sudo = SudoNone },
			code: response.ErrAgentInstallNotRoot, details: map[string]string{"sudo": "none"}},
		{name: "sudo password not given", facts: func(f *Facts) { f.Sudo = SudoPassword },
			code: response.ErrAgentInstallNotRoot, details: map[string]string{"sudo": "password"}},
		{name: "no bash", facts: func(f *Facts) { f.Bash = false },
			code: response.ErrAgentInstallNoTools, details: map[string]string{"missing": "bash"}},
		{name: "no tar", facts: func(f *Facts) { f.ToolsMissing = []string{"tar"} },
			code: response.ErrAgentInstallNoTools, details: map[string]string{"missing": "tar"}},
		{name: "an agent", facts: func(f *Facts) { f.SFPanelVersion, f.Role = "0.81.0", "agent" },
			code: response.ErrAgentInstallAlreadyAgent},
		{name: "a manager", facts: func(f *Facts) { f.SFPanelVersion, f.ClusterEnabled = "0.81.0", true },
			code: response.ErrAgentInstallAlreadyManager},
		{name: "a manager's files", facts: func(f *Facts) {
			f.SFPanelVersion, f.ManagerFiles = "0.81.0", []string{"/etc/sfpanel/cluster/ca.key", "/var/lib/sfpanel/cluster/raft-log.db"}
		}, code: response.ErrAgentInstallManagerFiles,
			details: map[string]string{"files": "/etc/sfpanel/cluster/ca.key,/var/lib/sfpanel/cluster/raft-log.db"}},
		{name: "operator TLS without a CA", facts: func(f *Facts) { f.SFPanelVersion, f.OperatorTLSNoCA = "0.81.0", true },
			code: response.ErrAgentInstallOperatorTLS},
		{name: "newer installed", facts: func(f *Facts) { f.SFPanelVersion = "0.82.0" },
			code: response.ErrAgentInstallNewerInstalled, details: map[string]string{"installed": "0.82.0", "version": "0.81.0"}},
		{name: "unreadable version installed", facts: func(f *Facts) { f.SFPanelVersion = "unknown" },
			code: response.ErrAgentInstallNewerInstalled, details: map[string]string{"installed": "unknown"}},
		{name: "hostname no node name", facts: func(f *Facts) { f.Hostname = "-web" },
			code: response.ErrAgentInstallNameInvalid, details: map[string]string{"hostname": "-web"}},
		{name: "given name invalid", input: func(in *JudgeInput) { in.Name = "web 01" },
			code: response.ErrAgentInstallNameInvalid, details: map[string]string{"name": "web 01"}},
		{name: "ip not the server's", input: func(in *JudgeInput) { in.IP = net.ParseIP("198.51.100.4") },
			code: response.ErrAgentInstallIPNotLocal, details: map[string]string{"ip": "198.51.100.4"}},
		{name: "panel port busy", facts: func(f *Facts) { f.PortsBusy = []BusyPort{{Port: 3628, Owner: "nginx"}} },
			code: response.ErrAgentInstallPortBusy, details: map[string]string{"port": "3628", "owner": "nginx"}},
		{name: "sfpanel on the port with none installed", facts: func(f *Facts) { f.PortsBusy = []BusyPort{{Port: 3629, Owner: "sfpanel"}} },
			code: response.ErrAgentInstallPortBusy, details: map[string]string{"port": "3629", "owner": "sfpanel"}},
		{name: "clock ahead", facts: func(f *Facts) { f.Now = probeLeaderNow.Add(31 * time.Second) },
			code: response.ErrAgentInstallClock, details: map[string]string{"skew_seconds": "31"}},
		{name: "clock behind", facts: func(f *Facts) { f.Now = probeLeaderNow.Add(-31 * time.Second) },
			code: response.ErrAgentInstallClock, details: map[string]string{"skew_seconds": "-31"}},
		{name: "clock past half the round trip", facts: func(f *Facts) { f.Now = probeLeaderNow.Add(32 * time.Second) },
			input: func(in *JudgeInput) { in.RTT = 2 * time.Second },
			code:  response.ErrAgentInstallClock},
		{name: "leader not reached", facts: func(f *Facts) { f.Reach = []bool{false, true} },
			code: response.ErrAgentInstallCannotReachManager, details: map[string]string{"manager": "192.0.2.1:3629"}},
		{name: "no manager probed", facts: func(f *Facts) { f.Reach = nil },
			code: response.ErrAgentInstallCannotReachManager},
		{name: "online without curl", facts: func(f *Facts) { f.ToolsMissing = []string{"curl"} },
			input: func(in *JudgeInput) { in.Mode = ModeOnline },
			code:  response.ErrAgentInstallNoTools, details: map[string]string{"missing": "curl"}},
		{name: "online without GitHub", facts: func(f *Facts) { f.GitHub = false },
			input: func(in *JudgeInput) { in.Mode = ModeOnline },
			code:  response.ErrAgentInstallNoInternet},
		{name: "disk short", facts: func(f *Facts) { f.DiskKB = 100 << 10 },
			code: response.ErrAgentInstallDisk, details: map[string]string{"free_kb": "102400"}},
		{name: "unknown mode", input: func(in *JudgeInput) { in.Mode = "sideways" },
			code: response.ErrAgentInstallFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, in := passingFacts(), passingInput()
			if c.facts != nil {
				c.facts(&f)
			}
			if c.input != nil {
				c.input(&in)
			}
			v := Judge(f, in)
			if v.Code != c.code {
				t.Fatalf("code = %q (%s), want %q", v.Code, v.Message, c.code)
			}
			if v.Message == "" {
				t.Error("a refusal has no message")
			}
			for k, want := range c.details {
				if got := v.Details[k]; got != want {
					t.Errorf("details[%s] = %q, want %q (all: %v)", k, got, want, v.Details)
				}
			}
		})
	}
}

func TestJudgePasses(t *testing.T) {
	cases := []struct {
		name       string
		facts      func(*Facts)
		input      func(*JudgeInput)
		standalone bool
		online     bool
		nodeName   string
	}{
		{name: "a clean server", online: true, nodeName: "web-01"},
		{name: "root needs no sudo", facts: func(f *Facts) { f.UID, f.Sudo = 0, SudoNone }, online: true, nodeName: "web-01"},
		{name: "sudo with its password given", facts: func(f *Facts) { f.Sudo = SudoPassword },
			input: func(in *JudgeInput) { in.SudoPassword = true }, online: true, nodeName: "web-01"},
		{name: "a derivative of debian", facts: func(f *Facts) { f.OSID, f.OSLike = "raspbian", "debian" }, online: true, nodeName: "web-01"},
		{name: "arm64", facts: func(f *Facts) { f.Arch = "aarch64" }, online: true, nodeName: "web-01"},
		{name: "a standalone panel on its ports", facts: func(f *Facts) {
			f.SFPanelVersion = "0.80.2"
			f.PortsBusy = []BusyPort{{Port: 3628, Owner: "sfpanel"}}
		}, standalone: true, online: true, nodeName: "web-01"},
		{name: "a standalone panel of the same version", facts: func(f *Facts) { f.SFPanelVersion = "0.81.0" },
			standalone: true, online: true, nodeName: "web-01"},
		{name: "SFPanel's data without its binary", facts: func(f *Facts) { f.SFPanelData = true },
			standalone: true, online: true, nodeName: "web-01"},
		{name: "a given name over an unusable hostname", facts: func(f *Facts) { f.Hostname = "localhost.localdomain_" + strings.Repeat("x", 60) },
			input: func(in *JudgeInput) { in.Name = "db-02" }, online: true, nodeName: "db-02"},
		{name: "the IP on Tailscale", input: func(in *JudgeInput) { in.IP = net.ParseIP("100.64.0.10") }, online: true, nodeName: "web-01"},
		{name: "31 s off within half the round trip", facts: func(f *Facts) { f.Now = probeLeaderNow.Add(31 * time.Second) },
			input: func(in *JudgeInput) { in.RTT = 2 * time.Second }, online: true, nodeName: "web-01"},
		{name: "30 s off", facts: func(f *Facts) { f.Now = probeLeaderNow.Add(-30 * time.Second) }, online: true, nodeName: "web-01"},
		{name: "another manager not reached", facts: func(f *Facts) { f.Reach = []bool{true, false, false} }, online: true, nodeName: "web-01"},
		{name: "auto without curl goes offline", facts: func(f *Facts) { f.ToolsMissing = []string{"curl"} }, nodeName: "web-01"},
		{name: "auto without GitHub goes offline", facts: func(f *Facts) { f.GitHub = false }, nodeName: "web-01"},
		{name: "offline stays offline", input: func(in *JudgeInput) { in.Mode = ModeOffline }, nodeName: "web-01"},
		{name: "offline needs no curl", facts: func(f *Facts) { f.ToolsMissing, f.GitHub = []string{"curl"}, false },
			input: func(in *JudgeInput) { in.Mode = ModeOffline }, nodeName: "web-01"},
		{name: "online", input: func(in *JudgeInput) { in.Mode = ModeOnline }, online: true, nodeName: "web-01"},
		{name: "free space unknown", facts: func(f *Facts) { f.DiskKB = -1 }, online: true, nodeName: "web-01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, in := passingFacts(), passingInput()
			if c.facts != nil {
				c.facts(&f)
			}
			if c.input != nil {
				c.input(&in)
			}
			v := Judge(f, in)
			if v.Code != "" {
				t.Fatalf("refused with %s: %s %v", v.Code, v.Message, v.Details)
			}
			if v.Standalone != c.standalone || v.Online != c.online || v.Name != c.nodeName {
				t.Errorf("standalone, online, name = %v, %v, %q; want %v, %v, %q",
					v.Standalone, v.Online, v.Name, c.standalone, c.online, c.nodeName)
			}
		})
	}
}

// probeOutput is what the login check and the probe print, one after the
// other, as the engine hands them to ParseFacts.
const probeOutput = `uid=1000
bash=1
sudo=nopasswd
os_id=debian
os_like=
debian=1
arch=aarch64
systemd=1
hostname=web-01
addrs=192.0.2.10,fe80::1,100.64.0.10,
sfpanel_version=0.80.2
sfpanel_data=1
role=
cluster_enabled=0
manager_files=
operator_tls_no_ca=0
ports=3628,3629
ports_busy=3628:sfpanel,3629:?
tools_missing=curl
github=0
reach_0=1
reach_1=0
disk_kb=2048000
opt_stacks=1
now=1800000000
`

func TestParseFactsIgnoresNoise(t *testing.T) {
	noisy := "Welcome to the server!\r\n\n" +
		"\x1b[1;32mcolour=on\x1b[0m\n" +
		strings.Replace(probeOutput, "arch=aarch64\n", "\x1b[0march=aarch64\x1b[0m\r\n", 1) +
		"some_unknown_key=7\n\nlast line without an equals sign\n"
	f, err := ParseFacts([]byte(noisy))
	if err != nil {
		t.Fatal(err)
	}
	want := Facts{
		Login:          Login{UID: 1000, Sudo: SudoNoPassword, Bash: true},
		OSID:           "debian",
		Debian:         true,
		Arch:           "aarch64",
		Systemd:        true,
		Hostname:       "web-01",
		Addrs:          []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("fe80::1"), net.ParseIP("100.64.0.10")},
		SFPanelVersion: "0.80.2",
		SFPanelData:    true,
		Ports:          []int{3628, 3629},
		PortsBusy:      []BusyPort{{Port: 3628, Owner: "sfpanel"}, {Port: 3629, Owner: "?"}},
		ToolsMissing:   []string{"curl"},
		Reach:          []bool{true, false},
		DiskKB:         2048000,
		OptStacks:      true,
		Now:            time.Unix(1_800_000_000, 0),
	}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("facts =\n%+v\nwant\n%+v", f, want)
	}
	if got := f.ReleaseArch(); got != "arm64" {
		t.Errorf("release arch = %q, want arm64", got)
	}

	for _, key := range []string{"uid", "sudo", "arch", "addrs", "now", "disk_kb", "ports_busy", "sfpanel_version", "sfpanel_data"} {
		t.Run("without "+key, func(t *testing.T) {
			var kept []string
			for _, line := range strings.Split(probeOutput, "\n") {
				if !strings.HasPrefix(line, key+"=") {
					kept = append(kept, line)
				}
			}
			_, err := ParseFacts([]byte(strings.Join(kept, "\n")))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("err = %v, want one naming %s", err, key)
			}
		})
	}
	t.Run("a key twice", func(t *testing.T) {
		_, err := ParseFacts([]byte(probeOutput + "arch=x86_64\n"))
		if err == nil || !strings.Contains(err.Error(), "arch") {
			t.Fatalf("err = %v, want one naming arch", err)
		}
	})
	t.Run("a malformed number", func(t *testing.T) {
		_, err := ParseFacts([]byte(strings.Replace(probeOutput, "now=1800000000", "now=soon", 1)))
		if err == nil || !strings.Contains(err.Error(), "now") {
			t.Fatalf("err = %v, want one naming now", err)
		}
	})
}

func TestParseLoginReadsTheLoginCheck(t *testing.T) {
	l, err := ParseLogin([]byte("motd line\nuid=0\nbash=1\nsudo=none\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !l.Root() || l.Sudo != SudoNone || !l.Bash {
		t.Errorf("login = %+v", l)
	}
	if _, err := ParseLogin([]byte("uid=0\nbash=1\nsudo=maybe\n")); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("an unknown sudo answer: err = %v", err)
	}
	if _, err := ParseLogin([]byte("uid=0\nsudo=none\n")); err == nil || !strings.Contains(err.Error(), "bash") {
		t.Errorf("no bash key: err = %v", err)
	}
}

func TestTheProbeScriptPassesBashN(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	manager := "198.51.100.7'$(id)':3629"
	version := "0.81.0'; touch /tmp/pwned; '"
	script := ProbeScript([]string{"192.0.2.1:3629", manager, "not an address"}, version, true)
	check := func(shell, name, text string) {
		cmd := exec.Command(shell, "-n")
		cmd.Stdin = strings.NewReader(text)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s -n %s: %v\n%s", shell, name, err, out)
		}
	}
	check(bash, "the probe", script)
	check(sh, "the login check", LoginCheckScript())

	host, _, _ := net.SplitHostPort(manager)
	for _, quoted := range []string{
		sshexec.ShellQuote(host),
		sshexec.ShellQuote(release.DownloadPrefix + version + "/checksums.txt"),
	} {
		if !strings.Contains(script, quoted) {
			t.Errorf("the probe does not carry %s quoted as %s", quoted, quoted)
		}
	}
	if strings.Contains(ProbeScript([]string{"192.0.2.1:3629"}, "0.81.0", false), "curl -") {
		t.Error("an offline probe asks GitHub")
	}
}

// stubTools makes dir a PATH holding the real tools named and a shell
// script for each stub.
func stubTools(t *testing.T, dir string, real []string, stubs map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range real {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed", tool)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// treeOf lists every path under root with its mode, size and time.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		tree[p] = fmt.Sprintf("%v %v %d", info.Mode(), info.ModTime(), info.Size())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// TestTheProbeReadsAServerAndChangesNothing runs the probe under bash over a
// server laid out in a directory: a standalone panel on port 8443 with an
// operator certificate, a manager's leftover token file, a leader that
// listens and a manager that does not. Every command it may run is a stub
// or a real tool on a PATH of its own, so it reaches nothing on this host.
func TestTheProbeReadsAServerAndChangesNothing(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	bin := filepath.Join(dir, "bin")
	curlArgs := filepath.Join(dir, "curl-args")

	writeFile(t, filepath.Join(root, "etc/os-release"), "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\n", 0o644)
	writeFile(t, filepath.Join(root, "etc/debian_version"), "trixie/sid\n", 0o644)
	if err := os.MkdirAll(filepath.Join(root, "run/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "etc/sfpanel/config.yaml"), `server:
    port: 8443
    tls:
        enabled: true
        cert_file: /etc/ssl/panel.pem # the operator's
        key_file: "/etc/ssl/panel.key"
docker:
    observability:
        enabled: true
cluster:
    enabled: false
    data_dir: /srv/cluster
    role: ""
`, 0o600)
	writeFile(t, filepath.Join(root, "srv/cluster/tokens.json"), "{}", 0o600)
	writeFile(t, filepath.Join(root, "usr/local/bin/sfpanel"), "#!/bin/sh\necho 'SFPanel 0.80.2 (commit: abc, built: today)'\n", 0o755)

	stubTools(t, bin, []string{"bash", "awk", "sed", "grep", "head", "sort", "tr", "cat", "timeout"}, map[string]string{
		"uname":    `echo x86_64`,
		"hostname": `echo web-01`,
		"date":     `echo 1800000000`,
		"ip": `echo '1: lo    inet 127.0.0.1/8 scope host lo'
echo '2: eth0    inet 192.0.2.10/24 brd 192.0.2.255 scope global eth0'
echo '3: tailscale0    inet6 fd7a:115c:a1e0::1/128 scope global'`,
		"ss": `echo 'State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process'
case "$*" in *:8443*) echo 'LISTEN 0 4096 *:8443 *:* users:(("sfpanel",pid=812,fd=9))' ;; esac`,
		"df": `echo 'Filesystem 1024-blocks Used Available Capacity Mounted on'
echo '/dev/sda1 20000000 1000000 900000 6% /'
echo '/dev/sda2 20000000 1000000 1500000 6% /var'`,
		"curl": `printf '%s\n' "$@" > ` + sshexec.ShellQuote(curlArgs),
		"tar":  `exit 0`,
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	closed.Close()

	version := `0.81.0'; touch "$R/pwned"; '`
	before := treeOf(t, root)
	cmd := exec.Command(bash, "-c", probeScript(root, []string{listener.Addr().String(), closedAddr, "not an address"}, version, true))
	cmd.Env = []string{"PATH=" + bin, "LC_ALL=C"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	if after := treeOf(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("the probe changed the server:\nbefore %v\nafter  %v", before, after)
	}

	f, err := ParseFacts(append([]byte("uid=0\nbash=1\nsudo=none\n"), out...))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := Facts{
		Login:           Login{UID: 0, Sudo: SudoNone, Bash: true},
		OSID:            "ubuntu",
		OSLike:          "debian",
		Debian:          true,
		Arch:            "x86_64",
		Systemd:         true,
		Hostname:        "web-01",
		Addrs:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.0.2.10"), net.ParseIP("fd7a:115c:a1e0::1")},
		SFPanelVersion:  "0.80.2",
		SFPanelData:     true,
		ManagerFiles:    []string{"/srv/cluster/tokens.json"},
		OperatorTLSNoCA: true,
		Ports:           []int{8443, 3629},
		PortsBusy:       []BusyPort{{Port: 8443, Owner: "sfpanel"}},
		ToolsMissing:    []string{"sha256sum"},
		GitHub:          true,
		Reach:           []bool{true, false, false},
		DiskKB:          900000,
		Now:             time.Unix(1_800_000_000, 0),
	}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("facts =\n%+v\nwant\n%+v\nprobe printed:\n%s", f, want, out)
	}

	args, err := os.ReadFile(curlArgs)
	if err != nil {
		t.Fatalf("the probe did not ask GitHub: %v", err)
	}
	if url := release.DownloadPrefix + version + "/checksums.txt"; !strings.Contains(string(args), "\n"+url+"\n") {
		t.Errorf("curl was given\n%s\nwant the URL %s as one argument", args, url)
	}
}

// TestTheProbeSeesSFPanelsDataWithoutItsBinary runs the probe over a server
// SFPanel was uninstalled from without --purge: install.sh's default keeps
// the configuration and the database. That is SFPanel before the install,
// which the operator confirms as a standalone panel and nothing purges.
func TestTheProbeSeesSFPanelsDataWithoutItsBinary(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	bin := filepath.Join(dir, "bin")
	writeFile(t, filepath.Join(root, "etc/os-release"), "ID=ubuntu\nID_LIKE=debian\n", 0o644)
	if err := os.MkdirAll(filepath.Join(root, "run/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "etc/sfpanel/config.yaml"), "server:\n  port: 3628\n", 0o600)
	writeFile(t, filepath.Join(root, "var/lib/sfpanel/sfpanel.db"), "SQLite format 3\x00", 0o600)
	stubTools(t, bin, []string{"bash", "awk", "sed", "grep", "head", "sort", "tr", "cat", "timeout"}, map[string]string{
		"uname": `echo x86_64`, "hostname": `echo web-01`, "date": `echo 1800000000`,
		"ip": `echo '2: eth0    inet 192.0.2.10/24 scope global eth0'`,
		"ss": `echo 'State Recv-Q Send-Q Local Address:Port Peer Address:Port Process'`,
		"df": `echo 'Filesystem 1024-blocks Used Available Capacity Mounted on'; echo '/dev/sda1 20000000 1000000 900000 6% /'`,
		"curl": `exit 0`, "tar": `exit 0`, "sha256sum": `exit 0`,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	leader := listener.Addr().String()
	cmd := exec.Command(bash, "-c", probeScript(root, []string{leader}, "0.81.0", false))
	cmd.Env = []string{"PATH=" + bin, "LC_ALL=C"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "\nsfpanel_data=1\n") {
		t.Fatalf("the probe printed\n%s\nwant sfpanel_data=1", out)
	}
	f, err := ParseFacts(append([]byte("uid=0\nbash=1\nsudo=none\n"), out...))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if f.SFPanelVersion != "" || !f.SFPanelData {
		t.Fatalf("sfpanel version %q, data %v; want no binary and its data", f.SFPanelVersion, f.SFPanelData)
	}
	in := passingInput()
	in.Managers, in.Mode = []string{leader}, ModeOffline
	if v := Judge(f, in); v.Code != "" || !v.Standalone {
		t.Fatalf("verdict = %+v, want a pass on SFPanel before, asking about the standalone panel", v)
	}
}

// TestTheLoginCheckRunsUnderSh runs the login check under sh, as a login
// shell that is not bash would, with stubs for id and sudo.
func TestTheLoginCheckRunsUnderSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	cases := []struct {
		name  string
		real  []string
		stubs map[string]string
		want  Login
	}{
		{name: "passwordless sudo", real: []string{"bash"},
			stubs: map[string]string{"id": "echo 1000", "sudo": `[ "$*" = "-n true" ] || exit 9`},
			want:  Login{UID: 1000, Sudo: SudoNoPassword, Bash: true}},
		{name: "sudo asks", real: []string{"bash"},
			stubs: map[string]string{"id": "echo 1000", "sudo": "exit 1"},
			want:  Login{UID: 1000, Sudo: SudoPassword, Bash: true}},
		{name: "root without sudo or bash",
			stubs: map[string]string{"id": "echo 0"},
			want:  Login{UID: 0, Sudo: SudoNone}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "bin")
			stubTools(t, bin, c.real, c.stubs)
			cmd := exec.Command(sh, "-c", LoginCheckScript())
			cmd.Env = []string{"PATH=" + bin}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("login check: %v\n%s", err, out)
			}
			l, err := ParseLogin(out)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if l != c.want {
				t.Errorf("login = %+v, want %+v\nprinted:\n%s", l, c.want, out)
			}
		})
	}
}
