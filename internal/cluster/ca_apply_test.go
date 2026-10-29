package cluster

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

// caManager is a manager over an FSM with no Raft, its cert dir holding the
// cluster's CA as a founder's does, and the CA a rotation moves to — both in
// replicated state, the record still to come. Like a running manager's, its
// TLS has loaded the CAs it trusts and the certificate it presents.
func caManager(t *testing.T) (*Manager, *CAPair, *CAPair, string) {
	t.Helper()
	certDir, tlsMgr := setupCertDir(t)
	crt, _ := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	key, _ := os.ReadFile(filepath.Join(certDir, "ca.key"))
	cur, err := ParseCAPair(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := GenerateCA("SFPanel CA - test-cluster (next)", time.Now())
	fsm := fsmWithConfig(t, map[string]string{
		configKeyCACert: string(cur.CertPEM), configKeyCAKey: string(cur.KeyPEM),
		configKeyCANextCert: string(next.CertPEM), configKeyCANextKey: string(next.KeyPEM),
	})
	if _, err := tlsMgr.ServerTLSConfig(); err != nil {
		t.Fatal(err)
	}
	m := &Manager{tls: tlsMgr, nodeID: "node-1", config: &config.ClusterConfig{DataDir: t.TempDir()}, raft: &RaftNode{fsm: fsm},
		hbReconnect: make(chan struct{}, 1)}
	return m, cur, next, certDir
}

func setCARecord(t *testing.T, m *Manager, cur, next *CAPair, seq uint64, phase CAPhase) {
	t.Helper()
	r := CARotation{ID: "r1", Seq: seq, Phase: phase, OldPin: cur.Pin(), NextPin: next.Pin()}
	applyCmd(t, m.raft.fsm, Command{Type: CmdSetConfig, Key: configKeyCARotation, Value: mustJSON(string(mustJSON(r)))})
}

func diskPins(t *testing.T, certDir string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	var pins []string
	for _, c := range pemCertificates(data) {
		pins = append(pins, CAPin(c.RawSubjectPublicKeyInfo))
	}
	return pins
}

// In the trust phase a manager trusts both CAs — written to ca.crt before it
// is trusted, the current CA first — and its certificate stays where it is.
func TestAManagerTrustsBothCAsInTheTrustPhase(t *testing.T) {
	m, cur, next, certDir := caManager(t)
	setCARecord(t, m, cur, next, 1, CAPhaseTrust)
	m.ApplyClusterCA()
	want := []string{cur.Pin(), next.Pin()}
	if got := diskPins(t, certDir); !slices.Equal(got, want) {
		t.Fatalf("ca.crt holds %v, want %v", got, want)
	}
	if got := m.tls.TrustPins(); !slices.Equal(got, want) {
		t.Fatalf("trusts %v", got)
	}
	if m.tls.ServedCertCAPin() != cur.Pin() {
		t.Fatal("the certificate moved before every node trusted the next CA")
	}
}

// In the reissue phase it moves its certificate to the next CA — same key,
// same names — presents it at once, and reconnects its heartbeat so that the
// leader sees it.
func TestAManagerMovesItsCertificateInTheReissuePhase(t *testing.T) {
	m, cur, next, certDir := caManager(t)
	keyBefore, _ := os.ReadFile(filepath.Join(certDir, "node.key"))
	setCARecord(t, m, cur, next, 2, CAPhaseReissue)
	m.ApplyClusterCA()
	if m.tls.ServedCertCAPin() != next.Pin() {
		t.Fatalf("presents a certificate from %q", m.tls.ServedCertCAPin())
	}
	if keyAfter, _ := os.ReadFile(filepath.Join(certDir, "node.key")); !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("the key changed")
	}
	select {
	case <-m.hbReconnect:
	default:
		t.Fatal("the heartbeat was not asked to reconnect")
	}
}

// At promote the next CA is the cluster's: ca.crt holds it alone and the CA
// key on disk is its key, even while replicated state still names the old
// pair as the cluster's — the leader writes that after the record.
func TestAManagerDropsTheOldCAAtPromote(t *testing.T) {
	m, cur, next, certDir := caManager(t)
	setCARecord(t, m, cur, next, 3, CAPhasePromote)
	m.ApplyClusterCA()
	if got := diskPins(t, certDir); !slices.Equal(got, []string{next.Pin()}) {
		t.Fatalf("ca.crt holds %v", got)
	}
	if key, _ := os.ReadFile(filepath.Join(certDir, "ca.key")); !bytes.Equal(key, next.KeyPEM) {
		t.Fatal("ca.key is not the next CA's key")
	}
	if _, _, err := m.tls.IssueNodeCert("joiner", []string{"127.0.0.1"}, CertRoleManager); err != nil {
		t.Fatalf("the CA pair on disk does not sign: %v", err)
	}
}

// After a restart the FSM holds the last snapshot until the node catches up,
// and the record it shows then can be an older step's: a manager never goes
// back to one, before or after its own restart.
func TestAManagerNeverGoesBackToAnOlderStep(t *testing.T) {
	m, cur, next, certDir := caManager(t)
	setCARecord(t, m, cur, next, 3, CAPhasePromote)
	m.ApplyClusterCA()
	setCARecord(t, m, cur, next, 1, CAPhaseTrust)
	m.ApplyClusterCA()
	if got := diskPins(t, certDir); !slices.Equal(got, []string{next.Pin()}) {
		t.Fatalf("an older record put back %v", got)
	}
	restarted := &Manager{tls: NewTLSManager(certDir), nodeID: "node-1", config: m.config, raft: m.raft, hbReconnect: make(chan struct{}, 1)}
	restarted.ApplyClusterCA()
	if got := diskPins(t, certDir); !slices.Equal(got, []string{next.Pin()}) {
		t.Fatalf("after a restart an older record put back %v", got)
	}
}

// A record that names a CA whose key has not arrived is a step half
// replicated: nothing moves, and the floor stays where it was.
func TestAManagerWaitsForAStepHalfReplicated(t *testing.T) {
	m, cur, next, certDir := caManager(t)
	applyCmd(t, m.raft.fsm, Command{Type: CmdDeleteConfig, Key: configKeyCANextKey})
	setCARecord(t, m, cur, next, 1, CAPhaseTrust)
	m.ApplyClusterCA()
	if got := diskPins(t, certDir); !slices.Equal(got, []string{cur.Pin()}) {
		t.Fatalf("ca.crt holds %v", got)
	}
	if _, err := os.Stat(filepath.Join(m.config.DataDir, caRotationFloorFile)); err == nil {
		t.Fatal("the floor moved for a step not applied")
	}
}

// A manager reports that it follows a CA rotation, and what it trusts.
func TestAManagerReportsCARotation(t *testing.T) {
	m, _, _, _ := caManager(t)
	if !slices.Contains(m.heartbeatCapabilities(), CapCARotation) {
		t.Fatal("CapCARotation not reported")
	}
}

// Following a rotation says each step once, and why a step waits once while
// the reason stays the same — never a CA key, which only replicated state
// and ca.key hold.
func TestFollowingACARotationLogsNoKey(t *testing.T) {
	logs := captureLogs(t)
	m, cur, next, _ := caManager(t)
	setCARecord(t, m, cur, next, 1, CAPhaseTrust)
	m.ApplyClusterCA()
	m.ApplyClusterCA()
	setCARecord(t, m, cur, next, 2, CAPhaseReissue)
	m.ApplyClusterCA()
	applyCmd(t, m.raft.fsm, Command{Type: CmdDeleteConfig, Key: configKeyCANextKey})
	setCARecord(t, m, cur, next, 3, CAPhasePromote)
	m.ApplyClusterCA()
	m.ApplyClusterCA()
	out := logs.String()
	for _, line := range []string{"phase=trust seq=1", "phase=reissue seq=2"} {
		if n := strings.Count(out, "applied a CA rotation step"+`" component=cluster `+line); n != 1 {
			t.Errorf("%q logged %d times:\n%s", line, n, out)
		}
	}
	if n := strings.Count(out, "waiting for the rest of a CA rotation step"); n != 1 {
		t.Errorf("the wait logged %d times:\n%s", n, out)
	}
	for _, secret := range []string{"PRIVATE KEY", pemBody(cur.KeyPEM), pemBody(next.KeyPEM)} {
		if strings.Contains(out, secret) {
			t.Fatalf("the logs carry %q:\n%s", secret, out)
		}
	}
}

// pemBody is the base64 of a PEM block's first line of data, enough to find
// the block anywhere it was copied.
func pemBody(p []byte) string {
	return strings.Split(string(p), "\n")[1]
}
