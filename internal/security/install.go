package security

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/release"
)

// Filesystem locations for the cosign binary. The default lives under
// /var/lib/sfpanel so it survives across upgrades and is owned by the
// service user; the fallback lives under /etc/sfpanel for air-gapped
// operators who pre-stage the binary themselves.
const (
	DefaultCosignPath  = "/var/lib/sfpanel/bin/cosign"
	FallbackCosignPath = "/etc/sfpanel/cosign"
	// MinCosignVersion is the minimum acceptable major.minor of an
	// already-present cosign binary. Cosign v2 introduced the keyless flow
	// shape and `verify-blob --certificate-identity-regexp` flags we rely
	// on; any v2.x is fine.
	MinCosignVersion = "2.0"
	// DefaultCosignReleaseTag is the version we self-bootstrap when no
	// existing binary is present. Operators can override via the
	// SFPANEL_COSIGN_VERSION env var (e.g. "v2.5.0").
	DefaultCosignReleaseTag = "v2.4.1"

	// cosignReleaseAsset, cosignSigAsset, cosignCertAsset name the artifacts
	// we download from the GitHub release page. Sigstore's release pipeline
	// publishes a keyless signature + certificate pair alongside each
	// platform binary.
	cosignReleaseAsset = "cosign-linux-amd64"
	cosignSigAsset     = "cosign-linux-amd64-keyless.sig"
	cosignCertAsset    = "cosign-linux-amd64-keyless.pem"

	// cosignDownloadTimeout caps any single GitHub asset download. Each
	// asset is small (cosign-linux-amd64 is ~80MB; sig/cert are < 8 KiB)
	// so 60s is generous on a slow link but stops a hung CDN connection.
	cosignDownloadTimeout = 60 * time.Second

	// cosignMaxBodyBytes caps the response body size when fetching
	// release assets. The cosign binary is well under this; the cap exists
	// to guard against a malicious or malfunctioning mirror returning an
	// unbounded stream.
	cosignMaxBodyBytes = 200 * 1024 * 1024 // 200 MiB
)

// Installer self-bootstraps a verified cosign binary when one isn't
// already present on the host. It is intentionally state-less — the
// caller wires Cmd/Path/FallbackPath/Get and EnsureCosign does the rest.
//
// All four fields are optional; sensible defaults are filled in at
// EnsureCosign time. The Get hook lets tests substitute the network
// without a real HTTP server.
type Installer struct {
	Cmd          exec.Commander
	Path         string                                                 // override DefaultCosignPath
	FallbackPath string                                                 // override FallbackCosignPath
	Get          func(ctx context.Context, url string) ([]byte, error) // override default HTTP GET
}

// EnsureCosign returns the path to a working cosign binary. It tries, in
// order:
//
//  1. An already-installed binary at i.Path (or DefaultCosignPath) whose
//     `cosign version` reports >= MinCosignVersion.
//  2. A network install: download cosign + signature + cert from
//     sigstore/cosign GitHub Releases, verify the blob via
//     release.VerifyCosignBlob with the pinned sigstoreReleaseIdentity,
//     atomically install to i.Path.
//  3. A pre-staged binary at i.FallbackPath (or FallbackCosignPath) for
//     air-gapped operators.
//
// On total failure the error wraps ErrCosignInstallFailed so callers can
// errors.Is-detect a clean refusal versus a transient hiccup.
func (i *Installer) EnsureCosign(ctx context.Context) (string, error) {
	path := i.Path
	if path == "" {
		path = DefaultCosignPath
	}
	fallback := i.FallbackPath
	if fallback == "" {
		fallback = FallbackCosignPath
	}
	get := i.Get
	if get == nil {
		get = httpGet
	}

	// 1) Already installed?
	if ok, _ := i.cosignVersionOK(path); ok {
		slog.Debug("cosign already installed", "path", path)
		return path, nil
	}

	// 2) Try the network install. Capture the original failure so we can
	// surface it when the fallback also misses.
	netErr := i.installFromNetwork(ctx, path, get)
	if netErr == nil {
		if ok, vErr := i.cosignVersionOK(path); ok {
			return path, nil
		} else {
			// Sanity check failed after install — something is very wrong
			// (binary corrupted post-rename, or wrong arch). Remove the
			// bad copy so the next attempt re-downloads.
			_ = os.Remove(path)
			netErr = fmt.Errorf("post-install version check failed: %w", vErr)
		}
	}

	// 3) Fallback to /etc/sfpanel/cosign for air-gapped operators.
	if ok, _ := i.cosignVersionOK(fallback); ok {
		slog.Info("cosign network install failed; using fallback", "fallback", fallback, "network_error", netErr)
		return fallback, nil
	}

	return "", fmt.Errorf("%w: network install: %v; fallback %q unavailable",
		ErrCosignInstallFailed, netErr, fallback)
}

// cosignVersionOK runs `<path> version` and reports whether the major.minor
// is >= MinCosignVersion. Returns (false, nil) for any IO/exec error so
// callers can treat "missing" and "broken" the same way; the error return
// is reserved for cases where the version string itself is unparsable
// (still treated as "not OK").
func (i *Installer) cosignVersionOK(path string) (bool, error) {
	if path == "" {
		return false, errors.New("empty path")
	}
	if _, err := os.Stat(path); err != nil {
		return false, err
	}
	out, err := i.Cmd.RunWithTimeout(10*time.Second, path, "version")
	if err != nil {
		return false, fmt.Errorf("exec %s version: %w", path, err)
	}
	v, err := parseCosignVersion(out)
	if err != nil {
		return false, err
	}
	return versionAtLeast(v, MinCosignVersion), nil
}

// cosignVersionRE matches the major.minor.patch in `cosign version` output.
// Cosign prints a multi-line block that begins with e.g.
//
//	"cosign version v2.4.1"
//
// or, in v2.x release builds, a "GitVersion: v2.4.1" line. We accept either.
var cosignVersionRE = regexp.MustCompile(`(?m)\b(?:cosign version|GitVersion:)\s+v?(\d+\.\d+(?:\.\d+)?)`)

func parseCosignVersion(out string) (string, error) {
	m := cosignVersionRE.FindStringSubmatch(out)
	if len(m) < 2 {
		return "", fmt.Errorf("could not parse cosign version from output: %q", strings.TrimSpace(out))
	}
	return m[1], nil
}

// versionAtLeast compares dotted major.minor[.patch] strings. Missing
// components are treated as 0. Non-numeric components fall back to 0
// rather than erroring — we only care that we got a v2+ binary, not the
// exact patch level.
func versionAtLeast(have, want string) bool {
	hp := splitVersion(have)
	wp := splitVersion(want)
	for i := 0; i < len(wp); i++ {
		var h int
		if i < len(hp) {
			h = hp[i]
		}
		if h > wp[i] {
			return true
		}
		if h < wp[i] {
			return false
		}
	}
	return true
}

func splitVersion(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			out[i] = 0
			continue
		}
		out[i] = n
	}
	return out
}

// installFromNetwork downloads cosign + sig + cert from the GitHub
// release page for the configured tag, verifies the blob, and atomically
// installs to dest. On any verification failure all three blobs are
// dropped from memory and dest is not written.
func (i *Installer) installFromNetwork(ctx context.Context, dest string, get func(ctx context.Context, url string) ([]byte, error)) error {
	tag := os.Getenv("SFPANEL_COSIGN_VERSION")
	if tag == "" {
		tag = DefaultCosignReleaseTag
	}
	base := "https://github.com/sigstore/cosign/releases/download/" + tag + "/"

	slog.Info("downloading cosign", "tag", tag, "dest", dest)

	binBlob, err := get(ctx, base+cosignReleaseAsset)
	if err != nil {
		return fmt.Errorf("download %s: %w", cosignReleaseAsset, err)
	}
	sigBlob, err := get(ctx, base+cosignSigAsset)
	if err != nil {
		return fmt.Errorf("download %s: %w", cosignSigAsset, err)
	}
	certBlob, err := get(ctx, base+cosignCertAsset)
	if err != nil {
		return fmt.Errorf("download %s: %w", cosignCertAsset, err)
	}

	if err := release.VerifyCosignBlob(binBlob, sigBlob, certBlob, sigstoreReleaseIdentity); err != nil {
		return fmt.Errorf("verify cosign blob: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, binBlob, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, dest, err)
	}
	return nil
}

// httpGet is the default `Get` used by EnsureCosign. It applies a fixed
// per-request timeout, caps the body size at cosignMaxBodyBytes, and
// returns the bytes read or any IO/HTTP error.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, cosignDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cosignMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return body, nil
}
