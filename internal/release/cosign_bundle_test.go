package release

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

// sfpanelIdentity is the identity pin used across the bundle tests — same
// prefix/issuer as SFPanelReleaseIdentity but spelled out so the tests stay
// hermetic if the production policy ever moves.
var testBundleIdentity = CosignIdentity{
	SubjectPrefix: "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v",
	Issuer:        "https://token.actions.githubusercontent.com",
}

const (
	testBundleSubject = "https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v0.56.0"
	testBundleIssuer  = "https://token.actions.githubusercontent.com"
)

// mintTestBundleJSON assembles a v0.3-form Sigstore bundle from a leaf PEM,
// a DER ECDSA signature and the blob it covers. mutate (optional) edits the
// decoded JSON object before re-marshalling, for malformed-input cases.
func mintTestBundleJSON(t *testing.T, leafPEM, sigDER, blob []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	block, _ := pem.Decode(leafPEM)
	if block == nil {
		t.Fatal("mint bundle: leaf PEM did not decode")
	}
	digest := sha256.Sum256(blob)
	obj := map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{
			"certificate": map[string]any{
				"rawBytes": base64.StdEncoding.EncodeToString(block.Bytes),
			},
		},
		"messageSignature": map[string]any{
			"messageDigest": map[string]any{
				"algorithm": "SHA2_256",
				"digest":    base64.StdEncoding.EncodeToString(digest[:]),
			},
			"signature": base64.StdEncoding.EncodeToString(sigDER),
		},
	}
	if mutate != nil {
		mutate(obj)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("mint bundle: marshal: %v", err)
	}
	return out
}

// signTestBlob mints a leaf under caKey and signs blob, returning the leaf
// PEM and DER signature — the shared setup for every bundle test.
func signTestBlob(t *testing.T, blob []byte, subject, issuer string) (leafPEM, sigDER []byte) {
	t.Helper()
	caPEM, caKey := mintTestCA(t)
	original := fulcioBundle
	fulcioBundle = caPEM
	t.Cleanup(func() { fulcioBundle = original })

	leafPEM, leafKey := mintTestLeaf(t, caKey, caPEM, subject, issuer)
	digest := sha256.Sum256(blob)
	sigDER, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return leafPEM, sigDER
}

// TestVerifyCosignBundle_GoldenPath_V03 proves the v0.3 certificate-form
// bundle verifies, and — because v0.3 bundles carry the leaf only — that a
// leaf-only chain builds against the embedded trust pool.
func TestVerifyCosignBundle_GoldenPath_V03(t *testing.T) {
	blob := []byte("ec53a3...  sfpanel_0.56.0_linux_amd64.tar.gz\n")
	leafPEM, sigDER := signTestBlob(t, blob, testBundleSubject, testBundleIssuer)
	bundle := mintTestBundleJSON(t, leafPEM, sigDER, blob, nil)

	if err := VerifyCosignBundle(blob, bundle, testBundleIdentity); err != nil {
		t.Fatalf("VerifyCosignBundle v0.3: %v", err)
	}
}

// mintTestIntermediate issues a CA-capable intermediate under the given root,
// mirroring the production Fulcio topology (root → intermediate → leaf).
func mintTestIntermediate(t *testing.T, rootKey *ecdsa.PrivateKey, rootPEM []byte) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	rootBlock, _ := pem.Decode(rootPEM)
	rootCert, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("intermediate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(3),
		Subject:               pkix.Name{Organization: []string{"sigstore.dev"}, CommonName: "test-fulcio-intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, rootCert, &key.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("intermediate cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}

// TestVerifyCosignBundle_GoldenPath_ThreeTierChain reproduces the REAL
// production topology: the embedded trust pool holds root + intermediate
// (both added as trusted roots by loadFulcioRoots), Fulcio issues the leaf
// from the intermediate, and the v0.3 bundle carries the leaf ONLY. This is
// the exact combination every v0.56+ self-update depends on — the 2-tier
// golden path above cannot catch a regression in it.
func TestVerifyCosignBundle_GoldenPath_ThreeTierChain(t *testing.T) {
	blob := []byte("three-tier artifact")
	rootPEM, rootKey := mintTestCA(t)
	interPEM, interKey := mintTestIntermediate(t, rootKey, rootPEM)

	original := fulcioBundle
	fulcioBundle = append(append([]byte{}, rootPEM...), interPEM...)
	t.Cleanup(func() { fulcioBundle = original })

	leafPEM, leafKey := mintTestLeaf(t, interKey, interPEM, testBundleSubject, testBundleIssuer)
	digest := sha256.Sum256(blob)
	sigDER, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	bundle := mintTestBundleJSON(t, leafPEM, sigDER, blob, nil)

	if err := VerifyCosignBundle(blob, bundle, testBundleIdentity); err != nil {
		t.Fatalf("VerifyCosignBundle three-tier leaf-only: %v", err)
	}
}

// TestVerifyRealBundleFromEnv is the release-pipeline smoke: release.yml runs
// it against the bundle cosign v3 just produced, verifying with the REAL
// embedded Fulcio roots and production identity pin. It breaks the
// self-referential-fixture loop (mintTestBundleJSON shares its field model
// with the verifier, so a model-vs-real-cosign drift would pass unit tests
// and only fail in production). Skips outside the pipeline.
func TestVerifyRealBundleFromEnv(t *testing.T) {
	blobPath := os.Getenv("SFPANEL_BUNDLE_SMOKE_BLOB")
	bundlePath := os.Getenv("SFPANEL_BUNDLE_SMOKE_BUNDLE")
	if blobPath == "" || bundlePath == "" {
		t.Skip("SFPANEL_BUNDLE_SMOKE_BLOB/BUNDLE not set (release-pipeline smoke only)")
	}
	blob, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if err := VerifyCosignBundle(blob, bundle, SFPanelReleaseIdentity()); err != nil {
		t.Fatalf("real cosign bundle failed in-binary verification: %v", err)
	}
}

// TestVerifyCosignBundle_RealV056Fixture verifies a REAL cosign v3.1.3
// bundle — vendored from the published v0.56.0 release — against the
// production Fulcio roots and identity pin. The synthetic fixtures above
// share their field model with the verifier, so only a real bundle can catch
// the model drifting from actual cosign output. The fixture stays valid
// forever: verifyCosignLeaf pins CurrentTime to the cert's NotBefore.
func TestVerifyCosignBundle_RealV056Fixture(t *testing.T) {
	blob, err := os.ReadFile("testdata/v0.56.0-checksums.txt")
	if err != nil {
		t.Fatalf("read fixture blob: %v", err)
	}
	bundle, err := os.ReadFile("testdata/v0.56.0-checksums.txt.sigstore.json")
	if err != nil {
		t.Fatalf("read fixture bundle: %v", err)
	}
	if err := VerifyCosignBundle(blob, bundle, SFPanelReleaseIdentity()); err != nil {
		t.Fatalf("real v0.56.0 bundle failed verification: %v", err)
	}
}

// TestVerifyCosignBundle_GoldenPath_ChainForm proves the v0.1/v0.2
// x509CertificateChain form (leaf-first) is accepted too.
func TestVerifyCosignBundle_GoldenPath_ChainForm(t *testing.T) {
	blob := []byte("chain-form artifact")
	leafPEM, sigDER := signTestBlob(t, blob, testBundleSubject, testBundleIssuer)

	block, _ := pem.Decode(leafPEM)
	bundle := mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
		obj["verificationMaterial"] = map[string]any{
			"x509CertificateChain": map[string]any{
				"certificates": []map[string]any{
					{"rawBytes": base64.StdEncoding.EncodeToString(block.Bytes)},
				},
			},
		}
	})

	if err := VerifyCosignBundle(blob, bundle, testBundleIdentity); err != nil {
		t.Fatalf("VerifyCosignBundle chain form: %v", err)
	}
}

func TestVerifyCosignBundle_RejectsWrongSubject(t *testing.T) {
	blob := []byte("artifact body")
	leafPEM, sigDER := signTestBlob(t, blob,
		"https://github.com/attacker/Evil/.github/workflows/release.yml@refs/tags/v1.0.0", testBundleIssuer)
	bundle := mintTestBundleJSON(t, leafPEM, sigDER, blob, nil)

	err := VerifyCosignBundle(blob, bundle, testBundleIdentity)
	if err == nil {
		t.Fatal("expected SAN-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "SAN does not start with") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestVerifyCosignBundle_RejectsWrongIssuer(t *testing.T) {
	blob := []byte("artifact body")
	leafPEM, sigDER := signTestBlob(t, blob, testBundleSubject, "https://gitlab.example.com")
	bundle := mintTestBundleJSON(t, leafPEM, sigDER, blob, nil)

	err := VerifyCosignBundle(blob, bundle, testBundleIdentity)
	if err == nil {
		t.Fatal("expected issuer-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "OIDC issuer mismatch") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestVerifyCosignBundle_RejectsUntrustedCA signs with a rogue CA while the
// REAL embedded Fulcio bundle stays in place — the chain must not build.
func TestVerifyCosignBundle_RejectsUntrustedCA(t *testing.T) {
	blob := []byte("artifact body")
	rogueCA, rogueCAKey := mintTestCA(t)
	leafPEM, leafKey := mintTestLeaf(t, rogueCAKey, rogueCA, testBundleSubject, testBundleIssuer)
	digest := sha256.Sum256(blob)
	sigDER, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	bundle := mintTestBundleJSON(t, leafPEM, sigDER, blob, nil)

	err := VerifyCosignBundle(blob, bundle, testBundleIdentity)
	if err == nil {
		t.Fatal("expected chain-verification error, got nil")
	}
	if !strings.Contains(err.Error(), "chain verification failed") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestVerifyCosignBundle_RejectsTamperedBlob(t *testing.T) {
	original := []byte("trusted checksums")
	tampered := []byte("evil checksums")
	leafPEM, sigDER := signTestBlob(t, original, testBundleSubject, testBundleIssuer)

	// (a) digest field rewritten to match the tampered blob, signature still
	// over the original → the ECDSA check must catch it.
	tamperedDigest := sha256.Sum256(tampered)
	bundleFixedDigest := mintTestBundleJSON(t, leafPEM, sigDER, original, func(obj map[string]any) {
		ms := obj["messageSignature"].(map[string]any)
		ms["messageDigest"].(map[string]any)["digest"] = base64.StdEncoding.EncodeToString(tamperedDigest[:])
	})
	err := VerifyCosignBundle(tampered, bundleFixedDigest, testBundleIdentity)
	if err == nil {
		t.Fatal("expected signature-verification error, got nil")
	}
	if !strings.Contains(err.Error(), "signature verification failed") {
		t.Errorf("wrong error: %v", err)
	}

	// (b) digest field left at the original → the fast digest check catches
	// the mismatch before any crypto runs.
	bundleOrigDigest := mintTestBundleJSON(t, leafPEM, sigDER, original, nil)
	err = VerifyCosignBundle(tampered, bundleOrigDigest, testBundleIdentity)
	if err == nil {
		t.Fatal("expected digest-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "bundle digest does not match blob") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestVerifyCosignBundle_RejectsMalformed(t *testing.T) {
	blob := []byte("artifact body")
	leafPEM, sigDER := signTestBlob(t, blob, testBundleSubject, testBundleIssuer)

	cases := []struct {
		name    string
		bundle  []byte
		wantErr string
	}{
		{"truncated JSON", []byte(`{"mediaType":"application/vnd.dev.sigstore.bun`), "parse bundle"},
		{"wrong media type", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			obj["mediaType"] = "application/json"
		}), "unexpected bundle media type"},
		{"no message signature", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			delete(obj, "messageSignature")
		}), "no message signature"},
		{"empty signature", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			obj["messageSignature"].(map[string]any)["signature"] = ""
		}), "no message signature"},
		{"dsse envelope", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			obj["dsseEnvelope"] = map[string]any{"payload": "eyJ9"}
		}), "DSSE envelope"},
		{"key-based bundle", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			obj["verificationMaterial"] = map[string]any{"publicKey": map[string]any{"hint": "k"}}
		}), "no certificate"},
		{"garbage cert DER", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			obj["verificationMaterial"] = map[string]any{"certificate": map[string]any{
				"rawBytes": base64.StdEncoding.EncodeToString([]byte("not a certificate")),
			}}
		}), "parse bundle cert"},
		{"unsupported digest algorithm", mintTestBundleJSON(t, leafPEM, sigDER, blob, func(obj map[string]any) {
			obj["messageSignature"].(map[string]any)["messageDigest"].(map[string]any)["algorithm"] = "SHA2_384"
		}), "unsupported bundle digest algorithm"},
		{"empty bundle", nil, "empty bundle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyCosignBundle(blob, tc.bundle, testBundleIdentity)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
