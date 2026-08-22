package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"strconv"
	"strings"
	"sync"
)

// PanelEndpoint describes how to reach ONE peer's panel HTTP port.
//
// It exists because the scheme decision used to live in three copies —
// middleware/proxy.go's nodeBaseURL, compose/migration_transport.go's
// byte-identical migrationNodeBaseURL, and the ws/wss switch inside
// RelayWebSocket. Three copies of a decision drift, and the drift is invisible
// until a multi-gigabyte stack migration fails halfway through against a peer
// that moved to TLS. One resolver, three callers.
type PanelEndpoint struct {
	// BaseURL is scheme://host:port with no trailing slash.
	BaseURL string
	// WSBaseURL is the same authority with the ws or wss scheme.
	WSBaseURL string
	// TLSConfig verifies the peer, or is nil when the peer serves plain HTTP.
	TLSConfig *tls.Config
}

// fallbackPanelPort is the last-resort port when a stored APIAddress carries
// none and the local config is unavailable.
//
// The previous code hardcoded 3628 unconditionally, which silently dialled the
// wrong port on any deployment that had moved server.port — including every
// install predating the 3628 default, which used 19443. The local port is the
// better guess: a cluster runs one build with one config shape, so a peer that
// failed to record its port almost certainly listens where this node does.
const fallbackPanelPort = 3628

// panelPort is the port to assume for a peer whose APIAddress omits one.
func (m *Manager) panelPort() int {
	if m != nil && m.config != nil && m.config.APIPort > 0 {
		return m.config.APIPort
	}
	return fallbackPanelPort
}

// PanelEndpointFor resolves how to reach a peer's panel port.
//
// The scheme comes from whether that node's panel CA is in replicated state,
// NOT from a prefix on APIAddress. The prefix looks like the natural home for
// it and three call sites already honour one, but it is not durable: every
// writer of APIAddress stores a bare ip:port, and verifySelfAddress applies a
// correction back to that bare form ten seconds after every leader boot. A
// scheme kept there would be silently erased.
func (m *Manager) PanelEndpointFor(node *Node) PanelEndpoint {
	if node == nil {
		return PanelEndpoint{}
	}
	authority := normalisePanelAuthority(node.APIAddress, m.panelPort())

	caPEM := ""
	if m != nil && m.raft != nil {
		caPEM = m.raft.GetFSM().GetState().Config[panelCAKey(node.ID)]
	}
	if caPEM == "" {
		return PanelEndpoint{
			BaseURL:   "http://" + authority,
			WSBaseURL: "ws://" + authority,
		}
	}
	return PanelEndpoint{
		BaseURL:   "https://" + authority,
		WSBaseURL: "wss://" + authority,
		TLSConfig: peerTLSConfig(node.ID, caPEM),
	}
}

// normalisePanelAuthority strips any scheme an older record may carry and
// appends the default port when none is present.
func normalisePanelAuthority(apiAddr string, port int) string {
	for _, prefix := range []string{"https://", "http://", "wss://", "ws://"} {
		apiAddr = strings.TrimPrefix(apiAddr, prefix)
	}
	apiAddr = strings.TrimSuffix(apiAddr, "/")
	if apiAddr == "" {
		return apiAddr
	}
	// An IPv6 literal is bracketed; a bare one has many colons and no brackets.
	if strings.HasPrefix(apiAddr, "[") {
		if strings.Contains(apiAddr, "]:") {
			return apiAddr
		}
		return apiAddr + ":" + strconv.Itoa(port)
	}
	if strings.Count(apiAddr, ":") == 1 {
		return apiAddr
	}
	if strings.Contains(apiAddr, ":") {
		// Bare IPv6 — bracket it so it can carry a port.
		return "[" + apiAddr + "]:" + strconv.Itoa(port)
	}
	return apiAddr + ":" + strconv.Itoa(port)
}

// peerTLSPool caches parsed per-peer trust pools. Parsing a PEM on every
// relayed request would be wasteful, and the cluster mTLS pool next door cannot
// be reused: it is documented as "loaded lazily, never invalidated", whereas
// this set changes whenever a node joins, leaves, or turns TLS on.
var peerTLSPool sync.Map // nodeID -> peerPoolEntry

type peerPoolEntry struct {
	pem string
	cfg *tls.Config
}

// peerTLSConfig builds a config trusting ONLY the named peer's authority.
//
// Deliberately not a union of every node's CA: a pool holding all of them would
// let any node in the cluster vouch for any other, so a single node whose CA
// key leaked could impersonate its peers on the relay path. One anchor per
// peer keeps the blast radius at that peer.
func peerTLSConfig(nodeID, caPEM string) *tls.Config {
	if cached, ok := peerTLSPool.Load(nodeID); ok {
		if entry, ok := cached.(peerPoolEntry); ok && entry.pem == caPEM {
			return entry.cfg
		}
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		// Unparseable replicated material: fall through to a config with an
		// empty pool, which fails the handshake loudly rather than silently
		// falling back to system roots (which would trust the public web PKI
		// for a private address).
		pool = x509.NewCertPool()
	}
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	peerTLSPool.Store(nodeID, peerPoolEntry{pem: caPEM, cfg: cfg})
	return cfg
}
