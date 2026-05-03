# Docker management roadmap

> **Status:** roadmap (not a spec). Each theme below will be turned into its
> own design + plan + implementation cycle. This document exists to
> establish sequencing and surface cross-cutting decisions BEFORE the first
> theme is specced in detail.

**Goal.** Make SFPanel's Docker management noticeably better than Portainer
or Dockge in dimensions where SFPanel's existing assets — Raft cluster
mode, OS-level management modules, cosign-signed update infrastructure —
give it an unfair advantage. Catch up on table-stakes container-CRUD
gaps along the way.

**Differentiation thesis.** Other panels manage Docker. SFPanel manages a
fleet of Linux servers that happen to run Docker. The roadmap leans into
the fleet/host integration angle rather than competing on Docker-feature
parity with Portainer's enterprise tier.

---

## Scope: 5 themes (B / C / D / E / F)

| # | Theme | One-liner |
|---|---|---|
| **F** | 관측성 (observability) | Container metrics history, restart/health timelines, Docker events → existing alert pipeline |
| **D** | Compose UX | Diff-before-apply, git-backed stacks, healthcheck composer, environment variants |
| **B** | OS × Docker | Unified port map, per-container egress firewall (DOCKER-USER), volume × disk view |
| **C** | 보안 / 공급망 | Cosign image verification, CVE scanning (trivy/grype), digest pinning |
| **E** | AppStore 진화 | Multi-catalog, dependency graph, cluster-aware install, app backup hooks |

A 6th angle (multi-host aggregated views) explicitly **defers** to a later
roadmap — without F's foundation it's mostly cosmetic, and without C's
signing it's risky to fan out installs.

---

## Sequencing

```
Wave 1 — foundation (parallel-friendly)
   ┌──────────────┐    ┌──────────────┐
   │  F (관측성)  │    │ D (Compose UX) │
   └──────┬───────┘    └──────┬───────┘
          │ events stream     │ git/diff/healthcheck
          │ metrics history   │
          ▼                   ▼
Wave 2 — leverage Wave 1
   ┌──────────────┐    ┌──────────────┐
   │ B (OS×Docker)│    │ C (Security) │
   └──────┬───────┘    └──────┬───────┘
          │ DOCKER-USER UI   │ cosign verifier extended to images
          ▼                   ▼
Wave 3 — capstone
   ┌──────────────────────────┐
   │  E (AppStore evolution)   │
   │  needs C (signing) +      │
   │  D (git-as-catalog)       │
   └──────────────────────────┘
```

Why F + D start first:

- **F** is the highest-leverage foundation. Container metrics history,
  Docker events listener, and healthcheck parsing are read by B and D
  later; building them once means B and D become smaller specs.
- **D** is independent of F at the API level (it's all compose YAML +
  git work) but its healthcheck composer benefits from F's healthcheck
  display, so they should land roughly together. Either order works.
- **B and C** share no infrastructure with each other but both want F to
  be in place — B for "show this container's recent events alongside
  its firewall rules," C for "warn on a CVE-tagged image that this
  container is currently running."
- **E** is the largest theme and the only one where pre-F/D capabilities
  would force significant rework. Last.

Estimated calendar: 4–5 months of focused work for all five. Each theme
is its own 2–4 week spec → plan → implement cycle.

---

## Cross-cutting decisions

These need to be agreed up front because every theme touches them.

### CC-1. Cluster awareness defaults

Every new Docker feature lands as a `?node=`-aware route by default
(matches the existing pattern). No new feature gets a cluster-mode escape
hatch unless explicitly justified. This means:

- New tables added under F (container metrics history, events log) live
  in the per-node SQLite — they are NOT replicated through Raft.
- New endpoints emit a `node_id` field in their responses so cross-host
  aggregation (a future roadmap) can ingest them later without schema
  changes.

### CC-2. Frontend page layout

Today's Docker pages (`web/src/pages/docker/Docker{Containers,Images,Networks,Volumes,Stacks}.tsx`)
are flat. Adding 5 themes risks page sprawl. Decision:

- **Containers / Images / Networks / Volumes / Stacks** keep their
  top-level slot. New views become tabs WITHIN those pages.
- F adds a **History tab** to the container detail drawer (metrics +
  events timeline) and a **Health column** to the stack list.
- D adds a **Git tab** to the stack detail drawer and a **Diff
  modal** triggered from the YAML editor.
- B adds a new top-level **포트 맵 (Port Map)** page (not under Docker
  — it crosses Docker / UFW / processes).
- C adds a new top-level **보안 (Security)** page (image inventory with
  signature + CVE status).
- E remains under the existing **App Store** top-level.

Sidebar grows from ~12 items to ~14 — acceptable.

### CC-3. Database schema additions

Each theme will add at most 2–3 tables. Cumulative additions:

| Theme | New tables |
|---|---|
| F | `container_metrics_history`, `container_events`, `container_health_state` |
| D | `compose_git_links`, `compose_env_overrides` (env-variants) |
| B | `container_egress_rules` (declarative; reconciled to DOCKER-USER on apply) |
| C | `image_signatures` (verification cache), `image_cve_scans` (cache) |
| E | `appstore_catalogs` (multi-source list), `appstore_dependencies` |

All new tables use the new `migrations.go` `{ID, Up}` pattern with
schema_version tracking we landed last week. Per-theme spec will lock
exact column names; the names above are placeholders.

### CC-4. Data retention defaults

Keeping with the existing pattern (audit / metrics / alert pruners):

- Container metrics history: 24h rolling window (1m granularity), pruned
  hourly, same shape as host metrics_history.
- Container events: 30d cap or 100k row cap, whichever first.
- CVE scans: 7d cache (CVE feeds update slowly; refresh is cheap).
- Image signatures: keep until the image is removed.

### CC-5. Back-compat with existing Docker code

No theme rewrites the existing `internal/docker/` SDK wrapper or the
`internal/feature/{docker,compose,appstore}` handlers. Each theme adds
*alongside*, then opportunistically migrates existing pages to consume
the new data. Goal: any single theme can ship without breaking the
others or the current code.

### CC-6. Cluster mode + non-cluster mode parity

Every feature must work on a single-node panel that's never enabled
cluster mode. Cluster-mode-only features are flagged in their per-theme
spec with a clear fallback for non-cluster operators.

---

## F. 관측성 (Observability)

**Why this differentiates.** Every panel shows current container CPU /
memory. Almost none show it over time. Portainer has a Pro Edition
feature for it; Dockge has none. SFPanel already has a host-level
`metrics_history` table and `alert_rules` infrastructure — extending
both to Docker is mostly schema + a collector goroutine.

**User-visible features (what we're building):**

1. **Container metrics history (24h)** — sparkline in the container
   list row, full chart in the detail drawer. Pulls from a new
   `container_metrics_history` table populated by a 60s-interval
   collector goroutine that reuses the existing `monitor` package
   shape.
2. **Restart timeline** — events log per container: start, stop, OOM,
   crash, restart-with-exit-code. Surfaced as a vertical timeline in
   the detail drawer.
3. **Healthcheck status** — parse `docker compose ps --format json` and
   `docker inspect` health state; show as a column in the stack list
   ("3 healthy / 1 unhealthy / 2 starting"). Tooltip with last 3
   healthcheck output lines.
4. **Docker events → existing alert manager** — new alert rule types:
   `container_restart_loop` (≥N restarts in T minutes),
   `container_oom`, `container_unhealthy`, `image_pull_failed`. Plug
   into existing `alert_channels` infrastructure (Slack/email/etc).
5. **Log-based alerts (stretch)** — regex match on container logs
   triggers alert. Defer to a follow-up if it bloats the spec.

**New backend pieces:**

- `internal/monitor/docker_history.go` — collector
- `internal/monitor/docker_events.go` — `docker events --format json`
  long-running listener with reconnect
- `internal/feature/docker/health_handler.go` — healthcheck endpoint
- New alert rule types in existing `internal/feature/alert/`

**Cluster considerations.** Per-node only. Each node collects its own
container metrics + events. The future "aggregated view" roadmap will
read these tables across nodes.

**Risks.** The Docker events stream is high-volume on busy hosts; need
to filter at the source (only events for containers we manage, not the
full `docker events` firehose). Metrics history at 1m × 24h × N
containers is bounded but could surprise on 100+ container hosts.

**Scope estimate.** 2–3 weeks. Heaviest Wave 1 theme.

---

## D. Compose UX

**Why this differentiates.** Dockge wins on compose-only simplicity but
has zero diff/git workflow. Portainer has stack-from-git but the UX is
clunky and entangled with their licensing tiers.

**User-visible features:**

1. **Diff before apply** — when editing a stack's `docker-compose.yml`
   in Monaco, an "변경사항 미리보기" (Preview Changes) button opens a
   side-by-side diff between deployed vs. proposed YAML. Highlights
   image-tag changes, port changes, volume mount changes prominently.
2. **Git-backed stacks** — link a stack to a git repo + branch + path.
   Webhook endpoint (`POST /api/v1/compose/:project/git/webhook`)
   pulls + redeploys on push. Polling fallback (every N minutes) for
   non-webhook setups.
3. **Healthcheck composer** — UI form (Monaco-overlay) to generate a
   `healthcheck:` block from interval/timeout/retries/start_period/
   command inputs. Inserts into the compose YAML at the right service.
4. **Environment variants (dev/staging/prod)** — operator defines
   override files (`docker-compose.dev.yml`, etc.) and the UI shows
   a variant selector. Applies via `docker compose -f base.yml -f
   override.yml up -d`.

**New backend pieces:**

- `internal/feature/compose/diff.go` — YAML diff renderer (server-side
  to keep the frontend smaller; ships rendered HTML or structured diff
  to UI).
- `internal/feature/compose/git.go` — `git clone`/`pull` orchestration
  via `Cmd.Run`, webhook signature verification.
- New tables `compose_git_links`, `compose_env_overrides` (CC-3).

**Cluster considerations.** Git-linked stack is per-node by default.
Future enhancement (out of scope): "deploy this git-linked stack to
all nodes."

**Risks.** Git auth — operators may want to link private repos.
Decision: support `https://` + token via per-link credential field
(stored encrypted), and `ssh://` via system SSH agent. Don't try to
manage SSH keys ourselves.

**Scope estimate.** 2–3 weeks.

---

## B. OS × Docker integrated views

**Why this differentiates.** SFPanel already has firewall (UFW /
DOCKER-USER) parsing, port-listing (`ss`), and disk parsers. No other
Docker panel has the OS modules to combine these. The integrated view
emerges almost for free from existing parsers.

**User-visible features:**

1. **Unified Port Map** — one page that shows every listening port on
   the host with its source: UFW rule / Docker DNAT / bare process.
   Clickable rows → "open firewall," "go to container," "go to
   process."
2. **Per-container egress firewall UI** — declarative rules ("this
   container can only reach 192.168.1.0/24 + the public net via
   port 443") rendered into DOCKER-USER iptables rules. Persistence
   in `container_egress_rules` table; reconciled at panel start and
   on rule change.
3. **Volume × disk integration** — the existing volume list shows real
   disk usage (`du -sb /var/lib/docker/volumes/<name>/_data`) and
   contributes to the disk page's pie chart. Volume rows link into
   the file browser at `/var/lib/docker/volumes/...`.
4. **systemd × Docker side-by-side dashboard (stretch)** — show
   sfpanel-managed systemd services and Docker containers in one
   "what's running on this host" view.

**New backend pieces:**

- `internal/feature/portmap/handler.go` — combines firewall, ss, docker
  data into a single response shape.
- Extends `internal/feature/firewall/firewall_docker.go` with rule
  templates for per-container egress rules.
- Extends `internal/feature/disk/disk_filesystems.go` to surface volume
  usage.

**Cluster considerations.** Per-node. Port map is inherently host-scoped.

**Risks.** Per-container egress firewall has subtle ordering issues
with Docker's own DOCKER-USER chain. Need careful design + tests
against real iptables state.

**Scope estimate.** 2–3 weeks (full); 1 week for just the unified port
map view alone.

---

## C. 보안 / 공급망

**Why this differentiates.** SFPanel just shipped cosign keyless
verification for its own update flow. The same verifier — extended for
container images — is a unique posture. Portainer has signature
verification only as a Pro tier feature.

**User-visible features:**

1. **Image signature verification** — extend `internal/release/cosign.go`
   to verify cosign signatures on registry images. Policy modes:
   off (default for back-compat) / warn / require. Per-registry
   policy override. Run on `docker pull` proxy + on stack `up -d`
   pre-flight.
2. **CVE scanning** — invoke trivy or grype on pulled images, cache
   results in `image_cve_scans` table (7d TTL). Surfaced as a column
   in the image list and a section in the container drawer ("This
   image: 2 CRITICAL, 5 HIGH").
3. **Digest pinning mode** — operator-toggle that rewrites every stack's
   `image: foo:tag` to `image: foo:tag@sha256:...` on next `up -d`,
   pinning the deployed digest.
4. **Cosign-signed AppStore catalog (E의 전제)** — already covered in
   E but the verifier work lives here.

**New backend pieces:**

- `internal/release/cosign_image.go` — extends existing verifier.
- `internal/feature/security/scan.go` — trivy/grype shell-out wrapper
  via `Cmd.Run`.
- Extends `internal/docker/client.go` `Pull` with verification gate.

**Cluster considerations.** Verification policy is replicated through
Raft FSM (so all nodes enforce the same policy); CVE scan results stay
per-node (each node scans its own images).

**Risks.** Trivy/grype binary distribution — these aren't bundled into
SFPanel. We either install on first scan (apt + retry) or document the
prerequisite. Decision: prefer auto-install with a clear UX.

**Scope estimate.** 3–4 weeks.

---

## E. AppStore 진화

**Why this differentiates.** Current AppStore is hardcoded to the
upstream `svrforum/SFPanel-appstore` GitHub repo. Operators can't add
their own catalogs. Other panels (Yacht, Easypanel) have closed or
single-source catalogs too.

**User-visible features:**

1. **Multi-catalog support** — operator-add catalog URLs (git or HTTP)
   via the AppStore settings page. Display catalog source per app in
   the list.
2. **Cosign signature verification for catalogs** — every catalog must
   ship a `catalog.sig` + `catalog.pem` (matches our v0.11.3 release
   pipeline). Use the verifier from C.
3. **Per-app dependency graph** — metadata field declares dependencies
   (`requires: [postgres-15]`); installer checks and offers to install
   prerequisites first.
4. **Cluster-aware install** — the install dialog shows all online
   nodes with current resource use; default-picks the best fit but
   the operator can override. Reuses node-list from F's metrics.
5. **Per-app backup hooks** — metadata declares
   `backup_command: docker exec ... pg_dump`. SFPanel cron module
   schedules them; backups land in a configurable target dir.

**New backend pieces:**

- `internal/feature/appstore/catalog.go` — multi-source resolver.
- `internal/feature/appstore/depgraph.go` — DAG resolution.
- Extends existing `appstore_installed_<id>` settings model with
  catalog source + version delta tracking.
- New tables `appstore_catalogs`, `appstore_dependencies`.

**Cluster considerations.** Catalog list replicates through Raft (one
operator policy fleet-wide). Installs target a specific node.

**Risks.** Dependency graph cycles + version compatibility — easy to
over-engineer. Start with a flat 1-level dependency model; expand
only if needed.

**Scope estimate.** 3–4 weeks.

---

## Sequencing summary

```
Month 1-2:  F  (관측성)         + D  (Compose UX)
Month 3-4:  B  (OS×Docker)      + C  (보안)
Month 5:    E  (AppStore 진화)
```

Each transition lands at a stable point: any wave can ship to production
without the next wave existing. Operators get value immediately at the
end of Wave 1.

---

## Out of scope for this roadmap

- Multi-host **aggregated views** (cross-cluster container search,
  fleet dashboard). Needs F first; revisit as a separate roadmap.
- **Image fan-out distribution** (leader pre-pulls images on followers).
  Belongs to a future cluster-Docker roadmap.
- **Swarm / Kubernetes**. Not happening on the SFPanel core line.
- **RBAC / multi-user**. Separate roadmap entirely.
- **Container build pipelines** (Dockerfile-from-git). Coolify's territory.

---

## Open questions to settle before each wave starts

Wave 1 (F + D):
- **Q-F1.** Container metrics retention — 24h is the default; should it
  be operator-configurable per-node in v1?
- **Q-F2.** Docker events stream filtering — do we filter to
  sfpanel-managed containers only, or store everything?
- **Q-D1.** Git auth — `https://` token via DB-encrypted credential vs.
  system SSH agent? Both? One first?
- **Q-D2.** Compose env-variant scope — file-based overrides only, or
  also support a UI-driven KV store that compiles to overrides?

Wave 2 / 3 questions deferred until Wave 1 lands.

---

## Next step

Review this roadmap. If approved, the next action is brainstorming
**theme F (관측성)** in detail and producing `docs/superpowers/specs/
YYYY-MM-DD-docker-observability-design.md`. After F's spec is written +
implemented + shipped, the same cycle for D, then B+C in parallel,
then E.
