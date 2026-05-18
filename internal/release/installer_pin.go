package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// VerifyInstaller checks the SHA-256 of an on-disk installer script against
// an operator-set env var. When the env var is empty/unset, the install
// proceeds with a slog.Warn (matches the documented "track-latest by default"
// stance in CLAUDE.md). When the env var is set, a mismatch returns an error
// and the caller is expected to abort the install.
//
//   - path     — filesystem path of the downloaded installer
//   - envVar   — name of the operator-set hash env var (e.g.
//                "SFPANEL_DOCKER_INSTALLER_SHA256")
//   - installer — short label used in the warning/error message
//                (e.g. "docker", "claude", "nvm", "tailscale")
//
// The expected hash is hex-encoded SHA-256; comparison is case-insensitive
// and leading/trailing whitespace is tolerated.
func VerifyInstaller(path, envVar, installer string) error {
	expected := strings.TrimSpace(os.Getenv(envVar))
	if expected == "" {
		slog.Warn("installer hash not pinned, track-latest mode",
			"installer", installer,
			"env_var", envVar,
			"hint", "set "+envVar+" to a sha256 hex digest to enable verification")
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s installer for verify: %w", installer, err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("hash %s installer: %w", installer, err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))

	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("%s installer SHA-256 mismatch: expected %s, got %s",
			installer, strings.ToLower(expected), actual)
	}
	slog.Info("installer hash verified", "installer", installer)
	return nil
}
