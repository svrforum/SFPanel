package security

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
)

// Filesystem locations consulted by EnsureCosign:
//   - DefaultCosignPath / FallbackCosignPath: SFPanel-managed locations.
//   - System PATH (/usr/bin, /usr/local/bin): apt-installed binary lands here.
const (
	DefaultCosignPath  = "/var/lib/sfpanel/bin/cosign"
	FallbackCosignPath = "/etc/sfpanel/cosign"

	// MinCosignVersion is the minimum acceptable major.minor. Cosign v2
	// introduced the keyless flow shape and the
	// `--certificate-identity-regexp` flag the verifier relies on; any
	// v2.x is acceptable.
	MinCosignVersion = "2.0"

	// cosignAptPackage is the package name we install via apt when no
	// existing cosign is found. Ubuntu 24.04 ships v2.x; older releases
	// without the package fall through to the operator-managed fallback.
	cosignAptPackage = "cosign"
)

// Installer locates a usable cosign binary, installing it via apt on
// first call when missing. Trust chain pivots from "cosign self-signed
// by sigstore" to "Ubuntu archive signing key" — pragmatic given the
// upstream cosign release identity (Google service account email SAN)
// is not directly verifiable by SFPanel's existing keyless verifier
// (which expects URI SANs as used by SFPanel's own GitHub Actions
// release pipeline).
//
// Operators in air-gapped environments can pre-stage a binary at
// FallbackCosignPath; it will be used without any apt call.
type Installer struct {
	Cmd  commonExec.Commander
	Path string // override DefaultCosignPath
	// FallbackPath, when non-empty, is consulted before any apt install.
	// Defaults to FallbackCosignPath.
	FallbackPath string
}

// EnsureCosign returns the path to a working cosign binary. Order of
// preference:
//
//  1. i.Path (or DefaultCosignPath) if present and version-OK.
//  2. i.FallbackPath (or FallbackCosignPath) if present and version-OK
//     — for air-gapped operators who pre-stage their own binary.
//  3. A binary already on $PATH (e.g. /usr/bin/cosign from a prior
//     install) if version-OK.
//  4. Run `apt-get install -y cosign` and re-resolve via $PATH.
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

	if ok, _ := i.cosignVersionOK(path); ok {
		return path, nil
	}
	if ok, _ := i.cosignVersionOK(fallback); ok {
		return fallback, nil
	}
	if sys := i.systemCosign(); sys != "" {
		if ok, _ := i.cosignVersionOK(sys); ok {
			return sys, nil
		}
	}

	// Install via apt.
	slog.Info("installing cosign via apt", "component", "security", "package", cosignAptPackage)
	if i.Cmd != nil {
		out, err := i.Cmd.RunWithEnv(commonExec.AptEnv(), "apt-get", "install", "-y", cosignAptPackage)
		if err != nil {
			return "", fmt.Errorf("%w: apt install: %s: %w",
				ErrCosignInstallFailed, strings.TrimSpace(out), err)
		}
	}

	// Re-resolve after install.
	if sys := i.systemCosign(); sys != "" {
		if ok, vErr := i.cosignVersionOK(sys); ok {
			return sys, nil
		} else if vErr != nil {
			return "", fmt.Errorf("%w: post-apt sanity check: %w", ErrCosignInstallFailed, vErr)
		}
	}
	return "", fmt.Errorf("%w: apt install completed but no cosign found on PATH", ErrCosignInstallFailed)
}

// systemCosign looks up cosign on $PATH and returns its absolute path,
// or "" if not found. Wraps exec.LookPath so tests can stub via
// commonExec.Commander when needed.
func (i *Installer) systemCosign() string {
	p, err := exec.LookPath("cosign")
	if err != nil {
		return ""
	}
	return p
}

// LocateCosign returns the path to a usable cosign binary WITHOUT
// triggering an install. Used by status endpoints to report what the
// next call to EnsureCosign would resolve. Returns "" if no cosign is
// currently locatable.
func (i *Installer) LocateCosign() string {
	path := i.Path
	if path == "" {
		path = DefaultCosignPath
	}
	fallback := i.FallbackPath
	if fallback == "" {
		fallback = FallbackCosignPath
	}
	if ok, _ := i.cosignVersionOK(path); ok {
		return path
	}
	if ok, _ := i.cosignVersionOK(fallback); ok {
		return fallback
	}
	if sys := i.systemCosign(); sys != "" {
		if ok, _ := i.cosignVersionOK(sys); ok {
			return sys
		}
	}
	return ""
}

// cosignVersionOK runs `<path> version` and reports whether the binary
// is usable. Strict version parsing is best-effort: apt-built cosign on
// Ubuntu reports `GitVersion: devel` (no ldflags), so we accept any
// binary that:
//   - is at least MinCosignVersion if the version string parses, OR
//   - prints recognizable cosign output (banner / "GitVersion:" line /
//     "cosign version" line) when the version string is non-numeric.
//
// (false, nil) for any IO/exec error so callers treat "missing" and
// "broken" the same way.
func (i *Installer) cosignVersionOK(path string) (bool, error) {
	if path == "" {
		return false, errors.New("empty path")
	}
	if _, err := os.Stat(path); err != nil {
		return false, err
	}
	if i.Cmd == nil {
		return false, errors.New("no commander configured")
	}
	out, err := i.Cmd.RunWithTimeout(10*time.Second, path, "version")
	if err != nil {
		return false, fmt.Errorf("exec %s version: %w", path, err)
	}
	if v, perr := parseCosignVersion(out); perr == nil {
		return versionAtLeast(v, MinCosignVersion), nil
	}
	// Version unparseable but cosign ran: accept if output looks like
	// cosign's own banner/help (apt builds without ldflags hit this).
	low := strings.ToLower(out)
	if strings.Contains(low, "cosign") && (strings.Contains(low, "gitversion") || strings.Contains(low, "container signing")) {
		return true, nil
	}
	return false, fmt.Errorf("could not recognize cosign output: %q", strings.TrimSpace(out))
}

// cosignVersionRE matches the major.minor.patch in `cosign version` output.
// Cosign prints either "cosign version v2.4.1" (one-line --version) or a
// multi-line block whose key line is "GitVersion: v2.4.1". Either matches.
var cosignVersionRE = regexp.MustCompile(`(?m)\b(?:cosign version|GitVersion:)\s+v?(\d+\.\d+(?:\.\d+)?)`)

func parseCosignVersion(out string) (string, error) {
	m := cosignVersionRE.FindStringSubmatch(out)
	if len(m) < 2 {
		return "", fmt.Errorf("could not parse cosign version from output: %q", strings.TrimSpace(out))
	}
	return m[1], nil
}

// versionAtLeast compares dotted major.minor[.patch] strings. Missing
// components count as 0; non-numeric components fall back to 0 too.
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
