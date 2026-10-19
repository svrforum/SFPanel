package cluster

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// CAPin is how a joining node recognizes the cluster it was told to join:
// "sha256:" and the hex SHA-256 of the cluster CA's public key (DER
// SubjectPublicKeyInfo), the shape kubeadm uses. Over the key, not the
// certificate: until the replicated proxy secret takes over, the proxy secret
// is the SHA-256 of the certificate PEM, and a pin over it would print that
// secret on every token page.
func CAPin(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NormalizeCAPin accepts a pin with or without "sha256:", in either case,
// with surrounding space, and returns it in CAPin's form.
func NormalizeCAPin(pin string) (string, error) {
	p := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(pin)), "sha256:")
	if len(p) != 64 {
		return "", errors.New("the CA fingerprint must be sha256: and 64 hex characters")
	}
	if _, err := hex.DecodeString(p); err != nil {
		return "", errors.New("the CA fingerprint is not hex")
	}
	return "sha256:" + p, nil
}

// errCAUnavailable is PreFlight's answer from a server that cannot read its
// own cluster CA.
const errCAUnavailable = "cluster CA unavailable"

// checkPreFlightPin keeps a PreFlight answer only if the CA key it carries is
// the one the join command names.
func checkPreFlightPin(resp *pb.PreFlightResponse, pin, leaderAddr string) error {
	if len(resp.GetCaPublicKey()) == 0 {
		if resp.GetError() == errCAUnavailable {
			return fmt.Errorf("the server at %s cannot read its cluster CA", leaderAddr)
		}
		return fmt.Errorf("the leader at %s is older than this node and cannot show which cluster it is; update the cluster first", leaderAddr)
	}
	if CAPin(resp.GetCaPublicKey()) != pin {
		return fmt.Errorf("the CA fingerprint does not match: the server at %s is not the cluster this join command was made for", leaderAddr)
	}
	return nil
}

// pinnedTLSConfig trusts exactly one key, the cluster CA's. The leader sends
// only its leaf, so the chain is checked here: the leaf's signature against
// that key, its validity period, and that it is not an agent's — the CA signs
// those too. Host names are not: a node is reached by whichever address the
// operator typed (LAN, Tailscale), and its certificate names one of them.
func pinnedTLSConfig(spkiDER []byte) (*tls.Config, error) {
	pub, err := x509.ParsePKIXPublicKey(spkiDER)
	if err != nil {
		return nil, fmt.Errorf("parse the cluster CA key: %w", err)
	}
	issuer := &x509.Certificate{PublicKey: pub, BasicConstraintsValid: true, IsCA: true, Version: 3}
	switch pub.(type) {
	case *ecdsa.PublicKey:
		issuer.PublicKeyAlgorithm = x509.ECDSA
	case ed25519.PublicKey:
		issuer.PublicKeyAlgorithm = x509.Ed25519
	case *rsa.PublicKey:
		issuer.PublicKeyAlgorithm = x509.RSA
	default:
		return nil, fmt.Errorf("unsupported cluster CA key type %T", pub)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// The standard verification needs the CA certificate, which the
		// joiner does not have yet; VerifyPeerCertificate below replaces it
		// with a stricter check against the one pinned key.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("the leader presented no certificate")
			}
			leaf, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return fmt.Errorf("parse the leader certificate: %w", err)
			}
			if err := leaf.CheckSignatureFrom(issuer); err != nil {
				return fmt.Errorf("the leader certificate is not signed by the pinned cluster CA: %w", err)
			}
			if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return errors.New("the leader certificate is outside its validity period")
			}
			if CertRole(leaf) == CertRoleAgent {
				return errors.New("the leader certificate is an agent's, not a manager's")
			}
			return nil
		},
	}, nil
}
