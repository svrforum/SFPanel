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
func (t *TLSManager) SaveNodeCert(certPEM, keyPEM []byte) error {
	if err := os.WriteFile(filepath.Join(t.certDir, "node.crt"), certPEM, 0644); err != nil {
		return fmt.Errorf("write node cert: %w", err)
	}
	if err := os.WriteFile(filepath.Join(t.certDir, "node.key"), keyPEM, 0600); err != nil {
		return fmt.Errorf("write node key: %w", err)
	}
	return nil
}

// SaveCACert writes the CA certificate (received from leader during join).
func (t *TLSManager) SaveCACert(caPEM []byte) error {
	if err := os.MkdirAll(t.certDir, 0700); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}
	return os.WriteFile(filepath.Join(t.certDir, "ca.crt"), caPEM, 0644)
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
