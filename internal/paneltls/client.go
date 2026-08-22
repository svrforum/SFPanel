package paneltls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Self describes how in-process code reaches this node's OWN panel port.
//
// Five separate places do that today — the cluster gRPC loopback hop, the
// leader's post-rolling-update self-update, the update watchdog's health
// probe, the `sfpanel cluster …` CLI, and the CLI update path. Before TLS they
// all hardcoded "http://127.0.0.1:%d". Each one that keeps its own copy of the
// scheme decision is a place the panel silently talks plaintext to a TLS-only
// listener, and two of those failures are severe: the watchdog rolls the binary
// AND the database back when its probe fails for 90 seconds, and the gRPC hop
// is the choke point for every cross-node request. So the decision lives here,
// once.
type Self struct {
	TLSEnabled bool
	// Dir is the managed certificate directory. Ignored when the operator
	// supplied their own pair.
	Dir string
	// CertFile and CAFile are operator-supplied paths, empty in managed mode.
	CertFile string
	CAFile   string
	Port     int
}

// Scheme is "https" when the panel terminates TLS itself, else "http".
func (s Self) Scheme() string {
	if s.TLSEnabled {
		return "https"
	}
	return "http"
}

// URL builds an absolute URL to this node's own panel port. path must begin
// with a slash.
//
// The host is always the loopback literal rather than the machine's hostname:
// resolution must not depend on DNS or /etc/hosts, both of which can be broken
// on a box whose panel is exactly what you need in order to fix them.
func (s Self) URL(path string) string {
	return fmt.Sprintf("%s://127.0.0.1:%d%s", s.Scheme(), s.Port, path)
}

// TLSConfig returns the config a loopback client needs, or nil when the panel
// serves plain HTTP.
//
// Verification stays ON. The certificate the panel serves is signed by a CA
// sitting on this same disk, so the honest thing to do is trust that CA
// specifically — not to skip verification because "it is only loopback". The
// pool is the system roots plus, in managed mode, the local CA; in
// operator-supplied mode, whatever ca_file names, plus the served certificate
// itself so a self-signed operator certificate also verifies.
func (s Self) TLSConfig() (*tls.Config, error) {
	if !s.TLSEnabled {
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	var added bool
	appendFile := func(path string) {
		if path == "" {
			return
		}
		pem, readErr := os.ReadFile(path)
		if readErr != nil {
			return
		}
		if pool.AppendCertsFromPEM(pem) {
			added = true
		}
	}

	if s.CertFile == "" {
		appendFile(New(s.Dir).CACertPath())
	} else {
		appendFile(s.CAFile)
		// A self-signed operator certificate is its own anchor.
		appendFile(s.CertFile)
	}

	if !added && s.CAFile == "" && s.CertFile == "" {
		return nil, fmt.Errorf("no local certificate authority found in %s", s.Dir)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// HTTPClient returns a client for talking to this node's own panel port.
func (s Self) HTTPClient(timeout time.Duration) (*http.Client, error) {
	cfg, err := s.TLSConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &http.Client{Timeout: timeout}, nil
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: cfg},
	}, nil
}

// PeerPool builds the trust pool for reaching OTHER nodes' panel ports, from
// the CA certificates replicated through the cluster FSM.
//
// Each node runs its own CA, so this pool is a growing, shrinking set rather
// than the single fixed anchor internal/cluster's mTLS pool uses — which is
// why it cannot reuse that pool (documented there as "loaded lazily, never
// invalidated"). Callers rebuild this whenever cluster membership changes.
func PeerPool(caPEMs map[string]string) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, pem := range caPEMs {
		pool.AppendCertsFromPEM([]byte(pem))
	}
	return pool
}
