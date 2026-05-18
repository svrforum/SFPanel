# T2 — Large surface, heavy I/O

**Reviewed at:** v0.13.15 commit `f5d38a4` · 2026-05-18
**Scope:** `compose` · `docker` (feature) · `packages` · `network` · `firewall`
**Method:** 5 parallel read-only subagent reviews (Opus 4.7), 26–30 item checklist each (extended per module)
**Companion umbrella:** `docs/superpowers/plans/2026-05-18-module-hardening-program.md`

---

## Findings count

| Module | P0 | P1 | P2 | P3 | Total |
|---|---|---|---|---|---|
| compose | 0 | 3 | 5 | 6 | 14 |
| docker (feature) | 0 | 3 | 5 | 5 | 13 |
| packages | **2** | 3 | 4 | 4 | 13 |
| network | **3** | 3 | 3 | 4 | 13 |
| firewall | **3** | 5 | 4 | 5 | 17 |
| **Total** | **8** | **17** | **21** | **24** | **70** |

**Running total (T1 + T2):** 11 P0, 34 P1, 46 P2, 56 P3 = **147 findings**.

**Status of 2026-04-19 P0 carry-forward (3rd-party installer hash verification):**

| Installer | Module | Status | Evidence |
|---|---|---|---|
| `get.docker.com` | packages | **OPEN** | `packages/handler.go:491-512` — curl → sh, no checksum, no env-var override implemented despite CLAUDE.md mentioning one |
| `claude.ai/install.sh` | packages | **OPEN** | `packages/handler.go:1086-1104` — same pattern |
| NVM `install.sh` | packages | **PARTIAL** | `packages/handler.go:635` — version-pinned to `v0.40.3` (git tag), but no SHA256 of fetched bytes |
| `tailscale.com/install.sh` | network | **OPEN** | `network/tailscale.go:273,295` — same pattern; not even listed in CLAUDE.md's documented exemptions |

---

## P0 — must fix

### P0-4. SanitizeOutput systematically missing on streaming SSE handlers
**Modules affected:** `packages` (~10 handlers), `network` (Tailscale install), `firewall` (entire module — 0 calls)
**Why P0:** CLAUDE.md rule "Never put raw command stderr into a client response" is broken across 12+ endpoints. ANSI escapes + private repo URLs with auth tokens + apt proxy creds + iptables raw output reach the client verbatim.

**Specific sites:**
- `packages/handler.go:231, 533, 671, 712, 734, 863, 870, 934, 972, 1122, 1212, 1300` — each `sendLine(scanner.Text())` skips Sanitize.
- `network/tailscale.go:283, 300, 307, 318, 327, 479` — Tailscale install SSE + multiple error paths.
- `network/network.go:239, 249, 269, 346, 380, 403, 414, 423, 475, 489` — raw `err.Error()` interpolated into user-facing message.
- `network/wireguard.go:219, 225, 240, 418, 484` — same pattern.
- `firewall/firewall_ufw.go:98, 113, 291, 380` — every ufw output passed through `strings.TrimSpace(output)` only.
- `firewall/firewall_fail2ban.go:267, 290, 429, 684, 729` — same.

**Fix sketch:** sweep replacing `sendLine(line)` → `sendLine(response.SanitizeOutput(line))` and every `err.Error()` interpolation in user-facing `Fail()` messages → `response.SanitizeOutput(err.Error())`. Single mechanical PR; touches one helper and a regex grep across the three modules.

### P0-5. 3rd-party installer hash verification carry-forward STILL OPEN
**Modules:** `packages` (3 installers), `network` (Tailscale)
**Status:** 2026-04-19 review flagged this as P0; **no remediation has shipped**. The CLAUDE.md doc says "operators who want deterministic builds should vendor a pinned copy and set the corresponding env var" — **no such env var is read in code**.

**Fix sketch (3 options):**
1. Implement the env vars CLAUDE.md describes: `SFPANEL_DOCKER_INSTALLER_SHA256`, `SFPANEL_NVM_INSTALLER_SHA256`, `SFPANEL_CLAUDE_INSTALLER_SHA256`, `SFPANEL_TAILSCALE_INSTALLER_SHA256`. When set, verify after download; hard-fail on mismatch. Documented "track-latest by default" stance preserved.
2. Pin hashes hardcoded with periodic CHANGELOG-tracked updates.
3. Vendor the install scripts into the repo and stop fetching at runtime (drops dependency on upstream availability + supply-chain risk).

Recommend option (1) — preserves operator opt-in and matches existing documented intent.

### P0-6. `firewall` mutation handlers skip the `Cmd.Exists()` guard
**File:** `firewall/firewall_ufw.go:104, 222, 339`; `firewall/firewall_fail2ban.go:77, 221, 249, 273, 298, 397, 553, 691`; `firewall/firewall_docker.go:298, 392`

12 handlers that mutate state lack the `Exists()` guard their read counterparts do. On a remote node accessed via `?node=` where ufw/fail2ban/iptables aren't installed, operator sees `command not found` 500 instead of a structured "not installed" response. Violates CLAUDE.md cluster-graceful-empty rule.

**Fix sketch:** small helper `func (h *Handler) require(tool string) error` that returns a structured 400/501 if missing. Call at top of every mutation handler. Mechanical.

### P0-7. `firewall.AddRule` has no lockout guard
**File:** `firewall/firewall_ufw.go:223-293`

`DeleteRule` and `EnableUFW` consult `firewall_lockout.go`; `AddRule` does not. `POST /firewall/rules {"action":"deny","port":"22"}` ahead of an existing allow does not warn the operator. UFW translates this to an iptables INSERT (`-I`) above existing allows. **Most likely real-world self-lockout path**, and the guard is missing entirely.

**Fix sketch:** preflight check — for `deny`/`reject`/`limit` on SSHPort/PanelPort, reject without `force=true`. Mirrors `DeleteRule`'s pattern.

### P0-8. `packages` `validPackageName` accepts leading hyphen
**File:** `packages/handler.go:35`

`^[a-zA-Z0-9._+\-]+$` accepts `-y`, `--reinstall`, `--target-release=…` as "package names". apt-get treats them as flags positionally (no `--` separator used). Not a confirmed RCE today (apt-get won't *install* a flag-named pkg), but blast radius widens with any flag that influences apt-get behavior.

**Fix sketch:** tighten to `^[a-zA-Z0-9][a-zA-Z0-9._+\-]*$` (must start with alnum). Add `--` separator before user-supplied args in every `apt-get` call as defense-in-depth.

---

## Per-module sections

### compose

**What it does:** Manages docker-compose projects (list/CRUD, up/down/restart, service ops, env edit, diff vs deployed, git import, update/rollback with image-ID manifest, healthcheck composer with backup-then-atomic-write, two SSE streamers `up-stream`/`update-stream`) on per-node filesystem under `cfg.Server.StacksPath` (default `/opt/stacks`).

**Findings:**

- **P1 — `handler.go:366-374, 397-407` SSE handlers stream raw docker stdout without `SanitizeOutput`.** Non-streaming counterparts (`Up`/`Down`) correctly wrap. Streaming path skips it. (Same root cause as P0-4.)
- **P1 — `handler.go:536-615` Apply/RemoveHealthcheck TOCTOU between sha256 precondition and rename.** Two concurrent Applies (same project, different services) both pass sha256, both write backup, race the rename — one silently drops the other's mutation. No per-project mutex.
- **P1 — `composex/safety.go:43-50` `cap_add` matcher misses canonical Docker forms.** Uppercases and exact-matches `ALL/SYS_ADMIN/SYS_MODULE/SYS_PTRACE`. **Misses** `CAP_SYS_ADMIN` (kernel-style — Docker accepts both forms), `DAC_READ_SEARCH`, `DAC_OVERRIDE`, `NET_ADMIN`, `BPF`, `PERFMON`, `SYS_BOOT`. Also no check for `group_add: ["docker"]` (host docker group join), `pid_mode`/`network_mode`/`ipc_mode` long-form keys (only short forms `pid`/`network`/`ipc` checked).
- **P2 — Symlink attack on `.tmp` and `.bak.healthcheck.*`** writes — `handler.go:592-607, 739-754`. If attacker (or earlier-installed compromised container) pre-creates `docker-compose.yml.tmp` as a symlink to `/etc/passwd`, `os.WriteFile` follows it as root. Use `O_CREATE|O_EXCL|O_NOFOLLOW`.
- **P2 — `handler.go:559` `ResolveComposeFile` skips `validateProjectName`.** `..` reaches `filepath.Join`. Practically fails safe (no compose file found at `/opt`), but trust posture wrong.
- **P2 — N+1 image inspect** in 4 sites: `GetRollbackInfo` (`compose.go:921`), `CheckStackUpdates` (`:770`), `UpdateStack`/`UpdateStackStream` (`:805, :432`). Each loops `InspectImage` per service. `container.Summary.ImageID` from cached `ListContainersCached` already has the data.
- **P2 — `runComposeStream` single io.Pipe** for stdout+stderr; goroutine closes writer. Belt-and-braces `defer pw.Close()` missing.
- **P2 — `handler.go:360, 392` SSE write error swallowed.** Client disconnect doesn't cancel subprocess.
- **P3** ×6 — `Commander` not injected into `ComposeManager` (contract drift), `hasComposeHealthcheck` indent-scanner missing tab support + comment false-positive, project-delete settings DELETE could be tighter, `git_import` error mapping by substring fragile, `CMD` argv split empty-element handling, rollback manifest write error ignored.
- **Test gaps (8)** — no SSE framing test, no sha256 success path test, no `composex.ValidateAdvancedCompose` test (the file has zero coverage), no `validateProjectName` negative tests, no rollback manifest tamper test, no backup prune edge-case test, no comment-line healthcheck false-positive test, no end-to-end disk-touching healthcheck test.

---

### docker (feature)

**What it does:** REST handlers for Docker container/image/volume/network management + DB-backed observability reads (1h metrics averages, per-container events, recent events, volume size cache), on a thin shared `*docker.Client` wrapper.

**Findings:**

- **P1 — `client.go:92-102, 611-625` Cache stampede.** `ListContainersCached`/`ListImagesWithUsage` lock only inside `cache.get/set`. Concurrent first-callers (e.g. dashboard page load firing 4 parallel requests) each round-trip the Docker socket. Use `golang.org/x/sync/singleflight`.
- **P1 — `client.go:86` ListContainers `All:true` unbounded.** Hosts with thousands of stopped containers return everything. Add hard cap or `?state=running|all` filter.
- **P1 — `handler.go:706` + `observability.go:35, 97, 140` DB queries don't use request context.** Inconsistent with `handler.go:74` which does. Switch to `QueryContext(ctx, ...)`.
- **P2 — `handler.go:344-346` PullImage always sends `"complete"` SSE frame** even on decode error / ctx cancel. Operator sees "complete" on failed pulls.
- **P2 — `observability.go:153` `rows.Scan` return ignored** in GetRecentEvents.
- **P2 — `client.go:617` `ListImagesWithUsage` caches result after full computation;** reads-during-fetch race with stale cache + the stampede (P1).
- **P2 — `client.go:483, 495` `CheckImageUpdate` always returns `(_, nil)`** with error embedded in struct. Function signature lies.
- **P2 — `handler.go:201` indentation regression** — body shallower than rest; `go fmt` would catch.
- **P3** ×5 — dead `safeLen[T]` abstraction, `RunOneShotExec` no slog.Warn on timeout, ports map allocation when NetworkSettings nil, no concurrent-access test for cache (the P1 issue), `parseRange` switch is hardcoded.
- **Test gaps (8)** — no cache stampede/race test, no `Handler.ListContainers` end-to-end (acknowledged in test comment — concrete `*docker.Client` vs interface), no `ObservabilityEnabled=false` short-circuit test, no malformed `before` cursor test, no `loadVolumeUsageMap` partial-data test, no PullImage error-mid-stream test, no `ContainerStatsBatch` empty test, no `CheckImageUpdates` empty-result test.

---

### packages

**What it does:** Single largest feature module (~40 KB), 19 routes for apt management + install/upgrade workflows for Docker, NVM/Node.js, Claude/Codex/Gemini CLIs. Streaming SSE via `os/exec` directly (documented exception).

**Findings:**

- **P0-4 — SanitizeOutput missing in 10+ streaming handlers** (see top). 
- **P0-5 — 3rd-party installer hash verification STILL OPEN** for `get.docker.com`, `claude.ai/install.sh`; PARTIAL for NVM (tag-pinned but not byte-hashed) (see top).
- **P0-8 — `validPackageName` accepts leading hyphen** (see top).
- **P1 — `bash -c` with interpolated `body.Version` and `nvmDir`** at `handler.go:692, 723, 768, 791, 812, 860, 867, 912, 969`. Currently blocked by `validNodeVersion` regex; one-line relaxation reopens command-injection. Refactor to pass via env (`cmd.Env = append(..., "VERSION="+...)`) referenced as `"$VERSION"` inside bash.
- **P1 — `bufio.Scanner` default 64 KB buffer in 6 streaming handlers** (`handler.go:531, 669, 710, 932, 1120, 1210, 1298). One long dpkg line truncates; `scanner.Err()` never checked. Apply the `scanner.Buffer(make([]byte, 64*1024), 1024*1024)` pattern from `handler.go:229` everywhere.
- **P1 — Streaming endpoints don't follow `-stream` suffix convention.** All 7 rely on hardcoded allowlist in `proxy.go:36-43`. Brittle — adding a new endpoint without editing proxy.go silently breaks cluster `?node=` forwarding.
- **P2 — `CheckUpdates` runs full `apt-get update` on every GET** (`handler.go:78`). Polling UI hits dpkg lock + 5-30s. Cache 5 min or split GET (read cache) / POST (refresh).
- **P2 — No `defer recover()` in SSE handlers.** Panic mid-stream leaves client half-stream.
- **P2 — No write deadline on SSE writes.** Slow client + chatty apt = stalled pipe → queued installs.
- **P3** ×4 — `findNVMDir` walks `/home` on every call, `findBinaryPath` "most-recent mtime wins" heuristic counter-intuitive, `/tmp/get-docker.sh`-style scripts not race-safe, `os.Remove` error log missing.
- **Test gaps (5)** — zero parser tests (parseUpgradablePackages, parseSearchResults, getInstalledPackages dpkg-query), zero injection-rejection tests for `validNodeVersion`, no SSE-lifecycle test, no flag-shape rejection test for package name, no `SanitizeOutput` coverage test.

---

### network

**What it does:** Host network management — netplan read/write/apply, interface listing via sysfs, DNS resolver config, WireGuard tunnels (BYO config), Tailscale install + status + login.

**Findings:**

- **P0-4 — SanitizeOutput missing across many handlers** (see top).
- **P0-5 — Tailscale install hash verification OPEN** (see top).
- **P0 (new) — Tailscale install endpoint not in cluster-streamable allowlist.** `/network/tailscale/install` at `tailscale.go:252` streams SSE 1-5 min; not in `isStreamingEndpoint` allowlist (`proxy.go:28-50`), no `-stream` suffix. With `?node=`, gRPC unary proxy buffers + 30 s timeout + 4 MB cap → breaks long installs on followers.
- **P0 (new) — netplan apply has no rollback and writes non-atomically.** `network.go:362-374, 951-960`. Bad config + remote operator = bricked SSH. Mitigations: atomic write (temp+rename — WireGuard already does this at `wireguard.go:395`), and either snapshot+auto-restore or `netplan try`.
- **P1 — Tailscale install streamer ignores request context** (`tailscale.go:273, 291`). `context.Background()`; disconnect doesn't cancel install (10-min timeout only).
- **P1 — Raw `err.Error()` / `%v` interpolation into user-facing messages.** Many sites, no Sanitize.
- **P1 — `netplan apply` cache invalidation precedes verification** (`network.go:372`). Stale data served if kernel state hasn't settled.
- **P2** ×3 — non-atomic netplan write, `netplan generate` stdout warnings suppressed on success, `detectInterfaceType` VLAN heuristic too loose.
- **P3** ×4 — `parseWGDump` field count mismatch tolerance, `isWGInterfaceActive` N subprocess calls, `CheckUpdate` uses deprecated `apt list`, `getTailscalePrefs` uses unstable `tailscale debug prefs`.
- **Test gaps (7)** — netplan round-trip, `containsDangerousWGDirective`, `parseResolvectlOutput`, `parseRouteLine`, `detectInterfaceType` with sysfs fixtures, Tailscale `Up` auth-URL extraction, validation suite edge cases.

---

### firewall

**What it does:** UFW rules + status, fail2ban jails (CRUD via `/etc/fail2ban/jail.d/*.local`), Docker DNAT inspection + DOCKER-USER iptables add/delete, `ss` listening enumeration. Via `Commander`, with SSH/panel-port lockout guard on UFW enable/delete.

**Findings:**

- **P0-4 — Zero `SanitizeOutput` calls in entire module** (see top).
- **P0-6 — 12 mutation handlers skip `Cmd.Exists()` guard** (see top).
- **P0-7 — `AddRule` has no lockout guard** (see top).
- **P1 — Concurrent `DeleteRule` race.** UFW renumbers rules after each delete. Operator A's "delete 5" + B's "delete 5" delete different rules. Lockout precheck has same TOCTOU. Serialize via `sync.Mutex` or use `ufw delete <rule-spec>` (non-numeric).
- **P1 — `getJailInfo` issues 5 subprocess forks per jail** (`firewall_fail2ban.go:148, 209-214`). With 10 active jails, `ListJails` forks 50 processes per request. Cache jail config 30s OR parse `.local` file directly.
- **P1 — `AddDockerUserRule` no `iptables -C` preflight** (`firewall_docker.go:357`). Repeated POST stacks duplicate rules. Companion LOG rule (`:364`) compounds.
- **P1 — Persisted DOCKER-USER rules file is `0644` and under `/etc/sfpanel/`.** Privilege-escalation primitive: `iptables-restore` runs at startup. Should be `0600` under `/var/lib/sfpanel`.
- **P1 — `/fail2ban/install` missing from cluster-proxy HTTP-relay allowlist.** `apt-get install fail2ban` over `?node=` times out at 30 s gRPC unary.
- **P2** ×4 — `UpdateJailConfig` non-atomic (up to 3+2N `fail2ban-client set` calls, no rollback), `iptables -t nat -L DOCKER` called twice in one request, Commander uses `context.Background()` (client disconnect orphans `apt-get install fail2ban`), `DeleteJail` uses `rm` subprocess instead of `os.Remove`.
- **P3** ×5 — `parseUFWStatus` untested, `parseSSOutput` field-index fragile, `validBanTime` rejects valid forms like `1mo`/`30d`, `validLogPath` rejects journald-only configs, `RestoreDockerUserRules` warn-only on failure.
- **Test gaps (9)** — handler-level lockout tests missing, `AddRule` lockout test (after P0 fix), `parseUFWStatus` no tests, `buildUFWAddArgs` no tests, DOCKER-USER lifecycle no tests, persistence round-trip untested, `Exists()`-less graceful degradation untested, `UpdateJailConfig` ignoreip diff loop untested, validation regexes have no negative cases.

---

## Cross-tier patterns (single fixes that touch many modules)

(These extend the T1 pattern list A–F. Continue lettering G–L.)

### Pattern G — `SanitizeOutput` missing on every streaming/error path

Affects: packages, network, firewall (and partially compose).
**Single fix:** mechanical sweep + a `golangci-lint` custom rule or grep-based test that fails CI when a `sendLine`/`Fail` reaches the response with raw subprocess output. Touches ~30 sites.

### Pattern H — Hardcoded streaming allowlist instead of `-stream` suffix

Affects: packages (all 7), network (Tailscale install), maybe more in T3-T5.
**Single fix:** either (a) rename routes to use `-stream` suffix consistently, OR (b) document the allowlist as authoritative and add a CI test that detects new SSE handlers not in the allowlist.

### Pattern I — Streaming subprocess + `context.Background()` instead of request context

Affects: network (Tailscale), packages (entire streaming surface — verify), firewall (Commander default — affects everyone).
**Single fix:** Pattern F from T1 (Commander gets context) extended to streaming exception handlers.

### Pattern J — `bufio.Scanner` default 64 KB buffer with no `Buffer()` raise

Affects: packages (6 handlers), likely network/Tailscale install.
**Single fix:** sweep replacing every `bufio.NewScanner` over subprocess output with `Buffer(make([]byte, 64*1024), 1024*1024)` + check `scanner.Err()`.

### Pattern K — N+1 subprocess invocations

Affects: firewall (jail info — 5 forks/jail), docker (per-container inspect), compose (serial image inspect in 4 sites).
**Single fix:** per-module — no single sweep but a pattern to apply.

### Pattern L — No `defer recover()` in SSE handler entry / streaming goroutines

Affects: packages, network (likely), monitor (already in T1 pattern B).
**Single fix:** extends T1 Pattern B (`safe.Go` helper) to SSE handlers — add an `sse.Handler` wrapper that recovers + sends error frame + closes.

### Pattern M — 3rd-party installer hash verification

**SINGLE TIME-CRITICAL ITEM.** Affects: packages (3), network (1).
**Single fix:** one `internal/release` helper that:
  - Reads `SFPANEL_*_INSTALLER_SHA256` env vars
  - On download, computes SHA-256 and compares
  - Hard-fail on mismatch, soft-pass with `slog.Warn("installer hash not pinned, track-latest mode")` if env unset
Adopted by 4 install paths.

---

## Recommended remediation grouping (updated from T1)

Renumbering after T2:

1. **`fix-p0-stream-auth-and-leaks`** (T1) — gRPC stream interceptor + heartbeat goroutine + RunWithTimeout(0)
2. **`fix-sanitize-output-sweep`** (NEW from T2) — Pattern G across packages/network/firewall/compose-streaming
3. **`fix-installer-hash-verify`** (NEW from T2) — Pattern M, `internal/release` helper + 4 callers
4. **`fix-firewall-lockout-and-exists`** (NEW from T2) — P0-6 + P0-7
5. **`fix-package-validators`** (NEW from T2) — P0-8 + scanner buffer + node-version bash escape
6. **`fix-netplan-atomic-and-rollback`** (NEW from T2) — atomic write + snapshot-and-restore
7. **`fix-async-writer-pattern`** (T1) — audit + security_events
8. **`fix-safe-goroutine-pattern`** (T1, extended with T2) — collectors + SSE handlers
9. **`fix-indexes-migration-28`** (T1, extended with T2) — audit_logs + refresh_tokens + cmh
10. **`fix-cluster-correctness-and-streaming-allowlist`** (NEW from T2) — Patterns H + streaming routes + tailscale install + fail2ban install allowlist
11. **`fix-auth-cluster-correctness`** (T1)
12. **`fix-monitor-rollup-and-retention`** (T1)
13. **`fix-firewall-perf-and-atomicity`** (NEW from T2) — N+1 subprocess (Pattern K) + UpdateJailConfig atomicity
14. **`fix-commander-context`** (T1, extended) — Pattern F + I
15. **`fix-db-pool-split`** (T1)
16. **`misc-p2-p3-sweep`** (T1, extended)

Suggested execution order: **1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12 → 13 → 14 → 15 → 16**.

Rationale: P0s first (1-6 close all 11 P0s). Then T1 mechanical sweeps (7-9). Then cluster + correctness (10-12). Then perf (13-14). Then refactors (15). Then cleanup (16).

---

## Exit criteria for T2

- [x] All 5 modules reviewed
- [x] 8 P0 findings flagged (5 new + 3 carry-forward verified OPEN)
- [x] Cross-tier patterns extended G–M
- [x] Recommended remediation list updated to 16 plans
- [ ] Continue to T3
