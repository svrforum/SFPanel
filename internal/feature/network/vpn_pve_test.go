package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

func withWGConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	prev := wgConfigDir
	wgConfigDir = dir
	t.Cleanup(func() { wgConfigDir = prev })
	if err := os.WriteFile(filepath.Join(dir, "wg0.conf"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const wgClientConf = "[Interface]\nPrivateKey = Y2xpZW50LXByaXZhdGU=\nAddress = 10.8.0.2/24\nDNS = 192.0.2.53\n\n[Peer]\nPublicKey = c2VydmVyLXB1YmxpYy1rZXk=\nEndpoint = 203.0.113.7:51820\nAllowedIPs = 10.8.0.0/24\n"

func ranCommand(m *exec.MockCommander, name string) bool {
	for _, c := range m.Calls {
		if c.Name == name {
			return true
		}
	}
	return false
}

// wg-quick hands DNS = to resolvconf, and Debian and Proxmox VE install none:
// it failed and removed the interface again, and at boot the unit failed. Up
// and autostart are refused with the reason instead.
func TestWireGuardRefusesDNSWithoutResolvconf(t *testing.T) {
	withWGConfig(t, wgClientConf)
	m := exec.NewMockCommander()
	h := &WireGuardHandler{Cmd: m}

	rec, e := serve(t, h.InterfaceUp, http.MethodPost, "/network/wireguard/interfaces/wg0/up", "", "name", "wg0")
	if rec.Code != http.StatusConflict || e.Error.Code != "WG_RESOLVCONF_MISSING" || e.Error.Details["iface"] != "wg0" {
		t.Errorf("up: got %d %s", rec.Code, rec.Body.String())
	}
	rec, e = serve(t, h.SetAutostart, http.MethodPost, "/network/wireguard/configs/wg0/autostart", `{"enabled":true}`, "name", "wg0")
	if rec.Code != http.StatusConflict || e.Error.Code != "WG_RESOLVCONF_MISSING" || e.Error.Details["iface"] != "wg0" {
		t.Errorf("autostart: got %d %s", rec.Code, rec.Body.String())
	}
	if ranCommand(m, "wg-quick") || ranCommand(m, "systemctl") {
		t.Errorf("ran %+v", m.Calls)
	}

	// Turning autostart off needs no resolver.
	if rec, _ := serve(t, h.SetAutostart, http.MethodPost, "/network/wireguard/configs/wg0/autostart", `{"enabled":false}`, "name", "wg0"); rec.Code != http.StatusOK {
		t.Errorf("autostart off: got %d %s", rec.Code, rec.Body.String())
	}
}

func TestWireGuardUpGoesAheadWhereDNSCanBeSet(t *testing.T) {
	for name, tc := range map[string]struct {
		conf       string
		resolvconf bool
	}{
		"resolvconf present": {wgClientConf, true},
		"no DNS line":        {strings.Replace(wgClientConf, "DNS = 192.0.2.53\n", "", 1), false},
	} {
		t.Run(name, func(t *testing.T) {
			withWGConfig(t, tc.conf)
			m := exec.NewMockCommander()
			if tc.resolvconf {
				m.SetOutput("exists:resolvconf", "", nil)
			}
			rec, _ := serve(t, (&WireGuardHandler{Cmd: m}).InterfaceUp, http.MethodPost, "/network/wireguard/interfaces/wg0/up", "", "name", "wg0")
			if rec.Code != http.StatusOK || !ranCommand(m, "wg-quick") {
				t.Errorf("got %d %s, calls %+v", rec.Code, rec.Body.String(), m.Calls)
			}
		})
	}
}

// A Proxmox host without a subscription has its enterprise repository answer
// 401, and the install stopped at apt-get update. wireguard-tools comes from
// Debian's: it goes on, and says which repository failed.
func TestWireGuardInstallGoesPastARepositoryThatRefusesAccess(t *testing.T) {
	m := &partialAptCommander{MockCommander: exec.NewMockCommander()}
	rec, e := serve(t, (&WireGuardHandler{Cmd: m}).Install, http.MethodPost, "/network/wireguard/install", "")
	var data struct {
		Warnings []struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if len(data.Warnings) != 1 || data.Warnings[0].Code != "APT_REPO_UNAUTHORIZED" ||
		data.Warnings[0].Details["uri"] != "https://enterprise.proxmox.com/debian/pve" {
		t.Errorf("warnings %+v", data.Warnings)
	}
	last := m.Calls[len(m.Calls)-1]
	if last.Name != "apt-get" || last.Args[len(last.Args)-1] != "wireguard-tools" {
		t.Errorf("wireguard-tools was not installed: %+v", m.Calls)
	}
}

// partialAptCommander fails apt-get update the way an unsubscribed Proxmox
// host does, and lets everything else succeed.
type partialAptCommander struct{ *exec.MockCommander }

func (p *partialAptCommander) RunWithEnvCtx(ctx context.Context, env []string, name string, args ...string) (string, error) {
	out, err := p.MockCommander.RunWithEnvCtx(ctx, env, name, args...)
	if name == "apt-get" && len(args) == 1 && args[0] == "update" {
		return "Hit:1 http://deb.debian.org/debian trixie InRelease\n" +
			"E: Failed to fetch https://enterprise.proxmox.com/debian/pve/dists/trixie/InRelease  401  Unauthorized [IP: 203.0.113.10 443]\n" +
			"E: The repository 'https://enterprise.proxmox.com/debian/pve trixie InRelease' is not signed.\n", errors.New("exit status 100")
	}
	return out, err
}

func tsCalls(m *exec.MockCommander) [][]string {
	var calls [][]string
	for _, c := range m.Calls {
		if c.Name == "tailscale" {
			calls = append(calls, c.Args)
		}
	}
	return calls
}

// On a host whose /etc/resolv.conf nothing manages — Proxmox VE — tailscaled
// took the file over. Connect keeps it out: with `tailscale set` before a bare
// `up`, which keeps the settings it finds, and on `up` itself when `up` has
// flags of its own, which must name every setting that is not the default.
func TestTailscaleConnectKeepsTheHostsDNS(t *testing.T) {
	for _, tc := range []struct {
		name string
		info platform.Info
		body string
		want [][]string
	}{
		{"proxmox", pveHost, `{"auth_key":"tskey-auth-k1"}`,
			[][]string{{"set", "--accept-dns=false"}, {"up"}}},
		{"proxmox, exit node", pveHost, `{"auth_key":"tskey-auth-k1","exit_node":"100.64.0.7"}`,
			[][]string{{"up", "--exit-node=100.64.0.7", "--exit-node-allow-lan-access=true", "--accept-dns=false"}}},
		{"systemd-resolved", netplanHost, `{"auth_key":"tskey-auth-k1"}`,
			[][]string{{"up"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform.SetForTest(t, tc.info)
			m := exec.NewMockCommander()
			// Always with an auth key: without one, Up runs tailscale itself.
			rec, _ := serve(t, (&TailscaleHandler{Cmd: m}).Up, http.MethodPost, "/network/tailscale/up", tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
			if got := tsCalls(m); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("tailscale %q, want %q", got, tc.want)
			}
		})
	}
}

// Where `tailscale set` fails — before the first login, say — the setting
// goes on `up`, where a node that was never set up has nothing else to name.
func TestTailscaleConnectNamesTheDNSSettingWhenSetFails(t *testing.T) {
	platform.SetForTest(t, pveHost)
	m := exec.NewMockCommander()
	m.SetOutput("tailscale", "", errors.New("exit status 1"))
	serve(t, (&TailscaleHandler{Cmd: m}).Up, http.MethodPost, "/network/tailscale/up", `{"auth_key":"tskey-auth-k1"}`)
	want := [][]string{{"set", "--accept-dns=false"}, {"up", "--accept-dns=false"}}
	if got := tsCalls(m); !reflect.DeepEqual(got, want) {
		t.Errorf("tailscale %q, want %q", got, want)
	}
}

// An exit node took the host's replies to its own LAN with it.
func TestAnExitNodeKeepsTheLAN(t *testing.T) {
	platform.SetForTest(t, netplanHost)
	for body, want := range map[string][]string{
		`{"exit_node":"100.64.0.7"}`: {"set", "--exit-node=100.64.0.7", "--exit-node-allow-lan-access=true"},
		`{"exit_node":""}`:           {"set", "--exit-node="},
	} {
		m := exec.NewMockCommander()
		rec, _ := serve(t, (&TailscaleHandler{Cmd: m}).SetPreferences, http.MethodPut, "/network/tailscale/preferences", body)
		if got := tsCalls(m); rec.Code != http.StatusOK || !reflect.DeepEqual(got, [][]string{want}) {
			t.Errorf("%s: %d, tailscale %q, want %q", body, rec.Code, got, want)
		}
	}
}

// Accepted routes go in table 52, ahead of the main table: a peer's route over
// the LAN this host is on captured its replies to that LAN — the browser and
// SSH reaching it, the cluster, the storage.
func TestAcceptingRoutesOverTheLANIsRefused(t *testing.T) {
	const kernelRoutes = `[{"dst":"192.0.2.0/24","dev":"vmbr0","protocol":"kernel","scope":"link","prefsrc":"192.0.2.10","flags":[]},` +
		`{"dst":"fe80::/64","dev":"vmbr0","protocol":"kernel","metric":256,"flags":[],"pref":"medium"}]`
	status := func(routes ...string) string {
		b, _ := json.Marshal(map[string]any{
			"BackendState": "Running",
			"Peer": map[string]any{
				"nodekey:1": map[string]any{"HostName": "exit1", "PrimaryRoutes": []string{"0.0.0.0/0", "::/0"}},
				"nodekey:2": map[string]any{"HostName": "router1", "PrimaryRoutes": routes},
			},
		})
		return string(b)
	}
	for _, tc := range []struct {
		name, target string
		routes       []string
		overlap      string
	}{
		{"the LAN itself", "/network/tailscale/preferences", []string{"198.51.100.0/24", "192.0.2.0/24"}, "192.0.2.0/24"},
		{"a network around it", "/network/tailscale/preferences", []string{"192.0.0.0/16"}, "192.0.0.0/16"},
		{"forced", "/network/tailscale/preferences?force=true", []string{"192.0.2.0/24"}, ""},
		{"elsewhere", "/network/tailscale/preferences", []string{"198.51.100.0/24"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform.SetForTest(t, pveHost)
			m := exec.NewMockCommander()
			m.SetOutput("tailscale", status(tc.routes...), nil)
			m.SetOutput("ip", kernelRoutes, nil)
			rec, e := serve(t, (&TailscaleHandler{Cmd: m}).SetPreferences, http.MethodPut, tc.target, `{"accept_routes":true}`)
			var set bool
			for _, c := range tsCalls(m) {
				set = set || reflect.DeepEqual(c, []string{"set", "--accept-routes"})
			}
			if tc.overlap == "" {
				if rec.Code != http.StatusOK || !set {
					t.Errorf("got %d %s, set %v", rec.Code, rec.Body.String(), set)
				}
				return
			}
			if rec.Code != http.StatusConflict || e.Error.Code != "TAILSCALE_ROUTE_OVERLAP" ||
				e.Error.Details["prefix"] != tc.overlap || e.Error.Details["peer"] != "router1" || set {
				t.Errorf("got %d %s, set %v; want 409 TAILSCALE_ROUTE_OVERLAP for %s from router1", rec.Code, rec.Body.String(), set, tc.overlap)
			}
		})
	}
}
