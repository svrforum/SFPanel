package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TLSManager handles cluster mTLS certificate operations.
//
// Node certs are hot-reloaded on disk change (checked at most once per
// certReloadDebounce) so a rotation script can overwrite node.crt/key
// atomically without restarting the process. The CA pool is loaded once —
// rotating the cluster CA still requires a coordinated restart across all
// nodes because every peer must trust the new CA simultaneously.
type TLSManager struct {
	certDir string

	mu           sync.Mutex // guards cached fields below
	cachedCert   *tls.Certificate
	cachedMtime  time.Time
	lastStatTime time.Time
	cachedCAPool *x509.CertPool // loaded lazily, never invalidated
}

// certReloadDebounce caps how often getNodeCert stats the filesystem.
// Rotations become effective within this interval.
const certReloadDebounce = 1 * time.Minute

func NewTLSManager(certDir string) *TLSManager {
	return &TLSManager{certDir: certDir}
}

// InitCA generates a self-signed CA for the cluster.
func (t *TLSManager) InitCA(clusterName string) error {
	if err := os.MkdirAll(t.certDir, 0700); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"SFPanel Cluster"},
			CommonName:   fmt.Sprintf("SFPanel CA - %s", clusterName),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}

	if err := writePEM(filepath.Join(t.certDir, "ca.crt"), "CERTIFICATE", caCertDER); err != nil {
		return err
	}

	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	if err := writePEM(filepath.Join(t.certDir, "ca.key"), "EC PRIVATE KEY", caKeyDER); err != nil {
		return err
	}

	return nil
}

// IssueNodeCert creates a TLS certificate signed by the cluster CA.
func (t *TLSManager) IssueNodeCert(nodeID string, addresses []string) (certPEM, keyPEM []byte, err error) {
	caCert, caKey, err := t.loadCA()
	if err != nil {
		return nil, nil, fmt.Errorf("load CA: %w", err)
	}

	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate node key: %w", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"SFPanel Cluster"},
			CommonName:   nodeID,
		},
		NotBefore: time.Now(),
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

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &nodeKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create node cert: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(nodeKey)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
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
func (t *TLSManager) SaveCACert(caPEM []byte) error {
	if err := os.MkdirAll(t.certDir, 0700); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}
	return os.WriteFile(filepath.Join(t.certDir, "ca.crt"), caPEM, 0600)
}

// LoadCACert reads the CA certificate.
func (t *TLSManager) LoadCACert() ([]byte, error) {
	return os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
}

// loadCAPool returns the cluster CA pool, loaded once per process. CA
// rotation intentionally requires a coordinated restart.
func (t *TLSManager) loadCAPool() (*x509.CertPool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cachedCAPool != nil {
		return t.cachedCAPool, nil
	}
	caCertPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("load CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("CA cert PEM did not contain any usable certificates")
	}
	t.cachedCAPool = pool
	return pool, nil
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

	t.lastStatTime = now
	if t.cachedCert != nil && info.ModTime().Equal(t.cachedMtime) {
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
	t.cachedMtime = info.ModTime()
	return t.cachedCert, nil
}

// ServerTLSConfig builds a TLS config for the gRPC server.
//
// The returned config uses GetCertificate so each TLS handshake picks up
// the latest node cert on disk (bounded by certReloadDebounce). This lets
// operators rotate certs without restarting the process — write the new
// node.crt/node.key atomically and the next handshake presents them.
func (t *TLSManager) ServerTLSConfig() (*tls.Config, error) {
	pool, err := t.loadCAPool()
	if err != nil {
		return nil, err
	}
	// Validate the cert is loadable up front so callers get a useful error
	// at wire-up time instead of deep inside a handshake.
	if _, err := t.getNodeCert(); err != nil {
		return nil, err
	}
	return &tls.Config{
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return t.getNodeCert()
		},
		ClientCAs:  pool,
		ClientAuth: tls.VerifyClientCertIfGiven, // established nodes present certs (verified); joining nodes present none (allowed for PreFlight/Join)
		MinVersion: tls.VersionTLS13,
	}, nil
}

// RaftServerTLSConfig builds a TLS config for the Raft transport listener
// (grpc_port + 1). Same cert callbacks and CA pool as ServerTLSConfig, but
// client certificates are mandatory: the Raft port is only ever dialed by
// established cluster members (ClientTLSConfig always presents the node
// cert), and unlike the gRPC port there is no interceptor layer to gate
// cert-less connections — so the PreFlight/Join allowance baked into
// VerifyClientCertIfGiven must not apply here.
func (t *TLSManager) RaftServerTLSConfig() (*tls.Config, error) {
	cfg, err := t.ServerTLSConfig()
	if err != nil {
		return nil, err
	}
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	return cfg, nil
}

// ClientTLSConfig builds a TLS config for gRPC client connections.
// Uses GetClientCertificate for the same rotation semantics as the server.
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
// Written atomically (temp + rename), unlike SaveCACert: ensureCAKey can call
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
	defer os.Remove(tmpName) // no-op once the rename below consumes tmpName
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

func (t *TLSManager) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.key"))
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	return cert, key, nil
}

func writePEM(path, blockType string, data []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  blockType,
		Bytes: data,
	}), 0600)
}
