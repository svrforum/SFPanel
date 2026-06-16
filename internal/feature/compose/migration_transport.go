package compose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
)

// packageDefinitionBundle writes a definition-only migration bundle (manifest +
// compose file + optional .env + extra referenced files) to w. Later milestones
// add bind-dir and named-volume data entries through the same BundleWriter.
func packageDefinitionBundle(w io.Writer, m MigrationManifest, stackDir string) error {
	bw := NewBundleWriter(w)
	if err := bw.WriteManifest(m); err != nil {
		return err
	}
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
			continue // best-effort for a missing referenced file in M1
		}
		if err := bw.WriteFile("compose/"+extra, data, 0o600); err != nil {
			return err
		}
	}
	return bw.Close()
}

// receiveBundle reads a bundle stream, verifies its SHA-256 matches expectedSha
// (sent out-of-band as a transfer header), and returns the parsed bundle. The
// hash covers the ENTIRE stream, so the tee is drained after parsing. A mismatch
// (or any parse/traversal error) yields an error and no usable files.
func receiveBundle(r io.Reader, expectedSha, stagingDir string) (MigrationManifest, map[string][]byte, error) {
	h := sha256.New()
	tee := io.TeeReader(r, h)
	m, files, err := NewBundleReader(tee).ReadAll(stagingDir)
	if err != nil {
		return m, files, err
	}
	if _, derr := io.Copy(io.Discard, tee); derr != nil {
		return m, nil, derr
	}
	got := hex.EncodeToString(h.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(got), []byte(expectedSha)) != 1 {
		return m, nil, fmt.Errorf("bundle hash mismatch")
	}
	return m, files, nil
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

// pushBundleToTarget streams an already-built bundle to the target node's
// migrate-import endpoint with internal-proxy auth + the SHA header. Returns the
// target's HTTP status + body. mTLS via the cluster client TLS config.
func (h *Handler) pushBundleToTarget(ctx context.Context, targetNodeID, username string, bundle []byte, sha string) (int, []byte, error) {
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
	client := &http.Client{Timeout: 30 * time.Minute, Transport: transport}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(bundle))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(migrationShaHeader, sha)
	req.ContentLength = int64(len(bundle))
	if secret := mgr.ProxySecret(); secret != "" {
		req.Header.Set(auth.InternalProxyHeader, secret)
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
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, nil
}
