# A3: 3rd-party installer hash verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` (inline). Steps use checkbox (`- [ ]`) syntax.

**Goal:** Close [R-final](../research/2026-05-18-module-hardening/R-final.md) P0-5 — the 2026-04-19 P0 carry-forward where `packages` (Docker, Claude, NVM) and `network/tailscale` fetch + execute remote install scripts without SHA256 verification. CLAUDE.md's "operators who want deterministic builds should vendor a pinned copy and set the corresponding env var" promise becomes real here.

**Architecture:** New `internal/release/installer_pin.go` helper + one-line caller updates at the 4 download sites. Soft-pass when env var unset (preserves CLAUDE.md "track-latest by default" stance), hard-fail on mismatch when set.

**Tech Stack:** Go 1.25, `crypto/sha256`, `os`, `log/slog`. No new dependencies.

**Out of scope:**
- Signing the installers (would require trusting a key chain we don't currently manage).
- Pre-pinning canonical hashes in source (these are floating-latest scripts; we don't track them).
- Migrating Docker/NVM/Claude/Tailscale install away from upstream scripts (separate plan if ever).

---

## File structure

- **Create** `internal/release/installer_pin.go` — single exported function `VerifyInstaller(path, envVar, installer string) error` + logger.
- **Create** `internal/release/installer_pin_test.go` — table-driven tests covering unset/match/mismatch + tampered-file detection.
- **Modify** `internal/feature/packages/handler.go` — insert verify call after each of the 3 curl downloads (Docker / NVM / Claude).
- **Modify** `internal/feature/network/tailscale.go` — insert verify call after the curl download.

---

## Task 1 — Write `internal/release/installer_pin.go` + tests

**Files:**
- Create: `internal/release/installer_pin.go`
- Create: `internal/release/installer_pin_test.go`

- [ ] **Step 1.1: Write the failing tests first**

Create `internal/release/installer_pin_test.go`:

```go
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// hashOf returns the hex-encoded SHA-256 of b. Helper for tests only.
func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVerifyInstaller_EnvUnsetPassesWithWarning(t *testing.T) {
	// When the operator hasn't pinned a hash, we must NOT block the install —
	// the documented stance is "track-latest by default".
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	if err := os.WriteFile(tmp, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, "") // explicit empty

	if err := VerifyInstaller(tmp, envVar, "test"); err != nil {
		t.Fatalf("expected nil error when env unset, got %v", err)
	}
}

func TestVerifyInstaller_MatchPasses(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	content := []byte("echo hello\n")
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, hashOf(content))

	if err := VerifyInstaller(tmp, envVar, "test"); err != nil {
		t.Fatalf("expected nil error on hash match, got %v", err)
	}
}

func TestVerifyInstaller_MismatchFails(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	if err := os.WriteFile(tmp, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	// Set a different hash on purpose.
	t.Setenv(envVar, hashOf([]byte("echo evil\n")))

	err := VerifyInstaller(tmp, envVar, "test")
	if err == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
}

func TestVerifyInstaller_CaseInsensitiveAndTrimsWhitespace(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "installer.sh")
	content := []byte("payload\n")
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		t.Fatal(err)
	}
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	// Pad with spaces and use upper case — both should be tolerated.
	t.Setenv(envVar, "  "+hashUpper(hashOf(content))+"  ")

	if err := VerifyInstaller(tmp, envVar, "test"); err != nil {
		t.Fatalf("expected accept on padded/upper hash, got %v", err)
	}
}

func TestVerifyInstaller_MissingFileFails(t *testing.T) {
	envVar := "SFPANEL_TEST_INSTALLER_SHA256"
	t.Setenv(envVar, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	err := VerifyInstaller(filepath.Join(t.TempDir(), "nonexistent"), envVar, "test")
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

// hashUpper is defined locally to avoid pulling strings.ToUpper into the
// test's import list a second time.
func hashUpper(h string) string {
	out := make([]byte, len(h))
	for i, c := range h {
		if c >= 'a' && c <= 'f' {
			out[i] = byte(c) - 32
		} else {
			out[i] = byte(c)
		}
	}
	return string(out)
}
```

- [ ] **Step 1.2: Run the tests to verify they fail**

Run: `go test ./internal/release/ -run TestVerifyInstaller -v`
Expected: build failure — `VerifyInstaller` is not defined yet.

- [ ] **Step 1.3: Implement `VerifyInstaller`**

Create `internal/release/installer_pin.go`:

```go
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
			"installer", installer, "env_var", envVar, "hint",
			"set "+envVar+" to a sha256 hex digest to enable verification")
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
```

- [ ] **Step 1.4: Run the tests to verify they pass**

Run: `go test ./internal/release/ -run TestVerifyInstaller -v`
Expected: all 5 subtests PASS.

---

## Task 2 — Wire into `packages.InstallDocker`

**Files:**
- Modify: `internal/feature/packages/handler.go:491-512`

- [ ] **Step 2.1: Insert verify call after `curl` download, before `sh`**

Find this block:

```go
dlCmd := osExec.CommandContext(dlCtx, "curl", "-fsSL", "https://get.docker.com", "-o", "/tmp/get-docker.sh")
```

(plus the few lines that handle the download error and then call `sh /tmp/get-docker.sh`).

Insert the verify call between the successful download and the `sh` invocation. Locate `cmd := osExec.CommandContext(ctx, "sh", "/tmp/get-docker.sh")` at line ~512 and immediately above it add:

```go
if err := release.VerifyInstaller("/tmp/get-docker.sh", "SFPANEL_DOCKER_INSTALLER_SHA256", "docker"); err != nil {
    sendLine(response.SanitizeOutput("ERROR: installer verification failed: " + err.Error()))
    os.Remove("/tmp/get-docker.sh")
    return
}
```

Also add the import for `release` and `response` if not already present. Re-check the import block at the top of `handler.go` — if `response` is already imported (it is, the handler uses `response.OK`/`response.Fail` everywhere), only add `"github.com/svrforum/SFPanel/internal/release"`.

- [ ] **Step 2.2: Run the package build to catch missing import**

Run: `go build ./internal/feature/packages/...`
Expected: clean build. If there's an "imported and not used" or "undefined" error, fix it then retry.

---

## Task 3 — Wire into `packages.InstallNode` (NVM)

**Files:**
- Modify: `internal/feature/packages/handler.go:635-653`

- [ ] **Step 3.1: Insert verify call after `curl`**

Locate `cmd := osExec.CommandContext(ctx, "bash", "/tmp/install-nvm.sh")` at line ~653 and immediately above it add:

```go
if err := release.VerifyInstaller("/tmp/install-nvm.sh", "SFPANEL_NVM_INSTALLER_SHA256", "nvm"); err != nil {
    sendLine(response.SanitizeOutput("ERROR: installer verification failed: " + err.Error()))
    os.Remove("/tmp/install-nvm.sh")
    return
}
```

- [ ] **Step 3.2: Verify build**

Run: `go build ./internal/feature/packages/...` — expected clean.

---

## Task 4 — Wire into `packages.InstallClaude`

**Files:**
- Modify: `internal/feature/packages/handler.go:1086-1104`

- [ ] **Step 4.1: Insert verify call after `curl`**

Locate `cmd := osExec.CommandContext(ctx, "bash", "/tmp/install-claude.sh")` at line ~1104 and immediately above it add:

```go
if err := release.VerifyInstaller("/tmp/install-claude.sh", "SFPANEL_CLAUDE_INSTALLER_SHA256", "claude"); err != nil {
    sendLine(response.SanitizeOutput("ERROR: installer verification failed: " + err.Error()))
    os.Remove("/tmp/install-claude.sh")
    return
}
```

- [ ] **Step 4.2: Verify build**

Run: `go build ./internal/feature/packages/...` — expected clean.

---

## Task 5 — Wire into `network/tailscale.go`

**Files:**
- Modify: `internal/feature/network/tailscale.go:273-295`

- [ ] **Step 5.1: Insert verify call after `curl`**

Locate `cmd := osExec.CommandContext(ctx, "sh", "/tmp/tailscale-install.sh")` around line 295 and immediately above it add:

```go
if err := release.VerifyInstaller("/tmp/tailscale-install.sh", "SFPANEL_TAILSCALE_INSTALLER_SHA256", "tailscale"); err != nil {
    sendLine(response.SanitizeOutput("ERROR: installer verification failed: " + err.Error()))
    os.Remove("/tmp/tailscale-install.sh")
    return
}
```

The `sendLine` callback shape in tailscale.go may differ from packages.go (it's the SSE writer closure). If the local helper is named differently, adapt — but keep the message text identical for grep-ability.

Add the import `"github.com/svrforum/SFPanel/internal/release"` to the network/tailscale.go file if not already present.

- [ ] **Step 5.2: Verify build + full unit tests**

Run: `go build ./... && go test ./internal/release/... ./internal/feature/packages/... ./internal/feature/network/...`
Expected: clean.

---

## Task 6 — Verification

- [ ] **Step 6.1: Full vet + broader test**

Run: `go vet ./... && go test ./internal/...`
Expected: clean. (We do NOT run integration tests; the curl-piped install paths aren't unit-testable in CI.)

---

## Task 7 — Commit

- [ ] **Step 7.1: Stage and commit**

```bash
git add internal/release/installer_pin.go \
        internal/release/installer_pin_test.go \
        internal/feature/packages/handler.go \
        internal/feature/network/tailscale.go \
        docs/superpowers/plans/2026-05-18-fix-installer-hash-verify.md

GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git commit -m "$(cat <<'EOF'
release + packages + network: pin 3rd-party installer hashes via env vars

- New internal/release.VerifyInstaller helper: warns when env unset (CLAUDE.md "track-latest by default" stance preserved), hard-fails on hash mismatch when env is set.
- Wired into the 4 install paths that previously fetched-and-executed without verification: Docker (SFPANEL_DOCKER_INSTALLER_SHA256), NVM (SFPANEL_NVM_INSTALLER_SHA256), Claude (SFPANEL_CLAUDE_INSTALLER_SHA256), Tailscale (SFPANEL_TAILSCALE_INSTALLER_SHA256).
- Failure path sanitizes the error message via response.SanitizeOutput before writing to the SSE stream.

Implements the env-var override that CLAUDE.md ("Hash pinning of upstream installers ... track-latest by default") promised but had not been built. Closes P0-5 from docs/superpowers/research/2026-05-18-module-hardening/R-final.md (2026-04-19 P0 carry-forward).
EOF
)"
```

---

## Self-review

- [x] All 4 install paths covered (docker / nvm / claude / tailscale).
- [x] No placeholders.
- [x] Error path uses `response.SanitizeOutput` per CLAUDE.md.
- [x] Cleanup (`os.Remove`) on verification failure matches existing cleanup-on-error pattern in each caller.
- [x] Env-var-empty soft-pass preserves documented track-latest stance.
- [x] CLAUDE.md compliance: svrforum env vars, no AI references in commit.
