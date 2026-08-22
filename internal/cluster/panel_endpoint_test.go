package cluster

import (
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

// The scheme decision used to live in three copies. These cases pin the single
// resolver they were replaced with.
func TestPanelEndpointFor(t *testing.T) {
	caPEM := makeCert(t, "node-a CA", true)
	fsm := fsmWithConfig(t, map[string]string{panelCAKey("tls-node"): caPEM})
	m := &Manager{raft: &RaftNode{fsm: fsm}}

	t.Run("a node with a replicated CA is https", func(t *testing.T) {
		ep := m.PanelEndpointFor(&Node{ID: "tls-node", APIAddress: "192.168.1.10:3628"})
		if ep.BaseURL != "https://192.168.1.10:3628" {
			t.Errorf("BaseURL = %q, want https", ep.BaseURL)
		}
		if ep.WSBaseURL != "wss://192.168.1.10:3628" {
			t.Errorf("WSBaseURL = %q, want wss", ep.WSBaseURL)
		}
		if ep.TLSConfig == nil || ep.TLSConfig.RootCAs == nil {
			t.Fatal("no trust pool for a TLS peer — the handshake would fall back to system roots")
		}
	})

	t.Run("a node with no replicated CA stays plaintext", func(t *testing.T) {
		ep := m.PanelEndpointFor(&Node{ID: "plain-node", APIAddress: "192.168.1.11:3628"})
		if ep.BaseURL != "http://192.168.1.11:3628" {
			t.Errorf("BaseURL = %q, want http", ep.BaseURL)
		}
		if ep.WSBaseURL != "ws://192.168.1.11:3628" {
			t.Errorf("WSBaseURL = %q, want ws", ep.WSBaseURL)
		}
		if ep.TLSConfig != nil {
			t.Error("plaintext peer should carry no TLS config")
		}
	})

	// A mixed-version cluster is the normal state during a rolling upgrade:
	// a peer still on the old binary has no replicated CA and must keep being
	// reached over HTTP, with no version negotiation anywhere.
	t.Run("mixed cluster resolves each peer independently", func(t *testing.T) {
		tlsEp := m.PanelEndpointFor(&Node{ID: "tls-node", APIAddress: "10.0.0.1:3628"})
		oldEp := m.PanelEndpointFor(&Node{ID: "old-node", APIAddress: "10.0.0.2:3628"})
		if tlsEp.BaseURL[:5] != "https" || oldEp.BaseURL[:5] == "https" {
			t.Errorf("mixed resolution wrong: %q / %q", tlsEp.BaseURL, oldEp.BaseURL)
		}
	})

	t.Run("nil node is safe", func(t *testing.T) {
		if ep := m.PanelEndpointFor(nil); ep.BaseURL != "" {
			t.Errorf("nil node = %q, want empty", ep.BaseURL)
		}
	})

	// Each peer gets its OWN anchor. A shared union pool would let any node in
	// the cluster vouch for any other on the relay path.
	t.Run("peers do not share a trust pool", func(t *testing.T) {
		other := makeCert(t, "node-b CA", true)
		fsm2 := fsmWithConfig(t, map[string]string{
			panelCAKey("a"): caPEM,
			panelCAKey("b"): other,
		})
		m2 := &Manager{raft: &RaftNode{fsm: fsm2}}
		a := m2.PanelEndpointFor(&Node{ID: "a", APIAddress: "10.0.0.1:3628"})
		b := m2.PanelEndpointFor(&Node{ID: "b", APIAddress: "10.0.0.2:3628"})
		if a.TLSConfig == b.TLSConfig {
			t.Error("two peers share one tls.Config — their anchors are not isolated")
		}
	})
}

// The port fallback used to be a hardcoded 3628, which silently dialled the
// wrong port on any deployment that had moved server.port — installs predating
// the 3628 default used 19443.
func TestPanelPortFallsBackToConfiguredPort(t *testing.T) {
	cfg := &config.ClusterConfig{APIPort: 19443}
	m := &Manager{config: cfg, raft: &RaftNode{fsm: fsmWithConfig(t, nil)}}
	ep := m.PanelEndpointFor(&Node{ID: "peer", APIAddress: "10.0.0.9"})
	if ep.BaseURL != "http://10.0.0.9:19443" {
		t.Errorf("BaseURL = %q, want the configured port 19443", ep.BaseURL)
	}

	// No config at all (standalone, or very early boot) still yields something
	// usable rather than a portless URL.
	bare := &Manager{raft: &RaftNode{fsm: fsmWithConfig(t, nil)}}
	if ep := bare.PanelEndpointFor(&Node{ID: "peer", APIAddress: "10.0.0.9"}); ep.BaseURL != "http://10.0.0.9:3628" {
		t.Errorf("BaseURL without config = %q, want the 3628 fallback", ep.BaseURL)
	}
}

func TestNormalisePanelAuthority(t *testing.T) {
	cases := map[string]string{
		"192.168.1.10:3628":         "192.168.1.10:3628",
		"192.168.1.10":              "192.168.1.10:3628",
		"http://192.168.1.10:3628":  "192.168.1.10:3628",
		"https://192.168.1.10:3628": "192.168.1.10:3628",
		"https://192.168.1.10/":     "192.168.1.10:3628",
		"panel.local:8443":          "panel.local:8443",
		"[fd00::1]:3628":            "[fd00::1]:3628",
		"fd00::1":                   "[fd00::1]:3628",
		"":                          "",
	}
	for in, want := range cases {
		if got := normalisePanelAuthority(in, 3628); got != want {
			t.Errorf("normalisePanelAuthority(%q) = %q, want %q", in, got, want)
		}
	}
}
