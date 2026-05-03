package release

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestVerifyCosignBlob_GoldenPath_Base64Cert proves that base64-wrapped
// cert PEM (cosign v2 --output-certificate default) is unwrapped before
// chain validation. Catches the regression where a real cosign-signed
// release would fail verification because the cert wasn't decoded.
func TestVerifyCosignBlob_GoldenPath_Base64Cert(t *testing.T) {
	blob := []byte("artifact body")
	const subject = "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v0.11.2"
	const issuer = "https://token.actions.githubusercontent.com"

	caPEM, caKey := mintTestCA(t)
	original := fulcioBundle
	fulcioBundle = caPEM
	defer func() { fulcioBundle = original }()

	leafPEM, leafKey := mintTestLeaf(t, caKey, caPEM, subject, issuer)
	digest := sha256.Sum256(blob)
	sigDER, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	sigB64 := []byte(base64.StdEncoding.EncodeToString(sigDER))

	// Wrap the cert PEM in another base64 layer (mimics cosign v2 output).
	leafPEMb64 := []byte(base64.StdEncoding.EncodeToString(leafPEM))

	if err := VerifyCosignBlob(blob, sigB64, leafPEMb64, CosignIdentity{
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        issuer,
	}); err != nil {
		t.Fatalf("VerifyCosignBlob with base64-wrapped cert: %v", err)
	}
}

// TestVerifyCosignBlob_GoldenPath synthesises a Fulcio-shaped cert chain
// (self-signed CA → leaf with the right SAN URI + OIDC issuer extension)
// and proves VerifyCosignBlob accepts a correctly-signed blob.
func TestVerifyCosignBlob_GoldenPath(t *testing.T) {
	blob := []byte("ec53a3...  sfpanel_0.11.2_linux_amd64.tar.gz\n")
	const subject = "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v0.11.2"
	const issuer = "https://token.actions.githubusercontent.com"

	// Replace the embedded Fulcio bundle with a freshly-minted CA so the
	// test owns the trust root. Restore at the end.
	caPEM, caKey := mintTestCA(t)
	original := fulcioBundle
	fulcioBundle = caPEM
	defer func() { fulcioBundle = original }()

	leafPEM, leafKey := mintTestLeaf(t, caKey, caPEM, subject, issuer)
	digest := sha256.Sum256(blob)
	sigDER, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sigB64 := []byte(base64.StdEncoding.EncodeToString(sigDER))

	if err := VerifyCosignBlob(blob, sigB64, leafPEM, CosignIdentity{
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        issuer,
	}); err != nil {
		t.Fatalf("VerifyCosignBlob: %v", err)
	}
}

func TestVerifyCosignBlob_RejectsWrongSubject(t *testing.T) {
	blob := []byte("artifact body")
	caPEM, caKey := mintTestCA(t)
	original := fulcioBundle
	fulcioBundle = caPEM
	defer func() { fulcioBundle = original }()

	const issuer = "https://token.actions.githubusercontent.com"
	leafPEM, leafKey := mintTestLeaf(t, caKey, caPEM,
		"https://github.com/attacker/Evil/.github/workflows/release.yml@refs/tags/v1.0.0", issuer)

	digest := sha256.Sum256(blob)
	sigDER, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	sigB64 := []byte(base64.StdEncoding.EncodeToString(sigDER))

	err := VerifyCosignBlob(blob, sigB64, leafPEM, CosignIdentity{
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        issuer,
	})
	if err == nil {
		t.Fatal("expected SAN-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "SAN does not start with") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestVerifyCosignBlob_RejectsWrongIssuer(t *testing.T) {
	blob := []byte("artifact body")
	caPEM, caKey := mintTestCA(t)
	original := fulcioBundle
	fulcioBundle = caPEM
	defer func() { fulcioBundle = original }()

	const subject = "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v0.11.2"
	leafPEM, leafKey := mintTestLeaf(t, caKey, caPEM, subject, "https://gitlab.example.com")

	digest := sha256.Sum256(blob)
	sigDER, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	sigB64 := []byte(base64.StdEncoding.EncodeToString(sigDER))

	err := VerifyCosignBlob(blob, sigB64, leafPEM, CosignIdentity{
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        "https://token.actions.githubusercontent.com",
	})
	if err == nil {
		t.Fatal("expected issuer-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "OIDC issuer mismatch") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestVerifyCosignBlob_RejectsTamperedBlob(t *testing.T) {
	original := []byte("trusted checksums")
	tampered := []byte("evil checksums")
	caPEM, caKey := mintTestCA(t)
	originalBundle := fulcioBundle
	fulcioBundle = caPEM
	defer func() { fulcioBundle = originalBundle }()

	const subject = "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v0.11.2"
	const issuer = "https://token.actions.githubusercontent.com"
	leafPEM, leafKey := mintTestLeaf(t, caKey, caPEM, subject, issuer)

	digest := sha256.Sum256(original) // sign original
	sigDER, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	sigB64 := []byte(base64.StdEncoding.EncodeToString(sigDER))

	err := VerifyCosignBlob(tampered, sigB64, leafPEM, CosignIdentity{
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        issuer,
	})
	if err == nil {
		t.Fatal("expected signature-mismatch error, got nil")
	}
}

func TestVerifyCosignBlob_RejectsUntrustedCA(t *testing.T) {
	blob := []byte("artifact body")
	// Trust the embedded (real Fulcio) bundle but sign with a self-issued CA.
	const subject = "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v0.11.2"
	const issuer = "https://token.actions.githubusercontent.com"

	rogueCA, rogueCAKey := mintTestCA(t)
	leafPEM, leafKey := mintTestLeaf(t, rogueCAKey, rogueCA, subject, issuer)

	digest := sha256.Sum256(blob)
	sigDER, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	sigB64 := []byte(base64.StdEncoding.EncodeToString(sigDER))

	err := VerifyCosignBlob(blob, sigB64, leafPEM, CosignIdentity{
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        issuer,
	})
	if err == nil {
		t.Fatal("expected chain-verification error, got nil")
	}
	if !strings.Contains(err.Error(), "chain verification failed") {
		t.Errorf("wrong error: %v", err)
	}
}

// --- test helpers ---

func mintTestCA(t *testing.T) (pemBytes []byte, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"sigstore.dev"}, CommonName: "test-fulcio"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}

func mintTestLeaf(t *testing.T, caKey *ecdsa.PrivateKey, caPEM []byte, subjectURI, issuer string) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	caBlock, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}

	uri, err := url.Parse(subjectURI)
	if err != nil {
		t.Fatalf("subject uri: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"sigstore.dev"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		URIs:         []*url.URL{uri},
		ExtraExtensions: []pkix.Extension{
			{
				Id:    asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1},
				Value: []byte(issuer),
			},
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), leafKey
}
