# Changelog

All notable changes to SFPanel are recorded here. Entries are derived from annotated git tags (`git tag -n50 <tag>`).

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/), with sections per release and the newest release at the top.

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
