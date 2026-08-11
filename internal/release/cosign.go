package release

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// fulcioBundle is the public-good Fulcio CA chain (root + intermediate)
// used by Sigstore's keyless flow. Embedded as a constant rather than
// fetched at runtime so update verification works on air-gapped nodes.
// Pulled from sigstore/root-signing/targets/fulcio_v1.crt.pem +
// fulcio_intermediate_v1.crt.pem; rotation is rare (next planned rotation
// is the 2031 expiry of the current root).
//
//go:embed fulcio_root.pem
var fulcioBundle []byte

// CosignIdentity describes the expected OIDC identity that signed a blob.
// The `Subject` is the Fulcio cert SAN URI (the GitHub Actions workflow
// reference for keyless signing); `Issuer` is the OIDC issuer URL
// (always https://token.actions.githubusercontent.com for GitHub Actions).
//
// SubjectPrefix lets the verifier accept any cert whose SAN URI *starts*
// with the given string — e.g. ".../release.yml@refs/tags/v" matches any
// version tag. Use this rather than a literal Subject when releases are
// versioned by tag.
type CosignIdentity struct {
	SubjectPrefix string
	Issuer        string
}

// VerifyCosignBlob verifies that `signature` (base64-encoded ECDSA over
// SHA-256 of `blob`) was produced by the certificate `certPEM`, that the
// cert chains back to the embedded Fulcio root, and that its SAN URI +
// OIDC issuer extension match `expected`.
//
// Returns nil on success. On failure the error message is suitable for
// surfacing to operators (no stack traces, no inner crypto noise).
//
// What this DOES NOT do (out of scope for an MVP):
//   - Verify the Rekor inclusion proof. The Sigstore transparency log
//     gives tamper-evidence beyond identity; we rely on the cert's identity
//     attestation only. Adding Rekor verification is a one-API-call upgrade
//     when the threat model needs it.
//   - Validate Signed Certificate Timestamps (SCTs). Cosign Fulcio certs
//     ship SCTs that prove the cert was logged before issuance; we trust
//     the cert's NotBefore/NotAfter window instead.
//
// Both gaps are mitigated by the GitHub Actions OIDC chain itself —
// only release.yml on a tagged commit can mint a signing identity
// matching SubjectPrefix.
func VerifyCosignBlob(blob, signature, certPEM []byte, expected CosignIdentity) error {
	if len(blob) == 0 {
		return errors.New("cosign: empty blob")
	}
	if len(signature) == 0 {
		return errors.New("cosign: empty signature")
	}
	if len(certPEM) == 0 {
		return errors.New("cosign: empty cert PEM")
	}
	if expected.SubjectPrefix == "" || expected.Issuer == "" {
		return errors.New("cosign: SubjectPrefix and Issuer are required")
	}

	leafCert, intermediates, err := parseCosignCertChain(certPEM)
	if err != nil {
		return fmt.Errorf("cosign: %w", err)
	}

	// Signature is base64-encoded; decode here, verify inside the shared core.
	sigDER, err := decodeCosignSig(signature)
	if err != nil {
		return fmt.Errorf("cosign: %w", err)
	}
	return verifyCosignLeaf(blob, sigDER, leafCert, intermediates, expected)
}

// verifyCosignLeaf is the verification core shared by the legacy (.sig/.pem)
// and Sigstore-bundle paths: chain to the embedded Fulcio roots, SAN-URI
// prefix + OIDC-issuer identity pin, then ECDSA over SHA-256 of blob.
func verifyCosignLeaf(blob, sigDER []byte, leafCert *x509.Certificate, intermediates *x509.CertPool, expected CosignIdentity) error {
	roots, err := loadFulcioRoots()
	if err != nil {
		return fmt.Errorf("cosign: %w", err)
	}

	// Fulcio certs are short-lived (10 minutes) — verify against the cert's
	// NotBefore so a release signed in CI in 2026 can still be verified
	// today. x509.Verify rejects expired-now certs by default; explicitly
	// pinning CurrentTime to NotBefore + 1 second is the conventional
	// workaround used by cosign itself.
	verifyOpts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		CurrentTime:   leafCert.NotBefore.Add(1 * time.Second),
	}
	if _, err := leafCert.Verify(verifyOpts); err != nil {
		return fmt.Errorf("cosign: cert chain verification failed: %w", err)
	}

	// SAN URI = OIDC subject for keyless. Fulcio puts URIs in the SAN
	// extension; multiple URIs are allowed but signing certs typically
	// carry one.
	matchedSAN := false
	for _, uri := range leafCert.URIs {
		if strings.HasPrefix(uri.String(), expected.SubjectPrefix) {
			matchedSAN = true
			break
		}
	}
	if !matchedSAN {
		uris := make([]string, 0, len(leafCert.URIs))
		for _, u := range leafCert.URIs {
			uris = append(uris, u.String())
		}
		return fmt.Errorf("cosign: cert SAN does not start with %q (got %v)",
			expected.SubjectPrefix, uris)
	}

	// OIDC issuer is in a custom extension OID 1.3.6.1.4.1.57264.1.1
	// (Fulcio's "OIDC Issuer" claim). Extension value is the raw issuer
	// URL bytes.
	issuer, err := extractOIDCIssuer(leafCert)
	if err != nil {
		return fmt.Errorf("cosign: %w", err)
	}
	if issuer != expected.Issuer {
		return fmt.Errorf("cosign: OIDC issuer mismatch — expected %q, got %q",
			expected.Issuer, issuer)
	}

	pub, ok := leafCert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("cosign: unexpected key type %T (want ECDSA)", leafCert.PublicKey)
	}
	digest := sha256.Sum256(blob)
	if !ecdsa.VerifyASN1(pub, digest[:], sigDER) {
		return errors.New("cosign: signature verification failed")
	}
	return nil
}

const sigstoreBundleMediaTypePrefix = "application/vnd.dev.sigstore.bundle"

// sigstoreBundle models the subset of the sigstore/protobuf-specs Bundle
// (protojson encoding) needed for offline verification. tlogEntries and
// timestampVerificationData are intentionally not modelled — like
// VerifyCosignBlob, verification uses the embedded Fulcio roots plus the
// identity pin only (no Rekor lookup).
type sigstoreBundle struct {
	MediaType            string `json:"mediaType"`
	VerificationMaterial struct {
		Certificate *struct {
			RawBytes []byte `json:"rawBytes"`
		} `json:"certificate"`
		X509CertificateChain *struct {
			Certificates []struct {
				RawBytes []byte `json:"rawBytes"`
			} `json:"certificates"`
		} `json:"x509CertificateChain"`
		PublicKey *struct {
			Hint string `json:"hint"`
		} `json:"publicKey"`
	} `json:"verificationMaterial"`
	MessageSignature *struct {
		MessageDigest *struct {
			Algorithm string `json:"algorithm"`
			Digest    []byte `json:"digest"`
		} `json:"messageDigest"`
		Signature []byte `json:"signature"`
	} `json:"messageSignature"`
	DSSEEnvelope json.RawMessage `json:"dsseEnvelope"`
}

// VerifyCosignBundle verifies a Sigstore bundle (`cosign sign-blob --bundle`,
// bundle spec v0.3; the v0.1/v0.2 certificate-chain form is accepted too)
// over blob against the embedded Fulcio roots and the expected identity.
func VerifyCosignBundle(blob, bundleJSON []byte, expected CosignIdentity) error {
	if len(blob) == 0 {
		return errors.New("cosign: empty blob")
	}
	if len(bundleJSON) == 0 {
		return errors.New("cosign: empty bundle")
	}
	if expected.SubjectPrefix == "" || expected.Issuer == "" {
		return errors.New("cosign: SubjectPrefix and Issuer are required")
	}
	var b sigstoreBundle
	if err := json.Unmarshal(bundleJSON, &b); err != nil {
		return fmt.Errorf("cosign: parse bundle: %w", err)
	}
	if !strings.HasPrefix(b.MediaType, sigstoreBundleMediaTypePrefix) {
		return fmt.Errorf("cosign: unexpected bundle media type %q", b.MediaType)
	}
	if len(b.DSSEEnvelope) > 0 {
		return errors.New("cosign: bundle carries a DSSE envelope, not a message signature")
	}
	if b.MessageSignature == nil || len(b.MessageSignature.Signature) == 0 {
		return errors.New("cosign: bundle carries no message signature")
	}
	// Fail fast on a digest mismatch — the authoritative check is the ECDSA
	// verification over sha256(blob) inside verifyCosignLeaf.
	digest := sha256.Sum256(blob)
	if md := b.MessageSignature.MessageDigest; md != nil {
		if md.Algorithm != "SHA2_256" {
			return fmt.Errorf("cosign: unsupported bundle digest algorithm %q", md.Algorithm)
		}
		if !bytes.Equal(md.Digest, digest[:]) {
			return errors.New("cosign: bundle digest does not match blob")
		}
	}
	leaf, intermediates, err := bundleCertChain(&b)
	if err != nil {
		return fmt.Errorf("cosign: %w", err)
	}
	return verifyCosignLeaf(blob, b.MessageSignature.Signature, leaf, intermediates, expected)
}

func bundleCertChain(b *sigstoreBundle) (*x509.Certificate, *x509.CertPool, error) {
	vm := &b.VerificationMaterial
	switch {
	case vm.Certificate != nil:
		leaf, err := x509.ParseCertificate(vm.Certificate.RawBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse bundle cert: %w", err)
		}
		// v0.3: leaf-only — the embedded Fulcio bundle carries the
		// intermediate as a trusted root, so the chain still builds
		// (see the loadFulcioRoots comment).
		return leaf, x509.NewCertPool(), nil
	case vm.X509CertificateChain != nil && len(vm.X509CertificateChain.Certificates) > 0:
		var leaf *x509.Certificate
		intermediates := x509.NewCertPool()
		for i, c := range vm.X509CertificateChain.Certificates {
			cert, err := x509.ParseCertificate(c.RawBytes)
			if err != nil {
				return nil, nil, fmt.Errorf("parse bundle cert %d: %w", i, err)
			}
			if i == 0 {
				leaf = cert
			} else {
				intermediates.AddCert(cert)
			}
		}
		return leaf, intermediates, nil
	default:
		return nil, nil, errors.New("bundle carries no certificate (key-based bundles are not supported)")
	}
}

func parseCosignCertChain(certPEM []byte) (leaf *x509.Certificate, intermediates *x509.CertPool, err error) {
	// cosign v2's `sign-blob --output-certificate` writes the PEM cert
	// base64-encoded one extra time. Detect by leading bytes — raw PEM
	// starts with "-----BEGIN", otherwise try base64-decode once.
	trimmed := bytes.TrimSpace(certPEM)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN")) {
		decoded, decErr := base64.StdEncoding.DecodeString(string(trimmed))
		if decErr == nil && bytes.HasPrefix(bytes.TrimSpace(decoded), []byte("-----BEGIN")) {
			certPEM = decoded
		}
	}

	intermediates = x509.NewCertPool()
	rest := certPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse cert: %w", parseErr)
		}
		if leaf == nil {
			leaf = cert
		} else {
			intermediates.AddCert(cert)
		}
	}
	if leaf == nil {
		return nil, nil, errors.New("no certificates in PEM input")
	}
	return leaf, intermediates, nil
}

func loadFulcioRoots() (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	rest := fulcioBundle
	count := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse fulcio cert: %w", err)
		}
		// Self-signed → root. Anything else → still trusted (intermediate
		// pinned alongside the root for offline use). x509.Verify will
		// pull intermediates from the chain that's verified, so adding
		// them as roots is conservative; mark them all trusted to avoid
		// the case where the leaf carries no intermediate.
		roots.AddCert(cert)
		count++
	}
	if count == 0 {
		return nil, errors.New("embedded Fulcio bundle is empty")
	}
	return roots, nil
}

// oidOIDCIssuer is the Fulcio extension OID for "OIDC Issuer" claim.
// 1.3.6.1.4.1.57264.1.1 = sigstore.dev iana arc / claim 1.
var oidOIDCIssuer = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}

func extractOIDCIssuer(cert *x509.Certificate) (string, error) {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidOIDCIssuer) {
			return string(ext.Value), nil
		}
	}
	return "", errors.New("cert lacks OIDC Issuer extension (1.3.6.1.4.1.57264.1.1)")
}

func decodeCosignSig(sig []byte) ([]byte, error) {
	// `cosign sign-blob --output-signature` emits raw base64 (with padding).
	// Strip newlines that may have been introduced by file writes.
	clean := strings.TrimSpace(string(sig))
	clean = strings.ReplaceAll(clean, "\n", "")
	clean = strings.ReplaceAll(clean, "\r", "")
	decoded, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("base64 decode signature: %w", err)
	}
	return decoded, nil
}

// SFPanelReleaseIdentity returns the CosignIdentity that this binary
// expects when verifying its own release artifacts. Centralised here so
// both the HTTP update handler and the CLI 'sfpanel update' use the same
// trust policy.
func SFPanelReleaseIdentity() CosignIdentity {
	return CosignIdentity{
		// Any tagged release on the canonical repo's release.yml workflow.
		SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
		Issuer:        "https://token.actions.githubusercontent.com",
	}
}
