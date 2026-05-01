# Deployment / Install / Update Hardening Plan

> **Goal:** Address 30+ findings from the 2026-05-02 multi-agent audit covering install script, build/release pipeline, self-update flow, and cluster bootstrap. Land changes in 4 area-scoped commits with TDD where the change is security-sensitive.

**Architecture:** Direct edits in main branch. Each area is one or two commits. Security-sensitive code (concurrent update lock, semver downgrade guard, restart-flush sequencing, cluster non-voter promotion, token persistence) gets a Go test first. Trivial config edits (file modes, Node version, ldflag, `npm ci`) skip TDD because they're verified by `make build` / `make ci` / shellcheck.

**Out of scope (explicit gaps left for maintainer):**
- Cosign / GPG / minisign release signing — needs maintainer-managed private key. Adding a `release.Verifier` interface placeholder so future infrastructure can plug in.
- Desktop wrapper (`release-desktop.yml`) deeper changes — only Node version alignment.
- 10-year CA rotation tooling — out of scope, just a documentation note.

---

## Area A — Self-update integrity (Priority 1)

**Files:**
- Modify: `internal/feature/system/handler.go` (RunUpdate + new lock + restart sequencing)
- Modify: `cmd/sfpanel/main.go` (CLI updatePanel: same downgrade + flush fixes)
- Create: `internal/feature/system/handler_test.go` (lock + downgrade tests)
- Create: `internal/release/version.go` (semver compare helper)
- Create: `internal/release/version_test.go`

### A.1 Add semver compare in `internal/release/version.go`
- TDD: write tests first (newer/equal/older/invalid).
- Behavior: parse `MAJOR.MINOR.PATCH` (no pre-release support — releases are plain). Return -1 / 0 / 1.

### A.2 RunUpdate: refuse non-forward updates
- Replace `if latest == h.Version` with semver guard. Same in `cmd/sfpanel/main.go:333`.
- New error code in `internal/api/response/errors.go`: `ErrUpdateDowngrade`.

### A.3 RunUpdate: serialize concurrent updates
- Add `var updateMu sync.Mutex` at package level + `TryLock`. Second caller gets SSE `error: another update is in progress`.

### A.4 RunUpdate: stream archive to disk, not memory
- Replace `io.ReadAll(io.LimitReader(..., 200MB))` with a `*os.File` in `os.MkdirTemp` and `io.Copy` with hash and size limit. Avoids OOM on small nodes.

### A.5 RunUpdate: flush SSE complete + sleep before fork-restart
- Mirror cluster leave/disband pattern: `sendEvent("complete", ...)` → `flusher.Flush()` → `time.Sleep(2*time.Second)` → fork systemctl restart. Same fix in `cmd/sfpanel/main.go`.

### A.6 RunUpdate: better backup naming + log
- Old `.bak` rotation is out of scope (no auto-rollback watchdog this round — explicitly documented as a known gap because reliable health-check requires a separate sentinel process).

---

## Area B — Install script idempotency & UX (Priority 2)

**Files:** Modify `scripts/install.sh`

### B.1 systemd unit guard
- Wrap `setup_systemd` body so it only writes the unit file when missing OR when a hash check shows the canonical content unchanged. New flag: `--force-systemd` to override.

### B.2 logrotate guard
- Same pattern for `/etc/logrotate.d/sfpanel`.

### B.3 Post-start health check
- After `systemctl start`, poll `systemctl is-active` for up to 10 s. On failure: print `journalctl -u sfpanel -n 30` and exit 1.

### B.4 systemd presence detect
- Skip `setup_systemd` (with clear message) when `[ ! -d /run/systemd/system ]`. Lets the script work in containers without aborting mid-install.

### B.5 JWT secret entropy
- Replace base64-truncate with `openssl rand -hex 32`. Fall back to `xxd -l 32 -p /dev/urandom | tr -d '\n'` if openssl missing.

### B.6 sha256sum availability check
- Add `sha256sum` to `check_commands`.

### B.7 grep PCRE → POSIX
- `print_success` line 240: replace `grep -oP 'port:\s*\K[0-9]+'` with `awk '/^server:/{flag=1;next} flag&&/port:/{print $2;exit}'` so the script works on Alpine/busybox grep.

### B.8 uninstall removes logrotate
- Add `rm -f /etc/logrotate.d/sfpanel` in `uninstall()`.

### B.9 Debian/Ubuntu family check
- Add `[ -f /etc/debian_version ]` guard with a warning.

---

## Area C — Build / release pipeline (Priority 2)

**Files:**
- Modify: `Makefile` (ldflags + npm ci + CGO_ENABLED)
- Modify: `.goreleaser.yaml` (before-hooks, frontend embed guard)
- Modify: `.github/workflows/ci.yml` (Node version alignment, lint guard)
- Modify: `.github/workflows/release-desktop.yml` (Node alignment)
- Modify: `web.go` (init guard against empty embed)

### C.1 Makefile: real version injection + npm ci + CGO_ENABLED=0
```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build:
	cd web && npm ci && npm run build
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o sfpanel ./cmd/sfpanel
```

### C.2 .goreleaser.yaml: before hooks
- Add `before.hooks: ["bash -c 'cd web && npm ci && npm run build'"]` so `goreleaser release --clean` is self-contained.

### C.3 web.go: empty-embed sentinel at compile time
- Embed `web/dist/index.html` as a separate `//go:embed` line and `init()` panic if it's empty. Catches CI misconfiguration.

### C.4 ci.yml: Node 22 → 20 alignment
- Change all three jobs to `node-version: '20'` matching `release.yml`.

### C.5 ci.yml: stop swallowing frontend lint
- Remove `|| true` from `cd web && npm run lint`.

### C.6 release-desktop.yml: Node 20
- Already 20; verify and make explicit.

---

## Area D — Cluster bootstrap & ops (Priority 1+3)

**Files:**
- Modify: `internal/cluster/tls.go:141` (node cert permission)
- Modify: `internal/cluster/manager.go` (HandleJoin uses non-voter then promote)
- Modify: `internal/cluster/token.go` (ttl bound + clearer errors)
- Modify: `internal/cluster/raft_fsm.go` (CmdAddJoinToken / CmdConsumeJoinToken)
- Modify: `internal/feature/cluster/handler.go` (ClusterUpdate quorum guard for simultaneous mode)

### D.1 Node cert permission 0644 → 0600
- `tls.go:141`. Trivial.

### D.2 Cluster simultaneous-update quorum guard
- Before fanning out, count voters & require `voters - simultaneous_count >= quorum`. Otherwise refuse with `ErrUpdateUnsafe`.

### D.3 Token persistence via Raft FSM
- New FSM commands: `CmdAddJoinToken`, `CmdConsumeJoinToken`. TokenManager becomes a thin facade reading/writing through Raft.
- Surviving leader restart now retains pending tokens.
- Tests: write/peek/validate/expiry roundtrip with mocked FSM.

### D.4 Non-voter promotion on join
- `HandleJoin`: `AddNonvoter(...)` first; promote to voter only after first heartbeat received and node reports ready. Reduces quorum risk on mid-join crash.
- Edge: existing 1-node bootstrap path (Init) still uses immediate voter — covered by `Bootstrap=true`.

### D.5 Document CA rotation gap
- Add a `docs/specs/cluster-ops.md` (new file) section "CA rotation playbook (manual today)".

---

## Self-Review

| Spec item | Plan task | Status |
|---|---|---|
| Update: signature verification | (Out of scope, documented) | OK |
| Update: downgrade guard | A.1, A.2 | ✓ |
| Update: concurrent lock | A.3 | ✓ |
| Update: OOM-safe download | A.4 | ✓ |
| Update: restart flush race | A.5 | ✓ |
| Update: auto-rollback watchdog | (Out of scope, documented) | OK |
| Install: systemd clobber | B.1 | ✓ |
| Install: logrotate clobber | B.2 | ✓ |
| Install: post-start check | B.3 | ✓ |
| Install: systemd absent | B.4 | ✓ |
| Install: JWT entropy | B.5 | ✓ |
| Install: sha256sum check | B.6 | ✓ |
| Install: grep PCRE | B.7 | ✓ |
| Install: uninstall logrotate | B.8 | ✓ |
| Install: Debian family | B.9 | ✓ |
| Build: ldflags | C.1 | ✓ |
| Build: npm ci | C.1 | ✓ |
| Build: CGO_ENABLED=0 | C.1 | ✓ |
| Build: goreleaser frontend | C.2 | ✓ |
| Build: empty embed guard | C.3 | ✓ |
| Build: Node version | C.4 | ✓ |
| Build: lint suppression | C.5 | ✓ |
| Cluster: node cert mode | D.1 | ✓ |
| Cluster: simultaneous quorum | D.2 | ✓ |
| Cluster: token persistence | D.3 | ✓ |
| Cluster: non-voter promotion | D.4 | ✓ |
| Cluster: CA rotation doc | D.5 | ✓ |

Items deliberately deferred (see "Out of scope" header): release signing, desktop pipeline beyond Node version, auto-rollback watchdog, recurring tag-branch protection.

---

## Commit plan

1. `area-a: harden self-update flow` (lock, semver, flush, disk-backed download)
2. `area-b: install.sh idempotency + ux` (systemd/logrotate guards, health check, jwt entropy)
3. `area-c: build pipeline parity` (Makefile ldflags, goreleaser hooks, embed sentinel, Node version)
4. `area-d-1: cluster cert permission + quorum guard` (small)
5. `area-d-2: cluster join token persistence` (FSM commands)
6. `area-d-3: cluster non-voter promotion`
