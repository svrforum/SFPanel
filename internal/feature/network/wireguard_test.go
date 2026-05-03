package network

import (
	"reflect"
	"testing"
)

// `wg show <iface> dump` real-shape output. Tab-separated.
//   line 0: <iface> private_key  public_key  listen_port  fwmark
//   line N: <peer> public_key  preshared_key  endpoint  allowed_ips  latest_handshake  rx  tx  persistent_keepalive
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
