package network

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// `wg show <iface> dump` real-shape output. Tab-separated.
//
//	line 0: <iface> private_key  public_key  listen_port  fwmark
//	line N: <peer> public_key  preshared_key  endpoint  allowed_ips  latest_handshake  rx  tx  persistent_keepalive
const wgDumpFixture = "PRIV1=\tPUB1=\t51820\toff\n" +
	"PEER1=\t(none)\t1.2.3.4:51820\t10.0.0.2/32,fd00::2/128\t1714000000\t1024\t512\t25\n" +
	"PEER2=\t(none)\t(none)\t(none)\t0\t0\t0\toff\n"

func TestParseWGDump(t *testing.T) {
	pub, port, peers := parseWGDump(wgDumpFixture)
	if pub != "PUB1=" {
		t.Errorf("public key: got %q, want PUB1=", pub)
	}
	if port != 51820 {
		t.Errorf("port: got %d, want 51820", port)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	want0 := WireGuardPeer{
		PublicKey:       "PEER1=",
		Endpoint:        "1.2.3.4:51820",
		AllowedIPs:      []string{"10.0.0.2/32", "fd00::2/128"},
		LatestHandshake: 1714000000,
		TransferRx:      1024,
		TransferTx:      512,
	}
	if !reflect.DeepEqual(peers[0], want0) {
		t.Errorf("peer 0: got %+v\nwant %+v", peers[0], want0)
	}

	// Peer 2: never connected — endpoint and allowed_ips are "(none)".
	if peers[1].Endpoint != "" {
		t.Errorf("peer 1 endpoint: expected empty for (none), got %q", peers[1].Endpoint)
	}
	if len(peers[1].AllowedIPs) != 0 {
		t.Errorf("peer 1 allowed_ips: expected empty for (none), got %v", peers[1].AllowedIPs)
	}
}

func TestParseWGDump_Empty(t *testing.T) {
	pub, port, peers := parseWGDump("")
	if pub != "" || port != 0 || peers != nil {
		t.Errorf("got pub=%q port=%d peers=%v; want all-zero", pub, port, peers)
	}
}

func TestParseWGConfField(t *testing.T) {
	const conf = `[Interface]
Address = 10.0.0.1/24
ListenPort = 51820
PrivateKey = SECRET=

[Peer]
PublicKey = PEER1=
AllowedIPs = 10.0.0.2/32
`
	cases := []struct {
		field string
		want  string
	}{
		{"Address", "10.0.0.1/24"},
		{"ListenPort", "51820"},
		{"PublicKey", "PEER1="},
		// case-insensitive match (parser lowercases both sides)
		{"address", "10.0.0.1/24"},
		// missing field
		{"Endpoint", ""},
	}
	for _, c := range cases {
		got := parseWGConfField(conf, c.field)
		if got != c.want {
			t.Errorf("parseWGConfField(%q) = %q; want %q", c.field, got, c.want)
		}
	}
}

// An interface that is up with no peer yet came back as "peers": null, and the
// page, reading its length, showed nothing but an error until a peer existed.
// The same happens when wg show fails.
func TestPopulateWGInterfacePeersNeverNull(t *testing.T) {
	const ifaceOnly = "cHJpdmF0ZQ==\tcHVibGlj\t51820\toff\n"
	for _, tc := range []struct {
		name string
		dump exec.MockResult
	}{
		{"up, no peers", exec.MockResult{Output: ifaceOnly}},
		{"wg show failed", exec.MockResult{Err: errors.New("Unable to access interface: No such device")}},
	} {
		cmd := exec.NewMockCommander()
		cmd.Outputs["wg"] = tc.dump
		iface := WireGuardInterface{Name: "wg0", Active: true}
		(&WireGuardHandler{Cmd: cmd}).populateWGInterface(&iface, "[Interface]\nAddress = 10.8.0.1/24\n")
		out, err := json.Marshal(iface)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), `"peers":[]`) {
			t.Errorf("%s: %s, want \"peers\":[]", tc.name, out)
		}
	}
}

// The page offered "enable at boot" and "disable at boot" side by side because
// it could not tell which applied. `systemctl is-enabled` exits non-zero for
// every answer but the enabled ones, so the word decides — and a question that
// could not be asked is unknown (no field), never "disabled".
func TestPopulateWGInterfaceAutostart(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  exec.MockResult
		want string // the autostart member of the JSON, "" for none
	}{
		{"enabled", exec.MockResult{Output: "enabled\n"}, `"autostart":true`},
		{"disabled exits 1", exec.MockResult{Output: "disabled\n", Err: errors.New("exit status 1")}, `"autostart":false`},
		{"runtime only, gone at the next boot", exec.MockResult{Output: "enabled-runtime\n"}, `"autostart":false`},
		{"masked", exec.MockResult{Output: "masked\n", Err: errors.New("exit status 1")}, `"autostart":false`},
		{"no unit file", exec.MockResult{Output: "not-found\n", Err: errors.New("exit status 4")}, `"autostart":false`},
		{"static: other units decide", exec.MockResult{Output: "static\n"}, ""},
		{"systemctl missing", exec.MockResult{Err: errors.New(`exec: "systemctl": executable file not found in $PATH`)}, ""},
		{"bus timeout", exec.MockResult{Output: "Failed to get unit file state for wg-quick@wg0.service: Connection timed out\n", Err: errors.New("exit status 1")}, ""},
	} {
		cmd := exec.NewMockCommander()
		cmd.Outputs["systemctl"] = tc.res
		iface := WireGuardInterface{Name: "wg0"}
		(&WireGuardHandler{Cmd: cmd}).populateWGInterface(&iface, "[Interface]\nAddress = 10.8.0.1/24\n")
		out, err := json.Marshal(iface)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		for _, m := range []string{`"autostart":true`, `"autostart":false`} {
			if strings.Contains(string(out), m) {
				got = m
			}
		}
		if strings.Contains(string(out), `"autostart":null`) {
			got = `"autostart":null`
		}
		if got != tc.want {
			t.Errorf("%s: autostart member %q, want %q (%s)", tc.name, got, tc.want, out)
		}
		var asked bool
		for _, c := range cmd.Calls {
			if c.Name == "systemctl" && reflect.DeepEqual(c.Args, []string{"is-enabled", "wg-quick@wg0"}) {
				asked = true
			}
		}
		if !asked {
			t.Errorf("%s: never ran systemctl is-enabled wg-quick@wg0: %+v", tc.name, cmd.Calls)
		}
	}
}
