package paneltls

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/config"
)

// An agent's panel must serve HTTPS (managers relay to it with its key), so a
// join turns managed TLS on when it is off; operator certificates need the
// CA file managers will trust.
func TestPrepareForAgent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.TLS.Dir = t.TempDir()
	ca, changed, err := PrepareForAgent(cfg)
	if err != nil || !changed || !cfg.Server.TLS.Enabled || !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Fatalf("managed TLS: changed=%v err=%v", changed, err)
	}
	if want, err := New(cfg.Server.TLS.Dir).CACertPEM(); err != nil || !bytes.Equal(ca, want) {
		t.Fatalf("not the CA behind the panel's certificate (%v)", err)
	}
	op := &config.Config{}
	op.Server.TLS.Enabled = true
	op.Server.TLS.CertFile, op.Server.TLS.KeyFile = "/x.crt", "/x.key"
	if _, _, err := PrepareForAgent(op); err == nil || !strings.Contains(err.Error(), "ca_file") {
		t.Fatalf("operator certificate without ca_file: %v", err)
	}
}

// install.sh writes tls.enabled before the panel first starts, so a join may
// find TLS on and no certificate yet: the CA is made then, and the config
// needs no change.
func TestPrepareForAgentBeforeTheFirstStart(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.TLS = config.TLSConfig{Enabled: true, Dir: filepath.Join(t.TempDir(), "tls")}
	ca, changed, err := PrepareForAgent(cfg)
	if err != nil || changed || !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

// An operator's certificate stays the panel's: the CA its managers trust is
// ca_file, and a pair the panel could not start with is refused before the
// join — the agent would register and then fail every boot.
func TestPrepareForAgentWithAnOperatorCertificate(t *testing.T) {
	dir := t.TempDir()
	m := New(filepath.Join(dir, "operator"))
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	want, err := m.CACertPEM()
	if err != nil {
		t.Fatal(err)
	}
	managedDir := filepath.Join(dir, "managed")
	cfg := &config.Config{}
	cfg.Server.TLS = config.TLSConfig{Dir: managedDir, CertFile: m.ServerCertPath(), KeyFile: m.ServerKeyPath(), CAFile: m.CACertPath()}
	ca, changed, err := PrepareForAgent(cfg)
	if err != nil || !changed || !cfg.Server.TLS.Enabled || !bytes.Equal(ca, want) {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(managedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("made a managed certificate beside the operator's")
	}
	cfg.Server.TLS.KeyFile = filepath.Join(dir, "missing.key")
	if _, _, err := PrepareForAgent(cfg); err == nil || !strings.Contains(err.Error(), "server.tls.key_file") {
		t.Fatalf("a key file that is not there: %v", err)
	}
	other := New(filepath.Join(dir, "other"))
	if _, err := other.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg.Server.TLS.KeyFile = other.ServerKeyPath()
	if _, _, err := PrepareForAgent(cfg); err == nil || !strings.Contains(err.Error(), "do not load as a pair") {
		t.Fatalf("a key that is not the certificate's: %v", err)
	}
}

// The agent is reached at the advertise IP by its managers and at 127.0.0.1
// by its own gRPC hop, both verifying the panel's certificate.
func TestCertCheckWantsLoopbackAndTheAdvertiseIP(t *testing.T) {
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Operator CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	leafFor := func(ips ...string) *config.Config {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "panel"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		for _, ip := range ips {
			tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
		}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		sub := t.TempDir()
		write := func(name string, typ string, b []byte) string {
			p := filepath.Join(sub, name)
			_ = os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600)
			return p
		}
		keyDER, _ := x509.MarshalECPrivateKey(key)
		cfg := &config.Config{}
		cfg.Server.TLS = config.TLSConfig{Enabled: true, Dir: dir, CertFile: write("panel.crt", "CERTIFICATE", der), KeyFile: write("panel.key", "EC PRIVATE KEY", keyDER), CAFile: write("ca.crt", "CERTIFICATE", caDER)}
		return cfg
	}
	if err := CertCheck(leafFor("192.0.2.10"))("192.0.2.10"); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("a certificate without 127.0.0.1: %v", err)
	}
	if err := CertCheck(leafFor("127.0.0.1"))("192.0.2.10"); err == nil || !strings.Contains(err.Error(), "192.0.2.10") {
		t.Fatalf("a certificate without the advertise IP: %v", err)
	}
	if err := CertCheck(leafFor("127.0.0.1", "192.0.2.10"))("192.0.2.10"); err != nil {
		t.Fatalf("a certificate for both: %v", err)
	}
}
