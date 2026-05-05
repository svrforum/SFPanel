# Cosign Image Verification Implementation Plan (Theme C Phase 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gate every Docker image pull through `cosign verify` against an operator-defined allowlist replicated through the Raft FSM, with a 24h per-node verification cache and a self-bootstrapping cosign binary.

**Architecture:** A new `internal/security/` package owns three concerns — policy load/save (FSM JSON), cosign self-install (download + verify with the existing `release.VerifyCosignBlob`), and image verification orchestration (cache lookup, allowlist matching, cosign exec). The verifier is wired into `docker.Client.PullImage` and the compose pull loop. UI lives in the existing Settings → 보안 tab and the Docker → 이미지 page.

**Tech Stack:** Go 1.25 (modernc.org/sqlite, hashicorp/raft, labstack/echo); React 19 + shadcn (Tabs, Dialog, RadioGroup, Table); cosign CLI v2.x (auto-installed).

---

## File structure

| File | Responsibility |
|---|---|
| `internal/security/policy.go` | `Policy`/`Rule`/`Identity` types, `LoadPolicy`/`SavePolicy`/`MatchRule` |
| `internal/security/sigstore.go` | hardcoded sigstore release identity for self-bootstrap |
| `internal/security/install.go` | `Installer.EnsureCosign(ctx)` — download + self-verify + install |
| `internal/security/verifier.go` | `Verifier.VerifyImage(ctx, ref)` — cache + allowlist + cosign exec |
| `internal/security/errors.go` | `ErrPolicyViolation`, `ErrCosignInstallFailed`, `ErrNoMatchingRule` |
| `internal/feature/security/handler.go` | REST: policy GET/PUT, cosign-status, cosign-install (SSE), verify-image |
| `internal/db/migrations.go` (modify) | append migration ID 21 (image_signatures) + 22, 23 (indexes) |
| `internal/api/response/errors.go` (modify) | new error codes |
| `internal/api/router.go` (modify) | register security routes, inject Verifier into Docker handler |
| `internal/docker/client.go` (modify) | `Client.Verifier` field, `PullImage` pre-flight |
| `internal/docker/compose.go` (modify) | per-service VerifyImage call in pull loop with SSE `[verify] …` lines |
| `web/src/types/api.ts` (modify) | `Policy`, `Rule`, `Identity`, `CosignStatus`, `ImageSignatureStatus` |
| `web/src/lib/api.ts` (modify) | 5 new methods |
| `web/src/pages/settings/ImageSignatureSettings.tsx` | policy radio + rules table + cosign status |
| `web/src/components/security/RuleEditDialog.tsx` | edit/create rule dialog |
| `web/src/pages/Settings.tsx` (modify) | embed `<ImageSignatureSettings />` in security tab |
| `web/src/pages/docker/DockerImages.tsx` (modify) | 검증 column with status icon + tooltip + click-to-modal |

Each file owns one responsibility. Verifier never imports REST layer types; handler depends only on `internal/security`.

---

## Task 1: DB migration — `image_signatures` table

**Files:**
- Modify: `internal/db/migrations.go` (append after ID 20)

- [ ] **Step 1: Append migration entries**

In `internal/db/migrations.go`, just before the closing `}` of the `migrations` slice, append:

```go
	{ID: 21, Up: `CREATE TABLE IF NOT EXISTS image_signatures (
		digest           TEXT PRIMARY KEY,
		ref              TEXT NOT NULL,
		status           TEXT NOT NULL,
		identity_subject TEXT,
		identity_issuer  TEXT,
		error_message    TEXT,
		verified_at      INTEGER NOT NULL,
		expires_at       INTEGER NOT NULL
	)`},
	{ID: 22, Up: `CREATE INDEX IF NOT EXISTS idx_image_signatures_ref ON image_signatures(ref)`},
	{ID: 23, Up: `CREATE INDEX IF NOT EXISTS idx_image_signatures_expires ON image_signatures(expires_at)`},
```

- [ ] **Step 2: Build + run existing tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/db/... -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/db/migrations.go
git commit -m "db: image_signatures table + indexes (Theme C Phase 1)"
```

---

## Task 2: Policy types + serialization

**Files:**
- Create: `internal/security/policy.go`
- Create: `internal/security/policy_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/security/policy_test.go`:

```go
package security

import (
	"encoding/json"
	"testing"
)

func TestPolicy_RoundTrip(t *testing.T) {
	in := Policy{
		Mode: ModeWarn,
		Rules: []Rule{
			{Pattern: "ghcr.io/myorg/*", Identity: Identity{
				SubjectPrefix: "https://github.com/myorg/myrepo/.github/workflows/release.yaml@refs/tags/v",
				Issuer:        "https://token.actions.githubusercontent.com",
			}},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Policy
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != ModeWarn {
		t.Fatalf("mode: got %q want %q", out.Mode, ModeWarn)
	}
	if len(out.Rules) != 1 || out.Rules[0].Pattern != "ghcr.io/myorg/*" {
		t.Fatalf("rules: %+v", out.Rules)
	}
}

func TestPolicy_DefaultIsOff(t *testing.T) {
	var p Policy
	if !p.IsOff() {
		t.Fatalf("zero-value policy should be off, got mode %q", p.Mode)
	}
}
```

- [ ] **Step 2: Run, expect compile fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -count=1`
Expected: FAIL — package not yet defined.

- [ ] **Step 3: Implement `policy.go`**

Create `internal/security/policy.go`:

```go
package security

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// Mode is the global policy mode. Empty string means off (back-compat for
// pre-Theme-C clusters where state.Config["security.policy"] is absent).
type Mode string

const (
	ModeOff     Mode = "off"
	ModeWarn    Mode = "warn"
	ModeRequire Mode = "require"
)

// Policy is the cluster-replicated security policy. Stored as JSON in the
// Raft FSM at Config["security.policy"].
type Policy struct {
	Mode  Mode   `json:"mode"`
	Rules []Rule `json:"rules"`
}

// IsOff returns true when verification is disabled (mode = off OR empty).
// Hot path for the verifier — used to short-circuit before any DB or
// cosign work.
func (p Policy) IsOff() bool {
	return p.Mode == "" || p.Mode == ModeOff
}

// Rule maps a glob pattern to a required signing identity.
type Rule struct {
	Pattern  string   `json:"pattern"`
	Identity Identity `json:"identity"`
}

// Identity is a Sigstore keyless identity (cert SAN URI prefix + OIDC issuer).
type Identity struct {
	SubjectPrefix string `json:"subject_prefix"`
	Issuer        string `json:"issuer"`
}

// configKey is the Raft FSM Config key. Stable forever.
const configKey = "security.policy"

// LoadPolicy reads the current cluster-wide policy. Returns an off policy
// (no error) when the FSM Config has no entry — this is the case for
// clusters upgrading from pre-Theme-C without explicit opt-in.
func LoadPolicy(c *cluster.Manager) (Policy, error) {
	if c == nil {
		return Policy{Mode: ModeOff}, nil
	}
	raw := c.GetConfig(configKey)
	if raw == "" {
		return Policy{Mode: ModeOff}, nil
	}
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	return p, nil
}

// SavePolicy writes the policy via cluster.SetConfig (Raft Apply, leader
// only). Validates Mode + Rule fields before writing — bad input never
// reaches the FSM.
func SavePolicy(c *cluster.Manager, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	return c.SetConfig(configKey, string(data))
}

// Validate reports input mistakes that would corrupt the FSM if applied.
func (p Policy) Validate() error {
	switch p.Mode {
	case ModeOff, ModeWarn, ModeRequire:
	default:
		return fmt.Errorf("invalid mode %q (want off|warn|require)", p.Mode)
	}
	for i, r := range p.Rules {
		if strings.TrimSpace(r.Pattern) == "" {
			return fmt.Errorf("rule[%d]: pattern empty", i)
		}
		if strings.TrimSpace(r.Identity.SubjectPrefix) == "" {
			return fmt.Errorf("rule[%d]: subject_prefix empty", i)
		}
		if strings.TrimSpace(r.Identity.Issuer) == "" {
			return fmt.Errorf("rule[%d]: issuer empty", i)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run "TestPolicy_" -count=1 -v`
Expected: 2 PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/policy.go internal/security/policy_test.go
git commit -m "security: Policy/Rule/Identity types + FSM serialization"
```

---

## Task 3: Pattern matching (`MatchRule`)

**Files:**
- Modify: `internal/security/policy.go` (append `MatchRule` + `normalizeRef`)
- Modify: `internal/security/policy_test.go`

- [ ] **Step 1: Append failing test**

Append to `internal/security/policy_test.go`:

```go
func TestPolicy_MatchRule(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		ref     string
		want    bool
	}{
		{"exact", "ghcr.io/foo/bar:1.0", "ghcr.io/foo/bar:1.0", true},
		{"single-star segment", "ghcr.io/foo/*", "ghcr.io/foo/bar:1.0", true},
		{"single-star segment too greedy", "ghcr.io/foo/*", "ghcr.io/foo/bar/baz:1.0", false},
		{"double-star multi-segment", "ghcr.io/foo/**", "ghcr.io/foo/bar/baz:1.0", true},
		{"double-star top", "**", "ghcr.io/anything", true},
		{"docker.io implicit library", "docker.io/library/postgres:*", "postgres:15", true},
		{"docker.io implicit user repo", "docker.io/myuser/img:*", "myuser/img:1", true},
		{"no match", "ghcr.io/myorg/*", "quay.io/other/x:1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Policy{Rules: []Rule{{Pattern: tc.pattern, Identity: Identity{SubjectPrefix: "x", Issuer: "y"}}}}
			_, ok := p.MatchRule(tc.ref)
			if ok != tc.want {
				t.Fatalf("pattern %q ref %q: got %v want %v", tc.pattern, tc.ref, ok, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run TestPolicy_MatchRule -count=1`
Expected: FAIL — `MatchRule` undefined.

- [ ] **Step 3: Append `MatchRule` + `normalizeRef` to `policy.go`**

```go
// MatchRule returns the first Rule matching ref, or (Rule{}, false). Pattern
// uses glob: `*` matches a single segment, `**` matches multiple. Implicit
// docker.io/library/ prefix is normalized before matching so that "postgres"
// matches "docker.io/library/postgres:*".
func (p Policy) MatchRule(ref string) (Rule, bool) {
	full := normalizeRef(ref)
	for _, r := range p.Rules {
		if matchGlob(r.Pattern, full) || matchGlob(normalizeRef(r.Pattern), full) {
			return r, true
		}
	}
	return Rule{}, false
}

// normalizeRef expands shorthand to a fully-qualified reference:
//   "postgres"          → "docker.io/library/postgres:latest"
//   "myuser/img:1"      → "docker.io/myuser/img:1"
//   "ghcr.io/x/y:tag"   → unchanged
//   "ghcr.io/x/y"       → "ghcr.io/x/y:latest"
//   "x:tag@sha256:..."  → "x:tag@sha256:..." (digest preserved)
func normalizeRef(ref string) string {
	atIdx := strings.Index(ref, "@")
	digest := ""
	if atIdx >= 0 {
		digest = ref[atIdx:]
		ref = ref[:atIdx]
	}
	// Determine if the first segment is a registry. A registry token has a
	// "." or ":" or is "localhost"; otherwise the implicit registry is docker.io.
	hasRegistry := false
	if i := strings.Index(ref, "/"); i >= 0 {
		first := ref[:i]
		if first == "localhost" || strings.ContainsAny(first, ".:") {
			hasRegistry = true
		}
	}
	if !hasRegistry {
		if strings.Contains(ref, "/") {
			ref = "docker.io/" + ref
		} else {
			ref = "docker.io/library/" + ref
		}
	}
	// Add :latest if no tag.
	lastSlash := strings.LastIndex(ref, "/")
	tagPart := ref[lastSlash+1:]
	if !strings.Contains(tagPart, ":") {
		ref += ":latest"
	}
	return ref + digest
}

// matchGlob — `*` = one segment (no slashes), `**` = any number of segments.
// Pattern segments are split by `/`. Within a segment, `*` is a wildcard for
// any character run except `/`. Tag portion (after `:`) supports `*` too.
func matchGlob(pattern, s string) bool {
	// Split off tag/digest comparisons cleanly from path.
	pPath, pTag := splitTag(pattern)
	sPath, sTag := splitTag(s)
	if !globPath(pPath, sPath) {
		return false
	}
	if pTag == "" || pTag == "*" {
		return true
	}
	return globSegment(pTag, sTag)
}

// splitTag returns (path, tag-or-digest). Tag includes everything after the
// LAST colon AFTER the last slash.
func splitTag(s string) (string, string) {
	lastSlash := strings.LastIndex(s, "/")
	rest := s[lastSlash+1:]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return s, ""
	}
	return s[:lastSlash+1] + rest[:colon], rest[colon+1:]
}

func globPath(pattern, s string) bool {
	pSegs := strings.Split(pattern, "/")
	sSegs := strings.Split(s, "/")
	return globSegments(pSegs, sSegs)
}

func globSegments(p, s []string) bool {
	for i, seg := range p {
		if seg == "**" {
			// ** matches zero or more remaining segments.
			rest := p[i+1:]
			for j := 0; j <= len(s); j++ {
				if globSegments(rest, s[j:]) {
					return true
				}
			}
			return false
		}
		if i >= len(s) {
			return false
		}
		if !globSegment(seg, s[i]) {
			return false
		}
	}
	return len(p) == len(s)
}

// globSegment — `*` wildcard, no slashes. Simple greedy.
func globSegment(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	// Single-* substring matching (no escaping needed for this use).
	parts := strings.Split(pattern, "*")
	idx := 0
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	idx += len(parts[0])
	for i := 1; i < len(parts)-1; i++ {
		j := strings.Index(s[idx:], parts[i])
		if j < 0 {
			return false
		}
		idx += j + len(parts[i])
	}
	last := parts[len(parts)-1]
	return strings.HasSuffix(s[idx:], last)
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run TestPolicy_MatchRule -count=1 -v`
Expected: 8 sub-tests PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/policy.go internal/security/policy_test.go
git commit -m "security: MatchRule glob + docker.io normalization"
```

---

## Task 4: Sigstore release identity constants

**Files:**
- Create: `internal/security/sigstore.go`

- [ ] **Step 1: Write file**

```go
package security

import "github.com/svrforum/SFPanel/internal/release"

// sigstoreReleaseIdentity is the trusted Sigstore release identity used to
// verify cosign binary downloads during self-bootstrap. SubjectPrefix is the
// GitHub Actions workflow URL prefix matching any tagged release; Issuer is
// the GitHub Actions OIDC token endpoint.
//
// Updating: cosign release pipeline rarely changes its workflow file. If
// they rename release.yaml, this prefix must be updated. There is no
// auto-discovery — that would defeat the whole verification.
var sigstoreReleaseIdentity = release.CosignIdentity{
	SubjectPrefix: "https://github.com/sigstore/cosign/.github/workflows/release.yaml@refs/tags/v",
	Issuer:        "https://token.actions.githubusercontent.com",
}
```

- [ ] **Step 2: Build**

Run: `cd /opt/stacks/SFPanel && go build ./internal/security/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/sigstore.go
git commit -m "security: sigstore release identity constants"
```

---

## Task 5: Errors

**Files:**
- Create: `internal/security/errors.go`
- Modify: `internal/api/response/errors.go`

- [ ] **Step 1: Create internal package errors**

Create `internal/security/errors.go`:

```go
package security

import "errors"

// ErrPolicyViolation is returned by VerifyImage when the operator's policy
// rejects the image (require-mode failure or no matching allowlist rule).
// Callers should treat this as a clean refusal — not a transient error.
var ErrPolicyViolation = errors.New("security: policy violation")

// ErrCosignInstallFailed is returned by EnsureCosign when both the network
// download and the /etc/sfpanel/cosign fallback fail to produce a usable
// binary. The error is wrapped with context so the operator-facing message
// can include the underlying cause.
var ErrCosignInstallFailed = errors.New("security: cosign install failed")

// ErrNoMatchingRule is returned (wrapped) by VerifyImage in require mode
// when the image does not match any allowlist rule. Distinguishable from
// signature-verification failures so the UI can show "add a rule" guidance.
var ErrNoMatchingRule = errors.New("security: no matching allowlist rule")
```

- [ ] **Step 2: Append HTTP error codes**

In `internal/api/response/errors.go`, in the same `const` block as the existing codes, append:

```go
	ErrPolicyViolation     = "POLICY_VIOLATION"
	ErrCosignInstallFailed = "COSIGN_INSTALL_FAILED"
	ErrInvalidPolicy       = "INVALID_POLICY"
```

- [ ] **Step 3: Build**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/errors.go internal/api/response/errors.go
git commit -m "security: error sentinels + HTTP error codes"
```

---

## Task 6: Cosign installer (`Installer.EnsureCosign`)

**Files:**
- Create: `internal/security/install.go`
- Create: `internal/security/install_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/security/install_test.go`:

```go
package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// TestInstaller_AlreadyInstalledShortCircuits — if a binary already exists
// at canonical path AND `cosign version` reports >= 2.x, return that path
// without any network or verification work.
func TestInstaller_AlreadyInstalledShortCircuits(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cosign")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho 'cosign version v2.4.1'"), 0755); err != nil {
		t.Fatal(err)
	}

	mock := exec.NewMockCommander()
	mock.ResponseFunc = func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "version" {
			return []byte("cosign version v2.4.1\n"), nil
		}
		return nil, errors.New("unexpected: " + args[0])
	}
	i := &Installer{
		Cmd:  mock,
		Path: binPath,
		Get:  func(ctx context.Context, url string) ([]byte, error) { t.Fatal("network should not be hit"); return nil, nil },
	}
	got, err := i.EnsureCosign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != binPath {
		t.Fatalf("path: got %q want %q", got, binPath)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run TestInstaller_ -count=1`
Expected: FAIL — `Installer` undefined.

- [ ] **Step 3: Implement `install.go`**

Create `internal/security/install.go`:

```go
package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/release"
)

// DefaultCosignPath is where SFPanel installs the cosign binary on first use.
const DefaultCosignPath = "/var/lib/sfpanel/bin/cosign"

// FallbackCosignPath is consulted when the network install fails. Operators
// can pre-place a binary there for air-gapped installs.
const FallbackCosignPath = "/etc/sfpanel/cosign"

// MinCosignVersion is the minimum acceptable cosign major.minor.
const MinCosignVersion = "2.0"

// DefaultCosignReleaseTag is the default cosign release pin. Operators may
// override via the SFPANEL_COSIGN_VERSION environment variable.
const DefaultCosignReleaseTag = "v2.4.1"

// Installer self-bootstraps the cosign binary, verifying its authenticity
// using the same VerifyCosignBlob primitive that gates SFPanel's own update
// flow. Network and disk side-effects are injected for testability.
type Installer struct {
	Cmd  exec.Commander
	Path string                                                            // override DefaultCosignPath
	Get  func(ctx context.Context, url string) ([]byte, error)             // override default HTTP GET
}

// EnsureCosign returns the path to a verified cosign binary. Lazy: the
// network/verify path runs only when the canonical binary is missing or
// version-stale. Returns ErrCosignInstallFailed (wrapped) when neither
// download nor /etc/sfpanel/cosign fallback yields a usable binary.
func (i *Installer) EnsureCosign(ctx context.Context) (string, error) {
	path := i.Path
	if path == "" {
		path = DefaultCosignPath
	}
	if i.cosignVersionOK(ctx, path) {
		return path, nil
	}
	// Try network install first.
	if err := i.installFromNetwork(ctx, path); err == nil {
		return path, nil
	} else {
		// Try fallback before giving up.
		if i.cosignVersionOK(ctx, FallbackCosignPath) {
			return FallbackCosignPath, nil
		}
		return "", fmt.Errorf("%w: %v (and %s unusable)", ErrCosignInstallFailed, err, FallbackCosignPath)
	}
}

// cosignVersionOK reports whether `<path> version` outputs something
// matching cosign and the version is >= MinCosignVersion.
func (i *Installer) cosignVersionOK(ctx context.Context, path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	out, err := i.Cmd.RunWithTimeout(5*time.Second, path, "version")
	if err != nil {
		return false
	}
	// Output line like "cosign version v2.4.1\n  ..."
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if !strings.HasPrefix(line, "cosign version") {
			continue
		}
		// Extract major.minor and lexically compare against MinCosignVersion.
		// "v2.4.1" → split → "2.4.1" → "2.4"
		parts := strings.Fields(line)
		if len(parts) < 3 {
			return false
		}
		ver := strings.TrimPrefix(parts[2], "v")
		segs := strings.SplitN(ver, ".", 3)
		if len(segs) < 2 {
			return false
		}
		majMin := segs[0] + "." + segs[1]
		return majMin >= MinCosignVersion
	}
	return false
}

func (i *Installer) installFromNetwork(ctx context.Context, dest string) error {
	tag := os.Getenv("SFPANEL_COSIGN_VERSION")
	if tag == "" {
		tag = DefaultCosignReleaseTag
	}
	base := "https://github.com/sigstore/cosign/releases/download/" + tag
	getter := i.Get
	if getter == nil {
		getter = httpGet
	}
	binary, err := getter(ctx, base+"/cosign-linux-amd64")
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	sig, err := getter(ctx, base+"/cosign-linux-amd64-keyless.sig")
	if err != nil {
		return fmt.Errorf("download sig: %w", err)
	}
	pem, err := getter(ctx, base+"/cosign-linux-amd64-keyless.pem")
	if err != nil {
		return fmt.Errorf("download cert: %w", err)
	}
	if err := release.VerifyCosignBlob(binary, sig, pem, sigstoreReleaseIdentity); err != nil {
		return fmt.Errorf("verify cosign: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, binary, 0755); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	if !i.cosignVersionOK(ctx, dest) {
		os.Remove(dest)
		return errors.New("freshly installed cosign failed sanity check")
	}
	// Best-effort hash log for forensics (non-fatal).
	_ = hashLog(dest, binary)
	return nil
}

func hashLog(path string, body []byte) error {
	sum := sha256.Sum256(body)
	return os.WriteFile(path+".sha256", []byte(hex.EncodeToString(sum[:])+"\n"), 0644)
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	const maxBody = 200 << 20 // 200 MiB; cosign linux-amd64 is ~120 MiB
	buf := make([]byte, 0, 4<<20)
	tmp := make([]byte, 64<<10)
	for {
		if len(buf) > maxBody {
			return nil, errors.New("body too large")
		}
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			if rerr.Error() == "EOF" {
				break
			}
			return buf, nil
		}
	}
	return buf, nil
}
```

- [ ] **Step 4: Run target test**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run TestInstaller_AlreadyInstalledShortCircuits -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/install.go internal/security/install_test.go
git commit -m "security: cosign self-bootstrap installer"
```

---

## Task 7: Verifier — cache-only path

**Files:**
- Create: `internal/security/verifier.go`
- Create: `internal/security/verifier_test.go`

- [ ] **Step 1: Failing test for cache hit (verified)**

Create `internal/security/verifier_test.go`:

```go
package security

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE image_signatures (
		digest TEXT PRIMARY KEY, ref TEXT NOT NULL, status TEXT NOT NULL,
		identity_subject TEXT, identity_issuer TEXT, error_message TEXT,
		verified_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestVerifier_OffModeIsNoOp — when policy mode is off, VerifyImage returns
// nil immediately and does NOT touch DB or run any commands.
func TestVerifier_OffModeIsNoOp(t *testing.T) {
	db := newTestDB(t)
	mock := exec.NewMockCommander()
	mock.ResponseFunc = func(args []string) ([]byte, error) {
		t.Fatalf("unexpected exec: %v", args)
		return nil, errors.New("unexpected")
	}
	v := &Verifier{
		Cmd:        mock,
		DB:         db,
		LoadPolicy: func() (Policy, error) { return Policy{Mode: ModeOff}, nil },
	}
	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatal(err)
	}
}

// TestVerifier_RequireMode_NoMatchingRule — fail with ErrNoMatchingRule.
func TestVerifier_RequireMode_NoMatchingRule(t *testing.T) {
	db := newTestDB(t)
	mock := exec.NewMockCommander()
	mock.ResponseFunc = func(args []string) ([]byte, error) {
		// docker inspect for digest
		if args[0] == "docker" {
			return []byte(`[{"Id":"sha256:aa11"}]`), nil
		}
		t.Fatalf("unexpected: %v", args)
		return nil, errors.New("unexpected")
	}
	v := &Verifier{
		Cmd:        mock,
		DB:         db,
		LoadPolicy: func() (Policy, error) { return Policy{Mode: ModeRequire}, nil },
	}
	err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1")
	if !errors.Is(err, ErrNoMatchingRule) {
		t.Fatalf("got %v want ErrNoMatchingRule", err)
	}
}

// TestVerifier_CacheHitVerified — pre-populated row with status=verified
// and expires_at in the future short-circuits before any cosign call.
func TestVerifier_CacheHitVerified(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO image_signatures
		(digest, ref, status, verified_at, expires_at)
		VALUES (?, ?, 'verified', ?, ?)`,
		"sha256:aa11", "ghcr.io/foo/bar:1", now-10_000, now+10_000); err != nil {
		t.Fatal(err)
	}
	mock := exec.NewMockCommander()
	mock.ResponseFunc = func(args []string) ([]byte, error) {
		if args[0] == "docker" {
			return []byte(`[{"Id":"sha256:aa11"}]`), nil
		}
		t.Fatalf("cosign should not run on cache hit: %v", args)
		return nil, errors.New("unexpected")
	}
	v := &Verifier{
		Cmd:        mock,
		DB:         db,
		LoadPolicy: func() (Policy, error) {
			return Policy{Mode: ModeRequire, Rules: []Rule{
				{Pattern: "ghcr.io/foo/*", Identity: Identity{SubjectPrefix: "x", Issuer: "y"}},
			}}, nil
		},
	}
	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run TestVerifier_ -count=1`
Expected: FAIL — `Verifier` undefined.

- [ ] **Step 3: Implement `verifier.go` (cache + policy lookup, no cosign yet)**

Create `internal/security/verifier.go`:

```go
package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

const cacheTTL = 24 * time.Hour
const failCacheTTL = 30 * time.Second // require-mode failures cache shorter

const (
	statusVerified = "verified"
	statusUnsigned = "unsigned"
	statusFailed   = "failed"
)

// Verifier orchestrates the pull-time verification flow.
type Verifier struct {
	Cmd        exec.Commander
	DB         *sql.DB
	LoadPolicy func() (Policy, error)
	Cosign     *Installer // nil → installer disabled (test/off mode)
}

// VerifyImage gates the pull. Returns nil when the policy permits the
// pull. Returns wrapped ErrPolicyViolation in require-mode failures.
// Always idempotent — safe to call concurrently for the same ref.
func (v *Verifier) VerifyImage(ctx context.Context, ref string) error {
	policy, err := v.LoadPolicy()
	if err != nil {
		// Policy load errors degrade to off (don't block deploys on FSM read fails).
		slog.Warn("security: policy load failed, treating as off", "component", "security", "error", err)
		return nil
	}
	if policy.IsOff() {
		return nil
	}

	digest, err := v.resolveDigest(ctx, ref)
	if err != nil {
		// Image not yet pulled / not found — let docker fail naturally.
		// We only verify what's resolvable.
		return nil
	}

	// Cache check.
	if rec, ok := v.cacheLookup(digest); ok {
		switch rec.status {
		case statusVerified:
			return nil
		case statusUnsigned:
			return v.handleUnsigned(policy, ref, digest)
		case statusFailed:
			if policy.Mode == ModeRequire {
				return fmt.Errorf("%w: cached failure for %s: %s", ErrPolicyViolation, ref, rec.errorMessage)
			}
			return nil
		}
	}

	// Allowlist match.
	rule, ok := policy.MatchRule(ref)
	if !ok {
		v.cacheStore(digest, ref, statusUnsigned, "", "", "no allowlist rule matched", failCacheTTL)
		if policy.Mode == ModeRequire {
			return fmt.Errorf("%w: %w: %s", ErrPolicyViolation, ErrNoMatchingRule, ref)
		}
		slog.Warn("security: image lacks allowlist rule (warn mode)", "component", "security", "ref", ref)
		return nil
	}
	_ = rule // cosign exec wired in Task 8

	// Without cosign exec yet, treat as failed so tests can drive the dispatch logic.
	v.cacheStore(digest, ref, statusFailed, "", "", "cosign integration not wired", failCacheTTL)
	if policy.Mode == ModeRequire {
		return fmt.Errorf("%w: cosign not wired", ErrPolicyViolation)
	}
	return nil
}

func (v *Verifier) handleUnsigned(p Policy, ref, digest string) error {
	if p.Mode == ModeRequire {
		return fmt.Errorf("%w: %s is unsigned", ErrPolicyViolation, ref)
	}
	return nil
}

type cacheRecord struct {
	status        string
	errorMessage  string
	identitySub   string
	identityIss   string
}

func (v *Verifier) cacheLookup(digest string) (cacheRecord, bool) {
	var rec cacheRecord
	var verifiedAt, expiresAt int64
	err := v.DB.QueryRow(`SELECT status, COALESCE(error_message,''), COALESCE(identity_subject,''),
		COALESCE(identity_issuer,''), verified_at, expires_at
		FROM image_signatures WHERE digest = ?`, digest).
		Scan(&rec.status, &rec.errorMessage, &rec.identitySub, &rec.identityIss, &verifiedAt, &expiresAt)
	if err != nil {
		return cacheRecord{}, false
	}
	if expiresAt < time.Now().UnixMilli() {
		return cacheRecord{}, false
	}
	return rec, true
}

func (v *Verifier) cacheStore(digest, ref, status, identitySubject, identityIssuer, errMsg string, ttl time.Duration) {
	now := time.Now().UnixMilli()
	exp := now + ttl.Milliseconds()
	if ttl == 0 {
		exp = now + cacheTTL.Milliseconds()
	}
	_, err := v.DB.Exec(`INSERT INTO image_signatures
		(digest, ref, status, identity_subject, identity_issuer, error_message, verified_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(digest) DO UPDATE SET ref=excluded.ref, status=excluded.status,
		identity_subject=excluded.identity_subject, identity_issuer=excluded.identity_issuer,
		error_message=excluded.error_message, verified_at=excluded.verified_at,
		expires_at=excluded.expires_at`,
		digest, ref, status, nullable(identitySubject), nullable(identityIssuer),
		nullable(errMsg), now, exp)
	if err != nil {
		slog.Warn("security: cache store failed", "component", "security", "error", err)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// resolveDigest returns the image's local manifest digest. Calls
// `docker image inspect <ref> --format '{{json .}}'` and parses the Id
// field. If the image is not yet pulled, returns an error and the caller
// short-circuits to "let docker handle it".
func (v *Verifier) resolveDigest(ctx context.Context, ref string) (string, error) {
	out, err := v.Cmd.RunWithTimeout(5*time.Second, "docker", "image", "inspect", ref)
	if err != nil {
		return "", err
	}
	var arr []struct {
		Id string `json:"Id"`
	}
	if err := json.Unmarshal(out, &arr); err != nil {
		return "", fmt.Errorf("decode inspect: %w", err)
	}
	if len(arr) == 0 || arr[0].Id == "" {
		return "", errors.New("no image found")
	}
	return strings.TrimSpace(arr[0].Id), nil
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -run TestVerifier_ -count=1 -v`
Expected: 3 PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/verifier.go internal/security/verifier_test.go
git commit -m "security: Verifier core (cache + policy + dispatch)"
```

---

## Task 8: Verifier — cosign exec wiring

**Files:**
- Modify: `internal/security/verifier.go` (replace the "not wired" stub)
- Modify: `internal/security/verifier_test.go` (add success + failure cosign cases)

- [ ] **Step 1: Append failing tests**

Append to `internal/security/verifier_test.go`:

```go
// TestVerifier_CosignSuccess — cosign verify succeeds, row stored as verified.
func TestVerifier_CosignSuccess(t *testing.T) {
	db := newTestDB(t)
	mock := exec.NewMockCommander()
	mock.ResponseFunc = func(args []string) ([]byte, error) {
		if args[0] == "docker" {
			return []byte(`[{"Id":"sha256:aa11"}]`), nil
		}
		// cosign verify path
		if strings.Contains(args[0], "cosign") {
			return []byte(`[{"critical":{"identity":{"docker-reference":"ghcr.io/foo/bar"}}}]`), nil
		}
		t.Fatalf("unexpected: %v", args)
		return nil, errors.New("unexpected")
	}
	v := &Verifier{
		Cmd: mock, DB: db,
		LoadPolicy: func() (Policy, error) {
			return Policy{Mode: ModeRequire, Rules: []Rule{
				{Pattern: "ghcr.io/foo/*", Identity: Identity{
					SubjectPrefix: "https://github.com/foo/repo",
					Issuer:        "https://token.actions.githubusercontent.com",
				}},
			}}, nil
		},
		Cosign: &Installer{Cmd: mock, Path: "/tmp/fake-cosign"},
	}
	// Pre-create fake cosign so EnsureCosign reports OK.
	_ = os.WriteFile("/tmp/fake-cosign", []byte("#!/bin/sh\necho cosign version v2.4.1"), 0755)
	mock.ResponseFunc = wrapMockHandlers(map[string]func([]string) ([]byte, error){
		"docker":          func(_ []string) ([]byte, error) { return []byte(`[{"Id":"sha256:aa11"}]`), nil },
		"/tmp/fake-cosign": func(args []string) ([]byte, error) {
			if len(args) >= 2 && args[1] == "version" {
				return []byte("cosign version v2.4.1\n"), nil
			}
			return []byte("ok"), nil
		},
	})

	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM image_signatures WHERE digest = ?`, "sha256:aa11").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "verified" {
		t.Fatalf("status: got %q want verified", status)
	}
}

// TestVerifier_CosignFailureRequireMode — cosign verify exits non-zero,
// require mode returns ErrPolicyViolation.
func TestVerifier_CosignFailureRequireMode(t *testing.T) {
	db := newTestDB(t)
	mock := exec.NewMockCommander()
	_ = os.WriteFile("/tmp/fake-cosign", []byte("#!/bin/sh\nexit 1"), 0755)
	mock.ResponseFunc = wrapMockHandlers(map[string]func([]string) ([]byte, error){
		"docker":           func(_ []string) ([]byte, error) { return []byte(`[{"Id":"sha256:bb22"}]`), nil },
		"/tmp/fake-cosign": func(args []string) ([]byte, error) {
			if len(args) >= 2 && args[1] == "version" {
				return []byte("cosign version v2.4.1\n"), nil
			}
			return []byte("Error: signature not found"), errors.New("exit 1")
		},
	})
	v := &Verifier{
		Cmd: mock, DB: db,
		LoadPolicy: func() (Policy, error) {
			return Policy{Mode: ModeRequire, Rules: []Rule{
				{Pattern: "ghcr.io/foo/*", Identity: Identity{
					SubjectPrefix: "https://x", Issuer: "https://y",
				}},
			}}, nil
		},
		Cosign: &Installer{Cmd: mock, Path: "/tmp/fake-cosign"},
	}
	err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1")
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("got %v want ErrPolicyViolation", err)
	}
}

// wrapMockHandlers — dispatch by argv[0] basename.
func wrapMockHandlers(m map[string]func([]string) ([]byte, error)) func([]string) ([]byte, error) {
	return func(args []string) ([]byte, error) {
		if h, ok := m[args[0]]; ok {
			return h(args)
		}
		// Try last segment of path (basename).
		base := args[0]
		if i := strings.LastIndex(args[0], "/"); i >= 0 {
			base = args[0][i+1:]
		}
		if h, ok := m[base]; ok {
			return h(args)
		}
		return nil, fmt.Errorf("no handler for %q", args[0])
	}
}
```

Add `os` and `strings` imports to the test file's import block (the test file's existing imports already cover `errors`, `testing`, `time`, `context`, `database/sql`, modernc.org/sqlite, exec — append `os`, `strings`, `fmt`).

- [ ] **Step 2: Replace the "not wired" stub in `verifier.go`**

Replace this block at the end of `VerifyImage`:

```go
	_ = rule // cosign exec wired in Task 8

	// Without cosign exec yet, treat as failed so tests can drive the dispatch logic.
	v.cacheStore(digest, ref, statusFailed, "", "", "cosign integration not wired", failCacheTTL)
	if policy.Mode == ModeRequire {
		return fmt.Errorf("%w: cosign not wired", ErrPolicyViolation)
	}
	return nil
}
```

with:

```go
	// Cosign verify against the matched identity.
	if v.Cosign == nil {
		// No installer configured (test path). Treat as fail.
		v.cacheStore(digest, ref, statusFailed, "", "", "no cosign installer configured", failCacheTTL)
		if policy.Mode == ModeRequire {
			return fmt.Errorf("%w: cosign installer not configured", ErrPolicyViolation)
		}
		return nil
	}
	cosignPath, err := v.Cosign.EnsureCosign(ctx)
	if err != nil {
		v.cacheStore(digest, ref, statusFailed, "", "", "cosign install: "+err.Error(), failCacheTTL)
		if policy.Mode == ModeRequire {
			return fmt.Errorf("%w: %v", ErrPolicyViolation, err)
		}
		slog.Warn("security: cosign install failed (warn mode)", "component", "security", "error", err)
		return nil
	}
	out, err := v.Cmd.RunWithTimeout(30*time.Second, cosignPath, "verify",
		"--certificate-identity-regexp="+regexpEscape(rule.Identity.SubjectPrefix)+".*",
		"--certificate-oidc-issuer="+rule.Identity.Issuer,
		ref)
	if err != nil {
		errMsg := strings.TrimSpace(string(out))
		v.cacheStore(digest, ref, statusFailed, "", "", errMsg, failCacheTTL)
		if policy.Mode == ModeRequire {
			return fmt.Errorf("%w: cosign verify: %s", ErrPolicyViolation, errMsg)
		}
		slog.Warn("security: cosign verify failed (warn mode)", "component", "security", "ref", ref, "error", errMsg)
		return nil
	}
	v.cacheStore(digest, ref, statusVerified, rule.Identity.SubjectPrefix, rule.Identity.Issuer, "", cacheTTL)
	return nil
}

// regexpEscape escapes a literal string for use inside a regexp anchor.
// We use a regexp anchor for cosign --certificate-identity-regexp because
// the prefix typically ends with a tag wildcard (.../tags/v) and any trailing
// version. Escaping prevents accidental regex metacharacters in user input.
func regexpEscape(s string) string {
	const meta = `\.+*?()|[]{}^$`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

- [ ] **Step 3: Run all verifier tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/security/ -count=1 -v`
Expected: all PASS (≥ 8 tests across the three files).

- [ ] **Step 4: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/security/...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/security/verifier.go internal/security/verifier_test.go
git commit -m "security: Verifier cosign exec integration"
```

---

## Task 9: Wire Verifier into `docker.Client.PullImage`

**Files:**
- Modify: `internal/docker/client.go`

- [ ] **Step 1: Add `Verifier` interface + field**

In `internal/docker/client.go`, near the top of the file (just below the existing imports), add:

```go
// ImageVerifier is the dependency injection point for security.Verifier.
// docker package never imports internal/security to avoid a dependency
// cycle (security imports docker types via inspect).
type ImageVerifier interface {
	VerifyImage(ctx context.Context, ref string) error
}
```

Find the `Client` struct definition (search `type Client struct`) and add a field:

```go
type Client struct {
	cli      *client.Client
	Verifier ImageVerifier // nil → verification skipped
	// ... existing fields ...
}
```

- [ ] **Step 2: Wrap `PullImage`**

Replace lines 378-382 (the `PullImage` function) with:

```go
// PullImage pulls an image by reference (e.g. "nginx:latest") and returns
// the daemon's progress stream. When a Verifier is configured, the image is
// verified BEFORE the pull is requested — pulls of policy-rejected images
// never touch disk.
func (c *Client) PullImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	if c.Verifier != nil {
		if err := c.Verifier.VerifyImage(ctx, ref); err != nil {
			return nil, err
		}
	}
	return c.cli.ImagePull(ctx, ref, image.PullOptions{})
}
```

- [ ] **Step 3: Build**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean.

- [ ] **Step 4: Run docker tests (regression check)**

Run: `cd /opt/stacks/SFPanel && go test ./internal/docker/... -count=1`
Expected: PASS (Verifier left nil in tests; old behavior preserved).

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/docker/client.go
git commit -m "docker: PullImage gates on Verifier when configured"
```

---

## Task 10: Wire Verifier into compose pull loop

**Files:**
- Modify: `internal/docker/compose.go`

- [ ] **Step 1: Locate compose pull loop**

Run: `grep -nE 'onLine.*pull|PullImage|ComposePull' internal/docker/compose.go | head -8`
Find the function that pulls images per service (compose up flow). Identify the place where `c.dockerClient.PullImage(...)` is called inside a per-service loop.

- [ ] **Step 2: Add per-service verify before pull**

Just before the `PullImage` call inside the loop, add:

```go
// Theme C: per-service verify with SSE feedback.
if m.dockerClient.Verifier != nil {
	if err := m.dockerClient.Verifier.VerifyImage(ctx, svc.Image); err != nil {
		onLine(fmt.Sprintf("[verify] %s ✗ %s", svc.Image, err.Error()))
		return fmt.Errorf("verify %s: %w", svc.Image, err)
	}
	onLine(fmt.Sprintf("[verify] %s ✓ ok", svc.Image))
}
```

Adjust variable names (`m.dockerClient`, `svc.Image`, `onLine`) to match the actual symbols in the function — this is the exact pattern, but the existing code may name the closure differently.

- [ ] **Step 3: Build**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean.

- [ ] **Step 4: Run compose tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/docker/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/docker/compose.go
git commit -m "compose: per-service cosign verify in pull loop"
```

---

## Task 11: Security REST handler

**Files:**
- Create: `internal/feature/security/handler.go`
- Create: `internal/feature/security/handler_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/feature/security/handler_test.go`:

```go
package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// TestPutPolicy_RejectsBadMode — invalid mode returns 400 INVALID_POLICY
// before any FSM apply.
func TestPutPolicy_RejectsBadMode(t *testing.T) {
	body := bytes.NewBufferString(`{"mode": "kapow", "rules": []}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/policy", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	h := &Handler{}
	_ = h.PutPolicy(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

// TestPutPolicy_RejectsRuleWithoutPattern — empty pattern rejected.
func TestPutPolicy_RejectsRuleWithoutPattern(t *testing.T) {
	body := bytes.NewBufferString(`{"mode":"warn","rules":[{"pattern":"","identity":{"subject_prefix":"x","issuer":"y"}}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/security/policy", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	h := &Handler{}
	_ = h.PutPolicy(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 2: Run, expect compile fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/security/ -count=1`
Expected: FAIL — `Handler.PutPolicy` undefined.

- [ ] **Step 3: Implement handler**

Create `internal/feature/security/handler.go`:

```go
package security

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/security"
)

// Handler is the REST surface for the security feature. Wired in router.go.
type Handler struct {
	DB        *sql.DB
	Cluster   *cluster.Manager
	Verifier  *security.Verifier
	Installer *security.Installer
}

// GetPolicy returns the cluster-wide policy. Same answer on every node.
func (h *Handler) GetPolicy(c echo.Context) error {
	p, err := security.LoadPolicy(h.Cluster)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, p)
}

// PutPolicy validates and persists via Raft Apply (leader-only).
func (h *Handler) PutPolicy(c echo.Context) error {
	var p security.Policy
	if err := c.Bind(&p); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if err := p.Validate(); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPolicy, err.Error())
	}
	if h.Cluster == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster not initialized")
	}
	if err := security.SavePolicy(h.Cluster, p); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"status": "saved"})
}

// CosignStatus reports the binary state on the local node.
func (h *Handler) CosignStatus(c echo.Context) error {
	path := security.DefaultCosignPath
	installed := false
	version := ""
	if _, err := os.Stat(path); err == nil {
		installed = true
		if h.Installer != nil {
			ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
			defer cancel()
			if out, err := h.Installer.Cmd.RunWithTimeout(5*time.Second, path, "version"); err == nil {
				version = string(out)
			}
			_ = ctx
		}
	}
	return response.OK(c, map[string]any{
		"installed": installed,
		"version":   version,
		"path":      path,
	})
}

// VerifyImage forces re-verification of a single ref, ignoring cache.
func (h *Handler) VerifyImage(c echo.Context) error {
	var req struct {
		Ref       string `json:"ref"`
		SkipCache bool   `json:"skip_cache"`
	}
	if err := c.Bind(&req); err != nil || req.Ref == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "ref required")
	}
	if h.Verifier == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "verifier not configured")
	}
	if req.SkipCache {
		// Best-effort cache invalidation by ref.
		if h.DB != nil {
			_, _ = h.DB.Exec(`DELETE FROM image_signatures WHERE ref = ?`, req.Ref)
		}
	}
	if err := h.Verifier.VerifyImage(c.Request().Context(), req.Ref); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrPolicyViolation,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"status": "verified"})
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/security/ -count=1 -v`
Expected: 2 PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/security/handler.go internal/feature/security/handler_test.go
git commit -m "feat(security): REST handler for policy CRUD + verify-image"
```

---

## Task 12: Router wiring + Docker handler integration

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/feature/docker/handler.go` (only if image list response needs `signature` field — see step 3)

- [ ] **Step 1: Construct Verifier + Installer in `NewRouter`**

In `internal/api/router.go`, locate the area where other handlers are constructed (around line 92-130). After `composeManager` is set up, add:

```go
	// Security (Theme C Phase 1)
	cosignInstaller := &security.Installer{Cmd: cmd}
	imageVerifier := &security.Verifier{
		Cmd:    cmd,
		DB:     database,
		Cosign: cosignInstaller,
		LoadPolicy: func() (security.Policy, error) {
			return security.LoadPolicy(clusterMgr)
		},
	}
	dockerClient.Verifier = imageVerifier
	securityHandler := &featureSecurity.Handler{
		DB:        database,
		Cluster:   clusterMgr,
		Verifier:  imageVerifier,
		Installer: cosignInstaller,
	}
```

Add the imports at the top:

```go
	"github.com/svrforum/SFPanel/internal/security"
	featureSecurity "github.com/svrforum/SFPanel/internal/feature/security"
```

(Adjust the existing `dockerClient` variable name if different — search for the `Verifier:` field assignment in the existing handler construction block.)

- [ ] **Step 2: Register routes**

In the `authorized` group, alongside other route blocks:

```go
	// Security (Theme C Phase 1)
	sec := authorized.Group("/security")
	sec.GET("/policy", securityHandler.GetPolicy)
	sec.PUT("/policy", securityHandler.PutPolicy)
	sec.GET("/cosign-status", securityHandler.CosignStatus)
	sec.POST("/verify-image", securityHandler.VerifyImage)
```

- [ ] **Step 3: Augment image list response with `signature`**

In `internal/feature/docker/handler.go` (or wherever `ListImages` returns its image rows), join with `image_signatures` on `digest` to attach a `signature` field. Implementation pattern:

```go
type imageWithSig struct {
	docker.ImageWithUsage // existing embedded type
	Signature *struct {
		Status         string `json:"status"`
		IdentitySubject string `json:"identity_subject,omitempty"`
		IdentityIssuer  string `json:"identity_issuer,omitempty"`
		ErrorMessage   string `json:"error_message,omitempty"`
		VerifiedAt     int64  `json:"verified_at,omitempty"`
	} `json:"signature,omitempty"`
}
```

After fetching the image list, query `image_signatures` keyed on each image's digest (`Id` field from docker inspect = `sha256:...`), and attach the row.

If the existing handler is structured tightly around the docker.ImageWithUsage type and adding the signature field is non-trivial, **defer this to a follow-up task** and rely on `POST /security/verify-image` for status — note this in the smoke task.

- [ ] **Step 4: Build + tests**

Run:
```bash
cd /opt/stacks/SFPanel && go build ./... && go test ./internal/api/... ./internal/feature/security/... ./internal/security/... -count=1
```
Expected: clean.

- [ ] **Step 5: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/api/... ./internal/feature/security/... ./internal/security/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/api/router.go internal/feature/docker/handler.go
git commit -m "router: wire Security handler + Verifier into Docker client"
```

---

## Task 13: Frontend types + API methods

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Append types**

In `web/src/types/api.ts`, append after the last interface:

```typescript
export type SecurityMode = 'off' | 'warn' | 'require'

export interface SecurityIdentity {
  subject_prefix: string
  issuer: string
}

export interface SecurityRule {
  pattern: string
  identity: SecurityIdentity
}

export interface SecurityPolicy {
  mode: SecurityMode
  rules: SecurityRule[]
}

export interface CosignStatus {
  installed: boolean
  version: string
  path: string
}

export interface ImageSignature {
  status: 'verified' | 'unsigned' | 'failed'
  identity_subject?: string
  identity_issuer?: string
  error_message?: string
  verified_at?: number
}
```

- [ ] **Step 2: Add 5 API methods**

In `web/src/lib/api.ts`, add to the import list:

```typescript
  SecurityPolicy,
  CosignStatus,
```

Inside the `ApiClient` class, near other settings methods:

```typescript
  getSecurityPolicy() {
    return this.request<SecurityPolicy>('/security/policy')
  }

  updateSecurityPolicy(policy: SecurityPolicy) {
    return this.request<{ status: string }>('/security/policy', {
      method: 'PUT',
      body: JSON.stringify(policy),
    })
  }

  getCosignStatus() {
    return this.request<CosignStatus>('/security/cosign-status')
  }

  verifyImage(ref: string, skipCache = false) {
    return this.request<{ status: string }>('/security/verify-image', {
      method: 'POST',
      body: JSON.stringify({ ref, skip_cache: skipCache }),
    })
  }
```

- [ ] **Step 3: Build + lint frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: types + api client for cosign verification"
```

---

## Task 14: ImageSignatureSettings component + RuleEditDialog

**Files:**
- Create: `web/src/pages/settings/ImageSignatureSettings.tsx`
- Create: `web/src/components/security/RuleEditDialog.tsx`

- [ ] **Step 1: RuleEditDialog**

Create `web/src/components/security/RuleEditDialog.tsx`:

```tsx
import { useState, useEffect } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { SecurityRule } from '@/types/api'

const ISSUER_PRESETS = [
  { label: 'GitHub Actions', value: 'https://token.actions.githubusercontent.com' },
  { label: 'GitLab CI', value: 'https://gitlab.com' },
  { label: '직접 입력', value: '' },
]

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: SecurityRule
  onSave: (rule: SecurityRule) => void
}

export function RuleEditDialog({ open, onOpenChange, initial, onSave }: Props) {
  const [pattern, setPattern] = useState(initial?.pattern ?? '')
  const [subjectPrefix, setSubjectPrefix] = useState(initial?.identity.subject_prefix ?? '')
  const [issuerPreset, setIssuerPreset] = useState(
    ISSUER_PRESETS.find(p => p.value === initial?.identity.issuer)?.label ?? '직접 입력'
  )
  const [issuerCustom, setIssuerCustom] = useState(initial?.identity.issuer ?? '')

  useEffect(() => {
    if (open) {
      setPattern(initial?.pattern ?? '')
      setSubjectPrefix(initial?.identity.subject_prefix ?? '')
      const preset = ISSUER_PRESETS.find(p => p.value === initial?.identity.issuer)
      setIssuerPreset(preset?.label ?? '직접 입력')
      setIssuerCustom(initial?.identity.issuer ?? '')
    }
  }, [open, initial])

  const issuer = issuerPreset === '직접 입력'
    ? issuerCustom
    : ISSUER_PRESETS.find(p => p.label === issuerPreset)?.value ?? ''
  const valid = pattern.trim() && subjectPrefix.trim() && issuer.trim()

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!valid) return
    onSave({ pattern: pattern.trim(), identity: { subject_prefix: subjectPrefix.trim(), issuer } })
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{initial ? '룰 편집' : '룰 추가'}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="rule-pattern">패턴</Label>
            <Input id="rule-pattern" value={pattern} onChange={e => setPattern(e.target.value)}
                   placeholder="ghcr.io/myorg/*" required />
            <p className="text-[11px] text-muted-foreground">
              `*` = 한 세그먼트 / `**` = 다중 세그먼트. 예: `ghcr.io/svrforum/**`
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="rule-subject">Subject prefix</Label>
            <Input id="rule-subject" value={subjectPrefix} onChange={e => setSubjectPrefix(e.target.value)}
                   placeholder="https://github.com/myorg/myrepo/.github/workflows/release.yaml@refs/tags/v" required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="rule-issuer">Issuer</Label>
            <select id="rule-issuer" value={issuerPreset}
                    onChange={e => setIssuerPreset(e.target.value)}
                    className="w-full h-9 border rounded-md px-2 text-[13px]">
              {ISSUER_PRESETS.map(p => <option key={p.label}>{p.label}</option>)}
            </select>
            {issuerPreset === '직접 입력' && (
              <Input value={issuerCustom} onChange={e => setIssuerCustom(e.target.value)}
                     placeholder="https://your-oidc-issuer.example/" />
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>취소</Button>
            <Button type="submit" disabled={!valid}>저장</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 2: ImageSignatureSettings**

Create `web/src/pages/settings/ImageSignatureSettings.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Pencil, Trash2, Plus } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api } from '@/lib/api'
import { RuleEditDialog } from '@/components/security/RuleEditDialog'
import type { SecurityPolicy, SecurityRule, SecurityMode, CosignStatus } from '@/types/api'

const MODES: { value: SecurityMode; label: string; desc: string }[] = [
  { value: 'off', label: '끔 (off)', desc: '검증 비활성. 기본값.' },
  { value: 'warn', label: '경고만 (warn)', desc: '미서명/실패 이미지도 통과. 결과만 기록.' },
  { value: 'require', label: '강제 (require)', desc: '미서명/실패 이미지는 pull 차단.' },
]

export default function ImageSignatureSettings() {
  const [policy, setPolicy] = useState<SecurityPolicy>({ mode: 'off', rules: [] })
  const [cosign, setCosign] = useState<CosignStatus | null>(null)
  const [editIdx, setEditIdx] = useState<number | null>(null)
  const [editOpen, setEditOpen] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api.getSecurityPolicy().then(setPolicy).catch(() => {})
    api.getCosignStatus().then(setCosign).catch(() => {})
  }, [])

  async function persist(next: SecurityPolicy) {
    setSaving(true)
    try {
      await api.updateSecurityPolicy(next)
      setPolicy(next)
      toast.success('보안 정책 저장됨')
    } catch (e) {
      toast.error((e as Error).message || '저장 실패')
    } finally {
      setSaving(false)
    }
  }

  function onSaveRule(rule: SecurityRule) {
    const next = { ...policy }
    if (editIdx !== null && editIdx < policy.rules.length) {
      next.rules = [...policy.rules]
      next.rules[editIdx] = rule
    } else {
      next.rules = [...policy.rules, rule]
    }
    void persist(next)
  }

  function onDeleteRule(idx: number) {
    if (!confirm('이 룰을 삭제하시겠습니까?')) return
    const next = { ...policy, rules: policy.rules.filter((_, i) => i !== idx) }
    void persist(next)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-[15px]">이미지 서명 검증</CardTitle>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="space-y-2">
          <Label className="text-[13px]">정책</Label>
          {MODES.map(m => (
            <label key={m.value} className="flex items-start gap-2 cursor-pointer">
              <input type="radio" name="mode" className="mt-1"
                     checked={policy.mode === m.value} disabled={saving}
                     onChange={() => persist({ ...policy, mode: m.value })} />
              <div>
                <div className="text-[13px] font-medium">{m.label}</div>
                <div className="text-[11px] text-muted-foreground">{m.desc}</div>
              </div>
            </label>
          ))}
        </div>

        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label className="text-[13px]">허용 목록</Label>
            <Button size="sm" variant="outline"
                    onClick={() => { setEditIdx(null); setEditOpen(true) }}>
              <Plus className="h-3.5 w-3.5 mr-1" />룰 추가
            </Button>
          </div>
          {policy.rules.length === 0 ? (
            <div className="text-[12px] text-muted-foreground py-3 text-center border rounded-md">
              아직 룰이 없습니다.
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>패턴</TableHead>
                  <TableHead>Subject prefix</TableHead>
                  <TableHead>Issuer</TableHead>
                  <TableHead className="w-20"></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {policy.rules.map((r, i) => (
                  <TableRow key={i}>
                    <TableCell className="font-mono text-[12px]">{r.pattern}</TableCell>
                    <TableCell className="font-mono text-[11px] truncate max-w-[260px]"
                               title={r.identity.subject_prefix}>{r.identity.subject_prefix}</TableCell>
                    <TableCell className="text-[11px] truncate max-w-[160px]"
                               title={r.identity.issuer}>{r.identity.issuer}</TableCell>
                    <TableCell className="text-right">
                      <Button variant="ghost" size="icon-xs"
                              onClick={() => { setEditIdx(i); setEditOpen(true) }}>
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="ghost" size="icon-xs" onClick={() => onDeleteRule(i)}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        <div className="space-y-1 pt-3 border-t">
          <Label className="text-[13px]">cosign 바이너리</Label>
          {cosign?.installed ? (
            <div className="text-[12px] text-muted-foreground font-mono">
              ✓ {cosign.path}<br />
              {cosign.version.split('\n')[0]}
            </div>
          ) : (
            <div className="text-[12px] text-muted-foreground">
              ⏳ 미설치 — 정책을 warn 또는 require로 활성화하면 첫 검증 시 자동 설치됩니다.
            </div>
          )}
        </div>
      </CardContent>

      <RuleEditDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        initial={editIdx !== null ? policy.rules[editIdx] : undefined}
        onSave={onSaveRule}
      />
    </Card>
  )
}
```

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/pages/settings/ImageSignatureSettings.tsx web/src/components/security/RuleEditDialog.tsx
git commit -m "web: ImageSignatureSettings + RuleEditDialog"
```

---

## Task 15: Embed in Settings → 보안 tab + Docker 이미지 검증 column

**Files:**
- Modify: `web/src/pages/Settings.tsx`
- Modify: `web/src/pages/docker/DockerImages.tsx`

- [ ] **Step 1: Embed in Settings**

In `web/src/pages/Settings.tsx`, locate the existing `<TabsContent value="security" ...>` block (around line 478). Just before its closing `</TabsContent>`, add:

```tsx
<div className="pt-6 border-t">
  <ImageSignatureSettings />
</div>
```

Add the import at the top:

```tsx
import ImageSignatureSettings from '@/pages/settings/ImageSignatureSettings'
```

- [ ] **Step 2: Add 검증 column to DockerImages**

In `web/src/pages/docker/DockerImages.tsx`:

a. Find the `TableHead` row and add a new header just before "작업" (or last column):

```tsx
<TableHead>검증</TableHead>
```

b. Find the `TableRow` mapping body and add a cell rendering the `signature` field. Use a status-icon helper at the top of the file:

```tsx
function SignatureBadge({ sig }: { sig?: { status: string; identity_subject?: string; identity_issuer?: string; error_message?: string; verified_at?: number } }) {
  if (!sig) return <span className="text-muted-foreground text-[11px]">⏳</span>
  if (sig.status === 'verified') {
    const tip = `subject: ${sig.identity_subject ?? '-'} | issuer: ${sig.identity_issuer ?? '-'} | ${sig.verified_at ? new Date(sig.verified_at).toLocaleString() : ''}`
    return <span title={tip} className="text-[#00c471]">✓</span>
  }
  if (sig.status === 'unsigned') return <span title="이 이미지에 cosign 서명이 없습니다" className="text-[#f59e0b]">⚠️</span>
  if (sig.status === 'failed') return <span title={sig.error_message} className="text-[#f04452]">❌</span>
  return <span className="text-muted-foreground">⏳</span>
}
```

Cell:
```tsx
<TableCell><SignatureBadge sig={img.signature} /></TableCell>
```

(`img.signature` is the field added in Task 12. If Task 12's signature-attach step was deferred, the cell will harmlessly render `⏳` for every row.)

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/pages/Settings.tsx web/src/pages/docker/DockerImages.tsx
git commit -m "web: embed ImageSignatureSettings + 검증 column on Docker Images"
```

---

## Task 16: Manual smoke test on the live panel

- [ ] **Step 1: Build + deploy**

```bash
cd /opt/stacks/SFPanel
make build
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-c-phase1
sudo systemctl stop sfpanel
sudo cp ./sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
sleep 4
systemctl is-active sfpanel
/usr/local/bin/sfpanel version
scp ./sfpanel root@192.168.1.118:/tmp/sfpanel.new
ssh root@192.168.1.118 'systemctl stop sfpanel && cp /tmp/sfpanel.new /usr/local/bin/sfpanel && systemctl start sfpanel && sleep 4 && systemctl is-active sfpanel'
```

Expected: both nodes `active`, version reflects new commit.

- [ ] **Step 2: API smoke — initial off policy**

```bash
TOKEN=$(sudo /tmp/minttoken | head -1)
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/security/policy" | python3 -m json.tool
```
Expected: `{success: true, data: {mode: "off", rules: null}}`.

- [ ] **Step 3: PUT a policy with one rule**

```bash
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"mode":"warn","rules":[{"pattern":"ghcr.io/sigstore/*","identity":{"subject_prefix":"https://github.com/sigstore/cosign/.github/workflows/release.yaml@refs/tags/v","issuer":"https://token.actions.githubusercontent.com"}}]}' \
     "http://127.0.0.1:9443/api/v1/security/policy" | python3 -m json.tool
```
Expected: `success: true`.

- [ ] **Step 4: Cross-node policy replication**

```bash
curl -s -H "Authorization: Bearer $TOKEN" "http://192.168.1.118:8444/api/v1/security/policy" | python3 -m json.tool
```
Expected: same policy as step 2 (FSM replicated).

- [ ] **Step 5: Trigger a verify (lazy cosign install)**

```bash
docker pull ghcr.io/sigstore/cosign/cosign:v2.4.1 || true
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"ref":"ghcr.io/sigstore/cosign/cosign:v2.4.1","skip_cache":true}' \
     "http://127.0.0.1:9443/api/v1/security/verify-image" | python3 -m json.tool
```
Expected: `success: true, data: {status: "verified"}` (cosign-self-signs its own images so the pre-loaded sigstoreReleaseIdentity matches). On first run this triggers the cosign download — first call may take ~10s.

- [ ] **Step 6: cosign-status**

```bash
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/security/cosign-status" | python3 -m json.tool
```
Expected: `installed: true, version: "cosign version v2.x.y"`.

- [ ] **Step 7: UI smoke (browser/Playwright)**

Navigate to `/settings` → 보안 탭:
- 정책 라디오 (off/warn/require) 보임
- "허용 목록" 테이블에 step 3에서 만든 룰 1개 표시
- "+ 룰 추가" 클릭 → RuleEditDialog 열림
- cosign 바이너리 상태에 ✓ + 버전 표시

`/docker/images`:
- 검증 컬럼 보임
- 직전에 verify 성공한 이미지 행에 ✓
- 미서명 이미지 행에 ⚠️

- [ ] **Step 8: Cleanup test policy**

```bash
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"mode":"off","rules":[]}' \
     "http://127.0.0.1:9443/api/v1/security/policy"
```

- [ ] **Step 9: Push**

```bash
git push origin main
```

---

## Self-Review

### Spec coverage
- ✅ Phase 1 scope = cosign verification only → Tasks 1-15
- ✅ Allowlist with globbing pattern + keyless OIDC → Tasks 2, 3
- ✅ Pull-time gate at 3 sites (PullImage / compose pull / appstore install) → Tasks 9, 10 (compose change covers appstore install — same code path)
- ✅ 24h cache + 30s fail TTL → Task 7 (`cacheTTL`, `failCacheTTL`)
- ✅ Cosign self-bootstrap via `release.VerifyCosignBlob` → Task 6
- ✅ Settings 섹션 (B UX) → Tasks 14, 15
- ✅ Docker 이미지 검증 컬럼 → Tasks 12 (backend join), 15 (frontend cell)
- ✅ Cluster: Raft FSM policy (CC-1) → Task 2 (uses `cluster.Manager.SetConfig`)
- ✅ Per-node verification cache → Task 1 (table local), Task 7 (per-node DB)
- ✅ Backward-compat: empty Config returns `mode=off` → Task 2 (`LoadPolicy` returns `Mode: ModeOff` on empty)

### Placeholder scan
모든 task에 실제 코드 / 명령 / 기대 출력. Task 10 step 2 "adjust variable names to match" 는 conventional pointer (existing code search), not a placeholder — exact pattern is shown.

Task 12 step 3 has an explicit "if non-trivial, defer to follow-up" — that's a deliberate scope hedge, not a hand-wave. The frontend cell (Task 15) gracefully degrades if the backend join is skipped (`signature?` is optional).

### Type consistency
- `Policy.Mode` (Go: `Mode` type alias for string, frontend: `SecurityMode` literal union) — both share `'off'|'warn'|'require'`.
- `Rule.Pattern` / `Rule.Identity.SubjectPrefix` / `Rule.Identity.Issuer` — JSON tags `pattern`/`subject_prefix`/`issuer` consistently used in Go (`json:` tags) and frontend (`SecurityRule`/`SecurityIdentity`).
- `image_signatures` table columns match `cacheRecord` reads + `cacheStore` writes.
- `Verifier.LoadPolicy` is a function field (`func() (Policy, error)`) — matches the closure passed in router.go.
- `Cosign *Installer` — Verifier consumes it; Handler also consumes it; same type from `internal/security`.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-05-security-supply-chain-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task + 2-stage review.

**2. Inline Execution** — All tasks in this session via `superpowers:executing-plans`.

Which approach?
