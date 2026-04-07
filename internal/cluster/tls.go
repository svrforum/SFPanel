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
	"time"
)

// TLSManager handles cluster mTLS certificate operations.
// TLS configs are cached after first load to avoid repeated disk reads.
type TLSManager struct {
	certDir        string
	cachedServer   *tls.Config
	cachedClient   *tls.Config
}

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

// ServerTLSConfig builds a TLS config for the gRPC server (cached after first call).
func (t *TLSManager) ServerTLSConfig() (*tls.Config, error) {
	if t.cachedServer != nil {
		return t.cachedServer, nil
	}
	cert, err := tls.LoadX509KeyPair(
		filepath.Join(t.certDir, "node.crt"),
		filepath.Join(t.certDir, "node.key"),
	)
	if err != nil {
		return nil, fmt.Errorf("load node cert: %w", err)
	}

	caCertPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("load CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCertPEM)

	t.cachedServer = &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS13,
	}
	return t.cachedServer, nil
}

// ClientTLSConfig builds a TLS config for gRPC client connections (cached after first call).
func (t *TLSManager) ClientTLSConfig() (*tls.Config, error) {
	if t.cachedClient != nil {
		return t.cachedClient, nil
	}
	cert, err := tls.LoadX509KeyPair(
		filepath.Join(t.certDir, "node.crt"),
		filepath.Join(t.certDir, "node.key"),
	)
	if err != nil {
		return nil, fmt.Errorf("load node cert: %w", err)
	}

	caCertPEM, err := os.ReadFile(filepath.Join(t.certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("load CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCertPEM)

	t.cachedClient = &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}
	return t.cachedClient, nil
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
