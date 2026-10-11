package platform

import (
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeProbe struct {
	files  map[string]string
	links  map[string]string
	output map[string]string // "name args..." -> stdout; missing means the command fails
}

func (f fakeProbe) ReadFile(path string) ([]byte, error) {
	if s, ok := f.files[path]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func (f fakeProbe) Exists(path string) bool {
	_, ok := f.files[path]
	return ok
}

func (f fakeProbe) Glob(pattern string) []string {
	var out []string
	prefix := strings.TrimSuffix(pattern, "*.yaml")
	prefix = strings.TrimSuffix(prefix, "*.yml")
	for path := range f.files {
		if strings.HasPrefix(path, prefix) && (strings.HasSuffix(pattern, ".yaml") && strings.HasSuffix(path, ".yaml") || strings.HasSuffix(pattern, ".yml") && strings.HasSuffix(path, ".yml")) {
			out = append(out, path)
		}
	}
	return out
}

func (f fakeProbe) Readlink(path string) (string, error) {
	if s, ok := f.links[path]; ok {
		return s, nil
	}
	return "", errors.New("not a link")
}

func (f fakeProbe) Output(name string, args ...string) (string, error) {
	if s, ok := f.output[name+" "+strings.Join(args, " ")]; ok {
		return s, nil
	}
	return "", errors.New("exit status 1")
}

const pveQuery = "dpkg-query -W -f=${db:Status-Abbrev} ${Version} pve-manager"

// What a PVE 9 host showed, read-only: os-release says Debian 13 and nothing
// else; only the package says Proxmox.
var pve9 = fakeProbe{
	files: map[string]string{
		"/etc/os-release":    "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"13\"\nVERSION_CODENAME=trixie\nID=debian\n",
		"/usr/sbin/ifreload": "",
		"/usr/sbin/ifup":     "",
		"/etc/resolv.conf":   "search example.lan\nnameserver 192.0.2.53\n",
	},
	output: map[string]string{pveQuery: "ii  9.2.3"},
}

var ubuntu = fakeProbe{
	files: map[string]string{
		"/etc/os-release":                 "PRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nID=ubuntu\n",
		"/etc/netplan/50-cloud-init.yaml": "network: {}\n",
		"/var/log/syslog":                 "",
	},
	links:  map[string]string{"/etc/resolv.conf": "../run/systemd/resolve/stub-resolv.conf"},
	output: map[string]string{},
}

func TestProxmoxIsTheHostWithPveManagerInstalled(t *testing.T) {
	got := Detect(pve9)
	want := Info{
		OSID: "debian", OSVersionID: "13", OSCodename: "trixie", OSPrettyName: "Debian GNU/Linux 13 (trixie)",
		PVE: true, PVEVersion: "9.2.3", NetworkStack: StackIfupdown2, JournalOnly: true, Resolver: ResolverFile,
	}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if name := got.DisplayName(); name != "Proxmox VE 9.2.3 (Debian 13)" {
		t.Errorf("display name %q", name)
	}
	if !got.IfupdownNetworking() {
		t.Error("ifupdown2 brings the network up; stopping networking.service would run ifdown -a")
	}
}

// The same os-release without pve-manager is plain Debian, and a removed
// package whose configuration remains ("rc") is not Proxmox any more.
func TestDebianWithoutPveManagerIsNotProxmox(t *testing.T) {
	for _, out := range []map[string]string{{}, {pveQuery: "rc  8.4.1"}} {
		p := pve9
		p.output = out
		if got := Detect(p); got.PVE || got.PVEVersion != "" {
			t.Errorf("output %v: detected as Proxmox: %+v", out, got)
		}
	}
}

func TestUbuntuWithNetplanAndResolved(t *testing.T) {
	got := Detect(ubuntu)
	if got.PVE || got.NetworkStack != StackNetplan || got.JournalOnly || got.Resolver != ResolverResolved {
		t.Errorf("got %+v", got)
	}
	if got.DisplayName() != "Ubuntu 24.04.3 LTS" {
		t.Errorf("display name %q", got.DisplayName())
	}
	if got.IfupdownNetworking() {
		t.Error("netplan hosts do not bring the network up through networking.service")
	}
}

func TestSetForTestOverridesUntilTheTestEnds(t *testing.T) {
	before := Get()
	t.Run("inner", func(t *testing.T) {
		SetForTest(t, Info{PVE: true, PVEVersion: "8.4.1"})
		if got := Get(); !got.PVE || got.PVEVersion != "8.4.1" {
			t.Errorf("override not seen: %+v", got)
		}
	})
	if got := Get(); got != before {
		t.Errorf("override outlived its test: %+v", got)
	}
}
