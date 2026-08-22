package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// makeCert builds a PEM certificate for the replication tests. isCA controls
// whether it is an authority, which is the property SetPanelCA screens on.
func makeCert(t *testing.T, cn string, isCA bool) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(42),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	if isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// Whatever lands in this key becomes a trust anchor for every peer's HTTP
// relay, so the screen on the way in is the only thing standing between a
// mistake and cluster-wide misplaced trust.
func TestValidatePanelCA(t *testing.T) {
	caPEM := makeCert(t, "SFPanel Local CA", true)
	leafPEM := makeCert(t, "panel", false)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"a real CA certificate is accepted", caPEM, false},
		{"a leaf certificate is rejected", leafPEM, true},
		{"a private key is rejected", keyPEM, true},
		{"empty input is rejected", "", true},
		{"non-PEM input is rejected", "not a certificate", true},
		{"PEM with garbage body is rejected", "-----BEGIN CERTIFICATE-----\nZ2FyYmFnZQ==\n-----END CERTIFICATE-----\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePanelCA(tc.in)
			if tc.wantErr && err == nil {
				t.Error("validatePanelCA accepted material it should have refused")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validatePanelCA rejected a valid CA: %v", err)
			}
		})
	}
}

func TestPanelCACerts(t *testing.T) {
	caA := makeCert(t, "node-a CA", true)
	caB := makeCert(t, "node-b CA", true)

	fsm := fsmWithConfig(t, map[string]string{
		panelCAKey("node-a"): caA,
		panelCAKey("node-b"): caB,
		// Neighbouring keys in the same flat map must not be mistaken for
		// panel CAs — cluster_ca_key in particular is a PRIVATE key.
		"cluster_ca_cert": makeCert(t, "cluster CA", true),
		"cluster_ca_key":  "-----BEGIN EC PRIVATE KEY-----\nsecret\n-----END EC PRIVATE KEY-----\n",
		"jwt_secret":      "hunter2",
		"cluster_name":    "prod",
	})
	m := &Manager{raft: &RaftNode{fsm: fsm}}

	got := m.PanelCACerts()
	if len(got) != 2 {
		t.Fatalf("PanelCACerts returned %d entries (%v), want 2", len(got), keysOf(got))
	}
	if got["node-a"] != caA || got["node-b"] != caB {
		t.Error("PanelCACerts returned the wrong certificate for a node")
	}
	for _, forbidden := range []string{"cluster_ca_cert", "cluster_ca_key", "jwt_secret", "cluster_name"} {
		if _, leaked := got[forbidden]; leaked {
			t.Errorf("PanelCACerts leaked the unrelated config key %q", forbidden)
		}
	}
	for id, v := range got {
		if v == "hunter2" || v == "prod" {
			t.Errorf("node %q carries a non-certificate value", id)
		}
	}
}

// Presence of a node's CA IS the "this node serves HTTPS" signal — the design
// deliberately does not use a scheme prefix in APIAddress, because
// verifySelfAddress rewrites that back to a bare ip:port after every leader
// boot.
func TestNodeServesTLS(t *testing.T) {
	fsm := fsmWithConfig(t, map[string]string{
		panelCAKey("tls-node"): makeCert(t, "tls-node CA", true),
		"jwt_secret":           "hunter2",
	})
	m := &Manager{raft: &RaftNode{fsm: fsm}}

	if !m.NodeServesTLS("tls-node") {
		t.Error("a node with a replicated panel CA should be treated as HTTPS")
	}
	if m.NodeServesTLS("plain-node") {
		t.Error("a node with no replicated panel CA should be treated as plain HTTP")
	}
	if m.NodeServesTLS("") {
		t.Error("an empty node id should never report TLS")
	}
}

// Standalone mode has no Raft at all. Every accessor has to tolerate that
// rather than panic, because the same code paths run with cluster.enabled off.
func TestPanelCAAccessorsWithoutRaft(t *testing.T) {
	m := &Manager{}
	if got := m.PanelCACerts(); len(got) != 0 {
		t.Errorf("PanelCACerts on a standalone manager = %v, want empty", got)
	}
	if m.NodeServesTLS("anything") {
		t.Error("NodeServesTLS on a standalone manager should be false")
	}
	m.forgetPanelCA("anything") // must not panic
	// Every FSM write is leader-only, and this is the asymmetry that made the
	// cluster CA precedent not transfer: one shared authority any eventual
	// leader can seed, versus a per-node certificate a permanent follower could
	// never publish for itself. Same shape as TestSeedClusterCA_GuardsNonLeader
	// — a live leadership transition needs a real Raft, so it is covered by the
	// two-node live check rather than here.
	if err := m.SetPanelCA("node", makeCert(t, "ca", true)); err != ErrNotLeader {
		t.Errorf("SetPanelCA without raft = %v, want ErrNotLeader", err)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
