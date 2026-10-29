package cluster

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// An agent's half of a CA rotation's certificates. A manager moves its own
// certificate from replicated state (ApplyClusterCA); an agent has none, and
// asks the leader over the mTLS it has (RenewCertificate) once a pong's hint
// names a CA its certificate is not from.
const (
	// renewalInterval is how often an agent's certificate may be renewed:
	// the leader takes one renewal per agent in it, and an agent asks no
	// more often.
	renewalInterval = time.Minute
	// maxCABundleBytes bounds a pong's CA bundle, a few CA certificates.
	maxCABundleBytes = 64 << 10
)

// renewalProof binds a renewal to the agent's own key, which only the agent
// and the managers hold: a certificate from the CA a rotation replaces is
// not enough to move to the next one — whoever holds that CA's key could
// make one for any agent's ID, and would keep a valid identity past the
// rotation meant to end it.
func renewalProof(agentKey, nodeID string, csr []byte) string {
	sum := sha256.Sum256(csr)
	mac := hmac.New(sha256.New, []byte(agentKey))
	fmt.Fprintf(mac, "renew-certificate\n%s\n%x", nodeID, sum)
	return hex.EncodeToString(mac.Sum(nil))
}

// HandleRenewCertificate signs the agent whose verified certificate is peer
// a certificate from the CA a CA rotation signs with now, and answers with
// the CAs to trust (leader only). The agent's word is taken for its key
// alone, and the request must meet all of these:
//
//   - the request is for the key of the certificate the agent presents now,
//     so the agent replaces node.crt alone, in one rename. A new key would
//     have the pair swapped as one, and an agent whose pair fails to load has
//     no standalone panel to boot into;
//   - it carries a proof made with the agent's own key (renewalProof). The
//     certificate is not enough: whoever holds a leaked key of the CA being
//     replaced could make one for any agent's ID and take a certificate from
//     the next CA with it;
//   - the certificate names the ID, the agent role and the IP of the agent's
//     record in replicated state, which managers dial;
//   - it is signed only while the rotation moves certificates — reissue, or
//     rollback on the way back — and at most once per renewalInterval per
//     agent: between rotations no signer stands open to agents.
//
// A leader whose FSM has not caught up in its term signs nothing
// (ErrNotLeader): it may hold a step the cluster has moved past, and an agent
// that took a certificate from a CA the cluster dropped would be cut off. A
// refusal changes nothing.
func (m *Manager) HandleRenewCertificate(peer *x509.Certificate, csr []byte, proof string, now time.Time) (certPEM, bundle []byte, err error) {
	if m.raft == nil || !m.raft.IsLeader() || !m.raft.caughtUpLeader(2*time.Second) {
		return nil, nil, ErrNotLeader
	}
	if peer == nil {
		return nil, nil, errors.New("a renewal needs the agent's client certificate")
	}
	id := peer.Subject.CommonName
	state := m.raft.GetFSM().GetState()
	node := state.Nodes[id]
	if node == nil || node.IsManager() {
		return nil, nil, fmt.Errorf("%s is not an agent of this cluster: only an agent renews its certificate this way", id)
	}
	rec, ok, err := caRotationOf(state)
	if err != nil {
		return nil, nil, err
	}
	if !ok || (rec.Phase != CAPhaseReissue && rec.Phase != CAPhaseRollback) {
		return nil, nil, ErrNoRenewalDue
	}
	// Held through the signature: two renewals of one agent at once must not
	// both find the last one a minute old.
	m.renewMu.Lock()
	defer m.renewMu.Unlock()
	if at, ok := m.renewedAt[id]; ok && now.Sub(at) < renewalInterval {
		return nil, nil, ErrRenewalTooSoon
	}
	// With no key there is nothing to prove: renewalProof("", …) is anyone's.
	agentKey := state.Config[AgentKeyConfigKey(id)]
	if agentKey == "" || !hmac.Equal([]byte(proof), []byte(renewalProof(agentKey, id, csr))) {
		return nil, nil, ErrRenewalProof
	}
	pub, err := csrPublicKey(csr)
	if err != nil {
		return nil, nil, err
	}
	if !pub.Equal(peer.PublicKey) {
		return nil, nil, ErrRenewalKey
	}
	ip, err := agentIP(node.APIAddress, node.GRPCAddress)
	if err != nil {
		return nil, nil, err
	}
	mat, err := caMaterialOf(state)
	if err != nil {
		return nil, nil, err
	}
	certPEM, err = SignNodeCSRBy(mat.Sign, id, []string{ip.String()}, CertRoleAgent, csr)
	if err != nil {
		return nil, nil, err
	}
	if m.renewedAt == nil {
		m.renewedAt = map[string]time.Time{}
	}
	m.renewedAt[id] = now
	slog.Info("renewed an agent's certificate for a CA rotation", "component", "cluster", "node_id", id, "phase", rec.Phase)
	return certPEM, mat.TrustPEM(), nil
}

// installRenewedCert makes certPEM this node's certificate once it is this
// node's in all the leader does not get to choose: for this node's key, its
// ID and the agent role, naming ip — where its managers dial it — and signed
// by a CA this node trusts now. It is presented at once.
//
// node.key stays: replacing one file in one rename leaves, after any crash, a
// pair that loads — an agent has no standalone panel to boot into, and a pair
// that does not load stops every boot until someone repairs it by hand.
func (t *TLSManager) installRenewedCert(certPEM []byte, nodeID, ip string) error {
	leaf, err := parseCertificatePEM(certPEM)
	if err != nil {
		return err
	}
	key, err := t.nodeKey()
	if err != nil {
		return err
	}
	if !key.PublicKey.Equal(leaf.PublicKey) {
		return errors.New("the renewed certificate is not for this node's key")
	}
	if leaf.Subject.CommonName != nodeID || CertRole(leaf) != CertRoleAgent {
		return fmt.Errorf("the renewed certificate is for %s in role %q, not this agent", leaf.Subject.CommonName, CertRole(leaf))
	}
	want := net.ParseIP(ip)
	if want == nil || !slices.ContainsFunc(leaf.IPAddresses, want.Equal) {
		return fmt.Errorf("the renewed certificate does not name %q, the address this agent registered at", ip)
	}
	if t.trustedIssuerPin(leaf) == "" {
		return errors.New("the renewed certificate is not signed by a CA this node trusts")
	}
	if err := replaceFile(filepath.Join(t.certDir, "node.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})); err != nil {
		return err
	}
	return t.ReloadNodeCert()
}

// nodeKey is this node's private key, node.key.
func (t *TLSManager) nodeKey() (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(filepath.Join(t.certDir, "node.key"))
	if err != nil {
		return nil, fmt.Errorf("read node key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("node key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse node key: %w", err)
	}
	return key, nil
}

// trustedIssuerPin is the CAPin of the CA this node trusts that signed cert,
// "" if none did.
func (t *TLSManager) trustedIssuerPin(cert *x509.Certificate) string {
	for _, ca := range t.trustedCAs() {
		if cert.CheckSignatureFrom(ca) == nil {
			return CAPin(ca.RawSubjectPublicKeyInfo)
		}
	}
	return ""
}

// parseCertificatePEM parses the certificate certPEM starts with.
func parseCertificatePEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("the certificate is not PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}
