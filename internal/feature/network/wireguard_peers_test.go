package network

import (
	"fmt"
	"strings"
	"testing"
)

// k builds a syntactically-valid 44-char base64 WireGuard key (43 chars + '=')
// from a short prefix, so tests don't hand-count key lengths.
func k(prefix string) string {
	return prefix + strings.Repeat("0", 43-len(prefix)) + "="
}

func sampleConf() string {
	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = 10.0.0.1/24
ListenPort = 51820

[Peer]
PublicKey = %s
AllowedIPs = 10.0.0.2/32

[Peer]
PublicKey = %s
AllowedIPs = 10.0.0.3/32
`, k("priv"), k("AAAA"), k("BBBB"))
}

func TestWgKeyRe(t *testing.T) {
	valid := []string{k("AAAA"), "abcd+fgh/jklmnopqrstuvwxyz0123456789ABCDEFG="}
	invalid := []string{
		"", "tooshort=",
		strings.Repeat("A", 43),       // no trailing '='
		strings.Repeat("A", 44) + "=", // too long
		"badchar$" + strings.Repeat("0", 35) + "=",
	}
	for _, key := range valid {
		if !wgKeyRe.MatchString(key) {
			t.Errorf("expected %q valid (len before '=' = %d)", key, len(key)-1)
		}
	}
	for _, key := range invalid {
		if wgKeyRe.MatchString(key) {
			t.Errorf("expected %q invalid", key)
		}
	}
}

func TestValidWGEndpoint(t *testing.T) {
	if !validWGEndpoint("vpn.example.com:51820") || !validWGEndpoint("1.2.3.4:51820") {
		t.Error("valid endpoints rejected")
	}
	for _, bad := range []string{"", "noport", "host:0", "host:99999", "host:abc"} {
		if validWGEndpoint(bad) {
			t.Errorf("expected %q rejected", bad)
		}
	}
}

func TestAppendPeerBlock(t *testing.T) {
	out := appendPeerBlock(sampleConf(), AddPeerRequest{
		PublicKey:           k("CCCC"),
		AllowedIPs:          []string{"10.0.0.4/32"},
		PersistentKeepalive: 25,
	})
	if !strings.Contains(out, "PublicKey = "+k("CCCC")) {
		t.Error("new peer public key not appended")
	}
	if !strings.Contains(out, "PersistentKeepalive = 25") {
		t.Error("keepalive not written")
	}
	if strings.Count(out, "[Peer]") != 3 {
		t.Errorf("expected 3 [Peer] blocks, got %d", strings.Count(out, "[Peer]"))
	}
}

func TestRemovePeerFromConfig(t *testing.T) {
	conf := sampleConf()

	out, removed := removePeerFromConfig(conf, k("AAAA"))
	if !removed {
		t.Fatal("expected removed=true")
	}
	if strings.Contains(out, k("AAAA")) {
		t.Error("removed peer key still present")
	}
	if !strings.Contains(out, k("BBBB")) {
		t.Error("non-target peer was dropped")
	}
	if !strings.Contains(out, "[Interface]") || !strings.Contains(out, "ListenPort = 51820") {
		t.Error("interface block damaged")
	}
	if strings.Count(out, "[Peer]") != 1 {
		t.Errorf("expected 1 remaining [Peer], got %d", strings.Count(out, "[Peer]"))
	}

	if _, removed2 := removePeerFromConfig(conf, k("ZZZZ")); removed2 {
		t.Error("expected removed=false for unknown key")
	}

	// Round trip: append then remove returns to two peers.
	added := appendPeerBlock(conf, AddPeerRequest{PublicKey: k("DDDD"), AllowedIPs: []string{"10.0.0.5/32"}})
	back, ok := removePeerFromConfig(added, k("DDDD"))
	if !ok || strings.Count(back, "[Peer]") != 2 {
		t.Errorf("round-trip failed: ok=%v peers=%d", ok, strings.Count(back, "[Peer]"))
	}
}
