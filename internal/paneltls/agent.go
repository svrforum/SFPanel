package paneltls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/svrforum/SFPanel/internal/config"
)

// PrepareForAgent readies the HTTPS an agent's panel must serve — its
// managers relay to it over HTTPS only, and trust the CA its registration
// carries — and returns that CA. TLS that is off is turned on, managed
// (changed reports it). An operator's certificate needs ca_file, the CA its
// managers will trust, and a pair the panel could not start with is refused
// here: the agent would register, then fail every boot. Both `agent join`
// and a manager's conversion to an agent run it.
func PrepareForAgent(cfg *config.Config) (caPEM []byte, changed bool, err error) {
	t := cfg.Server.TLS
	changed = !t.Enabled
	t.Enabled = true
	if t.Managed() {
		m := New(t.Dir)
		if _, err := m.Ensure(); err != nil {
			return nil, false, fmt.Errorf("prepare the panel's HTTPS certificate: %w", err)
		}
		// The CA the agent's link reports at every boot, read the same way.
		caPEM, err = m.CACertPEM()
	} else {
		if t.CAFile == "" {
			return nil, false, errors.New("an agent's panel must serve HTTPS that managers can verify: set server.tls.ca_file to the CA of your certificate")
		}
		for _, f := range []struct{ label, path string }{{"server.tls.cert_file", t.CertFile}, {"server.tls.key_file", t.KeyFile}} {
			if _, err := os.Stat(f.path); err != nil {
				return nil, false, fmt.Errorf("%s: %w", f.label, err)
			}
		}
		if _, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile); err != nil {
			return nil, false, fmt.Errorf("server.tls.cert_file and key_file do not load as a pair: %w", err)
		}
		caPEM, err = os.ReadFile(t.CAFile)
	}
	if err != nil {
		return nil, false, fmt.Errorf("read the panel's CA: %w", err)
	}
	cfg.Server.TLS = t
	return caPEM, changed, nil
}

// CertCheck refuses a panel certificate the agent could not be reached
// with: its managers dial it at the advertise IP and its own gRPC hop at
// 127.0.0.1, both verifying against the CA the registration reports. One for
// other names registered the agent, spent the token and failed every request.
func CertCheck(cfg *config.Config) func(advertise string) error {
	return func(advertise string) error {
		t := cfg.Server.TLS
		certPath, caPath := t.CertFile, t.CAFile
		if t.Managed() {
			m := New(t.Dir)
			certPath, caPath = m.ServerCertPath(), m.CACertPath()
		}
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read the panel's certificate: %w", err)
		}
		var leaf *x509.Certificate
		inter := x509.NewCertPool()
		for rest := certPEM; ; {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return fmt.Errorf("parse the panel's certificate: %w", err)
			}
			if leaf == nil {
				leaf = c
			} else {
				inter.AddCert(c)
			}
		}
		if leaf == nil {
			return fmt.Errorf("%s holds no certificate", certPath)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return fmt.Errorf("read the panel's CA: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return fmt.Errorf("%s holds no certificate", caPath)
		}
		for _, host := range []string{"127.0.0.1", advertise} {
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: host}); err != nil {
				return fmt.Errorf("the panel's certificate %s does not verify for %s against %s: %w — the agent could not be reached with it; issue one that names 127.0.0.1 and %s", certPath, host, caPath, err, advertise)
			}
		}
		return nil
	}
}
