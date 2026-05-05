# Theme C Phase 1 — Cosign Image Verification (Design)

> **Status:** approved (2026-05-05). Roadmap entry:
> `docs/superpowers/roadmaps/2026-05-03-docker-management-roadmap.md` §C.
> Phase 1 deliberately scopes down to cosign verification; CVE scanning,
> digest pinning, and signed-catalog support land in later phases.

## Goal

Extend SFPanel's existing keyless cosign infrastructure (today verifying
its own update binary) to verify container images on pull. Operator
declares an allowlist of `(pattern, identity)` rules; pulls of matching
images go through `cosign verify` and are gated by a global policy mode
(`off` / `warn` / `require`).

## Non-goals (Phase 1)

- Key-based cosign signatures (public key verification). Keyless OIDC only.
- Rekor inclusion proof verification (cosign CLI handles internally).
- CVE scanning (Phase 2).
- Digest pinning mode that rewrites compose YAML (Phase 2).
- Cosign-signed AppStore catalogs (Theme E Phase 2; reuses verifier).
- Notary v1 / Docker Content Trust (intentional, not on roadmap).
- RBAC / per-user trust policies (separate roadmap).

## Architecture

```
docker pull <image>
   │
   ▼
┌─ verifier.VerifyImage(ref) ─────────────────────────────────┐
│  1. Look up policy mode from FSM (security.policy)          │
│     mode=off → return nil immediately                       │
│  2. Resolve image digest via docker inspect                 │
│  3. Cache hit on (digest, expires_at>now)?                  │
│     status=verified → return nil                            │
│     status=failed   → return ErrPolicyViolation             │
│     status=unsigned → mode-dependent                        │
│  4. Find matching allowlist rule (registry/pattern globbing) │
│     no rule + mode=require → ErrPolicyViolation             │
│     no rule + mode=warn    → log+toast, cache as unsigned   │
│  5. EnsureCosign(ctx) → /var/lib/sfpanel/bin/cosign         │
│     not installed → bootstrap + self-verify                 │
│  6. Cmd.RunWithTimeout(30s, cosign, "verify", ref,          │
│         "--certificate-identity-regexp=...",                │
│         "--certificate-oidc-issuer=...")                    │
│     success → cache verified, return nil                    │
│     failure → mode-dependent (warn=pass+log, require=err)   │
└──────────────────────────────────────────────────────────────┘
   │
   ▼
proceed with pull / abort with sanitized error
```

## Components

Each file owns one responsibility, well-defined external API.

### `internal/security/verifier.go`

```go
type Verifier struct {
    Cmd     exec.Commander
    DB      *sql.DB
    Cluster *cluster.Manager
    Cosign  *Installer
}

// VerifyImage gates an image pull against the cluster security policy.
// Returns nil if the policy permits the pull. Returns ErrPolicyViolation
// when require-mode rejects it. Always records a row in image_signatures.
func (v *Verifier) VerifyImage(ctx context.Context, ref string) error
```

- Cache hot path ≤ 5ms (SQLite primary-key lookup on digest).
- Cache miss requires one `docker inspect` (local socket, ~10ms) + one
  `cosign verify` shell-out (1-3s typical).
- Concurrent pulls of the same image: SQLite `INSERT OR REPLACE` keyed
  on digest is race-safe.

### `internal/security/install.go`

Cosign self-bootstrap.

```go
type Installer struct {
    Cmd     exec.Commander
    HTTPGet func(ctx context.Context, url string) ([]byte, error)
}

// EnsureCosign returns the path to a verified cosign binary, installing
// it on first call. Returns ErrInstallFailed (with sanitized cause) if
// download or signature verification fails. Looks for a manually placed
// binary at /etc/sfpanel/cosign as a fallback before failing.
func (i *Installer) EnsureCosign(ctx context.Context) (string, error)
```

Bootstrap flow:
1. Already at `/var/lib/sfpanel/bin/cosign` with `cosign version` ≥ 2.x?
   Return path.
2. Fetch three files from `https://github.com/sigstore/cosign/releases/
   download/v<latest-2.x>/`:
   - `cosign-linux-amd64`
   - `cosign-linux-amd64-keyless.pem`
   - `cosign-linux-amd64-keyless.sig`
3. Use existing `release.VerifyCosignBlob(binary, sig, pem, identity)`
   where identity is hardcoded:
   ```go
   var sigstoreReleaseIdentity = release.CosignIdentity{
       SubjectPrefix: "https://github.com/sigstore/cosign/.github/" +
                      "workflows/release.yaml@refs/tags/v",
       Issuer:        "https://token.actions.githubusercontent.com",
   }
   ```
4. Verification passes: `0755` install at canonical path, `cosign version`
   sanity check, return path.
5. Verification fails: delete downloaded artifacts, return error.
6. Network fails: try `/etc/sfpanel/cosign` fallback. If exists and
   `cosign version` succeeds, use it (operator-vouched).

Lazy: triggered by first verifier call after mode flips off→warn/require.

### `internal/security/policy.go`

```go
type Policy struct {
    Mode  string `json:"mode"`  // "off" | "warn" | "require"
    Rules []Rule `json:"rules"`
}

type Rule struct {
    Pattern  string   `json:"pattern"`  // glob: "ghcr.io/myorg/*"
    Identity Identity `json:"identity"`
}

type Identity struct {
    SubjectPrefix string `json:"subject_prefix"`
    Issuer        string `json:"issuer"`
}

// LoadPolicy reads the current cluster-wide policy from FSM. Returns
// (Policy{Mode:"off"}, nil) if no policy is set — fresh clusters and
// pre-Theme-C upgrades both land here without surprise.
func LoadPolicy(c *cluster.Manager) (Policy, error)

// SavePolicy writes via cluster.SetConfig (Raft Apply, leader-only).
func SavePolicy(c *cluster.Manager, p Policy) error

// MatchRule returns the first matching Rule for ref, or (Rule{}, false)
// if no rule matches. Pattern uses globbing (* = single registry path
// segment, ** = multiple segments). Implicit docker.io/library/ is
// normalized before matching.
func (p Policy) MatchRule(ref string) (Rule, bool)
```

Storage: FSM `Config["security.policy"]` = JSON string of `Policy`.

### `internal/feature/security/handler.go`

REST handlers. Standard SFPanel pattern: `Handler` struct with injected
dependencies, registered once in `internal/api/router.go`.

### `internal/db/migrations.go` — new entry

```sql
CREATE TABLE IF NOT EXISTS image_signatures (
  digest           TEXT PRIMARY KEY,
  ref              TEXT NOT NULL,
  status           TEXT NOT NULL,
  identity_subject TEXT,
  identity_issuer  TEXT,
  error_message    TEXT,
  verified_at      INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_image_signatures_ref
  ON image_signatures(ref);
CREATE INDEX IF NOT EXISTS idx_image_signatures_expires
  ON image_signatures(expires_at);
```

Pruner: 24h TTL on cache rows. Reuse existing audit-pruner cadence in
`internal/feature/audit/`.

### Pull-time integration (3 sites)

a. **`internal/docker/client.go:380` `PullImage`** — wraps `cli.ImagePull`
   with `Verifier.VerifyImage(ctx, ref)` pre-flight.

b. **`internal/docker/compose.go`** — for every service `image:` field
   in the compose file, call `VerifyImage` before `docker compose up -d`.
   Stream `[verify] <ref> ✓/⚠️/✗ ...` lines into the existing SSE
   pipeline.

c. **`internal/feature/appstore/handler.go:690`** — install flow uses
   the same compose pull path; passes through (b) automatically.

Failure surface:
- `mode=off`: verifier.VerifyImage skipped at the top.
- `mode=warn`: failures log via `slog.Warn(component=security)`, recorded
  in `image_signatures(status=unsigned|failed)`, do NOT abort.
- `mode=require`: failures return `ErrPolicyViolation`, abort the pull,
  surfaced to UI with sanitized cosign stderr.

## Data Flow

```
Operator                          Leader                Follower(s)
  │                                  │                       │
  │ PUT /security/policy             │                       │
  ├─────────────────────────────────►│                       │
  │                                  │ cluster.SetConfig     │
  │                                  │ → Raft Apply          │
  │                                  ├──── replicate ───────►│
  │ 200 OK                           │ ◄─── apply ───────────┤
  │ ◄────────────────────────────────┤                       │
  │                                                          │
  │  (any node) docker compose up                            │
  │  → for each service.image:                               │
  │      Verifier.VerifyImage()                              │
  │      → policy = FSM read (replicated, sub-second lag)    │
  │      → cosign verify  ──► ImageSig cache (per-node)      │
```

## UX

### Settings → 보안 section

New section in the existing Settings page (NOT a new top-level page —
Phase 1 content alone wouldn't fill one). Layout:

- **이미지 서명 검증** — radio buttons (off/warn/require) + one-line
  description per mode.
- **허용 목록** — table of rules (pattern, identity), per-row edit + delete,
  "+ 룰 추가" button at bottom.
- **cosign 바이너리 상태** — installed path + version, "수동 재설치"
  button (POST /security/cosign-install, SSE stream).

Edit-rule dialog:
- Pattern (free text, globbing)
- Subject prefix (URL, free text)
- Issuer (preset dropdown: GitHub Actions / GitLab CI / Custom)

### Docker → 이미지 page: "검증" column

New column on the existing image list. Status icon + tooltip:
- ✓ verified — `subject: … | issuer: … | verified at: …`
- ⚠️ unsigned — `이 이미지에 cosign 서명이 없습니다`
- ❌ failed — sanitized cosign stderr
- ⏳ unknown — `아직 검증되지 않음 (정책 끔 또는 캐시 만료)`

Row click → modal with raw cosign output, full identity, "지금 재검증"
button (POST /security/verify-image with `cache=skip`).

## API

| Method | Path                              | Description                                                      |
|--------|-----------------------------------|------------------------------------------------------------------|
| GET    | `/api/v1/security/policy`         | Current policy (FSM read; same answer on every node).            |
| PUT    | `/api/v1/security/policy`         | Update policy (Raft Apply, leader-only).                         |
| GET    | `/api/v1/security/cosign-status`  | `{installed, version, path}`.                                    |
| POST   | `/api/v1/security/cosign-install` | Manual reinstall, SSE stream.                                    |
| GET    | `/api/v1/docker/images`           | Existing — response gains `signature: {status, identity, verified_at}` field. |
| POST   | `/api/v1/security/verify-image`   | `{ref, skip_cache?: bool}` → fresh verification, returns result. |

## Cluster considerations

| Concern             | Resolution                                                                   |
|---------------------|------------------------------------------------------------------------------|
| Policy storage      | Raft FSM `Config["security.policy"]` (JSON). Fleet-wide consistency.         |
| Verification cache  | Per-node SQLite. Each node verifies its own pulls.                           |
| Cosign binary       | Per-node `/var/lib/sfpanel/bin/cosign`. Each node bootstraps independently.   |
| `?node=` proxying   | Policy GET ignores `?node=` (same answer everywhere). Image list uses it.    |
| Backward-compat     | Empty `state.Config["security.policy"]` interpreted as `{mode:"off"}`. Pre-Theme-C clusters: zero behavior change until policy explicitly enabled. |

## Error handling

| Scenario                                   | Behavior                                                                        |
|--------------------------------------------|---------------------------------------------------------------------------------|
| Policy = `off`                             | Verifier short-circuits at step 1. Zero overhead.                              |
| `cosign` not installed, mode=`warn`        | Lazy install. Failure → log+pass.                                               |
| `cosign` not installed, mode=`require`     | Lazy install. Failure → abort pull with clear remediation message.              |
| No internet during bootstrap               | Fall back to `/etc/sfpanel/cosign` (operator-placed). Otherwise install fails.  |
| Private registry, missing docker auth      | `cosign verify` fails with auth error. Surface stderr verbatim (sanitized).     |
| Multi-arch manifest                        | `docker inspect` returns manifest-list digest; cosign verifies the list itself. |
| Concurrent pulls of same image             | SQLite UNIQUE on digest + INSERT OR REPLACE. Race-safe.                         |
| Policy changes mid-verification            | In-flight calls finish under old policy; subsequent calls use new.              |
| `require` mode + no matching rule          | Fail with: "이 이미지를 검증할 룰이 정의되지 않았습니다 — 운영자가 패턴을 추가하거나 정책을 warn으로 낮추세요." |
| `cosign verify` timeout (30s)              | Cache as `failed` with 30s TTL (allow quick retry). require → abort.            |

All cosign stderr passes through `response.SanitizeOutput` before
reaching the user. Per-handler error codes added to
`internal/api/response/errors.go` (`ErrPolicyViolation`,
`ErrCosignInstallFailed`, `ErrInvalidPolicy`).

## Testing

**Required (per CLAUDE.md "Required" criteria):**

- `internal/security/verifier_test.go`
  - cache hit branches (verified / unsigned / failed)
  - allowlist matching (exact / wildcard / no-match)
  - mode dispatch (off skips, warn passes+records, require errs)
  - Uses `exec.MockCommander` to assert `cosign verify` argv and inject
    stdout/exit-code scenarios.
- `internal/security/policy_test.go`
  - JSON round-trip (Policy ↔ FSM Config string)
  - Empty / malformed input safe defaults
  - Pattern matching cases (docker.io/library normalization, `*`, `**`)
- `internal/security/install_test.go`
  - Bootstrap success path with mock blob+sig+pem fixtures
  - Verification failure → downloaded artifacts deleted
  - `/etc/sfpanel/cosign` fallback when install fails
- `internal/feature/security/handler_test.go`
  - PUT validation (bad pattern, bad URL, missing fields)
  - Leader-only Apply on PUT (follower returns 503 with clear error)

**Skipped (per CLAUDE.md "Not required"):**

- End-to-end cosign binary execution (CI cosign install cost). Manual
  smoke covers it once.
- UI tests (Phase 13 manual smoke, Playwright drive).

## Implementation phases

This is the design for **Phase 1 only**. Phase 2 (planned, not specced
here) builds on this:

- CVE scanning (trivy/grype) reuses install pattern + per-node cache
  table.
- Digest pinning rewriter consumes the same allowlist + verification
  results.
- AppStore catalog signing (Theme E Phase 2) reuses `Verifier` with a
  catalog-specific policy entry.

Everything Phase 2 needs is exposed by Phase 1's `Verifier` API surface;
no Phase 1 internals leak.

## Open questions for the implementation plan

- Cosign release pinning: do we always fetch latest 2.x, or pin a known
  version per SFPanel release? **Decision deferred to plan**: latest 2.x
  default, with operator override via `SFPANEL_COSIGN_VERSION` env.
- Identity issuer dropdown presets: how many do we ship? **Decision
  deferred to plan**: GitHub Actions + GitLab CI + Custom (3 entries).
