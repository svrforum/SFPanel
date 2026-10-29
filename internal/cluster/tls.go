package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TLSManager handles cluster mTLS certificate operations.
//
// Node certs are hot-reloaded on disk change (checked at most once per
// certReloadDebounce) so a rotation script can overwrite node.crt/key
// atomically without restarting the process. The CAs it trusts are ca.crt's,
// read again by ReloadCAPool: a CA rotation (ca_rotation.go) changes them on a
// running node, and the next handshake of every listener and dialer uses them.
type TLSManager struct {
	certDir string

	mu           sync.Mutex // guards cached fields below
	cachedCert   *tls.Certificate
	cachedMtime  time.Time
	lastStatTime time.Time
	// caPool and caCerts are the CAs this node trusts — every certificate in
	// ca.crt, in the file's order — loaded at first use and again by
	// ReloadCAPool; every handshake reads them anew (serverConfig, liveTLS),
	// so a CA rotation takes effect without a restart. Both are replaced
	// whole, never changed in place: a handshake may still hold the old pool,
	// and a CertPool has no lock of its own.
	caPool  *x509.CertPool
	caCerts []*x509.Certificate
}

// certReloadDebounce caps how often getNodeCert stats the filesystem.
// Rotations become effective within this interval.
const certReloadDebounce = 1 * time.Minute

func NewTLSManager(certDir string) *TLSManager {
	return &TLSManager{certDir: certDir}
}

// certBackdate is how far before its issue a certificate is valid. Peers'
// clocks run apart — the leader warns at twenty seconds (clockSkewWarn) — and
// a certificate stamped "now" is refused, until the peer's clock catches up,
// by a peer whose clock runs behind; a rotation issues every certificate in
// the cluster within minutes.
const certBackdate = time.Hour

// CAPair is a CA certificate and its private key, parsed and checked to
// belong together.
type CAPair struct {
	Cert            *x509.Certificate
	Key             *ecdsa.PrivateKey
	CertPEM, KeyPEM []byte
}

// Pin is the CA's fingerprint, the one join commands carry (CAPin).
func (p *CAPair) Pin() string { return CAPin(p.Cert.RawSubjectPublicKeyInfo) }

// GenerateCA makes a cluster CA in memory: ECDSA P-256, a random 128-bit
// serial, valid from certBackdate before now for ten years. cn names it; a
// rotation's next CA carries the date, so that it and the one it replaces
// read apart in logs and in tools.
func GenerateCA(cn string, now time.Time) (*CAPair, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate CA serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"SFPanel Cluster"}, CommonName: cn},
		NotBefore:             now.Add(-certBackdate),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	return ParseCAPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// ParseCAPair parses a CA certificate and its key — the first certificate in
// certPEM, which may go on to hold other CAs, and the first PEM block of
// keyPEM — and checks the key is the certificate's. CertPEM and KeyPEM are
// those two blocks alone.
func ParseCAPair(certPEM, keyPEM []byte) (*CAPair, error) {
	var certBlock *pem.Block
	for rest := certPEM; certBlock == nil; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errors.New("no certificate in the CA cert PEM")
		}
		if block.Type == "CERTIFICATE" {
			certBlock = block
		}
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("the CA cert is not a CA certificate")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.New("failed to decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, ErrCAKeyMismatch
	}
	return &CAPair{
		Cert:    cert,
		Key:     key,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBlock.Bytes}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBlock.Bytes}),
	}, nil
}

// InitCA generates a self-signed CA for the cluster and writes it to ca.crt
// and ca.key.
func (t *TLSManager) InitCA(clusterName string) error {
	if err := os.MkdirAll(t.certDir, 0700); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}

	ca, err := GenerateCA(fmt.Sprintf("SFPanel CA - %s", clusterName), time.Now())
	if err != nil {
		return err
	}

	if err := writePEM(filepath.Join(t.certDir, "ca.crt"), "CERTIFICATE", ca.Cert.Raw); err != nil {
		return err
	}

	caKeyDER, err := x509.MarshalECPrivateKey(ca.Key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	if err := writePEM(filepath.Join(t.certDir, "ca.key"), "EC PRIVATE KEY", caKeyDER); err != nil {
		return err
	}

	return nil
}

// Certificate roles, carried in the node certificate's Subject
// OrganizationalUnit. The role lets a peer refuse an agent where only a
// manager belongs — the Raft port, the gRPC proxy — without the FSM, which an
// agent does not have.
const (
	CertRoleManager = "sfpanel-manager"
	CertRoleAgent   = "sfpanel-agent"
)

// CertRole returns the role a node certificate carries, or "" for one issued
// before roles existed (a manager's). A certificate that names both is held
// to the lesser role.
func CertRole(cert *x509.Certificate) string {
	role := ""
	for _, ou := range cert.Subject.OrganizationalUnit {
		switch ou {
		case CertRoleAgent:
			return CertRoleAgent
		case CertRoleManager:
			role = CertRoleManager
		}
	}
	return role
}

// IssueNodeCert is IssueNodeCertBy with the cluster CA on this node's disk.
func (t *TLSManager) IssueNodeCert(nodeID string, addresses []string, role string) (certPEM, keyPEM []byte, err error) {
	ca, err := t.diskCA()
	if err != nil {
		return nil, nil, err
	}
	return IssueNodeCertBy(ca, nodeID, addresses, role)
}

// IssueNodeCertBy creates a key and a TLS certificate for it, signed by ca,
// for a node in the given role (CertRoleManager or CertRoleAgent).
func IssueNodeCertBy(ca *CAPair, nodeID string, addresses []string, role string) (certPEM, keyPEM []byte, err error) {
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate node key: %w", err)
	}
	certPEM, err = signNodeCertBy(ca, nodeID, addresses, role, &nodeKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	// This error used to be discarded: a nil keyDER produces a syntactically
	// valid but empty PEM block that gets written to node.key and only fails
	// much later, mid-handshake, as an unexplained TLS error.
	keyDER, err := x509.MarshalECPrivateKey(nodeKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal node key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// ReissueNodeCert is ReissueNodeCertBy with the cluster CA on this node's
// disk.
func (t *TLSManager) ReissueNodeCert(nodeID string, addresses []string, role string) error {
	ca, err := t.diskCA()
	if err != nil {
		return err
	}
	return t.ReissueNodeCertBy(ca, nodeID, addresses, role)
}

// ReissueNodeCertBy re-signs this node's certificate with ca, for role,
// keeping its key. node.key is not touched and node.crt is replaced in one
// rename, its data synced first, so a crash, or the one-minute reload in
// getNodeCert, sees the old certificate or the new one — each matching the
// key on disk. A CA rotation relies on the file it leaves when the node next
// boots.
func (t *TLSManager) ReissueNodeCertBy(ca *CAPair, nodeID string, addresses []string, role string) error {
	keyPEM, err := os.ReadFile(filepath.Join(t.certDir, "node.key"))
	if err != nil {
		return fmt.Errorf("read node key: %w", err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return errors.New("node key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse node key: %w", err)
	}
	certPEM, err := signNodeCertBy(ca, nodeID, addresses, role, &key.PublicKey)
	if err != nil {
		return err
	}
	return replaceFile(filepath.Join(t.certDir, "node.crt"), certPEM)
}

// SignNodeCSR is SignNodeCSRBy with the cluster CA on this node's disk.
func (t *TLSManager) SignNodeCSR(nodeID string, addresses []string, role string, csrDER []byte) ([]byte, error) {
	ca, err := t.diskCA()
	if err != nil {
		return nil, err
	}
	return SignNodeCSRBy(ca, nodeID, addresses, role, csrDER)
}

// SignNodeCSRBy signs, with ca, a node certificate for the key in a
// certificate request. Only the key comes from the request: the leader sets
// CN, role and SANs, so a joining node cannot ask for a manager's name or
// another node's address.
func SignNodeCSRBy(ca *CAPair, nodeID string, addresses []string, role string, csrDER []byte) ([]byte, error) {
	pub, err := csrPublicKey(csrDER)
	if err != nil {
		return nil, err
	}
	return signNodeCertBy(ca, nodeID, addresses, role, pub)
}

// csrPublicKey returns the key a certificate request asks a certificate for,
// or ErrBadCSR. The request must be signed by that key — the requester holds
// it — and the key must be ECDSA P-256, what every other node certificate in
// the cluster carries (IssueNodeCert).
func csrPublicKey(csrDER []byte) (*ecdsa.PublicKey, error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, ErrBadCSR
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, ErrBadCSR
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, ErrBadCSR
	}
	return pub, nil
}

// signNodeCertBy signs a node certificate for pub with ca.
func signNodeCertBy(ca *CAPair, nodeID string, addresses []string, role string, pub *ecdsa.PublicKey) ([]byte, error) {
	if role != CertRoleManager && role != CertRoleAgent {
		return nil, fmt.Errorf("unknown certificate role %q", role)
	}

	// This error used to be discarded: a nil serial turns into an opaque x509
	// failure at signing time.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization:       []string{"SFPanel Cluster"},
			OrganizationalUnit: []string{role},
			CommonName:         nodeID,
		},
		NotBefore: time.Now().Add(-certBackdate),
		NotAfter:  time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
	}

	for _, addr := range addresses {
		if ip := net.ParseIP(addr); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, addr)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, pub, ca.Key)
	if err != nil {
		return nil, fmt.Errorf("create node cert: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), nil
}

// SaveNodeCert writes cert/key files for this node.
//
// Both files are written 0600. The cert is technically public material in
// mTLS, but writing it 0644 lets any local user read the cluster topology
// (subject CN includes node ID, SANs include node IP). Locking down to root
// matches the key, the data dir, and the config dir — no part of the cluster
// trust material should be visible to non-root processes on the host.
func (t *TLSManager) SaveNodeCert(certPEM, keyPEM []byte) error {
	if err := os.WriteFile(filepath.Join(t.certDir, "node.crt"), certPEM, 0600); err != nil {
		return fmt.Errorf("write node cert: %w", err)
	}
	if err := os.WriteFile(filepath.Join(t.certDir, "node.key"), keyPEM, 0600); err != nil {
		return fmt.Errorf("write node key: %w", err)
	}
	return nil
}

// SaveCACert writes the CA certificate (received from leader during join).
//
// 0600 to match SaveNodeCert and InitCA: the internal proxy secret is derived
// as sha256(ca.crt), so a world-readable CA lets any non-root local process
// recompute the secret and forge X-SFPanel-Internal-Proxy headers. Keep all
// cluster trust material root-only.
//
// Through a temporary file, synced, and a rename: a CA rotation rewrites it
// on a running node, whose handshakes, pins and signatures read it, and a
// boot stops on a truncated one.
func (t *TLSManager) SaveCACert(caPEM []byte) error {
	return replaceFile(filepath.Join(t.certDir, "ca.crt"), caPEM)
}

// LoadCACert reads the CA certificate.
func (t *TLSManager) LoadCACert() ([]byte, error) {
	return os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
}

// NodeCertLeaf parses the node certificate on disk.
func (t *TLSManager) NodeCertLeaf() (*x509.Certificate, error) {
	data, err := os.ReadFile(filepath.Join(t.certDir, "node.crt"))
	if err != nil {
		return nil, fmt.Errorf("read node cert: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("node cert is not PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// loadCAPool returns the pool of the CAs this node trusts, reading ca.crt at
// first use; ReloadCAPool reads it again.
func (t *TLSManager) loadCAPool() (*x509.CertPool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.caPool == nil {
		if err := t.readCAPoolLocked(); err != nil {
			return nil, err
		}
	}
	return t.caPool, nil
}

// ReloadCAPool reads ca.crt again, for the next handshake of every listener
// and dialer. A ca.crt that cannot be read, or holds no certificate, is an
// error and the trust stays as it was: a node that trusted nothing would
// reach no peer, and so hear of no fix.
func (t *TLSManager) ReloadCAPool() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.readCAPoolLocked()
}

// readCAPoolLocked replaces caPool and caCerts with ca.crt's certificates,
// taken as leniently as x509.CertPool.AppendCertsFromPEM always took them
// here (pemCertificates), and leaves both as they were when it fails. t.mu
// is held.
func (t *TLSManager) readCAPoolLocked() error {
	caCertPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
	if err != nil {
		return fmt.Errorf("load CA cert: %w", err)
	}
	certs := pemCertificates(caCertPEM)
	if len(certs) == 0 {
		return fmt.Errorf("CA cert PEM did not contain any usable certificates")
	}
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	t.caPool, t.caCerts = pool, certs
	return nil
}

// trustedCAs is caCerts, read at first use like the pool; nil when ca.crt
// cannot be read.
func (t *TLSManager) trustedCAs() []*x509.Certificate {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.caPool == nil && t.readCAPoolLocked() != nil {
		return nil
	}
	return t.caCerts
}

// TrustPins is the CAPin of each CA this node trusts, in ca.crt's order.
func (t *TLSManager) TrustPins() []string {
	cas := t.trustedCAs()
	pins := make([]string, 0, len(cas))
	for _, ca := range cas {
		pins = append(pins, CAPin(ca.RawSubjectPublicKeyInfo))
	}
	return pins
}

// ServedCertCAPin is the CAPin of the trusted CA that signed the certificate
// this node presents now, "" if none did.
func (t *TLSManager) ServedCertCAPin() string {
	cert, err := t.getNodeCert()
	if err != nil || cert.Leaf == nil {
		return ""
	}
	for _, ca := range t.trustedCAs() {
		if cert.Leaf.CheckSignatureFrom(ca) == nil {
			return CAPin(ca.RawSubjectPublicKeyInfo)
		}
	}
	return ""
}

// getNodeCert returns the current node cert, reloading from disk when the
// file mtime changes. The os.Stat call is debounced to certReloadDebounce
// so handshake-heavy paths don't hammer the filesystem.
func (t *TLSManager) getNodeCert() (*tls.Certificate, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	certPath := filepath.Join(t.certDir, "node.crt")
	keyPath := filepath.Join(t.certDir, "node.key")

	now := time.Now()
	if t.cachedCert != nil && now.Sub(t.lastStatTime) < certReloadDebounce {
		return t.cachedCert, nil
	}

	info, err := os.Stat(certPath)
	if err != nil {
		if t.cachedCert != nil {
			// Transient stat failure — keep serving the cached cert.
			t.lastStatTime = now
			return t.cachedCert, nil
		}
		return nil, fmt.Errorf("stat node cert: %w", err)
	}

	// Watch BOTH files. Keying the reload on node.crt alone left a blind spot:
	// a rotation that rewrote only the key — including this function's own
	// half-written-rotation path below, which serves the stale pair when the
	// key lands after the cert — was never noticed again, because the cert's
	// mtime had already been recorded. The pair stayed stale until a restart.
	newest := info.ModTime()
	if keyInfo, keyErr := os.Stat(keyPath); keyErr == nil && keyInfo.ModTime().After(newest) {
		newest = keyInfo.ModTime()
	}

	t.lastStatTime = now
	if t.cachedCert != nil && newest.Equal(t.cachedMtime) {
		return t.cachedCert, nil
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		if t.cachedCert != nil {
			// Half-written rotation (cert updated, key not yet).
			// Keep the previously-valid cert rather than break connectivity.
			return t.cachedCert, nil
		}
		return nil, fmt.Errorf("load node cert: %w", err)
	}
	t.cachedCert = &cert
	t.cachedMtime = newest
	return t.cachedCert, nil
}

// ReloadNodeCert loads node.crt and node.key for the next handshake now,
// past certReloadDebounce: a node that has just moved its certificate to
// another CA presents it at once, and the leader goes by what each node
// presents. A pair that does not load is an error, and the certificate
// presented stays as it was.
func (t *TLSManager) ReloadNodeCert() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	certPath := filepath.Join(t.certDir, "node.crt")
	keyPath := filepath.Join(t.certDir, "node.key")

	// Stat before loading, as getNodeCert does: a write in between then
	// shows as a newer mtime at its next stat rather than hiding behind this
	// one.
	info, err := os.Stat(certPath)
	if err != nil {
		return fmt.Errorf("stat node cert: %w", err)
	}
	newest := info.ModTime()
	if keyInfo, keyErr := os.Stat(keyPath); keyErr == nil && keyInfo.ModTime().After(newest) {
		newest = keyInfo.ModTime()
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("load node cert: %w", err)
	}
	t.cachedCert = &cert
	t.cachedMtime = newest
	t.lastStatTime = time.Now()
	return nil
}

// serverConfig is a listener's TLS config. The CAs it verifies clients
// against are the ones this node trusts at each handshake
// (GetConfigForClient), so a CA rotation reaches a running listener; the
// listener's own client-auth rule and check stay on every handshake. The
// config returned holds the same values for callers that read them; a change
// made to it reaches no handshake, which is why each listener's rule is a
// parameter here rather than set on the result.
func (t *TLSManager) serverConfig(clientAuth tls.ClientAuthType, verify func([][]byte, [][]*x509.Certificate) error) (*tls.Config, error) {
	if _, err := t.loadCAPool(); err != nil {
		return nil, err
	}
	// Validate the cert is loadable up front so callers get a useful error
	// at wire-up time instead of deep inside a handshake.
	if _, err := t.getNodeCert(); err != nil {
		return nil, err
	}
	build := func(*tls.ClientHelloInfo) (*tls.Config, error) {
		pool, err := t.loadCAPool()
		if err != nil {
			return nil, err
		}
		return &tls.Config{
			GetCertificate:        func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return t.getNodeCert() },
			ClientCAs:             pool,
			ClientAuth:            clientAuth,
			VerifyPeerCertificate: verify,
			MinVersion:            tls.VersionTLS13,
		}, nil
	}
	// The pool is loaded by now and never unloaded, so this build cannot fail.
	cfg, _ := build(nil)
	cfg.GetConfigForClient = build
	return cfg, nil
}

// ServerTLSConfig builds a TLS config for the gRPC server.
//
// The returned config uses GetCertificate so each TLS handshake picks up
// the latest node cert on disk (bounded by certReloadDebounce). This lets
// operators rotate certs without restarting the process — write the new
// node.crt/node.key atomically and the next handshake presents them.
func (t *TLSManager) ServerTLSConfig() (*tls.Config, error) {
	// Established nodes present certs (verified); joining nodes present none
	// (allowed for PreFlight/Join).
	return t.serverConfig(tls.VerifyClientCertIfGiven, nil)
}

// AgentServerTLSConfig builds the TLS config for an agent's gRPC listener:
// ServerTLSConfig's, with a client certificate required. A manager's listener
// lets a caller without a certificate reach PreFlight, Join and
// RegisterAgent. An agent serves none of them, so its handshake refuses such
// a caller before any method is looked at.
func (t *TLSManager) AgentServerTLSConfig() (*tls.Config, error) {
	return t.serverConfig(tls.RequireAndVerifyClientCert, nil)
}

// RaftServerTLSConfig builds a TLS config for the Raft transport listener
// (grpc_port + 1). Same cert callbacks and CA pool as ServerTLSConfig, but
// client certificates are mandatory: the Raft port is only ever dialed by
// established cluster members (ClientTLSConfig always presents the node
// cert), and unlike the gRPC port there is no interceptor layer to gate
// cert-less connections — so the PreFlight/Join allowance baked into
// VerifyClientCertIfGiven must not apply here.
func (t *TLSManager) RaftServerTLSConfig() (*tls.Config, error) {
	return t.serverConfig(tls.RequireAndVerifyClientCert, refuseAgentChains)
}

// refuseAgentChains is the Raft listener's check. Chains are verified against
// the cluster CA by now. An agent's certificate is signed by the same CA, and
// the Raft transport does not authenticate which member speaks: an agent let
// in here could claim leadership with a higher term.
func refuseAgentChains(_ [][]byte, chains [][]*x509.Certificate) error {
	if len(chains) == 0 || len(chains[0]) == 0 {
		return errors.New("no verified client certificate")
	}
	if CertRole(chains[0][0]) == CertRoleAgent {
		return errors.New("an agent certificate may not use the Raft transport")
	}
	return nil
}

// ClientTLSConfig builds a TLS config for gRPC client connections.
// Uses GetClientCertificate for the same rotation semantics as the server.
// It verifies servers against the CAs trusted when it is built, so a dialer
// that reconnects builds it again for each connection (liveTLS,
// tlsStreamLayer).
func (t *TLSManager) ClientTLSConfig() (*tls.Config, error) {
	pool, err := t.loadCAPool()
	if err != nil {
		return nil, err
	}
	if _, err := t.getNodeCert(); err != nil {
		return nil, err
	}
	return &tls.Config{
		GetClientCertificate: func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return t.getNodeCert()
		},
		RootCAs:    pool,
		MinVersion: tls.VersionTLS13,
	}, nil
}

// HasCA checks if CA certificate exists.
func (t *TLSManager) HasCA() bool {
	_, err := os.Stat(filepath.Join(t.certDir, "ca.crt"))
	return err == nil
}

// HasCAKey reports whether the CA private key is present on disk. Distinct from
// HasCA, which checks only the public ca.crt: a node can hold ca.crt — enough to
// bring up mTLS and serve joins — while lacking the ca.key needed to SIGN a
// joining node's cert. That asymmetry is exactly issue #5 (a leader that never
// founded the CA, or whose ca.key was deleted while ca.crt survived).
func (t *TLSManager) HasCAKey() bool {
	_, err := os.Stat(filepath.Join(t.certDir, "ca.key"))
	return err == nil
}

// SaveCAKey writes the CA private key to disk (0600), materializing it on a node
// that received the key via the Raft FSM rather than by founding the CA. 0600
// perms match InitCA/SaveNodeCert — the CA key is the cluster's root signing
// material and must never be readable by non-root processes.
//
// Written atomically (temp + rename): ensureCAKey can call
// this concurrently from parallel HandleJoin goroutines on a freshly-promoted
// leader, and loadCA reads ca.key without a lock. A plain truncating write would
// let a concurrent reader observe a half-written key and fail with a spurious
// PEM-decode error; the rename makes every read see either the old file or the
// complete new one.
func (t *TLSManager) SaveCAKey(keyPEM []byte) error {
	if err := os.MkdirAll(t.certDir, 0700); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}
	// Unique temp name per call: two concurrent SaveCAKey callers must not share
	// a fixed temp path, or one's rename races ahead and the other hits ENOENT.
	// os.CreateTemp creates the file 0600 — the perms the CA key requires.
	tmp, err := os.CreateTemp(t.certDir, "ca.key.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp CA key: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename below consumes tmpName
	if _, err := tmp.Write(keyPEM); err != nil {
		tmp.Close()
		return fmt.Errorf("write CA key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp CA key: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(t.certDir, "ca.key")); err != nil {
		return fmt.Errorf("rename CA key: %w", err)
	}
	return nil
}

// LoadCAKey reads the CA private key PEM from disk, used to replicate the key
// into the cluster FSM. Returns the raw PEM bytes as written by InitCA/SaveCAKey.
func (t *TLSManager) LoadCAKey() ([]byte, error) {
	return os.ReadFile(filepath.Join(t.certDir, "ca.key"))
}

var errEmptyAgentKey = errors.New("the agent key is empty")

// SaveAgentKey writes an agent's own internal-proxy key — the one its
// managers sign their requests to it with — to agent.key, 0600. Like
// SaveCAKey it replaces the file through a temporary one of its own.
func (t *TLSManager) SaveAgentKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errEmptyAgentKey
	}
	return replaceFile(filepath.Join(t.certDir, "agent.key"), []byte(key))
}

// LoadAgentKey reads the agent's internal-proxy key. An empty one is an
// error, not a key: the agent would come up refusing every request its
// managers send.
func (t *TLSManager) LoadAgentKey() (string, error) {
	path := filepath.Join(t.certDir, "agent.key")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read agent key: %w", err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("%s: %w", path, errEmptyAgentKey)
	}
	return key, nil
}

// diskCA is the CA pair on this node's disk: the first certificate in ca.crt
// and the key in ca.key. A key that is not that certificate's is refused
// here (ErrCAKeyMismatch), before anything is signed with it.
func (t *TLSManager) diskCA() (*CAPair, error) {
	certPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.key"))
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	ca, err := ParseCAPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	return ca, nil
}

// loadCA is diskCA's certificate and key.
func (t *TLSManager) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	ca, err := t.diskCA()
	if err != nil {
		return nil, nil, err
	}
	return ca.Cert, ca.Key, nil
}

// certificatesIn parses a PEM bundle that must hold certificates and nothing
// else, at least one: a bundle another node sends is refused whole, not
// trusted in part. A block pemCertificates would skip — not a certificate,
// with headers, or not parsing — is an error here, so a bundle this accepts
// is trusted whole once written to ca.crt.
func certificatesIn(pemBytes []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for len(pemBytes) > 0 {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("the bundle holds a PEM block that is not a certificate")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate in the bundle: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("the bundle holds no certificate")
	}
	return certs, nil
}

// pemCertificates returns the certificates in a PEM bundle the way
// x509.CertPool.AppendCertsFromPEM takes them: a block that is not a
// certificate, has headers or does not parse is skipped. It is for this
// node's own ca.crt, which the CA pool (loadCAPool) has always read that way.
func pemCertificates(pemBytes []byte) []*x509.Certificate {
	var certs []*x509.Certificate
	for len(pemBytes) > 0 {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		certs = append(certs, cert)
	}
	return certs
}

func writePEM(path, blockType string, data []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  blockType,
		Bytes: data,
	}), 0600)
}
