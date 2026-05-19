# Changelog

All notable changes to SFPanel are recorded here. Entries are derived from annotated git tags (`git tag -n50 <tag>`).

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/), with sections per release and the newest release at the top.

---

## [0.14.0] – 2026-05-19

End-to-end module hardening pass — closes every P0 from the 2026-05-18 review
(25/25), lands all 8 Phase B systemic sweeps, all 15 Phase C module
correctness items, both Phase D refactor steps, and the Phase E hygiene
sweep. 37 commits, no API breakage besides the documented `portmap` JSON
shape change (`container` → `containers []`).

### Security

- **firewall** — `AddRule` now refuses deny/reject/limit on SSH (22) or the
  panel port unless `?force=true`, mirroring the existing `EnableUFW`/
  `DeleteRule` lockout guards. Every UFW/fail2ban/iptables/ss mutator runs
  `requireTool` at entry so missing tooling on a cluster follower returns
  `501 TOOL_NOT_INSTALLED` instead of an opaque 500.
- **packages** — `validPackageName` now anchors to an alphanumeric leading
  character, blocking `--reinstall`/`-y`/`--allow-downgrades`-shaped values.
  Every `apt-get install/remove/upgrade` call also passes the `--` end-of-
  options separator as a second-line defense.
- **websocket** — `MetricsWS` arms keepalive (pong-driven read deadline +
  ping ticker) so half-open connections tear down in seconds instead of
  minutes. `Upgrader.CheckOrigin` enforces same-origin (or empty
  `Origin` for non-browser tooling). Legacy `?token=` WS auth is now
  loopback-only — long-lived JWTs in URLs leaked into access/proxy/shell
  logs; the modern `?ticket=` (single-use, 60s TTL) flow is the only path
  for remote clients.
- **appstore** — Advanced install now requires `{password}` in the body and
  re-verifies it against the admin row via bcrypt before writing the
  user-submitted compose YAML. Body capped at 1 MB. A stolen JWT alone is
  no longer enough to escalate to host root through this endpoint.
- **terminal** — Same-origin guard on the Upgrader. Per-reader bounded
  send queue replaces the synchronous fan-out so one slow client can't
  head-of-line-block the PTY reader or peer readers (P0-17). PTY reader
  goroutine wrapped in `sync.Once` to fix the racy `started` flag (P0-18).
- **auth** — `refresh.go` now logs `slog.Error` on tx.Commit failures and
  surfaces 500 (instead of "Session revoked") when the OWASP theft-
  detection family-revoke didn't actually persist. `loginAttempts`
  sync.Map now drained on a 1h tick so a panel hit by months of internet
  scanning doesn't accumulate stale per-IP attempt records.
- **process / services** — `services.Stop/Restart/Disable` refuses to act
  on `sfpanel.service` itself unless `?force=true`. `process.KillProcess`
  writes structured audit lines with pid/signal/username/path.

### Reliability

- **cluster** — `Manager.ProxySecret()` now caches the sha256(CA cert)
  derivation; the proxy middleware hit it on every cross-node request
  with a per-call disk read + hash. `ClusterUpdate` SSE now includes the
  remote node's response body in the per-node "Update failed" event so
  the operator sees the quorum-guard refusal / downgrade refusal text
  instead of just an HTTP status. `Leave` logs the specific failure mode
  (no leader / dial failed / RPC rejected) and points at
  `sfpanel cluster remove` for recovery.
- **system** — `RunUpdate` refuses to take a node offline when doing so
  would drop the cluster below Raft quorum. Heartbeat-based check is the
  second line of defense for operators who fan out `/system/update`
  directly (Ansible, parallel ssh) instead of routing through the
  rolling-update orchestrator.
- **portmap** — `PortBinding.Proto` is now plumbed through from Docker;
  UDP-only services (DNS, WireGuard, syslog) no longer disappear or pun
  onto an unrelated TCP listener. `PortMapRow.Containers` is a slice so
  two containers publishing the same host port both surface instead of
  last-write-wins.
- **files** — `UploadFile`/`MkDir`/`WriteFile` validate symlink leaves
  via `Lstat` + `EvalSymlinks` so a `/tmp → /etc/sfpanel` symlink can
  no longer bypass `isCriticalPath`. `logs.AddCustomSource` is the same
  shape. `ListDir` now caps at 10000 entries.
- **netplan** — `saveNetplanFile` writes atomically via temp file +
  fsync + rename; a power loss mid-write can no longer leave a half-
  YAML that `netplan-generate` refuses to parse.
- **settings** — `UpdateSettings` wraps the whole batch in a
  transaction; partial application on the third write of five can no
  longer leave settings half-applied. Per-key value validators
  (`terminal_timeout 0..24h`, `max_upload_size 1..1024 MB`) now reject
  out-of-range values before any write starts.
- **safe.Go** — New `internal/common/safe` package wraps every
  long-lived background goroutine (history collectors, retention
  pruners, terminal cleanup, update checker) with `recover()` + slog
  panic logging. A nil deref inside a background loop no longer takes
  the whole panel down.

### Performance

- **db** — 4 hot-path indexes added (`audit_logs(username,created_at)`,
  `audit_logs(protected,created_at)`, `container_metrics_history(ts)`,
  `alert_history(rule_id,created_at)`). `temp_store=MEMORY` PRAGMA
  added — retention pruners no longer hit /var/tmp. `PRAGMA optimize`
  on shutdown so the next boot reads back learned index-usage stats.
  `MaxOpenConns` widened 1 → 4 (WAL-aware step short of full pool
  split). New `db.AsyncWriter` drains audit_logs INSERTs through one
  bounded queue instead of per-request `go func()` spawns.
- **firewall** — `ListJails` runs `fail2ban-client status <jail>` in
  parallel up to GOMAXPROCS; an 8-jail host's panel refresh drops from
  ~800 ms to the slowest single jail.
- **appstore** — `ListApps` bulk-loads installed-state in one query
  instead of N+1; `findFreePort` reads `ss -tlnH` once into a port set
  and checks candidates against the set (was 100 subprocess calls
  worst case).
- **alert** — Compiled container-pattern regex cached across calls;
  the per-event-per-rule path no longer pays a fresh `regexp.Compile`.
- **disk** — `mdadm --detail` runs in parallel per array (was
  sequential at ~200 ms/array).
- **terminal** — `ringBuffer.Write` switched from byte-by-byte to
  bulk-copy with at most two `copy()` calls per write.

### Hygiene

- **proxy** — `/network/tailscale/install` and `/cluster/update` added
  to the streaming-endpoint allowlist. New
  `TestStreamingAllowlist_KnownSSEHandlers` CI test enumerates every
  SSE handler and asserts each is recognized.
- **exec** — New `Commander.RunCtx` accepts a caller-supplied context
  so handlers can propagate `c.Request().Context()` and have a client
  disconnect kill the subprocess. New `exec.PrepareScanner` helper
  sizes `bufio.Scanner` buffers to 64 KB initial / 1 MB max (was the
  default 64 KB ceiling that silently truncated long log lines).
- **request logger** — `/health`, `/system/info`,
  `/monitor/metrics`, and all `/ws/*` paths now skip the request log.
  An idle panel left open in a browser stops emitting 50k+ noise
  lines/day.

### Breaking

- `GET /api/v1/system/portmap` — `container` field renamed to
  `containers` and changed from object to array. Frontend updated in
  the same release.

---

## [0.13.15] – 2026-05-18

Follow-up to 0.13.14 — closes two issues that had been flagged as
"deferred to follow-up" in the previous release notes.

### Audit

- **`audit_logs.node_id` now stamps the local processing node** instead
  of the empty `c.QueryParam("node")` it used to read. The cluster
  proxy middleware strips `?node=` before the handler chain proceeds,
  so the previous read produced an empty `node_id` on every cluster-
  routed write — forensic reviewers could not tell which node served
  a given audit row. Both `AuditMiddleware` and `ClearAuditLogs` now
  read from a `func() string` resolved per-request against
  `mgr.LocalNodeID()`, so a node that joined a cluster mid-process
  starts stamping correctly without a restart.

### Cluster

- **Cluster status proxy retries once on stale connections.** The
  follower's per-minute poll of `/cluster/nodes` (and other read-side
  proxy-to-leader endpoints in the cluster handler) was alternating
  503/200 whenever the pooled gRPC connection died between calls.
  Confirmed in journal logs: every minute around the connection's
  idle timeout, one request 503'd before the next succeeded — leaving
  the UI to render "leader unreachable" banners on a perfectly
  healthy cluster. `h.proxyToLeader` now mirrors `proxyToNodeGRPC`'s
  retry path: on first failure, drop the dead conn, reconnect, retry
  once. Added matching `slog component=cluster` lines so operators
  can correlate transient 502/504s against the journal. Verified by
  hammering `/cluster/nodes` 30× from a follower post-fix — all 30
  returned 200.

---

## [0.13.14] – 2026-05-18

Hardening pass on the v0.13.13 follower auto-forward, plus extension
to five more cluster admin endpoints. Three independent code reviews
(security-focused, backend-focused, endpoint-survey) drove the fixes.

### Cluster

- **`X-Forwarded-For` propagated across follower→leader hop.** Before,
  every forwarded admin request appeared on the leader as `127.0.0.1`
  (the gRPC→loopback HTTP hop's source). The leader's per-IP rate
  limiter (`preRecordLoginAttempt`) collapsed every cluster admin
  auth onto one bucket, letting a single attacker on one follower
  lock out admin authentication cluster-wide. Now `proxyToNodeGRPC`
  appends `c.RealIP()` to the existing XFF chain; Echo's IPExtractor
  trusts loopback (already in the default trust list), so the
  leader's `c.RealIP()` returns the real client IP and the per-IP
  ledger keys correctly.
- **Anti-loop guard switched from a forge-able magic header to the
  cluster-internal proxy authentication.** Removed
  `X-SFPanel-Forwarded-To-Leader`; `ProxyToLeader` now checks
  `auth.IsInternalProxyRequest()` (mTLS / proxy-secret authenticated),
  so an external client can't disable the anti-loop with a spoofed
  header.
- **`LeaderNode()` returns nil when `LeaderID()` still points at the
  local node mid-step-down.** Defense against a brief race where
  `IsLeader()` flips to false a few ms before `LeaderID()` updates;
  otherwise the helper would return the local node and the forwarder
  would gRPC-self-call.
- **Cluster proxy failures now log `component=cluster` with target,
  address, path, and error.** Operators no longer have to correlate
  503/504s against an empty log.
- **`X-SFPanel-Original-Node` propagated** so the leader's audit /
  security_event row stamps the cluster node where the user actually
  authenticated, not the leader where the change landed. Empty when
  the request ran locally. Follows the same trust-boundary pattern
  as `X-SFPanel-Original-User` (stripped from external requests,
  re-set fresh from `mgr.LocalNodeID()` on the follower→leader hop).
- **Content-Type propagated from the proxied response** instead of
  hard-coded `application/json` — needed when future FSM-write
  endpoints return other media types.
- **Auto-forward extended to five more cluster admin endpoints**:
  `POST /cluster/token` (CreateToken), `DELETE /cluster/nodes/:id`
  (RemoveNode), `PATCH /cluster/nodes/:id/labels` (UpdateNodeLabels),
  `PATCH /cluster/nodes/:id/address` (UpdateNodeAddress — the
  load-bearing port-migration path), and `POST /cluster/leader-transfer`
  (TransferLeadership). All were previously returning 503 / "not the
  cluster leader" when called from a follower.

### Known issue (not fixed in this release)

- The `audit_logs.node_id` field written by the audit middleware
  (not the security_event writer) and by `ClearAuditLogs` still
  pulls from `c.QueryParam("node")`, which is always empty after
  the proxy middleware strips it. Cleanest fix is to inject the
  cluster manager into the audit handler / middleware — defer to a
  follow-up release.

---

## [0.13.13] – 2026-05-18

### Cluster

- **FSM-write endpoints auto-forward from follower to leader.** Admin
  password change, 2FA verify, and 2FA disable previously returned
  `503 "Account changes for cluster admins must run on the leader node.
  Switch to node X and retry"` when the operator happened to be logged
  into a follower — they had to manually pick the leader from the node
  selector and retry. The handler now transparently forwards the request
  to the leader via the existing gRPC proxy infrastructure and returns
  the leader's response, so any node accepts the request. Includes an
  `X-SFPanel-Forwarded-To-Leader` anti-loop guard so a brief leadership
  flap during the forward can't ping-pong the request between two peers
  that each think the other is leader. New `cluster.Manager.LeaderNode()`
  helper and reusable `middleware.ProxyToLeader()` for any future
  FSM-write endpoints. The pre-existing `failClusterPersist` 503
  fallback is retained for the rare case where no leader is currently
  elected (e.g. mid-election).

---

## [0.13.12] – 2026-05-17

Hotfix on top of 0.13.11.

### Settings

- **"Update available" navigation lands on the correct tab in
  cluster mode.** The dashboard update banner and the sidebar
  version button both routed to a bare `/settings`, which in
  cluster mode hides system/tuning/audit behind `?scope=node`
  and falls back to the General tab — so clicking "Go to
  Settings" from a new-version banner showed the language
  picker and no update UI. All three nav sites now use
  `/settings?scope=node&tab=system`; in single-node mode the
  `scope=node` is ignored and only `tab=system` takes effect,
  preserving the existing single-node behaviour.

---

## [0.13.11] – 2026-05-17

Hardening pass across alerting, auth, audit, and settings, plus
two structural changes: the `RunUpdate`/`RestoreBackup` restart
path now degrades gracefully on hosts without systemd (Docker
containers, bare-process installs), and the monolithic Settings
page was split into per-tab lazy modules.

### Alerts

- **Per-rule `node_scope` enforced at evaluate time.** Rules
  scoped to a specific node now skip evaluation entirely on
  other nodes instead of evaluating-then-discarding; the leader
  also stops fanning out per-rule notifications to nodes that
  aren't in scope. Channel secrets (`token`, `password`,
  `webhook_url`, etc.) are masked on `GET /alert/channels` so
  the operator UI never echoes them back in plaintext.
- **`AlertSettings.tsx` toggle preserves channel secrets.**
  Flipping `enabled` on a channel previously sent the masked
  secret back to the server, blanking the real value. The
  toggle now merges the patch with the existing channel record
  client-side and skips any field whose value is the mask
  placeholder. Missing Korean conntrack-fill alert label added
  to `i18n/ko.json`.
- **`AlertSettings.tsx` migrated to i18n keys.** ~80 hardcoded
  Korean strings (rule list, channel cards, modal labels) moved
  to `t('alerts.*')` for English parity.

### Auth

- **Security events recorded for password change + 2FA setup,
  verify, and disable.** Each writes an `audit_log` row tagged
  `event_type=security` so the audit tab shows a tamper-evident
  trail of credential mutations. Previously these went through
  unaudited and only the JWT-revocation side-effect was visible.

### Audit

- **`audit_log_cleared` rows preserved across clears via a
  `protected` column.** `DELETE /audit/logs` (clear-all and
  range-delete) now skips rows with `protected=1`, so the
  "audit log was cleared by X" entry survives subsequent
  clears. Range-delete support added: `?before=&after=` now
  bounds the delete instead of always wiping everything.

### Settings

- **Cluster-mode tab parity preserved across split.** Per-node
  settings (system / tuning / audit) only render when
  `?scope=node` is set in cluster mode; global settings
  (general / security / alerts) render in the default view.
  Single-node deployments see all tabs.
- **`Settings.tsx` split into lazy-loaded per-tab modules.**
  The 891-line monolith became a 33-line shell plus six
  per-tab files under `pages/settings/` (General, Security,
  Maintenance, Performance, Audit, AlertSettings). Each tab's
  state + handlers ship in their own chunk via `React.lazy`,
  cutting the initial settings bundle. New shared helpers
  `useApiAction` (loading + toast boilerplate) and
  `saveSetting` cover the common patterns.

### System

- **`RunUpdate` / `RestoreBackup` degrade gracefully without
  systemd.** Both handlers previously assumed `systemctl
  restart sfpanel` would work, which fails (or — worse, in a
  Docker container — talks to the *host's* systemd) on
  non-systemd hosts. New `lifecycle.IsSystemdActive()` helper
  (checks `/run/systemd/system` per `sd_booted(3)`) branches
  the restart strategy: under systemd, keep cycling the unit;
  elsewhere, self-exit with code 0 after flushing the response
  so the container entrypoint or external supervisor picks up
  the new binary / freshly-restored DB. The supervisor-less
  message tells the operator the process is going away.

### Docs

- **Dropped stale `healthcheck-composer-polish` plan**
  (features shipped in 0.13.0–0.13.7).

---

## [0.13.10] – 2026-05-16

Second-pass sweep on top of 0.13.9 — Opus 4.7 re-reviewed every
sidebar area for the Important + Improvement items that were
deferred from the critical-fix batch. Seven area commits, each
self-contained; this entry summarises the batch.

### Cluster

- **Stale-read indicator on `/cluster/overview` and `/cluster/nodes`.**
  Both endpoints now call `raft.VerifyLeader(2s)` and tag the response
  with `stale: true` when the leader can't confirm it's still the
  leader (e.g. mid-failover). The UI keeps rendering — better stale
  than nothing — but operators see a warning band so they don't act
  on snapshot data during a partition.
- **`POST /cluster/token` returns real grpc_port + advertise_address.**
  Previously the join URL hardcoded the panel's HTTP port (9443), so
  copy-pasting the token into `sfpanel cluster join` against a
  cluster on the non-default 3629 grpc port failed silently. The
  token response now reflects the actual values from
  `cluster.grpc_port` and `cluster.advertise_address` in
  `config.yaml`; the React token panel reuses them instead of guessing.
- **Cluster events ring: cap `Since()` at `maxEvents`.** A long-lived
  follower polling `/cluster/events?since=…` after a panel restart
  could request an unbounded slice that allocated several MB.
  Result set is now capped at the same `maxEvents` (256) the buffer
  itself uses.
- **30-day TTL ceiling on join tokens (constant).** The 0.13.9 fix
  hardcoded `30*24*time.Hour`; this release lifts that to a named
  `MaxTokenTTL` constant in `internal/cluster/types.go` so the limit
  is visible in one place.
- **Node label validation (Kubernetes-style).** `PUT /cluster/nodes/:id`
  now rejects label keys/values that violate the K8s
  `[a-z0-9A-Z]([-._a-z0-9A-Z]*[a-z0-9A-Z])?` shape; previously any
  string was accepted and rendered with broken CSS in the UI.
- **Leader self-update HTTP: explicit context + signed v2 header
  snapshot before `Shutdown`.** `ClusterUpdate` on the leader used
  to call `client.Do(req)` with no context and write the v2 header
  *after* `srv.Shutdown` had begun, racing the listener close. Both
  fixed; the self-update path is now deterministic.
- **`sfpanel cluster leader-transfer`** — graceful Raft leadership
  hand-off CLI for planned voter restarts. Wraps `raft.LeadershipTransfer`
  with a 30 s timeout; previous workflow required killing the leader
  and waiting for election.
- **Lint: staticcheck QF1003 tagged-switch on `env.Data.LeaderID`.**
  Cleanup of the chained `if`/`else if` in `cluster_commands.go:449`.

### Dashboard

- **Null-safe log slicing.** `data.lines.slice(-8)` crashed the
  dashboard when the SSE pushed an empty payload during agent
  reconnect. Now `(data.lines ?? []).slice(-8)`.
- **Composite stable keys for log rows.** React was warning about
  duplicate keys when the same logline arrived twice in a streaming
  burst; now keyed on `${timestamp}-${index}-${first 32 chars}`.
- **Single `getDashboardOverview` call** in `Layout.tsx` instead of
  `getSystemInfo` + `checkUpdate` round-trips on every render — the
  backend already merged these in 0.13.7.
- **Exact prefix match for `BottomNav` active state.** `/files` no
  longer highlights when `/files-anything-else` is the route. The
  test is `pathname === to || pathname.startsWith(to + '/')`.
- **`MetricsCard`: always render the track.** `clampedPercent > 0`
  let 0% bars vanish entirely; switched to `>= 0` so the empty
  state is still visible.
- **`MetricsChart`: chart created once.** uPlot was being torn down
  and rebuilt on every `xDomain` change — fine for correctness, an
  eyesore on slower machines. The `useEffect` now depends only on
  mount; `setData` handles in-place updates.
- **Monitor handler: partial OK instead of 500.** When `psutil`-style
  collection failed on one of host/CPU/memory, the whole endpoint
  returned 500. Now logs a WARN with `component=monitor` and returns
  the partial payload — the UI degrades gracefully to "N/A" cells.

### Docker

- **`ListContainers`: single GROUP BY query** instead of N+1 — for
  each project the loop used to issue one `SELECT … WHERE project = ?`
  per container. On a host with 30+ containers the projects view
  blocked for ~600 ms; now one query, one parse pass.
- **`ContainerLogs.tsx`: separate effects for terminal vs WS.** A
  single useEffect was reinitialising xterm.js on every WS reconnect,
  losing scrollback. Split into terminal-lifecycle (mount/unmount)
  and ws-lifecycle (open/close/reconnect) effects.
- **`ContainerShell.tsx`: `ws.binaryType = 'arraybuffer'`.** Without
  this, browsers default to `Blob`, which Chrome on iOS Safari handles
  inconsistently — occasional empty frames in the PTY stream.

### AppStore + Files + Cron

- **`appstore` install: atomic project directory creation.** Replaced
  `MkdirAll` (idempotent — silently accepts existing directories) with
  parent-`MkdirAll` + project-`Mkdir`. Two concurrent
  install clicks on the same template no longer race into the same
  directory and produce a corrupt half-install.
- **Upload basename blocklist.** The 0.13.9 fix added `.war`/`.ear`
  to the extension blocklist; this release adds a basename-level
  list (`.htaccess`, `.htpasswd`, `web.config`) — files with no
  extension or with the extension stripped that still constitute
  RCE on a misconfigured reverse proxy.
- **`CronJobs.tsx`: client-side `isPlausibleCronSchedule`.** Saves
  a round-trip on obviously broken schedules (`* * *` etc.) and
  removes the 800 ms toast delay operators saw when typing.

### Logs + Processes

- **`process/handler.go`: per-Handler cache.** The 60 s
  `top -bn1` cache lived in a package-scope variable, so all unit
  tests shared state and the cluster proxy's local-handler dispatch
  would occasionally see stale data from a previous test's
  `MockCommander` output. Moved onto the `Handler` struct.
- **`Logs.tsx`: debounced search (150 ms).** Typing in the filter
  box used to re-run the regex against the full 5500-line buffer
  on every keystroke. Now debounced.
- **`Logs.tsx`: slack-window slice.** Buffer slice threshold
  raised from 5000 to 5500 to avoid a full-array re-render every
  new line at steady-state.

### Network + Disk + Firewall

- **`disk_swap.go`: precheck `req.Path`, refuse to overwrite regular
  files.** The previous handler would happily `mkswap` a regular
  file, silently corrupting its contents. Now `os.Stat` first and
  rejects unless the path doesn't exist or is already a swap file.
- **`firewall_ufw.go`: split rule comment on `" # "` instead of last
  `'#'`.** Comments that legitimately contain `#` (e.g.
  `# allow webhook callback from #channel`) were being truncated.
- **`network/tailscale.go`: comment fix.** The block comment claimed
  the `tailscale up` subprocess was backgrounded; in fact it runs
  attached and blocks until the user authenticates. Comment now
  matches the code.

### Packages + Terminal + Settings

- **Node version regex tightening.** `^v?\d+(\.\d+)*$` accepted `v1`
  (too coarse — npm wouldn't resolve a major-only version against
  nvm) and `v1.2.3.4.5` (not a real release). Hoisted into a
  package-level `validNodeVersion` with `^v?\d+(\.\d+){0,2}$`.
- **Terminal "Clear" sends Ctrl-L (`\x0c`)** instead of literal
  `clear\r`. The previous behaviour was either harmless-but-noisy
  (typing `clear` inside vim) or actively wrong (typing `clear`
  inside `mysql` REPL, which then ran the SQL `clear` keyword and
  reset query history). `\x0c` is the universal terminal clear signal
  that every TUI handles correctly.
- **Cluster mode backup + restore warning prompts.** Both
  `handleDownloadBackup` and `handleRestoreBackup` in Settings.tsx
  now `window.confirm` with cluster-aware copy explaining that
  the single-node SQLite snapshot doesn't capture FSM state
  (admin/JWT secret/cluster_node).
- **Backup restore polling cap.** Previous restore flow polled
  `/auth/setup-status` forever; if the restore left the DB
  corrupt, the operator stared at an indefinite spinner. Now
  capped at 60 attempts (≈2 minutes) with a `restoreNoReturn`
  toast pointing to `journalctl -u sfpanel`.

### Operator notes

- This release is a pure code-cleanup pass. No schema changes, no
  new endpoints (beyond the `cluster leader-transfer` CLI), no
  config additions. Upgrade in place; no migrations.

---

## [0.13.9] – 2026-05-17

Security + stability sweep across 15 issues surfaced by the per-area
review of every sidebar feature. Each item has its own commit with a
focused regression test where applicable; this entry summarises the
batch.

### Security

- **Files API: tightened path validation + expanded read-protection.**
  `validatePath` no longer rejects legitimate filenames containing
  `..` (`/var/log/app..log` previously 400'd) and no longer accepts
  redundant segments like `/etc/./hostname` or `//etc/passwd` that
  the old `strings.Contains(p, "..")` check let through. Read-protection
  was previously narrow (`/etc/shadow`, `/etc/gshadow`,
  `/etc/sfpanel/`); the admin (or XSS riding their session) could
  read `/root/.ssh/id_rsa`, `/etc/sudoers`,
  `/var/lib/sfpanel/sfpanel.db` (admin password hashes + JWT secret).
  Added entries for sudoers/sudoers.d, SQLite live DB + WAL/SHM, and
  prefix rules for `/root/.ssh/`, `/etc/ssh/*_key`, `/home/*/.ssh/`.
- **Settings: allowlist on `PUT /settings` keys.** The endpoint
  accepted any key — admin/XSS could poison `appstore_cache`
  (unmarshal is unchecked), grow the settings table unbounded, or
  overwrite operator-tunable values past sane bounds. Restricted to
  `terminal_timeout` and `max_upload_size`; other modules already
  write their own keys via direct DB calls.
- **Cluster join tokens: 30-day max TTL.** `POST /cluster/token`
  previously accepted any `time.ParseDuration` value (`8760h` →
  1 year, `99999h` → ~11 years) and persisted the result.
- **UFW: SSH-lockout guard on enable + rule delete.** `EnableUFW`
  refused to flip default-incoming-deny without an existing ALLOW
  for SSH (22) or the panel port; `DeleteRule` simulates the
  post-delete state and refuses if removing the targeted rule
  leaves no access. Pass `?force=true` to override either.
- **Disk LVM/RAID: device-name regex no longer allows `/`.**
  The previous regex permitted `sda/anything`, which then became
  `/dev/sda/anything` — pointing `mdadm` / `pvcreate` / `pvremove`
  at unrelated kernel devices. Added `verifyBlockDevice` stat check
  that confirms `/dev/<name>` is actually a block device before
  invoking destructive tooling.

### Stability / resource leaks

- **Logs WS: kill subprocess on client disconnect.** The handler
  waited on the scanner goroutine after the client gone, but
  `scanner.Scan()` blocks on `tail -F`'s pipe — which never EOFs.
  Every tab close leaked a `tail -F` (and `grep` in filtered mode)
  for the lifetime of the panel. Now kills the primary subprocess
  inline, which closes the pipe → scanner exits → cleanup runs.
- **WebSocket exec/logs: ping/pong keepalive.** Half-open WS
  connections (browser tab crash, NAT timeout) left the docker
  exec session and bridge goroutines alive forever waiting on a
  `ReadMessage` that would never return. Added a 60s read deadline
  with a 25s ping ticker.
- **useWebSocket hook: clear pending reconnect timer before
  re-arming.** Two close paths could schedule reconnects without
  clearing the previous handle, leaking timers and double-firing.
- **Docker client cache: invalidate on mutating ops.** The 5 s
  containers-list cache went stale across Start/Stop/Restart/Remove/
  Pause/Unpause; the UI's ListProjectsWithStatus lagged visibly.
- **Cluster RemoveNode: quorum guard.** Removing a voter from a
  1- or 2-voter cluster previously took one click. Now requires
  `?force=true` when removal would drop the cluster below current
  Raft quorum (N/2 + 1).
- **Packages /upgrade: SSE instead of synchronous.** The old path
  used Commander's 5-minute timeout — a real distro upgrade
  routinely exceeded that, returning 500 mid-run with the dpkg
  lock still held. Now streams output and binds the apt subprocess
  to the request context so client disconnect kills it cleanly.
- **Packages install handlers: bind subprocess to request context.**
  All 11 sites in `packages/handler.go` swapped
  `context.WithTimeout(context.Background, …)` →
  `context.WithTimeout(c.Request().Context(), …)`. Client disconnect
  now propagates SIGTERM instead of letting the install run to
  completion against a closed pipe.

### Cluster correctness

- **Cluster proxy classifier: packages installs + docker images/pull
  marked as streaming.** Seven `/packages/install-*` routes plus
  `/packages/upgrade` and `/docker/images/pull` were falling through
  to the unary gRPC path (30 s timeout, 4 MB recv cap) when invoked
  with `?node=remote`. Clicking 'Install Docker' on a remote node
  silently timed out half-way.

### UX

- **Settings: Disable 2FA button.** The API and i18n strings shipped
  earlier, but the UI only ever offered Reconfigure — once 2FA was
  on, operators had no way to turn it off short of editing the
  database. Added a destructive button alongside Reconfigure.
- **Terminal: resolve PTY home directory at runtime.** Previously
  hardcoded `/root` — on non-root systemd installs the PTY chdir
  failed and the shell exited before the operator saw a prompt.
  Now prefers `HOME` env, then `os.UserHomeDir()`, then `/tmp`.
- **Packages search: allow multi-word queries.** The previous
  package-name regex rejected spaces, so 'redis server' got a 400.
  Split into a wider `validateSearchQuery` (apt-cache takes args
  via argv so no injection surface).
- **Docker healthcheck composer: SHA against on-disk YAML.** The
  composer dialog hashed the Monaco editor's buffer for the
  precondition check, so any unsaved edits in the editor made
  the server-side SHA mismatch with a misleading 'compose file
  changed externally' error. Split out a `diskYaml` state that
  mirrors what the server will read.

---

## [0.13.8] – 2026-05-17

Cluster observability hardening. Motivated by a real 2-day outage on
the author's homelab where two voters held diverged uncommitted entries
at the same Raft term and oscillated Follower↔Candidate forever
without any high-priority log line for the operator to alarm on.

### Added
- **`LeaderWatcher` emits ERROR-level `cluster has no leader` once the
  cluster has gone 60 s without a leader, repeating every 5 min while
  the condition persists.** Pure struct with TDD coverage; a goroutine
  in `Manager.Start`/`Init` pumps it on a 15 s tick and exits via the
  heartbeat manager's stop channel. External monitoring
  (systemd `OnFailure=`, Alertmanager, etc.) finally has something
  worth paging on — the underlying `hashicorp/raft` library only emits
  WARN-level per-election failures that operators learn to ignore.
- **`sfpanel cluster list`** prints a table of every cluster member
  with live role, status, API and gRPC addresses. Requires the local
  server to be running (the FSM lives behind raft; a second process
  would conflict on the port).
- **`sfpanel cluster status` shows live cluster info when the server
  is running** — Raft role (Leader / Follower / Candidate), current
  leader ID, peer count broken down by online/suspect/offline. The
  previous output (local config only) is preserved as the fallback
  for when the server is down.

### Fixed
- **Runbook now documents the 2-voter deadlock recovery procedure.**
  `docs/specs/cluster-partition-runbook.md` gains a "Recovery — deadlock
  from log divergence" section: identify the newer log via
  `last-term=N` vs `last-candidate-term=N-1` in pre-vote rejection
  lines, stop both services, start the newer-log node first, the
  older-log node catches up via `appendEntries rejected, sending older
  logs` truncation. ~10–15 s downtime, no data loss (the diverged
  entries were uncommitted, so nothing applied to either FSM).

### Operator notes
- The new ERROR line is `level=ERROR component=cluster
  msg="cluster has no leader" seconds_without_leader=N`. Hook it from
  Loki / promtail / journald-exporter with a `level=ERROR
  component=cluster` filter.
- `sfpanel cluster list` is the canonical "is the cluster healthy"
  command going forward. The HTTP API (`/cluster/overview`) has had
  this data all along; the CLI just couldn't surface it.

### Internal
- `internal/cluster/CLAUDE.md` sub-guide corrected — the previous
  "Heartbeat EOF noise is normal" note conflated two different log
  sources (raft library `requestVote` errors vs our application
  heartbeat warning); the latter is actually a symptom of cluster
  trouble.

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
