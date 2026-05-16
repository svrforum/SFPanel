# Changelog

All notable changes to SFPanel are recorded here. Entries are derived from annotated git tags (`git tag -n50 <tag>`).

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/), with sections per release and the newest release at the top.

---

## [0.13.7] – 2026-05-16

Second build fix for the desktop pipeline. Server code is identical
to 0.13.4–0.13.6; only `.github/workflows/release-desktop.yml`
changed.

### Fixed
- **`latest.json` manifest job now publishes successfully.**
  The 0.13.6 desktop builds all succeeded, but the manifest job
  bailed with *"Missing Linux signature"*. Two patterns in the
  workflow were stale against Tauri 2.10's actual artefact naming:
  - Linux: Tauri 2.10 signs the AppImage directly (e.g.
    `SFPanel_0.13.7_amd64.AppImage.sig`) — there is no
    `.AppImage.tar.gz` wrapper anymore. Updated the collect step
    to copy `*.AppImage.sig` and the manifest to point at
    `.AppImage` as the updater URL.
  - macOS: bundle is named `SFPanel.app.tar.gz` (no version, no
    arch infix). Loosened the manifest's signature glob to
    `*app.tar.gz.sig` and pointed the URL at `SFPanel.app.tar.gz`.

### Operator notes
- v0.13.6 release page is missing `latest.json` and the Linux
  AppImage signature — operators who installed the Linux AppImage
  from 0.13.6 won't see auto-update prompts until they re-install
  from 0.13.7.
- Existing 0.13.5/0.13.6 installs of Windows/macOS bundles will
  still get the auto-update prompt against 0.13.7 once `latest.json`
  is published (this release).

---

## [0.13.6] – 2026-05-16

Build fix for the v0.13.5 desktop release. Server code is identical
to 0.13.4/0.13.5; only the desktop bundle's npm dependency pins
changed.

### Fixed
- **Desktop build now succeeds.** The 0.13.5 `Release Desktop`
  workflow failed on all three platforms (Linux/Windows/macOS) with
  *"Found version mismatched Tauri packages"*: Cargo resolved
  `tauri = "2"` to `v2.10.3` while npm's `^2` slid forward to
  `@tauri-apps/api v2.11.0`. The Tauri bundler refuses to build
  when the npm and Rust crate minors disagree.
  Pinned `@tauri-apps/api`, `@tauri-apps/plugin-updater`, and
  `@tauri-apps/cli` to `~2.10.0` in `desktop/package.json` and
  regenerated `desktop/package-lock.json` so CI's `npm ci` resolves
  to 2.10.1 (matching the Cargo side). Any future minor bump now
  needs to be done on both sides at once.

### Operator notes
- v0.13.5's release page has only the server tarballs — no desktop
  installers, no `latest.json`. Operators who installed 0.13.5 via
  the server `.tar.gz` are fine. Desktop users should pull v0.13.6.
- `latest.json` (the auto-update manifest introduced in 0.13.5) ships
  for the first time as part of this release.

---

## [0.13.5] – 2026-05-15

Desktop tooling release. Server code is identical to 0.13.4; the
changes are confined to the desktop bundle so the version bump is
visible to operators on the release page (the desktop side has been
drifting behind the server for a long time).

### Changed
- **Desktop bundle now tracks the server version (lockstep).** The
  three desktop manifests (`desktop/package.json`,
  `desktop/src-tauri/Cargo.toml`,
  `desktop/src-tauri/tauri.conf.json`) all jumped from 0.6.2 → 0.13.5.
  Going forward, every release tag produces matching server tarballs
  and desktop bundles.

### Added
- **Signed auto-update for the desktop app.** Wired in
  `tauri-plugin-updater` with a freshly generated ed25519 minisign
  key pair. The public key is embedded in `tauri.conf.json`; the
  private key + (empty) password live in GitHub Secrets
  (`TAURI_SIGNING_PRIVATE_KEY`, `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`).
  The release workflow now produces `.sig` + updater archive pairs
  for every OS, and a new `manifest` job composes a single
  `latest.json` against
  `releases/latest/download/latest.json`. Desktop clients poll that
  URL, GitHub redirects to the current tag's manifest, and the
  built-in updater dialog prompts the user before applying the
  signed update. **First release where this is live** — existing
  ≤0.6.2 installs still need a one-time manual re-download because
  the pre-update build has no embedded public key.

### Operator notes
- The desktop release page entry will look different starting now:
  installer artefacts use the `0.13.5` prefix (same as the server
  tarballs) instead of the historical `0.6.2`.
- `latest.json` is a release asset alongside checksums and bundles.
  Don't delete it — it's the auto-update manifest.
- Key recovery: if you ever need to rotate the signing key, stage
  a "transition release" first that ships both old and new
  signatures. Replacing the public key in `tauri.conf.json`
  without that step will leave fielded clients unable to verify
  the next update and they'll have to re-install manually.

---

## [0.13.4] – 2026-05-15

Authentication bug-fix release. Three independent paths conspired to
push cluster-mode operators into a login loop where every fresh login
bounced back to /login within a couple of seconds; each is documented
below.

### Fixed
- **Refresh handler ignored the cluster FSM when verifying account
  existence.** Account replicated only in the FSM (no row in the local
  `admin` table) had every refresh attempt 401 with "User no longer
  exists" and the refresh-token row tombstone-deleted — guaranteeing
  the next access-token expiry kicked the user back to /login.
  `Refresh` now mirrors `Login`'s lookup order (FSM first, local DB
  fallback).
- **Four admin-management handlers carried the same FSM-blindness.**
  `Get2FAStatus`, `Verify2FA`, `Disable2FA`, and `ChangePassword` read
  / wrote only the local `admin` table. On a cluster-only account
  these either lied ("2FA disabled" when the FSM said otherwise) or
  silently no-op'd the UPDATE so the user got a "success" response
  while no state actually changed. New `loadAdminAccount` /
  `persistAdminAccount` helpers route reads through FSM-first lookup
  and route writes back to wherever the account lives (FSM goes via
  Raft Apply with a 503 + leader hint on followers; local goes UPDATE
  + Raft sync). 
- **v2 internal-proxy validator silently rejected every URL with a
  query string.** Signers feed path-with-query into
  `SignProxyRequestV2` so a captured header cannot be re-targeted to
  a different endpoint / query params, but the validator was checking
  the MAC against `r.URL.Path` (path component only) — so any
  forwarded request whose URL carried a query string flunked v2
  validation, the JWT middleware then looked for a Bearer token,
  found none (the loopback proxy strips Authorization in favour of
  v1/v2 headers), and returned 401 "Authorization header is required".
  Dashboard's `/logs/read?source=syslog&lines=8` was the visible
  casualty: when a browser had `current_node` pinned to a peer, those
  401s were the third path into the login loop. Validator now uses
  `r.URL.RequestURI()` so it sees exactly what the signer signed.

### Tests
- 8 new cases — 2 refresh handler (happy + truly-missing-user), 4
  admin-account helpers (local read, missing returns ErrNoRows, local
  update including NULL-totp, cluster-without-manager refusal), 2 v2
  proxy validator (round-trip with query, query-param rebinding
  rejected). FSM-positive paths for the cross-cluster flows are
  covered by the loopback integration probe in the deployment
  runbook; stubbing the concrete `*cluster.Manager` would require
  a wider refactor than this fix warrants.

### Operator notes
- **Mixed-version clusters need every node updated** for cross-node
  `?node=<peer>` proxy to validate query-string'd URLs. A
  follower-only or single-node deployment (or any deployment that
  doesn't pin `current_node` to a peer in the browser) is unaffected
  by the proxy half of the bug.
- **Browsers stuck in the loop pre-upgrade**: clear
  `localStorage["sfpanel_current_node"]` (DevTools → Application →
  Local Storage, or `localStorage.removeItem('sfpanel_current_node');
  location.reload()` in the Console) to break out without waiting
  for the binary update to land on every node.

### Changed
- **Default listening ports moved off the 9xxx block.** New installs
  bind 3628 HTTP, 3629 cluster gRPC, and 3630 Raft (gRPC + 1). The
  earlier defaults (19443 / 9444 / 9445) collided with common
  homeserver workloads. Existing operators see no change unless they
  remove the relevant lines from `config.yaml` — `config.go` only
  fills in the defaults when the field is absent.

#### Migration for existing operators

Operators upgrading via `sfpanel update` keep their current ports.
To switch to the new defaults:

```yaml
# /etc/sfpanel/config.yaml
server:
  port: 3628          # was 19443 (or whatever was set)

cluster:
  grpc_port: 3629     # was 9444; Raft transport auto-binds to 3630
```

Then `sudo systemctl restart sfpanel`. For **clustered** deployments
roll one node at a time with ≥ 10s between each (same constraint as
any restart sequence — see CLAUDE.md "Simultaneous restart of all
voters"). Update any reverse-proxy / firewall rules to match before
restarting the front-most node.

---

## [0.13.3] – 2026-05-15

Security hardening (F1 full). XSS-resistant session model and CSRF
protection on every state-changing request.

### Added
- **httpOnly refresh cookie + CSRF double-submit** — refresh tokens
  now live in a `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth`
  cookie that JS cannot read. A separate `sfpanel_csrf` cookie
  (JS-readable) is echoed on every POST/PUT/PATCH/DELETE via
  `X-CSRF-Token` — a cross-site attacker who tricks a victim's browser
  into POSTing to the panel cannot read the cookie cross-origin and
  cannot forge the header.
- `POST /api/v1/auth/logout` — revokes the refresh token in the DB
  and clears both cookies.
- `Secure` cookie flag derived per-request (`X-Forwarded-Proto` /
  `r.TLS`) so the default `:9443` plain-HTTP listener doesn't
  silently drop cookies but reverse-proxy-fronted TLS deployments
  get the strictest setting.

### Tests
- 12 new cases — 6 CSRF middleware (safe-method exempt, bootstrap
  exempt, mismatch rejected, header match accepted, internal proxy
  bypass), 6 cookie helpers (hardened flags, Secure tracks scheme,
  CSRF cookie JS-readable, ClearAuthCookies MaxAge=-1, entropy +
  uniqueness).

### Compatibility
- Refresh handler still accepts the legacy JSON body fallback for one
  release so in-flight v0.13.2 sessions don't break on upgrade. Will
  be removed in v0.14.0 after the cookie path has baked.

---

## [0.13.2] – 2026-05-15

Comprehensive security audit + stability patches across cluster proxy
auth, refresh token theft detection, CSP, WebSocket auth, and Go/npm
dependency lines.

### Security
- **Cluster proxy v2 header now enforced on HTTP routes** —
  `JWTMiddleware` delegates to `auth.IsInternalProxyRequest` so v2
  (HMAC + timestamp + nonce) is preferred over v1 static-secret.
  Previously WS endpoints used v2 but HTTP fell back to v1, leaving
  captured headers replayable indefinitely.
- **Refresh token theft detection (OWASP)** — `refresh_tokens` gains
  `family_id` + `consumed_at`. Each login starts a new family; each
  rotation tombstones the consumed row. A later replay of the
  tombstone triggers "theft detected → revoke entire family" so the
  attacker's chain dies. Migrations 24–26.
- **WebSocket auth via single-use ticket** — `POST /api/v1/auth/ws-ticket`
  mints a 60s opaque ticket; the JWT no longer lands in WS URLs (and
  thus no longer in browser history / reverse-proxy access logs).
  Eight WS call sites migrated (Terminal, ContainerShell,
  ContainerLogs, ComposeLogs, FirewallLogs, Logs, useWebSocket hook).
  Legacy `?token=` path kept for back-compat one release.
- **`SecurityHeaders` middleware** — emits Content-Security-Policy,
  X-Content-Type-Options=nosniff, X-Frame-Options=DENY,
  Referrer-Policy=strict-origin-when-cross-origin,
  Permissions-Policy on every response. Inline scripts forbidden;
  jsdelivr font CDN allowed. HSTS deliberately not set (panel binds
  plain HTTP by design — operator's reverse proxy emits HSTS).
- **Pretendard CDN SRI** — `<link>` pins SHA-384 hash, blocking
  silent CSS injection if the CDN is compromised.
- **JWT moved from localStorage to sessionStorage** — closing the tab
  clears the session; XSS surface shrinks from indefinite background
  tab to active session only. One-time migration from legacy
  localStorage location.
- **Proxy header hardening** — `ClusterProxyMiddleware` (outbound)
  and `cluster/grpc_server.go ProxyRequest` (inbound) both skip
  `Authorization` / `X-SFPanel-Original-User` /
  `X-SFPanel-Internal-Proxy*` when copying inbound request headers,
  then re-set trusted values explicitly. Defense in depth against
  a compromised cluster peer or an attacker who reaches a node
  directly with a forged claim header.
- **fail2ban `..` path traversal check** — template-override branch
  was missing the substring guard that the custom-jail branch
  already had.

### Updated
- `github.com/labstack/echo/v4` v4.15.1 → **v4.15.2** (Context.Scheme()
  header validation patch).
- `golang.org/x/crypto` v0.50.0 → **v0.51.0**.
- `google.golang.org/grpc` v1.80.0 → **v1.81.0** (current line; the
  critical GHSA-p77j-4mvh-x3m3 authz bypass was already patched at
  v1.79.3).
- npm: minor versions for `tailwindcss`, `react-router-dom`,
  `vite-plugin-pwa`, `tailwind-merge` (caret range). `npm audit`
  reports 0 vulnerabilities.

### Notes — deferred
- **Docker SDK v28 → v29** remains on v28.5.2+incompatible. moby/moby
  has shipped `github.com/docker/docker/v2` but only at
  `v2.0.0-beta.13` as of 2026-05-14 — production migration waits
  for GA.

---

## [0.13.1] – 2026-05-09

Stability + smooth-install patch series. No new user-facing features.

### Fixed
- **`saveConfig` permission leak** (`cmd/sfpanel/cluster_commands.go`)
  — `cluster init` / `cluster leave` were clobbering
  `/etc/sfpanel/config.yaml` to `0644`, exposing the JWT secret.
  Now writes `0600` matching every other write site. Test guards
  against regression.
- **Cluster boot-time FSM sync race** — replaced the fixed 5-second
  sleep with `IsLeader()` polling (200 ms tick, 30 s ceiling). Faster
  on fresh single-node clusters, more reliable on loaded hosts.

### Added
- **Pre-upgrade DB snapshot** — both `scripts/install.sh` (reinstall
  path) and `sfpanel update` (CLI) now write
  `<dbpath>.bak-<YYYYmmdd-HHMMSS>` before the binary swap. Retains the
  3 most recent snapshots; older ones pruned automatically.
- **systemd unit hardening** — `MemoryHigh=1G`, `TasksMax=4096`,
  `PrivateTmp=true`, `RestrictSUIDSGID=true` in the bundled
  `sfpanel.service`.
- **`GOMAXPROCS` / `GOGC` env override** honored — operators on
  larger cluster hosts can bump runtime concurrency without
  rebuilding.
- **`install.sh` cluster directory perm enforcement** — re-running
  install now forces `/etc/sfpanel/cluster/` to `0700` and `*.key`
  files to `0600`.
- **`print_success` operator guidance** — first-install banner now
  prompts to enable 2FA, front the panel with TLS, and restrict
  the listener port to LAN/VPN.

### Documentation
- README install / upgrade / cluster sections refreshed: `sudo bash`
  in every install snippet, cosign + SHA-256 dual verification
  documented, auto DB snapshot path + rollback recipe, rolling-restart
  guidance, `peers.json` quorum-loss recovery, TLS cert lifetime
  table, security section split into operator-applied vs
  automatic items.

---

## [0.13.0] – 2026-05-06

Healthcheck composer for Docker Compose stacks, plus a focused cleanup of two over-engineered features that didn't pay off in a home-server context.

### Added
- **Compose healthcheck composer** — click the ❤️ icon on a service row to open a 5-field dialog (test command, interval, timeout, retries, start_period) and apply the change to the compose YAML. Includes 5 presets (HTTP `/health`, `pg_isready`, redis `PING`, mysql ping, custom), a *Test now* button that runs the command in the live container before saving, and a *Healthcheck 제거* option for clean removal.
- The HeartPulse icon on each service row turns green when a healthcheck is present, dim when absent.
- `container_unhealthy` alert rule type (Theme F polish) — fires when a container's healthcheck status flips to unhealthy. Routes through the existing alert channel pipeline.
- Backup retention policy: keep last 5 `.bak.healthcheck.*` files per stack.

### Stability commitments preserved across PUT/DELETE healthcheck endpoints
- yaml.v3 Node-API round-trip preservation (comments, anchors, key order)
- Backup before write
- Pre-flight re-parse
- `base_yaml_sha256` concurrent-edit precondition
- No automatic deploy

### Removed
- **Template Forks** (Theme E Phase 1) — Raft FSM-replicated stack templates. `cp docker-compose.yml` covers the same need without coordinated state. Drops `~1300` lines of FSM, handler, and UI code.
- **Cosign image verification** (Theme C Phase 1) — popular self-hosted images aren't cosign-signed, so *require* mode universally failed. The advisory mode never produced useful signal. Drops `~2000` lines of policy engine, verifier, and UI. The `image_signatures` SQLite table (migrations 21–23) is left in place per the append-only migration policy.

---

## [0.12.0] – 2026-05-04

Per-container observability and cluster recovery improvements.

### Added
- **Theme F — Docker observability**
  - Per-container CPU/memory history (30s sampling × 24h retention) backed by `container_metrics_history`
  - Sparkline next to each container row in the Docker page
  - History tab inside the container detail drawer (24h chart + raw points)
  - Docker events timeline (`container_events` table, 8 event types: start, stop, kill, die, oom, health_status, create, destroy)
  - 3 new container alert rule types: `container_down`, `container_oom`, `container_restart_loop`
- **Quorum-loss recovery** — `peers.json` honored on Raft startup. If present, `RecoverCluster()` rewrites the local Raft configuration with the listed voters, then renames the file to `peers.info` to prevent re-application on the next boot.

### Fixed
- Cluster: never-heartbeated nodes now correctly reported as offline (was leaking stale FSM status).
- Alert manager: container alerts now flow through the shared Fire path (cooldown + channel routing + alert_history). Previously bypassed.

---

## [0.11.3] – 2026-05-03

Hotfix for the v0.11.2 release-signature verifier.

### Fixed
- cosign v2 wraps the PEM cert in an extra base64 layer; the v0.11.2 binary's verifier didn't decode this, so it couldn't verify the v0.11.3 release signature. The update flow now decodes that layer before parsing.

  Note: v0.11.2 → v0.11.3 falls back to SHA-256 verification only. v0.11.3 onwards has full keyless verification on every update.

---

## [0.11.2] – 2026-05-03 — *Systemic hardening*

Major hardening pass covering deployment, install, build pipeline, cluster ops, DB safety, parser tests, security audit, refresh tokens, split-brain fences, and binary signature verification.

### Added
- **First Sigstore-signed release** — keyless OIDC; `checksums.txt.sig` and `checksums.txt.pem` published as release assets. Update flow verifies the signature before trusting any hash in `checksums.txt`.
- **Self-update hardening** — concurrent-update lock, semver downgrade guard, disk-backed download, flush-before-restart, watchdog auto-rollback (binary + DB).
- **DB safety** — `schema_version` sentinel, transactional migrations, WAL-checkpoint before backup, background retention pruners.
- **Auth** — refresh-token endpoint with rotation, JWT secret minimum raised to 32 chars, trusted-proxies for `X-Forwarded-For`, credential-field bounds.
- **Cluster ops** — token persistence, non-voter promotion, simultaneous-update quorum guard, leader barrier, leader-confirmed reads (stale flag), proxy replay defense (timestamp + nonce HMAC), split-brain partition runbook.
- **Install** — idempotent systemd / logrotate, post-start health check, systemd-presence detection, sha256sum / awk preflight, openssl JWT entropy.

### Changed
- **Dependencies** — Go 1.25, docker SDK 28, sqlite 1.50, vite 8, plugin-react 6, rolldown (build 34s → 5s), eslint 10, typescript 6, lucide 1, marked 18, i18next 26. npm vulnerabilities 6 → 0.

### Tests added
- 11 priority parsers, schema migrations, cosign verification, refresh tokens, promote rate-limit, proxy replay defense.

---

## [0.11.1] – 2026-04-20

System-tuning expansion.

### Added
- **Sysctl coverage 37 → 61 parameters**, 23 new recommendations across the existing four categories plus a new conditional **conntrack** category for netfilter tuning on Docker-hosted workloads.
  - network (+8): `ip_forward`, bridge-nf-call-{iptables,ip6tables}, `tcp_slow_start_after_idle=0`, `tcp_notsent_lowat`, `tcp_no_metrics_save`, expanded `ip_local_port_range`, `tcp_rfc1337`
  - memory (+2): `vm.max_map_count=262144`, `kernel.pid_max=4194304`
  - filesystem (+5): `fs.protected_{symlinks,hardlinks,fifos,regular}`, `fs.suid_dumpable=0`
  - security (+6): full ASLR, `kptr_restrict=2`, `dmesg_restrict=1`, ptrace_scope, unprivileged_bpf_disabled, `bpf_jit_harden=2`
  - conntrack (NEW, conditional): `nf_conntrack_max`, faster TCP timeouts

---

## [0.11.0] – 2026-04-20 — *Cluster operational polish*

### Added
- `sfpanel cluster reissue-cert` CLI subcommand — re-issues this node's mTLS cert using the local CA. Hot-reload picks it up within ≤ 1 minute, no restart required.
- New e2e specs: `cluster-remote-node` exercising real `POST /auth/login`, `cluster-password-replication` validating CmdSetAccount FSM replication.

### Changed
- `defaultLogSources` lifted off the package-level mutable global onto the `logs.Handler` struct so parallel handlers don't race on map writes.
- All three CI workflows set `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24=true` ahead of the 2026-06 Node 20 removal from GitHub runners.

### Fixed
- `InitCluster` failure path now resets `h.Config.Cluster` to `{GRPCPort, DataDir, CertDir}` only. Previously retained `cfgCopy.NodeID` from a prior `mgr.Init` could match a stale Raft bootstrap configuration on retry.

### Docs
- CLAUDE.md documents cert lifetimes (10y CA / 5y node), rotation procedure, simultaneous-restart election window (~15–20 s), and the intentionally-unpinned upstream installer scripts.

---

## [0.10.x] – 2026-04-20

Cluster bug-fix series.

### v0.10.4 — *Remote-node UI*
- WS relay closure capture fixed; default scheme now `wss://` for cluster relay.

### v0.10.3 — *Init-at-runtime proxy + GetConfig field loss*
- Cluster init at runtime no longer drops the proxy chain; `GetConfig` reflects the post-init state.

### v0.10.2
- Cluster gRPC interceptor whitelist used the wrong proto package; corrected.

### v0.10.1
- Lint cleanups; version bump.

### v0.10.0
- Foundation tag for the cluster bug-fix series above.

---

## [0.9.0] – 2026-04-13 — *Cluster join redesign*

Re-architected the cluster join flow around `JoinEngine`, mTLS-first transport, and a deterministic state machine. See `docs/superpowers/specs/2026-04-13-cluster-join-redesign.md` for the design notes.

---

## [0.8.0] – 2026-04-07

### Added
- **Alert system** — `AlertManager` with 30s periodic evaluation, Discord and Telegram notification channels, channel routing, alert history.

---

## [0.7.0] – 2026-04-07 — *Modular architecture refactor*

### Changed
- Introduced `internal/common/exec` (Commander interface, SystemCommander, MockCommander) — single point for batch command execution with timeout / stderr capture / test substitutability.
- Migrated `services`, `cron`, `process`, `packages` to feature-module layout (`internal/feature/<name>/handler.go`).
- Single route registration point at `internal/api/router.go`.

---

## [0.6.x] – 2026-03-31

### v0.6.2
- Bug-fix release.

### v0.6.1
- AppStore optimizations + code-review feedback.

### v0.6.0
- **Tauri v2 desktop client** — cross-platform wrapper. Linux (deb/rpm/AppImage), Windows (msi/exe/portable), macOS (dmg).

---

## [0.5.x] – 2026-03-06 → 2026-03-24

### v0.5.6 — Docker Compose matching improvements + code-quality reinforcement
### v0.5.5 — Performance optimizations + cluster CPU improvements + Compose SSE streaming
### v0.5.4 — Cluster WS relay auth, node-switch UI, graceful error handling
### v0.5.3 — AppStore + system tuning + UI overhaul restored, search-icon fix
### v0.5.2 — WebSocket security hardening, release helper consolidation, README update
### v0.5.1 — Audit logs, WebSocket stability, metric downsampling
### v0.5.0 — Self-management, Compose backups, module path consolidation

---

## [0.3.0] – 2026-02-27 — *Firewall management*

UFW + Fail2ban support.

---

## [0.2.0] – 2026-02-27

Disk management + CLI commands (`reset`, `update`, `help`).

---

## [0.1.0] – 2026-02-26

Initial MVP.
