package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/svrforum/SFPanel/internal/config"
)

// caRotationFloorFile keeps, beside the Raft data whose lifetime it shares,
// the seq of the last CA rotation record this manager applied: after a
// restart its FSM can show an older record until it catches up, and applying
// that would put back a CA the rotation dropped.
const caRotationFloorFile = "ca-rotation.json"

// caFloor is caRotationFloorFile's contents.
type caFloor struct {
	Seq uint64 `json:"seq"`
}

// ApplyClusterCA brings this manager's CA files and trust in line with the
// CA rotation record (caMaterialOf), and its own certificate to the CA that
// signs now. Runs at activation, when the FSM changes a CA key, after a
// snapshot restore and on every watchLeader tick. ca.crt is written before
// what it holds is trusted, so a restart in any phase comes back trusting
// what this node trusted: its peers present certificates from those CAs.
// With no record nothing changes: a cluster that never rotated keeps its
// files as they are.
func (m *Manager) ApplyClusterCA() {
	if m.raft == nil || m.tls == nil {
		return
	}
	m.caApplyMu.Lock()
	defer m.caApplyMu.Unlock()
	state := m.raft.GetFSM().GetState()
	rec, ok, err := caRotationOf(state)
	if err != nil {
		m.noteCAApply("the CA rotation record does not parse", err)
		return
	}
	if !ok || rec.Seq < m.caRotationFloor() {
		return
	}
	mat, err := caMaterialOf(state)
	if err != nil {
		m.noteCAApply("waiting for the rest of a CA rotation step", err)
		return
	}
	if err := m.applyCAMaterial(mat); err != nil {
		m.noteCAApply("could not apply a CA rotation step; trying again", err)
		return
	}
	// Said once a step is recorded, not on every tick that finds it applied.
	moved := rec.Seq > m.caFloorSeq
	if err := m.raiseCARotationFloor(rec.Seq); err != nil {
		m.noteCAApply("applied a CA rotation step, but could not record it for a restart; trying again", err)
		return
	}
	m.caApplyNote = ""
	if moved {
		slog.Info("applied a CA rotation step", "component", "cluster", "phase", mat.Phase, "seq", mat.Seq)
	}
}

// applyCAMaterial puts mat on this manager in this order, writing each part
// only when it differs: ca.crt; the CA key, where this node keeps one — a
// manager that never signed has none, and ensureCAKey materializes the right
// one when it first must; the CAs it trusts; and its certificate, from the CA
// that signs. A moved certificate keeps the key and the names of the one it
// replaces, is presented at once, and is shown to the leader on a new
// heartbeat connection: the leader goes by the CA behind the certificate a
// node's connection presented. Caller holds caApplyMu.
func (m *Manager) applyCAMaterial(mat caMaterial) error {
	trust := mat.TrustPEM()
	if onDisk, err := m.tls.LoadCACert(); err != nil || !bytes.Equal(onDisk, trust) {
		if err := m.tls.SaveCACert(trust); err != nil {
			return fmt.Errorf("write ca.crt: %w", err)
		}
	}
	if m.tls.HasCAKey() {
		if onDisk, err := m.tls.LoadCAKey(); err != nil || !bytes.Equal(onDisk, mat.Disk.KeyPEM) {
			if err := m.tls.SaveCAKey(mat.Disk.KeyPEM); err != nil {
				return fmt.Errorf("write ca.key: %w", err)
			}
		}
	}
	if !slices.Equal(m.tls.TrustPins(), mat.TrustPins()) {
		if err := m.tls.ReloadCAPool(); err != nil {
			return fmt.Errorf("trust ca.crt: %w", err)
		}
	}
	if m.tls.ServedCertCAPin() == mat.SignPin() {
		return nil
	}
	leaf, err := m.tls.NodeCertLeaf()
	if err != nil {
		return err
	}
	var addrs []string
	for _, ip := range leaf.IPAddresses {
		addrs = append(addrs, ip.String())
	}
	addrs = append(addrs, leaf.DNSNames...)
	if err := m.tls.ReissueNodeCertBy(mat.Sign, m.nodeID, addrs, CertRoleManager); err != nil {
		return fmt.Errorf("move the node certificate to %s: %w", mat.SignPin(), err)
	}
	if err := m.tls.ReloadNodeCert(); err != nil {
		return err
	}
	m.reconnectHeartbeat()
	return nil
}

// noteCAApply warns why ApplyClusterCA moved nothing, once while the reason
// stays the same: it runs on every watchLeader tick. Caller holds caApplyMu.
func (m *Manager) noteCAApply(what string, err error) {
	if note := what + ": " + err.Error(); note != m.caApplyNote {
		m.caApplyNote = note
		slog.Warn(what, "component", "cluster", "error", err)
	}
}

// caRotationFloor is the seq of the last CA rotation record this manager
// applied, read from caRotationFloorFile the first time. A floor that cannot
// be read is 0, with a warning, as the proxy secret's floor is ignored then:
// the FSM decides alone, and the next step applied writes the floor again.
// With no data dir the floor lives in memory alone. Caller holds caApplyMu.
func (m *Manager) caRotationFloor() uint64 {
	if m.caFloorLoaded {
		return m.caFloorSeq
	}
	m.caFloorLoaded = true
	if m.config == nil || m.config.DataDir == "" {
		return m.caFloorSeq
	}
	path := filepath.Join(m.config.DataDir, caRotationFloorFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m.caFloorSeq
	}
	var f caFloor
	if err == nil {
		err = json.Unmarshal(data, &f)
	}
	if err != nil {
		slog.Warn("ignoring the recorded CA rotation step", "component", "cluster", "path", path, "error", err)
		return m.caFloorSeq
	}
	m.caFloorSeq = f.Seq
	return m.caFloorSeq
}

// raiseCARotationFloor records seq as the floor when it is higher. The floor
// moves in memory only once the file holds it, so a write that failed is
// tried again by the next apply: the file is what a restart reads. Caller
// holds caApplyMu.
func (m *Manager) raiseCARotationFloor(seq uint64) error {
	if seq <= m.caFloorSeq {
		return nil
	}
	if m.config != nil && m.config.DataDir != "" {
		data, err := json.Marshal(caFloor{Seq: seq})
		if err != nil {
			return err
		}
		if err := config.AtomicWriteFile(filepath.Join(m.config.DataDir, caRotationFloorFile), data, 0600); err != nil {
			return err
		}
	}
	m.caFloorSeq = seq
	return nil
}

// reconnectHeartbeat asks this manager's heartbeat for a new connection to
// the leader (StartLocalMetrics). A leader sends no heartbeat, and the signal
// waits unused: the stream it opens once it follows again is a new one
// anyway.
func (m *Manager) reconnectHeartbeat() {
	select {
	case m.hbReconnect <- struct{}{}:
	default:
	}
}
