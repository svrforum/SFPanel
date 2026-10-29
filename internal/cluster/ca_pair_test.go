package cluster

import (
	"bytes"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A next CA must be told apart from the one it replaces — by tools that key
// an issuer on its name and serial (NSS refuses a reused pair) and by an
// operator reading a log — and accepted by a peer whose clock runs behind.
func TestGenerateCAMakesACAOfItsOwn(t *testing.T) {
	now := time.Now()
	a, err := GenerateCA("SFPanel CA - c", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateCA("SFPanel CA - c (2026-10-29)", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Cert.SerialNumber.Cmp(b.Cert.SerialNumber) == 0 || a.Cert.SerialNumber.BitLen() < 64 {
		t.Fatalf("serials %v and %v", a.Cert.SerialNumber, b.Cert.SerialNumber)
	}
	if !a.Cert.IsCA || a.Pin() == b.Pin() {
		t.Fatal("not two distinct CAs")
	}
	if !a.Cert.NotBefore.Before(now.Add(-59 * time.Minute)) {
		t.Fatalf("NotBefore %v is not backdated", a.Cert.NotBefore)
	}
	again, err := ParseCAPair(a.CertPEM, a.KeyPEM)
	if err != nil || again.Pin() != a.Pin() {
		t.Fatalf("round trip: %v", err)
	}
}

// A certificate and a key from two CAs fail every signature later, deep in
// x509 — the state a CA swapped by hand left. The pair is refused at once.
func TestParseCAPairRefusesAKeyFromAnotherCA(t *testing.T) {
	a, _ := GenerateCA("a", time.Now())
	b, _ := GenerateCA("b", time.Now())
	if _, err := ParseCAPair(a.CertPEM, b.KeyPEM); !errors.Is(err, ErrCAKeyMismatch) {
		t.Fatalf("a mismatched pair: %v", err)
	}
}

// A manager moves its own certificate to another CA by re-signing the key it
// has, for the names it has, in one rename — ReissueNodeCert's contract.
func TestReissueNodeCertByKeepsTheKeyAndTheNames(t *testing.T) {
	certDir, tlsMgr := setupCertDir(t)
	keyBefore, _ := os.ReadFile(filepath.Join(certDir, "node.key"))
	next, _ := GenerateCA("next", time.Now())
	if err := tlsMgr.ReissueNodeCertBy(next, "node-1", []string{"127.0.0.1"}, CertRoleManager); err != nil {
		t.Fatal(err)
	}
	leaf, err := tlsMgr.NodeCertLeaf()
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.CheckSignatureFrom(next.Cert); err != nil {
		t.Fatalf("not signed by the given CA: %v", err)
	}
	keyAfter, _ := os.ReadFile(filepath.Join(certDir, "node.key"))
	if !bytes.Equal(keyBefore, keyAfter) || CertRole(leaf) != CertRoleManager || leaf.Subject.CommonName != "node-1" ||
		len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatalf("the reissue changed the key, the role, the name or the address: %+v %v", leaf.Subject, leaf.IPAddresses)
	}
	if !leaf.NotBefore.Before(time.Now().Add(-59 * time.Minute)) {
		t.Fatalf("leaf NotBefore %v is not backdated", leaf.NotBefore)
	}
}

// Every CA ever made had serial 1.
func TestInitCAUsesARandomSerial(t *testing.T) {
	_, a := setupCertDir(t)
	_, b := setupCertDir(t)
	ca, _, err := a.loadCA()
	if err != nil {
		t.Fatal(err)
	}
	cb, _, _ := b.loadCA()
	if ca.SerialNumber.Cmp(cb.SerialNumber) == 0 {
		t.Fatalf("both CAs have serial %v", ca.SerialNumber)
	}
}

// A bundle someone sends is certificates and nothing else; one read from
// this node's own ca.crt skips what is not a certificate, as before.
func TestCertificatesInIsStrictAndPemCertificatesIsNot(t *testing.T) {
	a, _ := GenerateCA("a", time.Now())
	b, _ := GenerateCA("b", time.Now())
	both := append(append([]byte{}, a.CertPEM...), b.CertPEM...)
	certs, err := certificatesIn(both)
	if err != nil || len(certs) != 2 || CAPin(certs[1].RawSubjectPublicKeyInfo) != b.Pin() {
		t.Fatalf("two certificates: %d %v", len(certs), err)
	}
	// Refused as what it is: a key block fails to parse as a certificate too,
	// which would pass for this refusal without the check on the block type.
	withKey := append(append([]byte{}, a.CertPEM...), a.KeyPEM...)
	if _, err := certificatesIn(withKey); err == nil || !strings.Contains(err.Error(), "not a certificate") {
		t.Fatalf("a private key was taken among the certificates: %v", err)
	}
	if _, err := certificatesIn(nil); err == nil {
		t.Fatal("no certificate at all was taken")
	}
	if got := pemCertificates(withKey); len(got) != 1 {
		t.Fatalf("lenient read: %d certificates", len(got))
	}
	// A certificate block with headers is one AppendCertsFromPEM skips: the
	// strict reader refuses it, so that no CA it takes goes untrusted.
	headed := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Comment": "a"}, Bytes: a.Cert.Raw})
	if _, err := certificatesIn(headed); err == nil || !strings.Contains(err.Error(), "not a certificate") {
		t.Fatalf("a certificate block with headers was taken: %v", err)
	}
	if got := pemCertificates(headed); len(got) != 0 {
		t.Fatalf("lenient read of a block with headers: %d certificates", len(got))
	}
}

// A node certificate and its own key are a pair, but not a CA: no peer takes
// what it signs.
func TestParseCAPairRefusesANodeCertificate(t *testing.T) {
	ca, _ := GenerateCA("a", time.Now())
	certPEM, keyPEM, err := IssueNodeCertBy(ca, "node-1", []string{"127.0.0.1"}, CertRoleManager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCAPair(certPEM, keyPEM); err == nil || !strings.Contains(err.Error(), "not a CA certificate") {
		t.Fatalf("a node certificate was taken as a CA: %v", err)
	}
}
