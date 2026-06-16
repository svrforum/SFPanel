package compose

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
