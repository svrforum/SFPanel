package cluster

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Replicated FSM config keys for the per-node PANEL certificate authority.
//
// This is a different trust domain from cluster_ca_cert / cluster_ca_key above.
// That one is the single shared authority for mutual TLS between nodes; this
// one is per-node, browser-facing, and holds the PUBLIC certificate only. There
// is deliberately no panel CA *key* key: a node's panel CA key never leaves the
// machine that generated it, so replicating this material does not widen the
// blast radius the way cluster_ca_key did.
//
// Presence is also the HTTPS signal. A node's entry existing means that node
// serves TLS, so peers know which scheme to use without a second replicated
// flag — and without depending on a scheme prefix in APIAddress, which
// verifySelfAddress rewrites back to a bare ip:port ten seconds after every
// leader boot.
const configKeyPanelCAPrefix = "panel_ca_cert/"

func panelCAKey(nodeID string) string { return configKeyPanelCAPrefix + nodeID }

// SetPanelCA records a node's panel CA certificate in replicated state.
//
// Leader-only, like every FSM write. A follower cannot publish its own
// certificate, which is exactly the asymmetry that makes the cluster CA
// precedent not transfer: that CA is one shared object any eventual leader can
// seed, whereas a permanent follower would never get its own panel CA into the
// FSM. Followers therefore ask the leader to call this on their behalf, the
// same way UpdateNodeAddress already handles per-node address correction.
func (m *Manager) SetPanelCA(nodeID, certPEM string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	if nodeID == "" {
		return fmt.Errorf("node id is required")
	}
	if err := validatePanelCA(certPEM); err != nil {
		return err
	}
	if m.raft.GetFSM().GetState().Config[panelCAKey(nodeID)] == certPEM {
		return nil // already replicated, unchanged
	}
	if err := m.SetConfig(panelCAKey(nodeID), certPEM); err != nil {
		return fmt.Errorf("replicate panel CA: %w", err)
	}
	slog.Info("replicated node panel CA", "component", "cluster", "node_id", nodeID)
	return nil
}

// panelCAChangeInterval is how often a reported panel CA may change. An
// agent reports its CA whenever it likes and every change is a Raft entry on
// every manager; a real one changes when its panel's TLS is set up again.
const panelCAChangeInterval = time.Minute

// SetReportedPanelCA stores the panel CA a node reported (ReportPanelCA),
// taking a change at most once per panelCAChangeInterval per node. Reporting
// the CA already stored changes nothing and is always answered.
func (m *Manager) SetReportedPanelCA(nodeID, certPEM string, now time.Time) error {
	m.panelCAReportMu.Lock()
	defer m.panelCAReportMu.Unlock()
	if m.raft != nil && m.raft.GetFSM().GetState().Config[panelCAKey(nodeID)] == certPEM {
		return nil
	}
	if at, ok := m.panelCAChangedAt[nodeID]; ok && now.Sub(at) < panelCAChangeInterval {
		return ErrPanelCAChangedRecently
	}
	if err := m.SetPanelCA(nodeID, certPEM); err != nil {
		return err
	}
	if m.panelCAChangedAt == nil {
		m.panelCAChangedAt = map[string]time.Time{}
	}
	m.panelCAChangedAt[nodeID] = now
	return nil
}

// maxPanelCABytes bounds a panel CA: a CA certificate is a kilobyte or two,
// and an operator's bundle a few of them.
const maxPanelCABytes = 64 << 10

// validatePanelCA accepts PEM certificates and nothing else, at least one of
// them a certificate authority — the CA behind the panel, or an operator's
// bundle of them.
//
// The check matters because whatever lands here becomes a trust anchor for
// every peer's HTTP relay, and is stored as given: a private key arriving by
// mistake, or anything an agent appends, would be replicated to every node
// and served through snapshots; a leaf certificate alone would silently
// produce a pool that verifies nothing.
func validatePanelCA(certPEM string) error {
	if len(certPEM) > maxPanelCABytes {
		return fmt.Errorf("panel CA is larger than %d KiB", maxPanelCABytes>>10)
	}
	rest := []byte(certPEM)
	sawCA := false
	for {
		rest = bytes.TrimSpace(rest)
		if len(rest) == 0 {
			break
		}
		// pem.Decode skips whatever precedes a block, and what is skipped
		// would be stored too.
		if !bytes.HasPrefix(rest, []byte("-----BEGIN ")) {
			return fmt.Errorf("panel CA holds something other than PEM certificates")
		}
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return fmt.Errorf("panel CA is not PEM")
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("panel CA must hold certificates only, got %s", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parse panel CA: %w", err)
		}
		sawCA = sawCA || cert.IsCA
	}
	if !sawCA {
		if len(bytes.TrimSpace([]byte(certPEM))) == 0 {
			return fmt.Errorf("panel CA is not PEM")
		}
		return fmt.Errorf("panel CA certificate is not a certificate authority")
	}
	return nil
}

// PanelCACerts returns the replicated panel CA certificate of every node that
// has one, keyed by node ID. Nodes absent from the map serve plain HTTP.
//
// Reading needs no leadership, so followers build the same trust pool as the
// leader.
func (m *Manager) PanelCACerts() map[string]string {
	if m.raft == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range m.raft.GetFSM().GetState().Config {
		if id, ok := strings.CutPrefix(k, configKeyPanelCAPrefix); ok && v != "" {
			out[id] = v
		}
	}
	return out
}

// NodeServesTLS reports whether a peer terminates TLS on its panel port.
func (m *Manager) NodeServesTLS(nodeID string) bool {
	if m.raft == nil || nodeID == "" {
		return false
	}
	return m.raft.GetFSM().GetState().Config[panelCAKey(nodeID)] != ""
}

// forgetPanelCA drops a removed node's certificate from replicated state.
//
// CmdRemoveNode deletes only the node's entry, so without this a
// decommissioned node's authority would stay a trust anchor for the rest of
// the cluster forever.
func (m *Manager) forgetPanelCA(nodeID string) {
	if m.raft == nil || nodeID == "" {
		return
	}
	if m.raft.GetFSM().GetState().Config[panelCAKey(nodeID)] == "" {
		return
	}
	if err := m.raft.Apply(Command{Type: CmdDeleteConfig, Key: panelCAKey(nodeID)}, 5*time.Second); err != nil {
		// Best-effort: the node is already being removed, and failing the
		// removal over a stale trust anchor would be the worse trade.
		slog.Warn("failed to drop removed node's panel CA from replicated state",
			"component", "cluster", "node_id", nodeID, "error", err)
		return
	}
	slog.Info("dropped removed node's panel CA", "component", "cluster", "node_id", nodeID)
}
