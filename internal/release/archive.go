package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MaxArchiveBytes caps a downloaded release archive at 200 MiB. Keeping the
// limit on the wire (LimitReader) and on disk (size check) prevents a
// compromised release host from filling the disk during the verify step.
const MaxArchiveBytes int64 = 200 << 20

// archiveCap is the cap DownloadArchive applies: MaxArchiveBytes, which tests
// lower rather than serve 200 MiB.
var archiveCap = MaxArchiveBytes

// maxChecksumsBytes caps checksums.txt. A release lists a handful of
// archives in it; anything near this size is not a checksums file.
const maxChecksumsBytes = 64 * 1024

// DownloadPrefix is where this project's release assets are downloaded from,
// up to the version: an asset of tag v<version> is at
// DownloadPrefix + version + "/" + name. Tests point it elsewhere.
var DownloadPrefix = "https://github.com/svrforum/SFPanel/releases/download/v"

// ArchiveName is the release archive of version (without a "v") for
// linux/arch, as GoReleaser names it.
func ArchiveName(version, arch string) string {
	return fmt.Sprintf("sfpanel_%s_linux_%s.tar.gz", version, arch)
}

// TaggedRelease lists the assets tag v<version> publishes — the archive of
// each arch, checksums.txt and the three signatures over it — without asking
// GitHub's API, whose sixty unauthenticated requests an hour per address an
// install should not spend. An asset the release lacks answers 404 when it is
// fetched, which VerifiedChecksums and DownloadArchive report as a failed
// download.
func TaggedRelease(version string, arches ...string) *Release {
	names := make([]string, 0, len(arches)+4)
	for _, arch := range arches {
		names = append(names, ArchiveName(version, arch))
	}
	names = append(names, "checksums.txt", "checksums.txt.sig", "checksums.txt.pem", "checksums.txt.sigstore.json")
	rel := &Release{TagName: "v" + version}
	for _, name := range names {
		rel.Assets = append(rel.Assets, Asset{Name: name, BrowserDownloadURL: DownloadPrefix + version + "/" + name})
	}
	return rel
}

// VerifiedChecksums downloads checksums.txt from assets and returns it once
// its signature holds, so the hashes in it can be trusted. progress, which
// may be nil, hears each stage as it starts.
//
// Releases at or after SignatureRequiredSince MUST carry signature assets —
// missing them is refused to prevent a supply-chain downgrade where an
// attacker who controls the GitHub Release simply deletes the .sig/.pem to
// fall through to SHA-only (which is no defence at all when they can also
// rewrite checksums.txt). Pre-cutoff targets fall back to SHA-256 only so the
// one-time upgrade path from old unsigned releases still works. The gate is
// on current AND target: a node already running a signed release must never
// drop to unsigned SHA-only, even if the target claims a pre-cutoff version.
//
// Bundle-first: v0.56+ releases also publish checksums.txt.sigstore.json
// (cosign v3 Sigstore bundle). When the bundle asset exists it is the only
// accepted proof — a bad bundle hard-fails with NO fallback to .sig/.pem,
// so corruption can't be "retried" through a second path. When absent
// (all releases ≤ v0.55.0) the legacy pair applies, including the gate above
// — stripping the bundle asset therefore never weakens verification.
func VerifiedChecksums(ctx context.Context, c *http.Client, assets []Asset, current, target string, progress func(string)) ([]byte, error) {
	say := func(msg string) {
		if progress != nil {
			progress(msg)
		}
	}
	checksumsURL := FindAssetURL(assets, "checksums.txt")
	if checksumsURL == "" {
		return nil, errors.New("Release checksums.txt not found; refusing unsigned update")
	}

	say("Downloading checksums...")
	checksumReq, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("Checksum request build failed: %v", err)
	}
	checksumResp, err := c.Do(checksumReq)
	if err != nil {
		return nil, fmt.Errorf("Checksum download failed: %v", err)
	}
	defer checksumResp.Body.Close()
	if checksumResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Checksum download failed (HTTP %d)", checksumResp.StatusCode)
	}
	checksums, err := io.ReadAll(io.LimitReader(checksumResp.Body, maxChecksumsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("Checksum read failed: %v", err)
	}
	if len(checksums) > maxChecksumsBytes {
		return nil, fmt.Errorf("Checksum read failed: checksums.txt exceeds %d bytes", maxChecksumsBytes)
	}

	if bundleURL := FindAssetURL(assets, "checksums.txt.sigstore.json"); bundleURL != "" {
		say("Verifying release signature (Sigstore bundle)...")
		bundle, err := fetchSmall(ctx, c, bundleURL)
		if err != nil {
			return nil, fmt.Errorf("Signature bundle download failed: %v", err)
		}
		if err := VerifyCosignBundle(checksums, bundle, SFPanelReleaseIdentity()); err != nil {
			return nil, fmt.Errorf("Signature verification failed: %v", err)
		}
		return checksums, nil
	}

	sigURL, certURL := FindAssetURL(assets, "checksums.txt.sig"), FindAssetURL(assets, "checksums.txt.pem")
	if sigURL == "" || certURL == "" {
		if SignatureRequiredForUpdate(current, target) {
			return nil, fmt.Errorf(
				"Signature required: release %s is missing checksums.txt.sig or checksums.txt.pem (refusing update to prevent supply-chain downgrade)", target)
		}
		say("Release predates Sigstore signing; falling back to SHA-256 only")
		return checksums, nil
	}

	say("Verifying release signature (Sigstore keyless)...")
	sig, err := fetchSmall(ctx, c, sigURL)
	if err != nil {
		return nil, fmt.Errorf("Signature download failed: %v", err)
	}
	cert, err := fetchSmall(ctx, c, certURL)
	if err != nil {
		return nil, fmt.Errorf("Cert download failed: %v", err)
	}
	if err := VerifyCosignBlob(checksums, sig, cert, SFPanelReleaseIdentity()); err != nil {
		return nil, fmt.Errorf("Signature verification failed: %v", err)
	}
	return checksums, nil
}

// DownloadArchive downloads archiveName from assets into dir, which the
// caller owns and removes, and checks it against the SHA-256 checksums (from
// VerifiedChecksums) lists for it. It returns the archive's path; on any
// failure nothing is left in dir.
func DownloadArchive(ctx context.Context, c *http.Client, assets []Asset, archiveName string, checksums []byte, dir string) (string, error) {
	body, err := OpenArchive(ctx, c, assets, archiveName)
	if err != nil {
		return "", err
	}
	defer body.Close()
	return SaveArchive(body, archiveName, checksums, dir)
}

// OpenArchive starts the download of archiveName from assets and returns its
// body, which the caller closes. The self-update opens it before verifying
// checksums.txt and reads it after, so OpenArchive and SaveArchive are the two
// halves of DownloadArchive.
func OpenArchive(ctx context.Context, c *http.Client, assets []Asset, archiveName string) (io.ReadCloser, error) {
	url := FindAssetURL(assets, archiveName)
	if url == "" {
		return nil, fmt.Errorf("Release asset not found: %s", archiveName)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("Download request build failed: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Download failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("Download failed (HTTP %d)", resp.StatusCode)
	}
	return resp.Body, nil
}

// SaveArchive streams body into dir/archiveName under the archive cap and
// checks it against the SHA-256 checksums lists for that name. It returns the
// archive's path; on any failure nothing is left in dir. The archive goes to
// disk rather than memory: small (256–512 MB) cluster nodes were OOM-killed
// mid-update when it was read whole.
func SaveArchive(body io.Reader, archiveName string, checksums []byte, dir string) (string, error) {
	// The name is joined to dir, so it must be one file name: an asset list
	// naming "../x" would otherwise write beside dir instead of in it.
	if archiveName == "" || archiveName == "." || archiveName == ".." || strings.ContainsAny(archiveName, `/\`) {
		return "", fmt.Errorf("Archive name %q is not a file name", archiveName)
	}
	expected, err := ParseExpectedSHA256(checksums, archiveName)
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, archiveName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("Cannot open archive temp: %v", err)
	}
	fail := func(err error) (string, error) {
		os.Remove(path)
		return "", err
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(body, archiveCap+1))
	closeErr := f.Close()
	if err != nil {
		return fail(fmt.Errorf("Download read failed: %v", err))
	}
	if closeErr != nil {
		return fail(fmt.Errorf("Archive flush failed: %v", closeErr))
	}
	if written > archiveCap {
		return fail(fmt.Errorf("Archive exceeds %d bytes, refusing to install", archiveCap))
	}
	if hex.EncodeToString(hasher.Sum(nil)) != expected {
		return fail(errors.New("Checksum verification failed"))
	}
	return path, nil
}

// fetchSmall GETs a signature-sized asset into memory, cut at 64 KiB.
func fetchSmall(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64*1024))
}
