package compose

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
)

// writeComposeEntries writes the small definition files (compose + optional .env
// + extra referenced files) into an open bundle. Shared by the definition-only
// and full bundles.
func writeComposeEntries(bw *BundleWriter, m MigrationManifest, stackDir string) error {
	composeData, err := os.ReadFile(filepath.Join(stackDir, m.ComposeFile))
	if err != nil {
		return fmt.Errorf("read compose: %w", err)
	}
	if err := bw.WriteFile("compose/"+m.ComposeFile, composeData, 0o600); err != nil {
		return err
	}
	if m.HasEnv {
		if env, rerr := os.ReadFile(filepath.Join(stackDir, ".env")); rerr == nil {
			if err := bw.WriteFile("compose/.env", env, 0o600); err != nil {
				return err
			}
		}
	}
	for _, extra := range m.ExtraFiles {
		data, rerr := os.ReadFile(filepath.Join(stackDir, extra))
		if rerr != nil {
			continue // best-effort for a missing referenced file
		}
		if err := bw.WriteFile("compose/"+extra, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// packageDefinitionBundle writes a definition-only migration bundle (manifest +
// compose file + optional .env + extra referenced files) to w.
func packageDefinitionBundle(w io.Writer, m MigrationManifest, stackDir string) error {
	bw := NewBundleWriter(w)
	if err := bw.WriteManifest(m); err != nil {
		return err
	}
	if err := writeComposeEntries(bw, m, stackDir); err != nil {
		return err
	}
	return bw.Close()
}

// shortHash is a 12-hex-char digest of an identifier, used to give each bind /
// image archive a unique, path-safe bundle entry name.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// packageFullBundle archives the stack's volume data, copied bind dirs, and
// images into tmpDir, fills the manifest's data fields (Archive/Bytes/Sha256),
// then writes the complete bundle to bundlePath and returns its whole-stream
// sha256 (computed while writing, so the caller needn't re-read GB of bundle).
// Source data is only READ — a failure leaves the source untouched.
func packageFullBundle(ctx context.Context, bundlePath string, m *MigrationManifest, stackDir, tmpDir string) (string, error) {
	type entry struct{ name, path string }
	var entries []entry

	for i := range m.Volumes {
		v := &m.Volumes[i]
		if !v.Copy {
			continue
		}
		arc := filepath.Join(tmpDir, fmt.Sprintf("vol-%d.tar", i))
		n, sha, err := archiveVolumeToFile(ctx, v.Docker, arc)
		if err != nil {
			return "", fmt.Errorf("archive volume %s: %w", v.Docker, err)
		}
		v.Archive = "volumes/" + shortHash(v.Docker) + ".tar"
		v.Bytes, v.Sha256 = n, sha
		entries = append(entries, entry{v.Archive, arc})
	}
	for i := range m.Binds {
		b := &m.Binds[i]
		if !b.Copy {
			continue
		}
		arc := filepath.Join(tmpDir, fmt.Sprintf("bind-%d.tar", i))
		n, sha, err := archiveBindToFile(ctx, b.Host, arc)
		if errors.Is(err, errSkipSpecial) {
			b.Copy = false // socket/device/missing — restore must not expect data
			continue
		}
		if err != nil {
			return "", fmt.Errorf("archive bind %s: %w", b.Host, err)
		}
		b.Archive = "binds/" + shortHash(b.Host) + ".tar"
		b.Bytes, b.Sha256 = n, sha
		if b.Kind == "in-stack" {
			if rel, rerr := filepath.Rel(stackDir, b.Host); rerr == nil {
				b.Rel = rel // placed under the TARGET's stack dir on restore
			}
		}
		entries = append(entries, entry{b.Archive, arc})
	}
	for i := range m.Images {
		im := &m.Images[i]
		arc := filepath.Join(tmpDir, fmt.Sprintf("img-%d.tar", i))
		n, sha, err := archiveImageToFile(ctx, im.Ref, arc)
		if err != nil {
			return "", fmt.Errorf("archive image %s: %w", im.Ref, err)
		}
		im.Archive = "images/" + shortHash(im.Ref) + ".tar"
		im.Bytes, im.Sha256 = n, sha
		entries = append(entries, entry{im.Archive, arc})
	}

	bf, err := os.Create(bundlePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = bf.Close() }()
	hsh := sha256.New()
	bw := NewBundleWriter(io.MultiWriter(bf, hsh))
	if err := bw.WriteManifest(*m); err != nil {
		return "", err
	}
	if err := writeComposeEntries(bw, *m, stackDir); err != nil {
		return "", err
	}
	for _, e := range entries {
		if err := bw.WriteFileFromPath(e.name, e.path, 0o600); err != nil {
			return "", err
		}
	}
	if err := bw.Close(); err != nil {
		return "", err
	}
	if err := bf.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hsh.Sum(nil)), nil
}

// receiveBundle reads a bundle stream, verifies its SHA-256 matches expectedSha
// (sent out-of-band as a transfer header), and returns the parsed bundle. The
// hash covers the ENTIRE stream, so the tee is drained after parsing. A mismatch
// (or any parse/traversal error) yields an error and no usable files.
func receiveBundle(r io.Reader, expectedSha, stagingDir string) (MigrationManifest, map[string][]byte, map[string]string, error) {
	h := sha256.New()
	tee := io.TeeReader(r, h)
	m, files, staged, err := NewBundleReader(tee).ReadAll(stagingDir)
	if err != nil {
		return m, files, staged, err
	}
	if _, derr := io.Copy(io.Discard, tee); derr != nil {
		return m, nil, nil, derr
	}
	got := hex.EncodeToString(h.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(got), []byte(expectedSha)) != 1 {
		return m, nil, nil, fmt.Errorf("bundle hash mismatch")
	}
	return m, files, staged, nil
}

// buildDefinitionManifest assembles the M1 (definition-only) manifest. Binds/
// Volumes/Images stay empty until later milestones.
func buildDefinitionManifest(stackID, composeFile string, hasEnv bool, sourceID, sourceArch, targetID string, d Disposition, overwrite bool) MigrationManifest {
	return MigrationManifest{
		SchemaVersion:      1,
		StackID:            stackID,
		ComposeProjectName: stackID,
		Source:             NodeRef{NodeID: sourceID, Arch: sourceArch},
		Target:             NodeRef{NodeID: targetID},
		ComposeFile:        composeFile,
		HasEnv:             hasEnv,
		Disposition:        d,
		Overwrite:          overwrite,
	}
}

// migrationNodeBaseURL mirrors the proxy middleware: panels are plain HTTP by
// default (TLS is a reverse proxy's job); honor an explicit scheme if stored.
func migrationNodeBaseURL(apiAddr string) string {
	if strings.HasPrefix(apiAddr, "http://") || strings.HasPrefix(apiAddr, "https://") {
		return apiAddr
	}
	return "http://" + apiAddr
}

// progressReader wraps the bundle stream and reports cumulative bytes sent at
// coarse intervals (~5% steps, min 16 MiB) so a multi-GB transfer surfaces
// progress in the SSE stream without flooding it.
type progressReader struct {
	r        io.Reader
	total    int64
	read     int64
	nextEmit int64
	step     int64
	onProg   func(sent, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.onProg != nil && (p.read >= p.nextEmit || (err == io.EOF && p.read > 0)) {
		for p.nextEmit <= p.read {
			p.nextEmit += p.step
		}
		p.onProg(p.read, p.total)
	}
	return n, err
}

// throttledReader caps read throughput at bps bytes/sec (0 = unlimited) so a
// large migration can be rate-limited (QoS) and not saturate the link — useful
// over a metered/shared WAN (e.g. Tailscale). It paces by comparing cumulative
// bytes against the time they "should" have taken, sleeping the difference; the
// sleep is ctx-cancellable so a cancelled migration doesn't wait it out.
type throttledReader struct {
	r     io.Reader
	bps   int64
	ctx   context.Context
	start time.Time
	sent  int64
}

func (t *throttledReader) Read(b []byte) (int, error) {
	n, err := t.r.Read(b)
	if n > 0 && t.bps > 0 {
		if t.start.IsZero() {
			t.start = time.Now()
		}
		t.sent += int64(n)
		expected := time.Duration(float64(t.sent) / float64(t.bps) * float64(time.Second))
		if d := expected - time.Since(t.start); d > 0 {
			select {
			case <-t.ctx.Done():
				return n, t.ctx.Err()
			case <-time.After(d):
			}
		}
	}
	return n, err
}

// pushBundle streams body (length contentLength) to the target node's
// migrate-import endpoint with internal-proxy auth + the SHA header, returning
// the target's HTTP status + body. mTLS via the cluster client TLS config. No
// fixed client Timeout — a multi-GB transfer can outlast any deadline; the
// caller's opCtx bounds (and cancels) the whole migration instead. onProgress
// (optional) is called with cumulative/total bytes as the body streams out.
// rateBytesPerSec > 0 caps transfer throughput (QoS).
func (h *Handler) pushBundle(ctx context.Context, targetNodeID, username string, body io.Reader, contentLength int64, sha string, onProgress func(sent, total int64), rateBytesPerSec int64) (int, []byte, error) {
	mgr := h.clusterManager()
	if mgr == nil {
		return 0, nil, fmt.Errorf("cluster is not enabled")
	}
	node := mgr.GetNode(targetNodeID)
	if node == nil {
		return 0, nil, fmt.Errorf("unknown target node %q", targetNodeID)
	}
	const importPath = "/api/v1/docker/compose/migrate-import"
	target := migrationNodeBaseURL(node.APIAddress) + importPath

	tlsCfg := &tls.Config{}
	if cfg, err := mgr.GetTLS().ClientTLSConfig(); err == nil && cfg != nil {
		tlsCfg = cfg.Clone()
	}
	transport := &http.Transport{TLSClientConfig: tlsCfg}
	defer transport.CloseIdleConnections() // one-shot push: don't leak the kept-alive conn
	client := &http.Client{Transport: transport}

	reqBody := body
	if rateBytesPerSec > 0 {
		reqBody = &throttledReader{r: reqBody, bps: rateBytesPerSec, ctx: ctx}
	}
	if onProgress != nil && contentLength > 0 {
		step := contentLength / 20
		if step < 16<<20 {
			step = 16 << 20
		}
		reqBody = &progressReader{r: reqBody, total: contentLength, step: step, nextEmit: step, onProg: onProgress}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, reqBody)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(migrationShaHeader, sha)
	req.ContentLength = contentLength
	if mgr.ProxySecret() != "" {
		if v2 := auth.SignProxyRequestV2(http.MethodPost, importPath); v2 != "" {
			req.Header.Set(auth.InternalProxyHeaderV2, v2)
		}
	}
	if username != "" {
		req.Header.Set("X-SFPanel-Original-User", username)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// pushBundleFileToTarget streams a staged bundle file (no in-memory copy) to the
// target's migrate-import endpoint. onProgress (optional) reports bytes sent;
// rateBytesPerSec > 0 caps transfer throughput.
func (h *Handler) pushBundleFileToTarget(ctx context.Context, targetNodeID, username, bundlePath, sha string, onProgress func(sent, total int64), rateBytesPerSec int64) (int, []byte, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return 0, nil, err
	}
	return h.pushBundle(ctx, targetNodeID, username, f, fi.Size(), sha, onProgress, rateBytesPerSec)
}
