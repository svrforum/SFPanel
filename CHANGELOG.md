# Changelog

All notable changes to SFPanel are recorded here. Entries are derived from annotated git tags (`git tag -n50 <tag>`).

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/), with sections per release and the newest release at the top.

---

## [0.64.2] – 2026-08-23

### Fixed

- **A cluster node could not register its certificate authority with the leader.** Requests forwarded between nodes never declared a content type, so the receiving handler rejected the body outright. Every earlier caller of that path sent a GET with no body, so nothing had exercised it until now — the symptom was a follower repeatedly reporting that the leader had refused its authority, leaving peers reaching it over plain HTTP. Found on a live two-node cluster, not in a test.

## [0.64.1] – 2026-08-23

### Fixed

- **The desktop auto-updater could not download v0.64.0.** Tauri names its bundles from a version field kept by hand in `tauri.conf.json`, while the update manifest builds its URLs from the release tag. The field still said 0.63.1, so the manifest advertised 0.64.0 and pointed at files that did not exist — every desktop update attempt got a 404. The version is now stamped from the tag at build time, so the two cannot diverge again. Server installs were unaffected.
- The tuning end-to-end spec asserted that `net.bridge.*` is always offered. Since 0.63.0 those parameters appear only when `br_netfilter` is loaded, which is not the case on a CI runner, so the suite had been failing since that release.

## [0.64.0] – 2026-08-23

### Added

- **HTTPS on the panel port, set up for you.** Set `server.tls.enabled` and the panel generates its own certificate authority on first boot, issues itself a certificate from it, and serves HTTPS on the same port — no `openssl`, no manual steps. Download the authority from Settings → System, install it once per device, and the browser warning stops for good. `install.sh` turns this on for **fresh installs only**: it never rewrites an existing `config.yaml`, so panels already deployed keep serving plain HTTP until their operator opts in, and setups that terminate TLS at a reverse proxy simply never do.
- The authority lasts ten years, the server certificate one. That split is deliberate: the file you install on your phone and laptop is the authority, so that is what carries the long life, while the certificate itself stays under the 398-day limit platforms enforce. It renews on restart before it expires and reissues if the host's addresses change — the authority is untouched either way, so devices only ever install it once. Docker's per-project bridges are left out of the certificate, or an ordinary `docker compose up` would reissue it.
- Settings → System shows what the panel is serving: both expiry dates, the authority's SHA-256 fingerprint so you can match it against what your device shows after installing it, and every name and address the certificate covers.
- Operators who supply their own `cert_file` and `key_file` keep full control: the panel never renews or replaces that material, and refuses to start rather than quietly serving something else in its place.

### Fixed

- **The web terminal dropped you into a shell with no aliases and no prompt.** `ll` was not found and the root prompt was bare, because the session's `HOME` resolution had two steps that failed for the same reason — on Linux the fallback only re-reads `$HOME`, which systemd does not set for a unit with no `User=`. Every session landed in `/tmp`, where there is no `.bashrc`, and nothing reported an error. The home now comes from the OS user database, and `USER`/`LOGNAME`/`SHELL` are set too.
- **The terminal now names what you are typing into** — the account and host appear in the tab bar, in a warning colour when that is root. In a cluster the same page targets a different machine depending on the node picker, and a root prompt looks identical on all of them.
- **The terminal follows the app theme** instead of staying dark in light mode, and **Ctrl+Shift+C copies the selection** — xterm draws its selection into a canvas, so the browser's own copy never saw it. Paste already worked and was left alone.
- The disk overview no longer opens with Docker volume usage above the disks themselves.

### Internal

- Six latent defects in the cluster code, each reproduced before being changed: a nil dereference across twelve `RaftNode` methods; two discarded errors in certificate issuance, one of which could write an empty private key that only failed later mid-handshake; a discarded marshal error that would apply an empty FSM entry silently; a certificate reload that watched only the certificate file, so a key-only rotation was never noticed; a CA seeding guard that could never repair a missing certificate; and a comment describing self-healing behaviour that does not exist in the code.
- Five separate places used to hardcode a plaintext loopback URL to the panel's own port. They now share one resolver. The update watchdog's copy was the consequential one — ninety seconds of failed health probes rolls back the binary *and* the database, so a panel with TLS on would have reverted every self-update it attempted.
- Peers learn each other's certificate authority through replicated cluster state, public certificates only, over the existing config-set command so that a node on an older build applies it as an ordinary write and preserves it through its own snapshots. Mixed-version clusters need no negotiation: a peer with no replicated authority is simply still reached over HTTP.

### Upgrade notes

- Nothing changes unless you turn TLS on. When you do, **the origin changes**: `http://host:3628` and `https://host:3628` are different origins, so you will be logged out once and per-origin browser state (terminal tabs, font size) resets. Old `http://` bookmarks fail at the transport with no redirect — the panel serves HTTPS only, with no plaintext fallback by design.
- The desktop app cannot connect until the operating system trusts the authority. Install it first.

## [0.63.1] – 2026-08-21

### Fixed

- **The pre-paint theme script was being blocked by the panel's own CSP**, so dark-mode users got a white flash on every page load. `index.html` carries one inline script — the theme applier that runs before the app bundle precisely to prevent that flash — and `script-src` was `'self'` only. The script had been dead in production since the policy was tightened, with a console entry as the sole symptom. It cannot move into the bundle (that runs after parse, which *is* the flash) or take a nonce (the page is a static embedded asset), so the CSP now allows it by hash. The hash is computed at startup from the embedded `index.html` rather than stored as a constant: a constant goes stale the moment the script is edited and fails just as silently as this bug did. `'unsafe-inline'` is not used, so the policy is no weaker than before.
- Note when upgrading: the service worker caches the shell response together with its headers, so an existing browser session keeps the old policy until it refreshes.

## [0.63.0] – 2026-08-21

### Added

- **Network drives — connect an SMB/CIFS or NFS share from a NAS or file server** under Disk. `/etc/fstab` is the only place the configuration lives, so nothing drifts when someone edits it by hand; entries the panel wrote carry a marker comment, and hand-written ones are listed but never rewritten or deleted. Shares can be discovered from a server rather than typed, and the connection is testable before it is saved.
- Two properties shape it. The share is **mounted before anything is written to fstab**, and persisted only once that succeeded — the reverse order saves an entry nobody has proven works, which is how a bad fstab line reaches the next boot. Every entry gets `_netdev` and `nofail`, so an unreachable NAS cannot drop the host into an emergency shell. Passwords never reach fstab (it is world-readable): they go to a 0600 file referenced by `credentials=`, and the option sanitiser rejects `password=`/`credentials=`/`username=` along with whitespace and `#`, which would otherwise forge extra fstab fields or a whole new entry.
- Mount attempts are bounded at 25 seconds, and NFS uses `retry=0` interactively — `mount.nfs` against a dead server otherwise grinds for about two minutes, outliving the browser's own request timeout. fstab keeps the default retry, where `nofail` already makes a slow NAS harmless.

### Fixed

- **The filesystem list now sorts network drives first**, then block devices, then pseudo and container-layer mounts. `df` emits kernel order, which put a freshly attached drive last — below thirty-odd Docker overlay entries, the one place its owner would not look.
- **Server tuning stopped recommending downgrades.** The values were fixed constants, so as distributions raised their defaults the panel began proposing the older, smaller number as an improvement: on a current Ubuntu kernel it offered to cut `vm.max_map_count` from 1048576 to 262144, shrink the `vm.min_free_kbytes` reserve to a quarter, and lower `kernel.unprivileged_bpf_disabled` from 2 to 1 — a write the kernel refuses anyway. Parameters where a larger number is strictly better now keep the host's value when it already meets the recommendation, on the apply path as well as the status view.
- **`rp_filter` no longer asks for strict mode.** Strict reverse-path filtering drops packets whose reply would leave by a different interface — ordinary on a host running Docker bridges or a Tailscale subnet route — and turns a working peer unreachable with nothing in the logs. Loose mode is what systemd ships and still blocks spoofing.
- **`vm.swappiness` accounts for zram.** The old value assumed swap is slow; on a host with a zram device it is not, and a low setting defeats the compressed swap it was configured for.
- `net.ipv4.ip_local_port_range` no longer starts at 1024, where an early outbound connection can squat on a port a service is about to bind. The two `net.bridge` parameters are offered only when `br_netfilter` is loaded, matching the gate the conntrack category already used.

## [0.62.0] – 2026-08-21

### Changed

- **The logo is now vector artwork.** The old mark was a raster illustration — soft gradients, a bevelled cube, a circuit trace and a miniature server rack in mixed stroke weights — and below roughly 32px the detail turned to mush while the interlocked S and F stopped reading as letters, which covers most of where the mark actually appears. The replacement is constructed on a 64-unit grid: hexagon at R=27 with a 4-unit corner radius, monogram 24x30 on a uniform 6-unit stroke, and counters exactly as wide as the stroke so the S doesn't fill in at favicon size. The S is knocked out with `fill-rule="evenodd"`, so it is a real hole and the mark sits correctly on any background; its upper-left — stem plus the top and middle bars — is an F, which is how one shape carries both letters.
- **In-app the mark inherits `currentColor`**, so it picks up the primary token in light and dark, and the wordmark is live text in the app's typeface rather than a bitmap. The old banner baked "SERVER MANAGEMENT PANEL" into the image at a size unreadable in a 240px sidebar; the product name alone carries it now.
- **Icons split into two systems.** Web favicons keep the hexagon, which has to supply its own container. App icons — desktop, PWA, and the iOS home screen, which cannot use transparency — put the same S on a blue rounded square, because the platform already draws a container and nesting a second one only shrinks the letter. Desktop icons were regenerated with the official `tauri icon` CLI.
- READMEs switch banner artwork on `prefers-color-scheme` via `<picture>`.
- `web/public` drops from roughly 754 KB to 27 KB, and the repo banner from 147 KB to 15 KB per variant.

## [0.61.0] – 2026-08-21

### Fixed

- **The sidebar disappeared and came back on every page load of a clustered panel.** The shell assumed "not clustered" on mount, drew the standard sidebar, then swapped it for the cluster one once `/cluster/status` answered — at which point the cluster sidebar issued the same call again and rendered *nothing* until it returned. Two sequential round-trips with no sidebar at all in between. `Layout` now owns cluster status and passes it down, remembering the last answer so a reload draws the right sidebar immediately, and a skeleton holds the slot while a lookup is in flight. Loading, "no cluster" and "lookup failed" are three different states; returning `null` conflated them.
- **A failed node lookup rendered as an empty cluster.** The sidebar substituted an empty array when `/cluster/nodes` failed, so "leader unreachable" looked like "this cluster has no nodes", with nothing on screen to say otherwise. The last good list is kept and the failure is shown, and the local node id now comes from the status payload so it survives a failed lookup.
- **Leader-proxied reads spent the write timeout.** The three endpoints the UI polls on a timer — status, nodes and overview — used the same 10-second budget as an FSM write. That only ever mattered when the leader was unreachable, and then every 15-second poll cycle paid it in full, parking browser connections for ten seconds at a time. Reads now use a 5-second timeout plus a short breaker keyed on the leader address, so an unreachable leader costs one dial per cooldown rather than one per request. The breaker reports a cached failure without probing, which suits a poll that repeats seconds later but would be wrong for a write — FSM writes take a different path (`middleware.ProxyToLeader`) and are unchanged.

## [0.60.1] – 2026-08-14

### Fixed

- The heartbeat stream's new "closed" warning fired on ordinary reconnects — a leader change or a shutdown cancels the stream from this side, which surfaced as `context canceled` and was logged as a problem. Only a peer-side termination is reported now. (Observed immediately after the v0.60.0 rollout.)

## [0.60.0] – 2026-08-14

### Fixed

- **A long-lived cluster eventually wedged its own heartbeat, silently.** The heartbeat RPC is a bidirectional stream and the leader answers every ping with a pong, but the follower never read them. HTTP/2 hands back stream-level flow-control credit only when the application actually reads, so the unread pongs filled the window until the leader's `Send()` blocked — permanently, and inside the branch that processes a received ping, so the leader's own 90-second idle timeout never got the chance to fire. The leader then stopped reading, the follower's sends blocked in turn, its collector stopped, and the node marked *itself* suspect and then offline. Nothing was logged the whole time, because a blocked send never returns an error. Restarting the service was the only recovery. The follower now drains the reply stream, cancelling the stream on error so the existing redial path takes over. Observed on a live two-node cluster: a node sat offline for 16 hours with an empty log.
- **Cluster gRPC connections gained keepalive** (20 s ping, 10 s ack deadline, with a matching server enforcement policy) so a connection killed without the process noticing — an unclean peer death, an idle path losing its NAT entry — fails within ~30 s instead of waiting on kernel retransmit timeouts. This is defence in depth and explicitly *not* the fix for the wedge above: PING frames are not flow controlled, so the transport keeps ACKing them no matter what the application is stuck on.

## [0.59.0] – 2026-08-13

Closes out the deferred items from the v0.58.0 modularity pass.

### Added

- **Unit tests for the frontend's pure-logic modules** — 186 tests over firewall rule validation, the log parsers, the shared formatting/path/status helpers, the navigation registry and the log-view utilities. `vitest` is the only new dependency (25 packages, reusing the existing vite/rolldown tree) and runs in the node environment: no jsdom, no DOM testing library, no component tests. Wired into the existing CI frontend job. It paid for itself immediately — the two parser bugs below are its first catches.

### Fixed

- **Firewall log rows showed a dash instead of the interface for every outbound entry.** The key extractor returns its `-` placeholder for both absent and empty keys, so the intended "IN, else OUT" fallback always stopped at `IN=`'s placeholder and the OUT branch was unreachable.
- **The log-parser registry answered lookups for inherited object keys** (`constructor`, `toString`, …), reporting a parsed view and handing back a prototype member that dies on use. Unreachable today because the backend prefixes custom source ids, but the lookups no longer depend on that.
- **Superseded WebSockets were never retired.** Changing a socket's endpoint left the old socket's close handler seeing a cleared cleanup flag (a close event always arrives in a later task, after the effect re-run reset it), so it armed a reconnect and opened a second socket that overwrote the ref — orphaning one nothing would close while it kept delivering duplicate frames. Connections now carry a generation that every async continuation checks, which also covers React StrictMode leaving two ticket mints in flight during development.
- **Tailscale's installer could be started twice.** The post-install refresh now runs when the output dialog is dismissed (so a successful install no longer unmounts the log before it can be read), which left the install button live until the refresh landed; it is disabled for that window.

### Changed

- The live-log socket now sits on the shared `useWebSocket` hook, which gained endpoint query parameters and a local-node pin; it keeps only the log-specific frame parsing and per-frame batching. `/ws/cluster/overview` — the one WebSocket route with no cluster relay wrapper — pins itself local accordingly.
- The cluster stack list gained the load-failure banner and retry the single-node list already had, both now rendering the same component; Tailscale dropped its private stream dialog for the shared one; `nodeStatusBadge` retires the last duplicated status-color mapping; the shared dialog's strings moved out of the packages namespace into `common.output`, and five keys orphaned by the migration were pruned.

## [0.58.0] – 2026-08-11

A frontend modularity release: a menu-by-menu review of the SPA (8 parallel reviewers, 182 accepted improvement points) followed by a full application pass. No backend changes.

### Changed

- **Every oversized page decomposed.** Packages 1,580→31 lines (per-card files), AlertSettings 866→31 (three sections on typed api methods), DiskLVM 648→78, Logs 1,122→568, Fail2ban 1,270→634, Files 1,311→891, AppStoreDetail 1,108→790, DockerContainers 1,542→1,073, DockerStacks 1,497→1,218, Terminal 802→547 (session component extracted verbatim). 27 new extracted components follow the existing `pages/<area>/components/` precedent.
- **Copy-paste infrastructure single-sourced.** One navigation registry replaces four drifted copies (the More drawer regains dashboard/docker/terminal and drops the double-listed logs; Stacks appears in the datacenter sidebar); node-status colors, restart-wait polling, copy feedback, path helpers, sub-nav tabs, pagination, guide accordions, status pills and the log viewer (Logs + FirewallLogs now share one virtualized, auto-reconnecting viewer) each collapse to one implementation.
- **Dialogs standardize on the shared confirm/prompt primitives** across docker, files, disk, network, firewall, cron and logs pages (in-dialog spinners are replaced by toast/refresh completion; LV deletion gains type-to-confirm).

### Fixed

- **Cluster `?node=` scope was silently dropped by every raw-text stream.** Distribution upgrades, Docker/Node/CLI installs and the Tailscale installer ran on the *local* node even while you were browsing a remote one — and since the package list itself was node-scoped, the UI showed a remote node's pending updates and then upgraded the local host. These streams now route through the api client and execute on the node you are viewing, using the node scope the backend relay already accepted. **Operators upgrading from ≤ v0.57 should note that these actions now target the selected node**, which is what the surrounding UI always claimed.
- **Container log options broke the WebSocket ticket.** Changing tail/timestamps/stream/since built a URL with two `?` separators, so the auth ticket was parsed as part of the last option and the socket was rejected — container logs only connected with default options. Options now travel as proper query parameters (a comment on `buildWsUrl` documents the constraint).
- **Dashboard firewall pill tints never rendered** (`'var(--x)'+'15'` is invalid CSS) — the intended 8% tints now actually paint.
- **Post-join cluster restart polling could 401 forever** (the replicated JWT secret invalidates the browser token; the old loop required an authenticated OK) — restart waits now probe an unauthenticated endpoint with a bounded 5-minute cap, including the previously endless update wait.
- Wrong-TOTP submissions showed "enter your 2FA code" instead of "code invalid" (error-code-based detection now); login/setup errors are translated; the fail2ban "Filter missing" template label became reachable; IPv6 sources are accepted by firewall rule validation; RAID creation lists unused partitions again alongside whole disks; the firewall live-log buffer no longer grows unbounded.
- Nine referenced-but-undefined i18n keys added (caught by a changed-files key sweep); 143 new keys total, en/ko parity exact.

### Removed

- Dead frontend surface: the never-rendered NodeSelector sidebar block, MobileHeader, two unused ui-kit components, 21 orphaned api-client methods (~150 lines) and the dead `AlertRuleType`.

## [0.57.0] – 2026-08-11

The final step of the v1 proxy-credential retirement, plus a permanent real-bundle regression guard for the release verifier.

### Security

- **The legacy v1 static-secret proxy header no longer authenticates anything.** Completing the two-step retirement started in v0.56.0 (send dropped), the receive branch is now removed: a request carrying only `X-SFPanel-Internal-Proxy` is rejected, and only the replay-resistant v2 HMAC authenticates cluster-internal traffic. Peers older than v0.56.0 can no longer relay to a v0.57 node — upgrade them locally first (rolling v0.56 → v0.57 is unaffected; v0.56 senders are already v2-only). The defensive header-strip lists on the relay and gRPC receive paths are kept.
- **The Sigstore bundle verifier is now regression-guarded by a real release bundle.** The actual cosign v3.1.3 bundle from v0.56.0 is vendored as a test fixture and verified against the embedded production Fulcio roots on every test run — the synthetic fixtures share their field model with the verifier and cannot catch the model drifting from real cosign output. Also verified and documented: `install.sh`'s detached-flag verification works under cosign v3 (deprecated-with-warning), with the bundle asset noted as the migration path if cosign v4 removes the flags.

## [0.56.0] – 2026-08-11

A security-hardening release executing the deferred backlog from the v0.55.0 audit: the cluster drops its legacy replayable credential from the wire, release signing gains the standardized Sigstore bundle without abandoning already-deployed verifiers, the largest untested surface (netplan) gets frozen under tests, and the two drifted spec documents are re-synced.

### Security

- **Cluster-internal proxy auth is now v2-only on the wire.** The legacy v1 static-secret header (`X-SFPanel-Internal-Proxy`) is no longer attached to any outbound cluster traffic — HTTP/SSE relay, WebSocket relay, the gRPC loopback hop, rolling cluster updates, leader self-update, and stack migration all authenticate with the replay-resistant v2 HMAC header only. The v1 header was already inert against any peer ≥ v0.11.2 (a present v2 header always short-circuits v1), so this drops a credential that would replay forever if ever captured, without changing behaviour in supported clusters. Inbound v1 validation is retained this release so mixed v0.55/v0.56 clusters interoperate during a rolling upgrade; **it will be removed in the next release.** Peers older than v0.13.2 were already unsupported for cross-version relay.
- **Release signing now dual-publishes the standardized Sigstore bundle.** Every release keeps the legacy `checksums.txt.sig`/`checksums.txt.pem` pair (cosign v2 pin) so already-deployed panels can keep verifying self-updates indefinitely, and additionally publishes `checksums.txt.sigstore.json` (cosign v3 bundle). The in-binary verifier prefers the bundle when the asset exists — parsed offline against the embedded Fulcio roots with the same release-workflow identity pin, ignoring the transparency-log material — and hard-fails on a bad bundle with no fallback to the legacy pair, so stripping or corrupting the bundle can never weaken verification below the current level. `install.sh` continues to verify the legacy pair.

### Tests & docs

- The netplan write path and the resolvectl/`ip route` text parsers (previously the largest untested surface in the repo, ~1,360 lines) now have 16 table-driven test functions freezing current behaviour, including four documented parser quirks kept as-is for later fixes. File discovery gained a test seam (`netplanDir`) — no production behaviour change.
- `docs/specs/frontend-spec.md` and `docs/specs/tech-features.md` re-synced to v0.55.0 (they had drifted since v0.40.0).

## [0.55.0] – 2026-08-11

A security-and-maintenance release: the CI pipeline is fully green again, every open Dependabot PR was reviewed and either merged or superseded, and all fixable dependency advisories are cleared from shipped binaries. Includes the two cluster fixes that had been sitting unreleased on main since July (issue #5).

### Security

- **gRPC bumped to v1.82.1**, clearing GO-2026-6061 — the advisory's vulnerable code paths are reachable through the cluster's mTLS gRPC server (heartbeat/subscribe streams). mTLS already limited exposure to cluster peers; shipped binaries now carry the fixed transport.
- **Go toolchain bumped to 1.25.12**, clearing the crypto/tls advisory GO-2026-5856 from shipped binaries.
- **Indirect dependencies lifted past their advisories** — x/net 0.56.0 (GO-2026-5942), x/text 0.39.0 (GO-2026-5970), OpenTelemetry 1.44.0 (GO-2026-5158). None were on called code paths; the bumps clear the vuln-scan gate and shrink the audit surface.
- **Frontend advisories cleared via `npm audit fix`** — react-router(-dom) 7.18.2 (open-redirect/DoS set), dompurify 3.4.13 (sanitizer config-pollution set), fast-uri 3.1.5, brace-expansion 5.0.9. `npm audit` is now clean including dev dependencies.

### CI & build

- CI is green again: fixed the two `staticcheck` violations (`parser.ParseDir` deprecation in the error-code uniqueness test, an untagged switch in the compose git import) that had kept `go-lint` red since the Go 1.25 toolchain bump.
- The release signing step now pins cosign v2: cosign v3 removed the `--output-signature`/`--output-certificate` flags, and deployed panels verify updates against those legacy artifacts — the bundle-format migration needs its own coordinated release.
- The vuln-scan gate now keys on fixability (`Fixed in: N/A`) instead of the docker/docker module name — unfixable advisories in other modules (x/crypto's openpgp deprecation) no longer wedge CI red, and a future *fixable* docker/docker advisory now correctly fails the gate.
- The PWA service worker keeps Monaco out of the precache under vite 8.1+ (rolldown 1.1 renamed the chunk `monaco-*` → `editor.api2-*`, which slipped past the old ignore pattern and would have added ~3.5 MB to every client's SW install).

### Dependencies

The Dependabot backlog (19 PRs, oldest from June) was reviewed per-PR and merged; superseded PRs were closed with the landing commit referenced. Highlights:

- **Go**: echo 4.15.4 (upstream security backport — SFPanel serves its SPA through its own handler, so the static-file advisory never applied), modernc.org/sqlite 1.56.0, gopsutil 4.26.6, docker/go-connections 0.7.0 (adds idle-connection limits to the long-lived Docker client), OpenTelemetry aligned at 1.44.0.
- **GitHub Actions** (all SHA-pinned, majors verified against this repo's usage): checkout 7.0.1, setup-node 7.0.0, setup-go 6.5.0, goreleaser-action 7.2.2, cosign-installer 4.1.2 (cosign itself pinned to v2 — see above).
- **Web**: vite 8.1.0, @vitejs/plugin-react 6.0.3, @types/node 26, eslint-plugin-react-refresh 0.5.3.
- **Desktop**: tauri crate 2.11.1 paired with @tauri-apps/api 2.11.1 and @tauri-apps/cli 2.11.4; serde_with/rand patch bumps.
- **e2e**: Playwright 1.62.1; typescript aligned with web at ^6.0.3 (the proposed TypeScript 7 jump was declined — nothing in e2e invokes tsc, and web's toolchain needs 6.x).

### Fixed

- **Cluster follower metrics no longer flap with a constant heartbeat-EOF loop.** The leader closed a follower's metrics-heartbeat gRPC stream after 30 s idle, the exact interval at which the follower sends — and because the idle timer resets on each ping *received* while the follower sends on a fixed schedule, any network/scheduling jitter let the timer fire just before the next ping and killed the stream, reconnecting endlessly (~1/min per follower, so a follower's metrics/online status kept dropping on the leader's dashboard). The stream idle timeout is now 3× the send interval and the two values are coupled in one place so they can't drift back into the race.
- **Cluster join no longer fails when the current leader isn't the founding node.** The cluster CA private key was created only on the node that ran `cluster init` and was never replicated, so a join served by any other leader — after a leadership change, or if the founder's `ca.key` was lost — failed to sign the new node's certificate with `load CA: open …/ca.key: no such file or directory` (issue #5), and reinstalling didn't help because the installer never regenerates a CA. The CA key is now replicated through the Raft FSM alongside the JWT secret and materialized to disk on demand, so any elected leader can sign joins; existing clusters self-heal the next time the key-holding node leads. A leader that genuinely has no CA key anywhere now returns an actionable error instead of a raw file-not-found.

---

## [0.54.0] – 2026-06-27

A public-release-readiness pass driven by a whole-project review: security, supply-chain, legal, and onboarding hardening. No new user-facing features.

### Security

- **First-run setup is restricted to loopback/LAN sources.** A fresh install binds `0.0.0.0` with no admin and a public `/auth/setup` route, so a remote host could claim the admin of a root-power panel before the operator. Setup now requires a loopback/RFC1918 source (public hosts use an SSH tunnel); the installer warns when no firewall confines the port, and both READMEs put "restrict + TLS" ahead of "create admin".
- **Password policy.** The single root-equivalent account now requires 12+ characters and rejects a common-password denylist (was an 8-char minimum with no denylist), in both first-run setup and password change.
- **Go toolchain bumped to 1.25.11**, clearing 27 reachable Go-stdlib CVEs from shipped binaries.

### Supply chain & CI

- All GitHub Action `uses` are pinned to commit SHAs; added `dependabot.yml` (gomod, npm, github-actions).
- CI gained a vulnerability-scan gate (`govulncheck` + `npm audit`), least-privilege permissions, and concurrency cancellation; the two tag-triggered release workflows now serialize; Node is unified via `.nvmrc`.

### Legal & onboarding

- Added `THIRD-PARTY-LICENSES.md` (regenerable via `make third-party-licenses`) and now ship it plus `LICENSE` inside the release tarball.
- Added `SECURITY.md` (private disclosure), `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, bug/feature/PR templates, and release/CI/license README badges.

### Performance & fixes

- **Monaco no longer loads on the login page** — the 3.5 MB editor left the entry bundle and now rides the lazy Files/Compose chunks.
- One correct PWA manifest ships (the bogus auto-generated `manifest.webmanifest` is disabled); fail2ban error codes moved to `response` constants; cluster WS-relay goroutines recover from panics; the service-name regex no longer accepts a leading hyphen; `crypto/rand` failures are checked; the cluster-join address hint uses the current gRPC port.

---

## [0.53.0] – 2026-06-23

A UI/UX polish pass — dark mode, semantic design tokens, and an end-to-end keyboard-accessibility sweep — plus a terminal-feature hardening campaign driven by a deep review of the PTY/exec WebSocket path.

### Added

- **Dark mode.** The design system's dark theme is now wired up and activated, with the foundation design-system fixes that preceded it.
- **Keyboard accessibility across the SPA.** Every interactive surface gained `focus-visible` rings, icon-only controls gained `aria-label`s (reusing existing i18n keys), click-only rows/cells/headers became keyboard-operable (`role`/`tabIndex`/Enter-Space), and hover-hidden row actions now reveal on keyboard focus.
- **Terminal auto-reconnect.** A dropped WebSocket transparently reconnects to the same live PTY session with bounded exponential backoff; the server replays scrollback so the session resumes instead of leaving a dead terminal.
- **Durable audit trail for the highest-privilege actions.** Opening a host shell (`/ws/terminal`) or a container exec session now writes a queryable `audit_logs` row — these `/ws/*` routes previously bypassed the audit middleware entirely.
- **Mobile touch-scroll for the container shell and log viewers.** `ContainerShell` / `ContainerLogs` / `ComposeLogs` now share the terminal's touch-drag scrollback handler, so output that scrolled past the box is reachable on a phone.

### Changed

- **Hardcoded brand/terminal hex swept to semantic tokens.** The brand-colour sweep plus `Terminal.tsx`'s chrome (tab bar, toolbar, search bar, mobile bar) now follow the theme, so the terminal page is no longer a dark island in light mode.
- **Cluster WS relay keepalive.** The relay now pings the client leg every 30s and re-arms its read deadline on the browser's auto-pong, so a remote terminal streaming output without keystrokes is no longer torn down after ~60s.
- **Terminal tabs are scoped per node.** Persisted tabs are namespaced by node id, so switching nodes shows that node's own tab set instead of reusing the same `session_id` and spawning a duplicate PTY per tab.
- **CSWSH origin check centralized.** The same-origin-or-empty WebSocket check (and its Tauri allowlist) lives once in `internal/common/wsorigin` instead of being copy-pasted across the terminal, websocket and logs handlers.

### Fixed

- **Phase-2 UI correctness bugs:** an invisible copy button, an unsafe firewall rule edit, and a blank loader state.
- **Terminal backend hardening:** ignore degenerate `0×0` resize frames that wedged full-screen TUIs; close a reattach liveness race where a just-exited shell was treated as alive; and replace a fragile Tailwind-class-string DOM query for the active terminal with a stable `data-` attribute.

---

## [0.52.0] – 2026-06-21

Install / update / uninstall lifecycle hardening, plus a couple of cluster-mode robustness fixes. No feature surface changes.

### Added

- **`/api/v1/health` is now a real readiness probe.** It pings the SQLite connection (bounded by a 2s timeout) and returns `503` when the DB is unreachable, instead of always answering `200` regardless of backend state. A reverse proxy, load balancer, or external cluster health-check can now distinguish "process is up" from "process can actually serve requests" and stop routing to a node whose DB has gone away.
- **`install.sh` upgrade rollback + version pinning + signature enforcement.** An upgrade now backs up the live binary before the swap and automatically reverts it (and restarts the service) if the new binary fails to come up, so a bad release can't leave the panel offline. `SFPANEL_VERSION=v0.X.Y` pins the install to a specific tag instead of always-latest (for reproducible installs / staged rollouts), and `SFPANEL_REQUIRE_COSIGN=1` makes a missing or invalid cosign signature a hard failure rather than a warning.
- **`uninstall --purge`.** Plain `uninstall` now preserves config, database, and logs (and prints exactly what it kept, plus the untouched `/opt/stacks` stack data); `--purge` additionally removes `/etc/sfpanel`, `/var/lib/sfpanel`, and `/var/log/sfpanel` for a clean teardown.

### Changed

- **Uninstall is cluster-aware.** When the node being removed is a cluster member, `uninstall` now runs `sfpanel cluster leave` first so the node departs the Raft configuration gracefully instead of being orphaned as a permanently-offline voter that the surviving nodes must prune by hand.
- **Cosign verification tightened.** The signer-identity match is now anchored to `https://github.com/<repo>/.github/workflows/release.yml@refs/tags/v…` (previously a looser substring), and an unsigned download emits a loud, explicit warning rather than passing quietly.
- **Cleaner output and stricter perms.** Install/uninstall messaging no longer prints a live panel URL or systemd-specific hints on hosts without systemd, and the SQLite database plus its `-wal`/`-shm` sidecars are created `0600`.

### Fixed

- **Clear error when a cross-node update is rejected by a too-old peer.** Triggering an update on a remote node whose binary predates the update-relay HMAC auth returned a bare `401`/`403`; the panel now explains that the target node is likely too old to authenticate the relay and that it should be updated locally (SSH + `install.sh`) first.
- **Update auto-rollback no longer silently discards post-snapshot writes.** When the watchdog rolls a failed update back to the previous binary + DB snapshot, it first copies the *live* database aside to `<db>.pre-rollback` (and clears stale `-wal`/`-shm` sidecars), so any writes the failed binary accepted between the snapshot and the rollback are recoverable instead of lost.
- **Update-check surfaces version-compare failures.** A semver comparison error during the update check is now reported (`compare_error`) instead of being swallowed and reported as "no update available".
- **Portable cluster detection in `install.sh`.** Cluster-membership detection during uninstall now uses a pipe instead of a process substitution, so it works under the POSIX `sh` some minimal hosts symlink `bash` to.

---

## [0.51.0] – 2026-06-18

### Added

- **Transfer rate limit (QoS) for stack migration.** The migrate dialog now takes an optional transfer-rate cap (MB/s; 0 or blank = unlimited) so a large node-to-node migration doesn't saturate the link — useful over a metered or shared WAN (e.g. Tailscale). The cap is applied to the bundle push on the source via a cancellable pacing reader; it composes with the existing transfer-progress reporting and is recorded in the migration audit log.

---

## [0.50.0] – 2026-06-18

### Fixed

- **Node-to-node stack migration: shared-volume corruption & data-loss races closed.** Two distinct stacks that share a named/external volume could be migrated to the same target concurrently and race `clearVolume` + extract on the one shared volume, corrupting it (and, under a `delete` disposition, destroying the source over a corrupted target). Restores that touch the same docker volume are now serialized per-volume (a second one 409s). On an acked overwrite, the pre-existing target volume is now archived aside before it is wiped and restored if the import later fails — a failed overwrite no longer destroys the prior tenant's volume data (previously only the stack *definition* was backed up).
- **Migration import hardening.** Restored tar archives now reject device/FIFO nodes and setuid/setgid regular files (a compromised cluster member could otherwise plant a setuid-root binary as root). The resolved compose (after `.env` interpolation) is re-validated on the target, so an edited `.env` can't smuggle `privileged`/`network_mode: host`/`devices` past the raw-text safety check. The import health gate now waits for declared Docker health-checks to report healthy (and catches a crash-looping container) instead of treating a momentary "running" as success.
- **Crash recovery.** Orphaned migration staging (`.mig-pkg-*`/`.migrate-stage-*`) and helper containers left by a killed migration are now swept at boot; a leftover overwrite backup (`.migbak`) is restored when its stack vanished, and is hidden from the stack list instead of surfacing as a phantom project. The migration scratch namespace is reserved at stack-create time.
- **Pre-flight disk check now covers the docker storage filesystem** (where volume + image data actually lands), not just the stacks root — so a migration no longer passes pre-flight then fails with ENOSPC mid-restore. Same-device layouts are summed; separate devices are checked per-portion.
- **Split-brain avoidance.** After a transfer *connection* error (the target detaches its restore + `up` from the connection), the source now probes the target before rolling back — if the stack is running there, the source is left stopped instead of restarting into two live copies.

### Added

- Migration transfer streams byte progress to the SSE client, every terminal outcome is written to the structured audit log, and staging archives are freed incrementally during restore to cut peak target disk use. The migrate dialog now requires a pre-flight to pass before Start.

---

## [0.49.3] – 2026-06-17

### Fixed

- **Proxied remote-node API responses no longer arrive double-compressed.** Completing the 0.49.1/0.49.2 cluster-proxy fixes: a `?node=` request for a non-streaming endpoint (e.g. the dashboard's `/system/overview` host-info card) was gzipped *twice*. The gRPC loopback on the target node forwarded the browser's `Accept-Encoding`, so the target compressed the body, then the forwarding edge node compressed it again — while `Content-Encoding` still announced gzip only once. The browser decoded a single layer and `JSON.parse` choked on the leftover gzip magic byte, leaving the host-info card (and any other proxied JSON) blank. The loopback handler and the HTTP relay now strip `Accept-Encoding`, so the body is compressed exactly once, by the edge node, for the browser.
- **HTTP `?node=` proxy no longer pre-refuses a reachable peer that reads "offline".** The same stale-heartbeat guard removed from the WebSocket relay in 0.49.2 also gated the gRPC/HTTP proxy path: a `?node=` API call returned `503 node is offline` whenever the leader's heartbeat view of the peer was stale — which it routinely is right after a peer restart, and a follower never sees a sibling as online at all. The proxy now lets the gRPC call be the source of truth; a genuinely-down node still fails fast on connection-refused, matching the WS relay and the cluster-stacks aggregator.

---

## [0.49.2] – 2026-06-17

### Fixed

- **WebSocket relay no longer refuses a reachable peer that reads "offline".** Follow-up to 0.49.1: the relay wrapper fast-failed a `?node=` WebSocket with `503 node is offline` whenever the leader's heartbeat view of the peer was `offline` — but in a 2-node cluster that view goes stale right after a restart (and a follower never sees a sibling as online at all), so a perfectly reachable peer's dashboard/logs/terminal could be refused. The relay now attempts the connection and lets the TCP dial fail for a genuinely-down node, matching how the cluster-stacks aggregator already ignores possibly-stale status.

---

## [0.49.1] – 2026-06-17

### Fixed

- **Remote-node live data in cluster mode.** Opening a peer node's dashboard (and its logs / terminal / container exec) failed to stream — the metrics WebSocket 401'd with "authentication failed", so the CPU/memory charts never populated. The single-use WS ticket was being minted on the *target* node (the mint request was node-scoped via `?node=`), but a `?node=` WebSocket always connects to and is authenticated on the local node *before* the cluster relay forwards it — so the local node could not validate a peer-minted ticket. The ticket is now always minted locally; every `?node=` WebSocket stream (metrics, logs, terminal, exec) authenticates and relays correctly.

---

## [0.49.0] – 2026-06-17

Lighter binary and fewer wasted requests — no feature changes.

### Changed

- **Binary ~43 MB → ~33 MB.** Two embedded-weight cuts: (1) Monaco now imports the slim `editor.api` with an explicit language allowlist instead of the full `monaco-editor` barrel, dropping the ~6.9 MB TypeScript language worker that was embedded (via `go:embed`) but never actually spawned — the worker override already routed TypeScript to the base editor worker. The Docker/Files editors are unchanged: YAML/JSON/CSS/HTML and every other mapped language still highlight (only TS IntelliSense, already disabled, is gone). The embedded `web/dist` drops 17 MB → 9.6 MB. (2) The GitHub compose-import path drops `go-git` (and its ~7 MB crypto/diff transitive tree, 26 modules) in favour of a plain `net/http` fetch of the one compose file via the GitHub Contents API — same public / private-PAT / branch behaviour and the same typed auth / not-found errors.
- **Background tabs stop polling.** The cluster sidebar, Cluster Nodes, Processes and Services pages now use the existing `useVisibleInterval` hook (fetch on mount + while the tab is visible, paused while hidden) instead of a `setInterval` that kept firing in unfocused tabs.

---

## [0.48.0] – 2026-06-17

The cluster-wide Docker view (**Cluster › Docker**) becomes a master-detail: selecting a stack opens its full detail — services, editor, logs, and all actions — inline, scoped to the stack's own node, without leaving the page. The cluster sidebar tree now collapses by default to give the detail more room.

### Changed

- **Cluster › Docker is now a master-detail.** The left list groups every node's stacks by node; selecting one shows the same full detail panel as the single-node Docker page (services / editor / logs / up · down · redeploy / update-check / migrate / rollback / delete), scoped to that stack's node. It reuses the single-node page via a `clusterMode` prop rather than duplicating the detail.
- **Migrate, node chip, and refresh follow the route node.** The detail header shows a node chip when operating on a non-local node; migrating uses the stack's own node as the source (and the dialog shows it); the list and detail refresh after an action.
- **The cluster sidebar tree collapses by default** — a 2–3 node tree is mostly empty otherwise, so this reclaims width for content. A user who expands it keeps it expanded.

### Fixed

- **Same-named stacks on different nodes no longer mistarget.** The owning node is now in the URL (`/cluster/stacks/:node/:name`), not only the global node context, so opening e.g. `web` on node A and then `web` on node B reloads the detail for the correct node — previously the panel could keep node A's services/compose while actions hit node B. This was a real data-safety gap for clusters with duplicate stack names.

---

## [0.47.0] – 2026-06-17

A cluster-wide Docker stacks dashboard. In cluster mode a new **Cluster › Docker** tab (under Nodes) lists every node's compose stacks in one view, grouped by node, and lets you open a stack on its node or migrate it elsewhere from one place. The per-node Docker › Stacks page stays single-node.

### Added

- **Cluster › Docker Stacks tab** — a dedicated page aggregating every cluster node's compose stacks, grouped by node with live status. `GET /docker/compose/cluster-stacks` fans out concurrently: the local node listed directly, remote nodes via the cluster proxy. It *tries* each node's proxy (bounded 15s) rather than trusting the local heartbeat status — which on a follower can read a healthy peer as offline — so an unreachable node yields a stable error code and an empty list (never a 500), and a reachable Docker-less node reads as empty. Each node carries its health status for the status dot.
- **Migrate from anywhere.** The per-node migrate action takes an explicit source-node id, so any node's stack can be migrated without first switching the global node context. The migrate dialog shows the source node ("From: …") and routes pre-flight and the stream to it.
- **Node wayfinding.** Opening a node's stack switches the global node context and re-highlights the active node in the cluster sidebar tree; the stack-detail header shows a node chip so destructive actions (stop/down/delete/rollback) can't land on the wrong machine unnoticed. Both lists refresh after a migration completes.

### Changed

- The cluster-wide view moved off the Docker › Stacks page (where it was an in-list toggle) to its own **Cluster › Docker** tab; the Docker › Stacks page is single-node again. Node compose-stack responses now carry the node's health `status`, and node-level fetch errors are stable machine codes mapped to translated copy instead of raw strings.

### Accessibility

- Cluster stack rows and the migrate affordance are real buttons with focus rings — keyboard-reachable and touch-friendly (the migrate icon is always visible, not hover-only), and the cluster view works on mobile. The migrate dialog's target select and disposition group are properly labelled.

---

## [0.46.1] – 2026-06-17

### Fixed

- **Stack migration works on hosts without a `docker0` bridge.** The volume
  archive/restore helpers run a throwaway container; with default networking it
  attaches to the default bridge, which fails on hosts whose `docker0` is absent
  or custom — so a stack with a named volume couldn't migrate even though its
  own compose-managed containers run fine. The helpers only tar a mounted volume
  and never need a network, so they now run with `--network none`.

---

## [0.46.0] – 2026-06-17

Node-to-node stack migration grows from definitions to **full-fidelity data + image transfer**, with an in-panel UI. Where M1 (v0.45.0) carried only the compose file and `.env`, a migration now also moves every named volume's contents, copied bind-mount data, and the stack's images (always `docker save`/`load`, no registry dependency) — each entry SHA-256-verified end to end, so a `200` from the target implies verified-intact data, which is what makes the source `delete` disposition safe. The whole bundle is staged to disk and streamed (no GB held in memory), and the target's root-run restore path is extensively hardened against a hostile peer manifest. A "Migrate to node" dialog drives the flow from the Docker Stacks page when the panel is in cluster mode.

### Added

- **Stack migration now transfers data and images, not just definitions.** The source archives each named volume (read-only helper-container `tar`), each copied bind directory (host `tar`), and each image (`docker save`) to a temp file, SHA-256s it, then streams the assembled bundle from disk — nothing multi-GB is held in memory. The target streams each data entry back to disk, re-verifies its per-entry SHA, then `docker load`s images and recreates volumes / restores binds **before** `docker compose up`. All volumes are copied (external volumes included); special files (sockets, devices, named pipes, irregular files) and missing bind paths are skipped with a pre-flight warning rather than failing the run. New `migration_data.go` / `migration_import.go` restore primitives in `internal/feature/compose`.
- **Pre-flight sizes the real transfer.** The disk-space block now sums the actual bytes to be copied (`du -sb` per volume/bind via the helper image, `docker image inspect` per image) instead of estimating from definitions, and emits absolute-bind and large-transfer (> 5 GiB) warnings. Sizing is best-effort: an un-sizable entry contributes 0 rather than aborting the migration.
- **"Migrate to node" dialog** (`web/src/pages/docker/components/MigrateStackDialog.tsx`), surfaced from the Docker Stacks page in cluster mode: pick a target node, review pre-flight blocks and warnings, choose the disposition with an explicit overwrite acknowledgement, then watch a live phase timeline. Wired through the API client and types, with English/Korean strings.

### Security

The bundle restored on the target is built from a manifest supplied by another cluster node — integrity-checked (per-entry SHA) but **not trusted** — and is extracted as root. The restore path is hardened accordingly:

- **Manifest identifiers are gated before they reach a `docker` argv.** Volume names and image references in the manifest are validated against leading-alphanumeric allowlist regexes (`validDockerVolume`, `validImageRef`) **before** any `docker exec`, blocking argv flag-smuggling (a value like `-foo` parsed as a flag) and out-of-spec names.
- **Every archive is scanned for escape before extraction.** `validateTarSafe` rejects absolute or `..` member names and absolute / out-of-root symlink and hardlink targets, applied before any `tar -x` (host or helper container). Bind archives are additionally pinned to their own leaf via `tarTopLevelIs`, so a crafted extra/renamed top-level entry can't create or merge into a sibling directory under the shared parent.
- **Absolute binds are deny-listed and never blind-overwritten.** `absBindRestorable` requires an absolute path and refuses `/` and the system trees `/etc`, `/usr`, `/bin`, `/sbin`, `/lib`, `/boot`, `/root`, `/var` (panel DB + Raft/BoltDB state, cron, logs), `/home` (`.ssh/authorized_keys`), `/sys`, `/proc`, `/dev`, `/run`, and `/opt/stacks` — a relative manifest `Host` (which would resolve against the service CWD `/`) is rejected too. An absolute host bind is **never** `RemoveAll`'d; a non-empty target is refused (the operator clears it deliberately) rather than wiped. In-stack binds are confined under the freshly written stack dir and may not resolve to the stack dir itself (which would destroy the just-restored compose definition).
- **Volume overwrite is gated and restores an exact replica.** Restoring into a pre-existing named volume is refused unless the operator acked overwrite; on overwrite the volume is cleared first (`clearVolume`) so the result is an exact copy of the source rather than the migrated files overlaid onto a prior tenant's data. Failed-import cleanup never `down -v`s a colliding pre-existing volume — it removes only the volumes this import freshly created (`createdVols`).
- **In-memory growth is capped and recovery can't be cancelled by the failure that triggered it.** Small in-memory entries (manifest / `.env`) are bounded (`maxSmallEntryBytes`, 16 MiB) while bulk data streams to disk (`maxMigrationBundleBytes` raised to 64 GiB), and rollback / failed-import cleanup runs on a **fresh** `context.Background()` with its own 5-minute timeout, so an op-timeout failure can't no-op its own recovery.

### Fixed

- **Copy buttons work over plain HTTP.** `navigator.clipboard` is only exposed in a secure context (HTTPS/localhost), so every copy button silently failed when the panel was served plain HTTP over a LAN IP (the common reverse-proxy-less setup). A shared `copyText()` helper (`web/src/lib/utils.ts`) uses the Clipboard API in secure contexts and falls back to a hidden `<textarea>` + `execCommand('copy')` otherwise; the cluster join-token, app-store env, Tailscale/WireGuard, and 2FA recovery-code copy actions all route through it.

---

## [0.45.0] – 2026-06-17

Cold node-to-node Docker stack migration (M1, definition-only). An operator can
move a compose stack from one cluster node to another: the source is quiesced
(stopped), its compose file and `.env` are bundled and SHA-256-checksummed, then
pushed to the target over the mTLS internal proxy, where they are restored,
brought up with `docker compose up`, and verified healthy before the source's
fate is decided. A chosen disposition — `retain` (keep the stopped source),
`delete` (tear it down only after the target is confirmed healthy), or `clone`
(restart the source so both run) — is applied last, and any pre-finalize failure
rolls the source back to running. M1 carries stack *definitions* only (compose +
`.env`); bind-mount, named-volume, and image data transfer are deferred to later
milestones, and there is no UI yet — the routes drive a future panel page.

### Added

- **Node-to-node cold stack migration** under `/api/v1/docker/compose`:
  `POST /:project/migrate/preflight` and `POST /:project/migrate` (SSE) on the
  source, plus `GET /migrate/target-info` and `POST /migrate-import` on the
  target. The migrate stream emits ordered phase events — `preflight`,
  `quiesce`, `package`, `transfer`, `restore`, `up`, `healthcheck`, `finalize`,
  and on failure `rollback`/`error`. Pre-flight runs a cross-node check (target
  reachability/arch via `ProxyToNode`, published-port conflict scan with
  `start-end` ranges expanded, existing-stack collision) and blocks the run
  before anything is stopped. The transfer bundle is a single streamed,
  path-safe archive of the compose file and `.env`; the target re-hashes the
  whole stream and refuses a bundle whose SHA-256 doesn't match. New
  `migration_*.go` set in `internal/feature/compose` (types, manifest +
  dispositions, resolved-config extraction, bundle packaging, transport,
  pre-flight, import, handler). M1 is definition-only: the manifest's
  `MountSpec`/`VolumeSpec`/`ImageSpec` slots exist but carry no data yet (M2/M3).
- The compose handler now resolves the cluster manager **dynamically** via a
  mutex-guarded `SetClusterMgr` wired at boot and re-invoked from
  `OnManagerActivated`, so a node whose cluster is brought up at runtime (live
  `cluster init`/`join` without a restart) can migrate immediately instead of
  failing with "cluster is not enabled".
- Migration routes and the SSE phase sequence are documented in
  `docs/specs/api-spec.md` and `docs/specs/websocket-spec.md`, with an env-gated
  two-node e2e spec (`e2e/tests/stack-migration.spec.ts`) covering a definition
  migrate and rollback.

### Security

- **Cross-node relay authentication fixed.** The SSE/HTTP cluster relays strip
  the routing `?node=` param from the outbound request, but the v2
  internal-proxy MAC was signed over the *inbound* request URI (still carrying
  `?node=`). The peer validates the MAC over the node-stripped URI it actually
  receives, and `IsInternalProxyRequest` only falls back to v1 when the v2
  header is *absent* — not when it fails — so every `?node`-routed SSE/binary
  relay 401'd. `setAuthHeaders` now signs `httpReq.URL.RequestURI()` (the
  outbound URI the peer validates), restoring cross-node-initiated stack
  migration as well as large `?node` file/backup relays
  (`internal/api/middleware/proxy.go`).
- **Stack ids are path-traversal-validated** on both the source and the target:
  a leading-alphanumeric allowlist regex plus an explicit `..` check, applied to
  the `:project` route param and again to the id carried inside the received
  bundle, so a hostile bundle can't escape the stacks root on restore.
- **Overwrite is data-safe.** The target refuses (409) to replace an
  already-existing stack unless the source operator explicitly acked overwrite;
  when it does overwrite, it sets the prior tenant aside and keeps its named
  volumes, so a failed import can never destroy pre-existing data.

### Fixed

- **A client disconnect can no longer abort a migration mid-flight.** The
  orchestration and import paths run on a detached `context.Background()` with
  their own timeouts (25 min migrate, 10 min import) instead of the request
  context, so a dropped client or an SSE-relay timeout cannot cancel the
  quiesce/transfer/restore — or, critically, the rollback that restores the
  source — while the operator is gone.
- **Migrations are serialized per stack id** via a `sync.Map` marker
  (`409 Conflict` on a second concurrent run for the same stack), so two runs
  can't interleave destructively with one's cleanup wiping the other's healthy
  stack.
- **Honest rollback reporting.** A pre-finalize failure now claims "source
  restarted" only when the restart actually succeeded; otherwise it emits an
  error telling the operator the source must be started manually.
- **The `delete` disposition checks the teardown result.** Source removal now
  inspects the `down -v` error instead of swallowing it, so a failed teardown
  isn't reported as a clean removal.
- Idle connections are closed after the one-shot bundle push, and the SSE relay
  window is widened to 30 min for `/migrate` so the relay outlives a live
  migration.

---

## [0.44.0] – 2026-06-15

Post-v0.43.0 audit follow-ups. A multi-agent review of the whole tree, with
every high/medium finding adversarially re-verified against the code before
acting — two flagged items (a cluster "impersonation" gap and an error-code
"status drift") were investigated and deliberately *not* changed because the
recommended fixes were wrong for this codebase.

### Security

- **Cluster: `peers.json` recovery is validated.** `raft.RecoverCluster`
  bypasses consensus, so a typo'd, empty, or self-excluding `peers.json` could
  silently install an unrecoverable Raft configuration. It is now rejected at
  boot (non-empty server set that includes the local node, with parseable
  host:port addresses) instead of applied. Also documented why `ProxyRequest`
  intentionally preserves the `X-SFPanel-Original-User` header: it carries the
  forwarding node's already-authenticated identity for audit attribution, the
  RPC is gated on a verified cluster-CA client cert, and every member already
  shares the JWT secret — so stripping it would break cross-node audit
  attribution without closing any real gap.
- **App Store: fail closed on weak secrets.** Auto-generated stack secrets now
  abort the install if `crypto/rand` fails, instead of writing a predictable
  zero-derived password into the stack `.env`.
- **Uniform SSE sanitization** across the packages / app-store / system-update
  install streams, so error and status lines built from raw Go errors can't
  leak ANSI escapes or secret patterns.

### Fixed

- **Pre-update rollback backup is consistent.** The `.bak` taken before a
  binary update now uses `VACUUM INTO` instead of a plain copy of the live
  WAL-mode DB, so a commit from a still-active background writer can't leave
  the watchdog's rollback target stale.
- **App Store install reports pull failures directly** — it checks the
  `docker compose pull` exit code instead of failing later at `up -d` with a
  vaguer error.
- **Volume-usage cache no longer grows unbounded** — rows for volumes that no
  longer exist are pruned each tick (guarded so a transient list failure can't
  wipe the cache).
- **Cluster API error codes now match their HTTP status** — node-not-found →
  `NOT_FOUND`, node-already-exists → `ALREADY_EXISTS`, token errors →
  `INVALID_TOKEN` (were generic `INTERNAL_ERROR` / `INVALID_REQUEST`).
- **Crash screen is localized** (was hardcoded Korean — it renders outside the
  i18n context) and the app-store README renderer defensively escapes
  interpolated URLs.

### Changed

- **Scoped `errcheck` in CI** for the security/state packages
  (`cluster`/`api`/`auth`/`db`) via a dedicated config + `make lint-errcheck`,
  catching dropped errors on state-mutating calls — the cluster-join rollback
  now logs a failed config restore loudly instead of swallowing it. Repo-wide
  `errcheck` stays off (best-effort `_ =` is idiomatic there).
- Error-code string uniqueness is enforced by a test; the SPA and
  trusted-proxy helpers were extracted out of `router.go` (690 → 601 lines);
  UFW-parse regexes are hoisted to package level.

### Tests / Docs

- The systemd-migration test stubs `daemon-reload` (no real `systemctl`; the
  package dropped from a ~25s outlier to ~0s) and asserts the reload is
  issued; the app-store catalog test now fails instead of skipping when its
  tracked in-repo fixture is missing. `docs/specs/db-schema.md` is refreshed
  for the current connection pool (`MaxOpenConns=4`) and the
  `schema_migrations`-tracked migration model (35 steps).

---

## [0.43.0] – 2026-06-11

### Security

- **Cluster: closed three trust-boundary gaps.** The WebSocket relay no longer
  dials a remote node for an unauthenticated request (empty username), which had
  let anyone who knew a node UUID reach a remote container `exec`. The Raft
  transport now requires a verified client certificate instead of accepting
  certless TLS connections, and heartbeats are bound to the peer certificate CN
  so a member can no longer spoof another node's identity.
- **Auth: credential changes now revoke other sessions.** Changing the password
  or toggling 2FA deletes the user's other refresh-token chains, so a stolen
  chain no longer survives a password rotation. The browser keeps its own
  session via the request cookie. Audit log user filters now escape `%`/`_` so
  they can't act as wildcards.
- **App Store / Compose: hardened the safety validator.** Bind-mount blocking now
  covers `/var/lib/docker` and `/run/containerd` (and the `/var/run` symlink
  alias), `security_opt` parsing catches the `=` separator form
  (`apparmor=unconfined`), and string-form `privileged` values are caught.
- **Installers: removed a /tmp TOCTOU.** Installer scripts (Docker, Node, Claude,
  Tailscale) download to a private `0600` temp file instead of a predictable
  `/tmp` name, closing a local race between the hash check and execution.

### Fixed

- **Desktop: restored the Tauri client.** The CSRF double-submit introduced in
  the security campaign had silently broken the desktop app since v0.13.10 (its
  webview origin can't read the API cookie). Bearer requests with no cookies are
  now exempt from CSRF, the webview omits credentials so login passes CORS, the
  Tauri origins are allowed on the WebSocket handlers, and the desktop version is
  re-aligned so the auto-updater manifest resolves.
- **Backups are now consistent.** Both backup paths snapshot the database with
  `VACUUM INTO` (a transactionally consistent copy) instead of a plain copy of a
  live WAL-mode file that could be stale or torn; restores reject a backup that
  fails a `PRAGMA quick_check`.
- **Docker routes are gated on the socket's presence** so a Docker-less host no
  longer registers `/docker` routes that 500 — while a daemon that is merely down
  recovers without a panel restart.
- Compose project/`.env` writes are now atomic (`0600`); request logging records
  errors on skipped paths; the audit writer rejects submits after shutdown.

### Changed

- **License: MIT → GNU AGPL-3.0.** SFPanel is now licensed under the AGPL-3.0
  (`LICENSE` added). Self-hosting, modification and redistribution stay free;
  offering a *modified* version as a network service now requires publishing the
  corresponding source (§13). Honest self-hosters take on no new obligation. A
  separate commercial/closed-source license is available on request
  (svrforum.com@gmail.com). Past MIT-licensed commits remain MIT.
- **README: English translation + language switcher.** Added `README.en.md`
  (full translation) with a 한국어 · English toggle on both files; refreshed the
  Korean README intro (value-prop summary) and the feature table (90+ app store,
  cron run logs, 10,000-line terminal scrollback, mobile/PWA).
- **CI: `cmd/` tests now run, and the Playwright e2e suite was revived** (port and
  CSRF fixes) with a single-node smoke job.

---

## [0.42.0] – 2026-06-03

### Added

- **App Store: post-install experience.** The install success screen now offers
  **Open app** (`http://<host>:<port>`) and **Manage in Docker** (→ the app's
  Docker stack), not just Close. The install form shows a live access-URL
  preview, and generated passwords get a copy button + a "stored only in the
  stack's .env" note.
- **App Store: uninstall.** Installed apps gain an Uninstall action (with a
  destructive confirm) wired to a new `DELETE /appstore/apps/:id`
  (`docker compose down -v` + remove the stack dir + drop the installed marker).
  `GetInstalled` was previously dead UI surface.
- **App Store: deep-linkable detail** — `/appstore/:appId` opens an app directly
  and is back-button friendly. Distinct catalog empty states: search-miss (with
  "clear search") and load-error (with retry), instead of one ambiguous block.

### Changed

- **App Store catalog moved into this repo** under `appstore/` (was the separate
  `svrforum/SFPanel-appstore` repo). It's still fetched at runtime from
  `raw.githubusercontent.com/svrforum/SFPanel/main/appstore/`, so adding or
  updating an app stays decoupled from panel releases — a catalog-only commit
  reaches every panel within the cache TTL, no binary update needed. 45 apps
  migrated; the old repo now redirects to the new location.

### Fixed

- **App Store security:** reject newline injection in simple-mode `.env` values.
- De-duplicated the App Store icon-URL helper (was copy-pasted with a hardcoded
  base in two places).

## [0.41.0] – 2026-06-03

### Fixed

- **cluster: disband was a no-op after any restart.** The boot path injected the
  Raft manager into the handler via a struct literal, bypassing the one code
  path (`setManager`) that registers the `SetOnDisband` callback — so a node that
  booted into an existing cluster never honored a replicated `CmdDisband`.
  "Disband" returned 200 but nothing wiped state or restarted. Register the
  callback at boot (`ActivateBootManager`) and add an FSM replay-index guard so a
  stale committed `CmdDisband` can't self-destruct the node on restart.
- **cluster: admin account is now synced down to the local DB on disband/leave.**
  Account state only flowed local→FSM at init; while clustered, password/2FA/
  recovery changes went only to the replicated FSM. Dropping back to standalone
  then fell back to the stale pre-cluster local row (login would fail with the
  cluster credentials). `syncClusterAdminToLocalDB` now writes the FSM admin into
  the local table before the FSM is abandoned.
- **system: update-check used a string `!=` instead of a semver compare,** so a
  build whose version string differed from the latest release tag but was newer
  falsely reported "update available." Now uses `IsForwardUpdate`, matching the
  dashboard overview.
- **install/update hardening:** verify `checksums.txt` with cosign in
  `install.sh` (not SHA-256 only); the signature fallback now gates on both the
  current and target version so a node past the signing cutoff never accepts an
  unsigned update; DB snapshots include the WAL/SHM sidecars and prune only the
  timestamped main files; the CLI `sfpanel update` now backs up the binary and
  arms the rollback watchdog; the watchdog is systemd-aware (signals the process
  on non-systemd hosts); the alert manager runs under `safe.Go`; migrations are
  validated as strictly-ascending at boot.
- **mobile: horizontal-scroll and broken detail modals.** The app shell used
  `overflow-auto`, so an over-wide child slid the whole page left/right; page
  headers, selects, dashboard chart header, and the shadcn tab bar now wrap /
  scroll correctly. Detail dialogs (container/stack/network/disk/interface)
  stopped clipping label/value pairs (`min-w-0` propagation through Tabs +
  per-row truncation). The app shell now tracks `visualViewport.height` so the
  mobile soft keyboard no longer pushes the terminal under the keyboard and
  scrolls the page.

### Changed

- **settings: consolidated 6 tabs into 4** (Account / System / Alerts / Audit).
  Account merges Security (password/2FA/recovery) + language; System merges
  Maintenance (update/backup) + the former "Performance" limits (terminal
  timeout, upload size). A scope badge ("This node" / "Cluster-wide") clarifies
  what you're editing in cluster mode.

## [0.40.0] – 2026-06-03

### Fixed

- **network/wireguard** — apply the down-interface fix (v0.39.0) to the
  **interface list** too, not just the single-interface view. The WireGuard page
  lists interfaces via the list endpoint, so a stopped interface was still
  showing an empty public key / no peers there (and the add-peer client config
  read its public key from the list). Extracted a shared `populateWGInterface`
  helper used by both endpoints. (Found via end-to-end browser testing.)

## [0.39.0] – 2026-06-03

### Fixed

- **network/wireguard** — a generated client config (and QR) for a **stopped**
  interface had an empty server `PublicKey` and missing endpoint port, because
  those were read from `wg show`, which only reports data while the interface is
  up. `GetInterface` now derives the public key from the config's PrivateKey,
  reads the listen port from the config, and parses configured peers from the
  config file when the interface is down — so client configs are valid and the
  peer list is correct even before the tunnel is started. (Found via end-to-end
  browser testing.) Config-peer parsing is tested.

## [0.38.0] – 2026-06-03

### Fixed

- **auth** — **disabling 2FA from the UI now works.** The backend (correctly)
  requires the current TOTP code to disable 2FA — a guard against a session-only
  attacker downgrading the account — but the Settings → Security flow only
  prompted for the password, so disabling 2FA always failed with 400 "Current
  2FA code is required". It now prompts for the password **and** the current 2FA
  code. (Found via end-to-end browser testing.)
- **auth** — disabling 2FA also **clears the recovery codes** (they're inert
  without 2FA), so re-enabling starts from a clean slate.

## [0.37.0] – 2026-06-03

UX follow-up — **loading skeletons + error states, more pages**.

### Changed

- **cron / firewall / docker** — the cron jobs, firewall rules, and Docker
  images / volumes / networks list pages now get the same
  loading-skeleton / inline-error-with-Retry / empty-state treatment introduced
  in v0.33.0, so a failed fetch is distinct from an empty list. Skeletons show
  only on first load.

## [0.36.0] – 2026-06-03

UX follow-up — **styled prompt dialogs**.

### Changed

- **ui** — the remaining native `window.prompt()` calls (disable-2FA password,
  appstore advanced-install re-auth) are replaced by a styled `usePrompt()`
  dialog + app-level `PromptProvider`, with a proper masked password field. No
  more raw browser prompts in the destructive/auth flows.

## [0.35.0] – 2026-06-03

Per-feature improvement pass, round 19 (final) — **mobile card fallbacks**.

### Changed

- **ui** — the wide data tables on the **audit log**, **alert history**, and
  **firewall port-map** views now collapse to stacked label/value **cards on
  small screens** instead of overflowing horizontally. The desktop table is
  unchanged (`hidden md:*`); the card list renders the same filtered/paginated
  rows with the same badges, links, and actions. No new strings (card labels
  reuse the existing column-header keys).

## [0.34.0] – 2026-06-03

Per-feature improvement pass, round 18 — **2FA recovery codes**.

### Added

- **auth** — **2FA recovery codes**: generate a set of one-time codes (Settings →
  Security) to log in if you lose your authenticator. On the login screen, "use a
  recovery code instead" swaps the TOTP field for a recovery-code field; a valid
  code is consumed on use. Codes are stored hashed (SHA-256) and **replicate
  through the Raft FSM** for cluster admins — decoupled from the account record
  (migration 35 + a new `CmdSetRecoveryCodes`) so a password/TOTP change can't
  wipe them. Consuming a cluster admin's code is a leader write; on a follower the
  login is refused with a "use the leader node" hint rather than risk a reusable
  code. Generation, hashing/normalization, consume-once, and the
  password-change-preserves-codes guarantee are all tested.

## [0.33.0] – 2026-06-03

Per-feature improvement pass, round 17 — **loading skeletons + error states**.

### Changed

- **docker / services / process** — the container, service, and process list
  pages now distinguish **loading** (a skeleton placeholder), **failed to load**
  (an inline red error block with the message + a Retry button), and **genuinely
  empty** (the empty state) — previously a swallowed fetch error looked identical
  to an empty list. Skeletons only show on first load, so background refreshes
  don't flash over existing rows.

## [0.32.0] – 2026-06-03

Per-feature improvement pass, round 16 — **styled confirmation dialogs**.

### Changed

- **ui** — every native `window.confirm()` is replaced by a styled, on-brand
  confirmation dialog via a new `useConfirm()` hook + app-level `ConfirmProvider`
  (~22 call sites across cluster, disk, docker, files, alerts, security, backup,
  tuning, audit, WireGuard, and the compose healthcheck composer). Destructive
  actions get a red confirm button; multi-line prompts split into title +
  description. One previously hardcoded Korean confirm string is now i18n'd
  (`compose.healthcheck.removeConfirm`, both locales).

## [0.31.0] – 2026-06-03

Per-feature improvement pass, round 15 — **cluster overview WebSocket push**.

### Changed

- **cluster** — the cluster dashboard now receives a combined **status + overview
  + recent events** snapshot over `/ws/cluster/overview` instead of polling three
  HTTP endpoints every 15s. A single shared sampler per node rebuilds the
  snapshot from the local (Raft-replicated) FSM + event bus every 5s and fans it
  out to every open dashboard — no per-tab leader RPC. Followers serve their
  replicated view flagged `stale` (the UI already renders a banner). The page
  keeps one mount-time fetch for instant first paint, then live-updates over the
  socket. Snapshot builder + broadcaster lifecycle are race-tested.

## [0.30.0] – 2026-06-03

Per-feature improvement pass, round 14 — **type-to-confirm for destructive ops**.

### Added

- **ui/safety** — a reusable **type-to-confirm dialog**: irreversible actions now
  require typing the exact device/array/cluster name before the destructive
  button enables. Applied to **disk format**, **partition delete**, **RAID array
  delete**, and **cluster disband** (the disband's plain `window.confirm` is
  replaced). A stray click can no longer trigger data loss on these flows.

## [0.29.0] – 2026-06-03

Per-feature improvement pass, round 13 — **Docker firewall iptables dedup**.

### Changed

- **firewall** — `GET /firewall/docker` now reads the NAT `DOCKER` chain **once**
  and derives both the published-ports view and the reverse-DNAT map from that
  single output, instead of running `iptables -t nat -L DOCKER` twice per
  request. Parsers were split into pure functions over a pre-fetched listing;
  behavior is unchanged. The single-fetch invariant is tested.

## [0.28.0] – 2026-06-03

Per-feature improvement pass, round 12 — **WireGuard peer management**.

### Added

- **network/wireguard** — manage peers from the UI instead of hand-editing raw
  config: **generate a keypair** (`POST /network/wireguard/keypair`), **add a
  peer** (`POST .../configs/:name/peers`) which appends a validated `[Peer]`
  block and applies it live with `wg set` when the interface is up, **remove a
  peer** (`DELETE .../configs/:name/peers?public_key=…`), and **toggle boot
  autostart** (`wg-quick@<name>` enable/disable). The add-peer flow generates a
  client keypair, assembles the client config browser-side (the server never
  stores the client private key), and renders it as copyable text + a **QR
  code** for mobile import. Public key / preshared key / CIDR / endpoint inputs
  are validated server-side; the config append/remove parsing is tested.

## [0.27.0] – 2026-06-03

Per-feature improvement pass, round 11 — **SMART self-tests**.

### Added

- **disk** — **trigger a SMART self-test** (short or long) on a drive (`POST
  /disks/:device/smart/test`); the test runs on the device and smartctl returns
  an ETA. The SMART view now also parses and shows the drive's **self-test log**
  (type, status, pass/fail, power-on hours when run) so prior and just-completed
  results are visible. Self-test log parsing is tested.

## [0.26.0] – 2026-06-03

Per-feature improvement pass, round 10 — **scheduled local backups**.

### Added

- **system** — **scheduled backups**: enable a recurring local backup (interval
  in hours, retention count) that writes timestamped `tar.gz` archives (DB +
  config + compose files) to a `backups/` dir beside the database and prunes to
  the retention limit. A background runner (`StartBackupScheduler`) checks every
  10 minutes and runs when due; the last run's time/status/error are recorded
  (migration 33–34, `backup_schedule`). New endpoints: `GET/PUT
  /system/backup/schedule`, `POST /system/backup/schedule/run`, and download/
  delete of individual archives (name pattern-validated against traversal). The
  Maintenance tab gains a schedule form, run-now, and an archive list with
  download/delete. Archive creation, listing, pruning, and the name guard are
  tested.

## [0.25.0] – 2026-06-03

Per-feature improvement pass, round 9 — **generic webhook alert channel**.

### Added

- **alert** — a third notification channel type, **webhook**, alongside Discord
  and Telegram. It POSTs a JSON body carrying a Slack/Mattermost-compatible
  `text` field plus the structured alert fields (title, message, severity,
  timestamp), so it works with Slack incoming webhooks or any custom receiver.
  Arbitrary http(s) targets are allowed by design (homelab receivers live on the
  LAN); the URL is validated and delivery is bounded by the shared 10s client.
  URL validation, payload shape, and non-2xx handling are tested.

## [0.24.0] – 2026-06-03

Per-feature improvement pass, round 8 — **metrics sampling optimization**.

### Changed

- **monitor** — the `/ws/metrics` dashboard stream now shares a **single
  sampler** instead of each connected viewer running its own 2-second
  `GetMetrics` poll (≈5 syscalls per tick per viewer). One background goroutine
  samples on the interval and fans the result out to all subscribers; it runs
  only while at least one client is attached, drops a tick for any slow client
  rather than stalling the rest, and hands a freshly opened dashboard the last
  cached sample immediately. No change to the WS payload. Broadcaster lifecycle
  is race-tested.

## [0.23.0] – 2026-06-03

Per-feature improvement pass, round 7 — **standalone Docker container creation**.

### Added

- **docker** — create a **standalone container** from the UI (`POST
  /docker/containers`) without a compose file or shell: image (pulled if
  missing), name, published ports (host/container/proto), environment variables,
  volume binds (with read-only), restart policy, network, command, and
  auto-start. The form is an explicit, validated subset of the Docker create API
  rather than a full HostConfig passthrough; image ref, container name, restart
  policy, and host ports are validated server-side (tested).

## [0.22.0] – 2026-06-03

Per-feature improvement pass, round 6 — cluster **rolling-update UX**.

### Changed

- **cluster** — the cluster-wide update view is now a **per-node stepper** with
  an overall progress bar (completed / total, failure count) instead of a flat
  scrolling log. Each node shows a localized step state (updating, waiting for
  restart, transferring leadership, back online, slow restart, skipped, failed)
  derived from the structured SSE the backend was already emitting. No backend
  change.

## [0.21.0] – 2026-06-03

Per-feature improvement pass, round 5 — deepening the **files** module.

### Added

- **files** — **recursive name search** (`GET /files/search`) under the current
  directory, bounded by a result cap and a wall-clock deadline (the response
  flags truncation); **copy** of a file or directory tree (`POST /files/copy`,
  refuses to clobber, copy-into-self, or write a critical path, and skips
  non-regular files so it can't be tricked through a symlink); and
  **multi-select delete** in the UI (per-row checkboxes + bulk delete with a
  per-item result summary). Copy/search path handling and the copy guards are
  tested.

## [0.20.0] – 2026-06-03

Per-feature improvement pass, round 4 — deepening the **process** module.

### Added

- **process** — the process list now carries **parent PID, absolute resident
  memory (RSS), and nice value** alongside the existing CPU/mem%. The page gains
  a **tree view** (parent→child, built from PPID with a cycle guard), a **renice**
  control (clamped −20..19, same protected-PID guards as kill), and two new
  **job-control signals — STOP (pause) and CONT (resume)** — in the signal menu.
  Renice and the new signals refuse protected PIDs and panel-spawned children,
  same as kill; validation is tested.

## [0.19.0] – 2026-06-03

Per-feature improvement pass, round 3 — finishing the "backend exists, UI
missing" gaps and a security fix surfaced while testing the self-update flow.

### Fixed

- **web/security** — streaming POST requests that bypass the JSON api client
  (system self-update, backup/restore, image pull, compose up/update, file
  upload, and the appstore/packages/tailscale install streams) omitted the
  `X-CSRF-Token` header, so `CSRFProtect` rejected every one with **403**. This
  silently broke the in-panel self-update (and several install flows) from the
  browser. All streaming call sites now route headers through a shared
  `api.streamHeaders()` that carries both the Bearer token and the CSRF
  double-submit token.
- **compose** — the per-service healthcheck indicator used an indentation-based
  line scanner that missed quoted service names, flow-style blocks, and
  healthchecks pulled in via YAML anchors + the `<<` merge key. Replaced with a
  real YAML parse (also honors `healthcheck: {disable: true}`). Parser tested.

### Added

- **cluster** — **list and revoke pending join tokens** (`GET/DELETE
  /cluster/tokens`). The list is redacted (masked value + short fingerprint;
  full tokens are never returned) so a mis-issued invite can be invalidated
  before use.
- **cluster** — **edit a node's advertised address** (API + gRPC) inline and
  **leave the cluster** from the local node's row, with a quorum-loss force
  override. Both backend routes existed but had no UI.
- **terminal** — **reattach to a preserved PTY session** (`GET
  /terminal/sessions` + a picker). The server kept sessions and scrollback alive
  across disconnects, but the frontend always minted a fresh session id, leaving
  them unreachable.

## [0.18.0] – 2026-06-03

Per-feature improvement pass, round 2 (deepening existing modules).

### Added

- **disk** — a **Disk Usage explorer** tab: click-to-drill, largest-first size
  bars, path bar. Backed by the existing `du` handler (which gains `-x` so
  pointing it at `/` stays on one filesystem). The endpoint shipped earlier but
  had no UI.
- **audit** — **filter** the log by user (substring), HTTP method, and status
  class (2xx/4xx/5xx/≥400), and show the **node** column. Parameterized queries
  (injection-safe, tested).
- **alert** — a **node multi-select** for `specific`-scope rules; previously such
  rules couldn't target any node from the UI so they never fired.

### Fixed

- **alert** — the history table's Node/Status columns always rendered blank
  because the frontend read `node`/`status` while the API returns
  `node_id`/`sent_channels`; aligned the type and derive a delivered status.

## [0.17.0] – 2026-06-03

Feature/UX improvements from the 2026-06-02 per-feature review (round 1 of an
ongoing pass — deepening existing modules, no new subsystems).

### Added

- **dashboard** — the metrics-history chart now plots **root-disk usage** (amber
  series) alongside CPU and memory. `metrics_history` gains a `disk_percent`
  column (migration 32, old rows default 0).
- **services** — **view the unit file** (`systemctl cat`, read-only) via a
  Logs/Unit-file toggle in the service dialog. New `GET /system/services/:name/cat`.
- **cron** — **run a job on demand** with captured output. New `POST /cron/:id/run`
  (executes the entry via `sh -c`, 5m timeout) + a per-job run-now button/dialog.
- **firewall** — the lockout-guard **force override** is now reachable from the
  UI: a 409 from enable/add/delete opens a dialog showing the guard's reason and
  re-runs with `force=true` on confirm. API errors now carry HTTP status/code.

### Fixed

- **packages** — streamed installs/upgrades showed a green "completed" even when
  the underlying apt/npm/dev-tool command failed; the output dialog now detects
  `ERROR:` markers and renders a real failure state.

## [0.16.2] – 2026-06-02

Spec documentation refresh + two small accuracy fixes. A page-by-page review
(dashboard through desktop) brought `docs/specs/{tech-features,frontend-spec,
api-spec}.md` back in line with current behavior — they had drifted since the
2026-04-19 snapshot (cluster tree UI, container observability, dev-tool
installers, port map, scoped audit clear, SSE install/upgrade, etc.).

### Fixed

- **web/src/types/api.ts** — `AuditLogEntry` now includes the `node_id` and
  `protected` fields the backend already returns.
- **web/src/pages/Connect.tsx** — the desktop connect screen's example URLs and
  placeholder used the dropped legacy `:19443` port; updated to `:3628`.

### Docs

- Refreshed all feature/page/API spec sections to match the shipped code
  (no behavioral change to the server).

## [0.16.1] – 2026-06-02

Frontend serving hot-fix. A live UI walkthrough (desktop + mobile) found that an
already-open tab breaks after a panel upgrade: it requests old lazy-chunk hashes,
the SPA fallback served `index.html` (200 `text/html`) for the missing `.js`,
tripping strict MIME checking, and the whole app died behind a generic
ErrorBoundary that a plain refresh couldn't always clear.

### Fixed

- **internal/api/router.go** — `spaHandler` now returns `404` for a missing
  concrete asset (a path whose last segment has an extension) instead of falling
  back to `index.html`; extensionless client routes still fall back. `index.html`
  is served `Cache-Control: no-cache` and hashed `/assets/*` `immutable` so an
  upgrade takes effect immediately while assets stay long-cached. Pinned by
  `internal/api/spa_handler_test.go`.
- **web/src/main.tsx + components/ErrorBoundary.tsx** — reload once on
  `vite:preloadError` (and on a chunk-load error reaching the ErrorBoundary),
  guarded to at most one reload per 10s so a genuinely missing chunk can't loop.
- **web/vite.config.ts** — PWA `registerType: 'autoUpdate'` so a new service
  worker takes over on the next load instead of waiting on a prompt the app never
  surfaced (which had left upgraded panels serving a stale precached shell).
- **web/index.html** — add the standard `mobile-web-app-capable` meta (the
  `apple-` one is deprecated and warned on every page).
- **web/src/components/cluster/TreePanel.tsx** — sort the sidebar node list
  stably (local node first, then by name) instead of the random Go-map order that
  reshuffled it between pages.

## [0.16.0] – 2026-06-02

Module hardening sweep. Stability, security, and optimization fixes across ~20
feature modules from the 2026-06-02 multi-module review. No breaking changes;
additive API only (optional cron `expected_raw`, `CRON_CONFLICT` /
`PROXY_RESPONSE_TOO_LARGE` error codes). Full findings + verification log in
`docs/superpowers/research/2026-06-02-module-review/findings.md`; plan in
`docs/superpowers/plans/2026-06-02-module-hardening-sweep.md`.

### Fixed

- **auth** — serialize refresh-token rotation so two concurrent refreshes of the
  same token under WAL no longer deadlock (`SQLITE_BUSY_SNAPSHOT`) into a 500;
  record an audit event for unknown-username login failures.
- **alert** — dispatch notifications on a bounded async worker queue so a slow
  webhook can't stall the serial docker-events listener or the evaluate ticker;
  reserve the cooldown atomically to close a concurrent double-send race.
- **logs** — match the custom-source allowlist on path-segment boundaries (was a
  boundary-less prefix that silently depended on caller trailing-slash
  discipline); count filtered totals from the in-memory grep result.
- **firewall** — capture the DNAT rule protocol from the regex instead of a
  whole-line `udp` substring scan (a mis-detect produced an ineffective DROP);
  serialize DOCKER-USER chain edits so concurrent renumbering can't delete the
  wrong rule; write fail2ban jail configs atomically.
- **disk** — guard `findMountPoint` against an empty resolved device path (was
  mis-gating format/resize via pseudo-fs); bound `smartctl` with a 45s timeout;
  accept `/dev/mapper` LVM targets in `ExpandFilesystem`.
- **cluster** — close the opposite relay connection on one-sided failure (was
  waiting out a 60s deadline); return a clear error when a proxied response
  exceeds the gRPC cap; re-fetch nodes from the live FSM during a rolling update.
- **db** — check `rows.Scan`/`rows.Err` on event, alert, audit, and settings
  reads; make audit-clear count+tombstone+delete a single transaction.
- **system/files** — surface restore-restart failures (was fire-and-forget) and
  stream the file-edit backup instead of reading the original fully into memory.
- **terminal** — add a WS read deadline + keepalive so half-open clients are
  detected promptly instead of at TCP RTO.

### Changed

- **process/services/disk** — collect TTL-cache data without holding the write
  lock so a refresh no longer blocks concurrent dashboard readers; move the
  services and disk caches onto the Handler.
- **docker** — parallelize image-update checks with a per-image timeout.
- **cluster** — expire pooled gRPC connections on idle rather than creation age;
  run background loops via `safe.Go`.
- **cron** — optional `expected_raw` optimistic-concurrency guard against
  line-index TOCTOU on update/delete (409 `CRON_CONFLICT` on mismatch; omitting
  it preserves prior behavior).

---

## [0.15.3] – 2026-05-24

Security hot-fix. Closes the 4 most operator-impacting Critical findings
from the 2026-05-24 four-domain review (fresh install / single-node
update / cluster lifecycle / cluster runtime). Phase 1 of
`docs/superpowers/plans/2026-05-24-cluster-and-update-hardening.md`.

### Fixed

- **internal/api/middleware/proxy.go** (C1) — `setAuthHeaders` signed the
  v2 internal-proxy HMAC over `URL.Path` (path only) while the receiver
  in `internal/auth/proxyauth.go` verifies against `URL.RequestURI()`
  (path + query). Every cross-node HTTP relay carrying a query string
  failed the v2 check and was rejected 401 — `?node=<id>` on
  `/files/download?path=…`, `/files/upload?path=…`, `/logs/read?source=…`,
  query-bearing `/system/restore`. Now signs over `RequestURI()` so the
  signer and verifier consume identical bytes. Pinned by
  `TestSetAuthHeaders_V2SignsPathPlusQuery`.
- **internal/feature/auth/handler.go** (C2) — `GetSetupStatus` and
  `SetupAdmin` decided "setup required" from the local SQLite `admin`
  table only. A node that joined an existing cluster has an empty local
  table (admin lives in the Raft FSM), so the bootstrap endpoint stayed
  open and would accept an attacker-supplied admin that could overwrite
  FSM admin on a later leadership term. Both endpoints now consult the
  cluster FSM first and refuse with 409 when an admin already exists.
  Single-node installs are unaffected (cluster manager is nil and the
  check short-circuits). The test-only `ClusterAccountsFn` seam doc was
  tightened to spell out it MUST NOT be set in production.
- **internal/feature/system/handler.go + internal/release/version.go**
  (C3) — the update verifier fell through to SHA-256-only when a release
  omitted `checksums.txt.sig` / `.pem`, letting an attacker who controls
  the GitHub release page strip the signature assets and ship a malicious
  tarball under matching checksums. A new `SignatureRequiredSince =
  "0.13.0"` constant makes any update targeting v0.13.0 or later abort
  unless both signature assets are present. Pre-v0.13.0 targets keep the
  SHA-256 fallback (preserves the one-time upgrade path from unsigned
  releases).
- **internal/feature/system/handler.go** (C4) — the decompressed sfpanel
  binary entry was read with an unbounded `io.ReadAll(tr)`; the on-wire
  200 MiB cap does not bound decompression, so a high-ratio gzip bomb
  could OOM the host. Capped at 256 MiB (`maxBinaryEntryBytes`, 5× the
  real binary) via `io.LimitReader`, aborting cleanly past the cap.

### Operator note

Deploy to both cluster nodes sequentially (transfer leadership between
them), not simultaneously — a fanned-out restart of all voters can delay
leader re-election by ~15–20 s. The C1 fix restores cross-node file
download / log read / restore through `?node=<id>`; the C3 fix means a
self-built release without a Sigstore signature will now refuse to
self-update at v0.13.0+, which is intentional.

---

## [0.15.2] – 2026-05-24

Cluster-update orchestrator bugfix. The v2 internal-proxy nonce cache
treated the JWT middleware → CSRF middleware re-check on the same
inbound request as a "replay", rejecting every cross-node POST that
relied on the internal-proxy bypass. `/cluster/update` was the headline
casualty: peers returned `HTTP 403 CSRF_TOKEN_MISSING` even though the
v1+v2 headers were correctly minted by the gRPC proxy.

Symptoms before the fix:
- GET via cluster proxy (`?node=<id>`) worked, because CSRF middleware
  skips safe methods so `IsInternalProxyRequest` was called only once
  (from JWT middleware).
- POST via cluster proxy returned 403 — JWT consumed the nonce,
  CSRF re-validated, the cache saw it as a replay.
- Web-based "Rolling Update" / "Simultaneous Update" failed for every
  cluster topology.

### Fixed

- **internal/auth/proxyauth.go** — `registerNonce` now accepts a
  repeated nonce within a 50 ms grace window as a same-request middleware
  re-check, rather than rejecting it as a replay. Outside the window it
  still rejects (network-replay attackers cannot fit a captured-then-
  replayed request inside 50 ms on any realistic link). Added two unit
  tests:
  - `TestIsInternalProxyRequest_RepeatedCallsWithinRequest` — pins the
    same-request bypass that JWT+CSRF re-check depends on.
  - `TestIsInternalProxyRequest_ReplayAfterWindowRejected` — pins the
    genuine-replay rejection outside the window.
- Existing replay-rejection tests updated to sleep past the grace
  window before the replay attempt.

### Operator note

For the immediate upgrade from a pre-0.15.2 follower, the orchestrator
path still hits the bug on the follower's side until the follower is
running 0.15.2. Upgrade one follower manually once via the GitHub
Release tarball (or `gh release download`); subsequent cluster updates
from the web UI work as designed.

---

## [0.15.1] – 2026-05-23

CLI bugfix. `sudo sfpanel cluster <state-changing-op>` (leader-transfer,
token, init, join, leave, remove) all returned 403 `CSRF_TOKEN_MISSING`
because the CLI's `callLocalAPI` helper sent only the Bearer JWT — the
browser flow's CSRF cookie+header pair was missing. Discovered when
trying to drain leadership before a rolling cluster upgrade.

### Fixed

- **cmd/sfpanel** — `callLocalAPI` now attaches a synthetic same-value
  CSRF cookie+header pair on state-changing methods so CLI calls satisfy
  `CSRFProtect` middleware. Safe methods (GET/HEAD/OPTIONS) continue to
  bypass CSRF entirely. Implementation lives in a new
  `attachCSRFIfNeeded` helper with three unit tests in
  `cluster_commands_test.go`.

---

## [0.15.0] – 2026-05-22

Module-hardening follow-up. The 2026-05-22 branch-level review surfaced
25 issues — one P0 (cluster `GetStatus` nil panic during raft init), the
rest a mix of self-protection gaps, leaked secrets, context-leaks, and
graceful-empty regressions on minimal nodes. All 25 landed as
self-contained commits; this entry summarises the batch. No API breakage.

### Security

- **terminal** — PTY session keys are now bound to the authenticated
  user. A cross-user `session_id` (guessed or copy-pasted from another
  operator's tab) can no longer attach to a PTY the other operator
  started. Empty proxy usernames rejected up front so two cross-node
  sessions can't collide on an empty-string key. WS relay propagates the
  authenticated username through the cluster proxy with the HMAC-signed
  v2 header.
- **alert** — Telegram and Discord delivery errors no longer interpolate
  the bot token or webhook URL into the returned error string. New
  `ErrChannelError` code; `TestChannel` returns the generic
  `"channel delivery failed; check channel configuration"` regardless of
  the underlying transport failure.
- **disk** — `Mount`, `Unmount`, and `FormatPartition` now refuse system
  mountpoints (`/`, `/etc`, `/var`, `/var/lib/sfpanel`, `/home/*`,
  `/boot`, etc.). The lookup is mockable so tests cover the guard.
- **files** — `ReadFile` and `DownloadFile` open with `O_NOFOLLOW` and
  refuse leaf symlinks; the previous symlink-aware check only ran on
  writes. Write path now uses copy-first backup so a crash mid-write
  leaves the original intact.
- **compose** — `ResolveComposeFile` validates the project name (no
  path separators, no `..`) before composing the on-disk path; closes
  the traversal vector that healthcheck and backup endpoints exposed.
  Backup and tmp files are written at `0o600`.
- **appstore** — Compose YAML now written at `0o600` (was the umask
  default `0o644`) and the write is atomic via temp + rename so a
  half-installed stack can't be picked up by `docker compose`.
- **system** — `RestoreBackup` serialises concurrent calls behind a
  mutex, streams archive entries instead of loading the whole tarball
  into RAM, runs a WAL checkpoint before swap, and preserves the
  original file modes (was clobbering everything to `0o644`).
- **auth** — Access JWT is now generated *before* the refresh token is
  marked consumed. A transient JWT-signing failure on retry can no
  longer trip the OWASP family-revoke path and lock the operator out
  while their refresh chain looks valid client-side.
- **cluster** — `GetStatus` no longer dereferences a nil overview
  during raft init. A `/cluster/status` poll racing the first FSM apply
  used to crash the node mid-boot.

### Changed

- **terminal** — Orphaned PTY sessions (zero readers attached) are now
  reaped 5 minutes after the last reader leaves, regardless of the
  operator-tunable `terminal_timeout` setting. The previous behaviour
  let a terminal opened and forgotten with no reader keep its PTY alive
  for the full inactivity window.
- **disk** — `lsblk`, `smartctl`, `parted`, `mdadm`, and `dd` now honor
  `c.Request().Context()`. Client disconnect (browser close, NAT
  timeout) kills the subprocess inside ~200 ms instead of letting it
  run to the 5-minute Commander timeout.
- **packages** — `install`, `remove`, and `upgrade` now check for the
  `dpkg-lock-frontend` holder up front and return `409 Conflict`
  immediately with the PID + process name of whatever is holding the
  lock. The old behaviour blocked silently inside `apt-get` for as long
  as the other process took.
- **firewall** — `EnableUFW`'s lockout precheck now consults
  `ufw show added` when UFW is inactive (instead of assuming "no rules
  configured = lockout"). Operators who correctly added an SSH ALLOW
  before enabling UFW no longer hit a spurious 409.
- **docker** — `PullImage` validates the image reference against the
  Docker name grammar and rejects refs longer than 512 chars before
  invoking the daemon, so a typo-by-XSS can't trigger an arbitrary
  HTTP fetch through the docker socket.
- **compose** — Backup files and tmp files are written at `0o600`
  (mirroring the appstore change).
- **alert** — `TestChannel` returns the generic message
  `"channel delivery failed; check channel configuration"` with code
  `CHANNEL_ERROR` regardless of the underlying transport failure.

### Added

- **`internal/common/sysguard/`** — new package centralising the
  self-protection deny-lists: `IsProtectedSystemdUnit` (units that
  must never be stopped/disabled/masked through the panel API),
  `IsProtectedPID` (PIDs 0/1/2 and the panel process itself), and
  `IsPanelChildPID` (pgid-based check for subprocesses sfpanel itself
  spawned — apt, docker compose, terminal PTYs). `services` and
  `process` migrated; new module deny-list rules go here.
- **`services` self-protection extended** — `dbus.service` and
  `systemd-journald.service` join `sfpanel.service` on the no-touch
  list. Both are panel-critical: dbus is required for systemctl IPC,
  journald is the only path through which the panel reads service logs.
- **`process.KillProcess` refuses any PID in the panel's process
  group** — apt, docker compose, terminal PTYs, and anything else
  sfpanel spawned share the panel's pgid. The pgid check catches them
  in one shot; the existing PID-not-found path covers PIDs that have
  already exited.

### Fixed

- **disk, cron** — `ListDisks`, `ListFilesystems`, and the cron handlers
  return a graceful empty result (or `503 TOOL_NOT_INSTALLED` for the
  cron mutator paths) when the underlying binary is missing. Minimal
  cluster followers without `lsblk`/`smartctl`/`crontab` no longer 500
  on these list endpoints.
- **logs** — Scanner-drain wait on subprocess teardown is now bounded
  by a 2-second deadline. A wedged scanner goroutine can no longer keep
  the response open indefinitely while holding the upstream `tail -F`
  pipe.
- **websocket** — `ContainerExecWS` synchronises its two relay
  goroutines (stdin→docker, docker→stdout) before the handler returns,
  closing the race where the response writer was reused after one
  goroutine had already started the next request's frame.
- **network/tailscale** — `install` and `up` parent their subprocess
  context on `c.Request().Context()`. A client disconnect during
  Tailscale install no longer leaves a 10-minute `apt-get install
  tailscale` running in the background against a closed pipe.
- **`response.SanitizeOutput`** — now actually strips ANSI escape
  sequences. The previous implementation pattern-matched a subset of
  the format and was a no-op for the most common CSI sequences;
  command output containing colour escapes was passing through to the
  client verbatim. Disk, logs, appstore, and websocket call sites
  switched to the helper.

Plan reference: `docs/superpowers/plans/2026-05-22-module-hardening-followup.md`.

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
