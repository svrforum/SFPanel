# Cluster operations

Operational procedures for the optional Raft + mTLS cluster mode. Background
on the architecture lives in `tech-features.md` and `websocket-spec.md`; this
file is the runbook for things operators do **after** the cluster is healthy.

---

## Restart-storm avoidance

**Symptom:** Web UI of every cluster node returns 502/503 simultaneously after
a deploy fan-out (Ansible, k0s, etc.) restarts every node together.

**Why it happens:** Each node's Raft has to finish BoltDB log replay + the
peer mTLS handshake before pre-vote can succeed. While the first peer is
still bootstrapping, the other's `requestVote` RPC hits an empty listener and
returns EOF. Re-election typically takes 15–20 s. `HeartbeatTimeout` and
`ElectionTimeout` are already tight (`raft.go:40-41`), so making them smaller
just produces more flapping.

**Mitigation:** Roll voters one at a time with ≥ 10 s between each. The web
UI's cluster update flow rolls the managers this way in either mode: one at
a time, each answering on the new release before the next starts (see
§ Update orchestration). If you fan out manually, wait for
`sfpanel cluster status` to report a leader before continuing.

`mode=simultaneous` changes only how the agents go — all of them in one
wave. Agents are not Raft voters, so no mode takes the voters down together.

---

## Join token persistence

Tokens are persisted to `{Cluster.DataDir}/tokens.json` (mode 0600), so a
leader restart does not invalidate pending invites — as long as the same node
is leader again. Tokens are not replicated: they live only on the node that
created them, and after leadership moves to another node the new leader
answers "token does not exist" for them. Issue new tokens on the current
leader with `sfpanel cluster token`. The file holds the HMAC secret plus the
token map; protect it like any other cluster credential. If the file is
corrupted or deleted, all pending invites become invalid.

---

## Joining a manager

A manager joins as a Raft non-voter and becomes a voter once its Raft has
caught up: the leader promotes it on a heartbeat (every 30 seconds) whose
Raft applied index is within 64 entries of the leader's own. A joiner whose
Raft port — the gRPC port + 1, 3630 by default — the leader cannot reach
stays a non-voter: as a voter the leader cannot reach, it could cost the
cluster its quorum. The leader's log says why. Raft's own errors name the
joiner's Raft address (`failed to heartbeat to`, `failed to appendEntries
to`), and a joiner that heartbeats before its Raft has caught up is logged
as `joining manager not promoted yet: its Raft has not caught up — is the
leader able to reach its Raft port (the gRPC port + 1)?`. Open the port,
and once its Raft has caught up the next heartbeat promotes it. A manager on
a release that does not report its applied index is promoted at its first
heartbeat.

---

## TLS material

| Material | Lifetime | Rotation | Notes |
|---|---|---|---|
| Cluster CA | 10 years (`InitCA`) | Manual, coordinated restart | No in-place rotation — every node must trust the new CA simultaneously |
| Node cert | 5 years (`IssueNodeCert`) | `sudo sfpanel cluster reissue-cert` | Hot-reload within ≤ 60 s; debounced re-stat in `getNodeCert` |

Both `node.crt` and `node.key` live in `{Cluster.CertDir}` and are written
0600. The cert is technically public material in mTLS, but locking it down
matches the rest of the trust material on the host (config, DB, key).

A node certificate carries its role in the subject's OU: `sfpanel-manager`
or `sfpanel-agent`. The Raft port refuses an agent's certificate, and of the
cluster gRPC methods that need a certificate an agent may call only
AgentHeartbeat and ReportPanelCA, each for itself; a manager heartbeats on
Heartbeat, which refuses an agent. A manager whose certificate
predates roles reissues it with the manager role by itself, on its first
leader-watch tick after the upgrade, keeping its node ID and addresses and
signing with the CA key it holds through replicated state; `reissue-cert`
issues with the manager role too.

### CA rotation playbook (no automation today)

There is no `sfpanel cluster rotate-ca` command. The 10-year horizon is far
enough out that a manual playbook is acceptable, but rotate **before**
expiry — once the CA is past `NotAfter`, every gRPC handshake fails and the
cluster cannot self-recover.

1. **Plan a maintenance window.** Treat CA rotation as a coordinated cluster
   restart. All voters must be online and reachable from the operator.
2. **Run `sfpanel cluster reissue-cert` on every node first.** Cycling node
   certs ahead of CA rotation puts every node on a fresh cert that you can
   re-sign with the new CA in step 4.
3. **Generate the new CA locally.** Use the same key params as
   `internal/cluster/tls.go` (P-256 ECDSA, NotAfter = now + 10 y).
4. **For each node:** stop sfpanel → replace `{CertDir}/ca.crt` (and key on
   the original CA holder) with the new CA → re-issue node certs against the
   new CA, each with its role → start sfpanel. A join command carries the
   CA's fingerprint, so every one printed before the rotation stops working.
5. **Verify** with `sfpanel cluster status` after every voter is back. If any
   node is stuck, copy the new CA cert manually and restart that node.

`cachedCAPool` is loaded once at startup (`loadCAPool`), so step 4
implicitly requires a restart — there is no hot-reload path for the CA.

---

## Update orchestration

`POST /api/v1/cluster/update` runs on the leader. It looks the latest
release up once, then updates the cluster in this order:

1. **Agents, in waves** of `wave_size` (default 1). The agents of a wave
   update together, and the next wave starts once every one of them has
   finished. `mode=simultaneous` puts every agent in one wave.
2. **The other managers, one at a time**, in either mode.
3. **The leader**, last.

Within each of these, nodes go by name. A node already on the release is
not asked, and one the leader's heartbeats show offline is skipped.

- It can be started from any node: the UI sends it to the leader with
  `?node=<leader_id>`, relayed through the node the browser is on. That
  relay lasts up to two hours (most event streams are relayed for five
  minutes), but the node is usually one the update restarts, so the progress
  stream is cut off part way; the orchestration runs on its own goroutine and
  finishes regardless of whether anyone is still watching. Closing the tab
  does not cancel it.
- One cluster update at a time: a second request while one is running gets
  `409 UPDATE_IN_PROGRESS`.
- The leader sends the release it looked up to every node with the request,
  so a run asks GitHub's API once however many nodes it updates: the API
  allows sixty unauthenticated requests an hour per address. A node takes
  that release only if every download in it is this project's release
  download of that tag (`400` otherwise); the checksum, the signature over it
  and the downgrade guard below still decide what is installed. A node still
  on a release that does not read it ignores what is sent and looks the
  latest release up itself. If the leader's lookup fails, nothing is
  updated.
- The orchestrator calls each node's `/api/v1/system/update?force=true`. The
  quorum guard on that route is for operators updating nodes by hand; the
  orchestrator has already made the quorum decision, and letting the guard
  veto it refused the old leader of every two-node cluster.
- A node's update **fails** when:
  - the request fails or answers an HTTP error;
  - its stream reports an error — a download, checksum or signature
    failure. The node answers `200` before it streams, so these are only in
    the stream;
  - it does not answer on the new release within four minutes of its update
    returning, which covers the restart, the rollback watchdog's ninety
    seconds and a boot. The leader asks the node's own `/api/v1/health` over
    the cluster's gRPC, so a node whose heartbeats go elsewhere is judged
    all the same. A node that still answers on its old release was rolled
    back, or never restarted, and the failure says so.
- **A failure stops the run.** A wave in which any agent failed ends it once
  the wave is done, before any manager is touched (`Wave 1 of 2 failed
  (web-01): the update stopped, and no manager was updated`). A manager that
  fails ends it before the next manager and the leader.
- **The leader updates itself only after handing leadership over** to
  another manager voter — one the run has just updated, else one already on
  the release. If no handover works, the run stops and the leader is left as
  it is (`Leadership could not be handed over …: the leader was not
  updated`). With no other manager voter to hand over to, as in a
  one-manager cluster, it updates without a handover. Its Raft keeps running
  through the download: the restart after a successful update stops it as
  any restart does, and a failed update leaves the node in the cluster as it
  was. The run ends once the leader's own update is installed, with
  `leader_restarting: true`; it does not wait for the leader to come back.
- **If leadership moves, the run stops** (`Leadership moved during the
  update: run it again on the new leader to finish`). The leader checks
  before each wave, each manager and its own update. In a two-manager
  cluster the other manager's restart costs the quorum until it is back, and
  the leader can lose its role meanwhile. Run the update again on the new
  leader: the nodes already on the release are not asked again.
- `/api/v1/system/update` itself is serialised by an in-process mutex; a
  second concurrent call returns `409 UPDATE_IN_PROGRESS`.
- The downgrade guard (`internal/release.IsForwardUpdate`) refuses any
  same-or-older target version. Bypass requires editing the binary.
- Archives are streamed to a temp file (`os.MkdirTemp`) and hashed via
  `TeeReader` so memory-constrained nodes (256 MB cluster nodes) can still
  receive a 200 MiB tarball without OOM.
- `checksums.txt` is verified against a Sigstore keyless cosign signature
  before any hash inside it is trusted — v0.56+ verifiers prefer the
  standardized bundle (`checksums.txt.sigstore.json`) when the release
  publishes one, falling back to the legacy pair
  (`checksums.txt.sig` + `checksums.txt.pem`) only when the bundle asset is
  absent. The Fulcio cert's SAN URI must start with
  `https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v`
  and the OIDC issuer extension must be GitHub Actions
  (`https://token.actions.githubusercontent.com`). See
  `internal/release/cosign.go` for the verification path. Releases that
  predate the signing pipeline (no .sig/.pem assets) fall back to plain
  SHA-256 with a notice in the SSE stream.

### Auto-rollback watchdog

Self-update spawns a detached watchdog process from the BACKUP binary
(`/usr/local/bin/sfpanel.bak`) before triggering `systemctl restart`. The
watchdog polls the panel's `/api/v1/system/info` on 127.0.0.1 — over HTTPS
when the panel serves it — for 90 s. If the new binary never responds, the
watchdog `rename(2)`s `.bak` back over `sfpanel` and restarts the service.
See `cmd/sfpanel/watchdog.go`.

This is a forward-only feature: pre-watchdog binaries used as `.bak`
don't know the `watchdog-update` subcommand and exit 2 immediately. Once
a node has updated *to* a watchdog-aware binary, every subsequent update
inherits the rollback safety net.

A node the watchdog rolled back answers on its old release, which a cluster
update counts as a failure and stops on (see above).

---

## Disband and leave

- `cluster leave` removes the calling node from the cluster and exits the
  process (`os.Exit(1)`). systemd's `Restart=always` brings it back up in
  standalone mode.
- A follower asks the leader to take it out. A leader first hands
  leadership to another online manager voter and then asks it the same way,
  so the new leader removes it from Raft and deletes its record.
- When no leader took the node out — none was known or could be reached, or
  it refused — the leave goes on and says so (`no leader took this node out
  of the cluster: the others may still list it …`): remove the node on the
  leader with `sudo sfpanel cluster remove <node-id>`. The CLI prints this
  when the service is stopped; through the running service — the UI, or
  the CLI, which hands the leave to it — the answer's message ends with the
  same warning and this node's ID.
- A leave removes the cluster's data and certificate directories before it
  saves the config as standalone. One it cannot remove ends the leave with
  the config still naming the cluster: remove it by hand and leave again.
- `cluster disband` runs on the leader; a request sent to another manager is
  forwarded to it. Raft knows no agents, so the leader first removes every
  agent — the one thing an agent understands — and waits up to 15 seconds
  for the online ones to hear it. The answer names the agents it could not
  tell (offline ones, and any that did not hear in time); each needs
  `sudo sfpanel agent reset --yes` on its own server. The leader then
  removes Raft state and TLS material cluster-wide via the FSM, and exits
  each manager's process. The local SQLite DB is **not** wiped — audit
  logs, monitor history, cron, and Docker state are local-only and persist
  after the cluster goes away.
- When no leader is left at all, `?force=true` disbands only the node it is
  sent to and can tell no agent: the answer names them all. See
  `cluster-partition-runbook.md` § Forced local disband.

If you want a clean wipe after disband, manually delete `{Cluster.DataDir}`,
`{Cluster.CertDir}`, and the `cluster:` block in `config.yaml`.

---

## Agents

An agent is a server the cluster manages without making it a member of Raft.
It runs the same binary with `cluster.role: agent` and holds no replicated
state: it does not vote, and it has no cluster JWT secret, no admin account,
no login or setup screen and no web UI. It serves the node routes — system,
files, Docker, logs, terminal and the rest — and only to requests one of its
managers signed with that agent's own key. Operators reach it from a
manager's panel by selecting the node (`?node=<agent-id>`); cluster
management (`/cluster/*`) stays on the managers.

What an agent holds is no credential anywhere else: its certificate
(OU `sfpanel-agent`) gets it nothing on a manager's gRPC port but its own
heartbeat and panel-CA report, its key is accepted by no other node, and the
managers' internal proxy secret never reaches it.

### Joining

- **Upgrade every manager first.** A manager from before agents takes an
  agent for one of its own: it forwards the operator's session token to it,
  signs with the managers' secret, and makes it a Raft voter. The leader
  therefore registers an agent only when every other manager is online and
  reports, on its heartbeat, that its release knows agents; until then the
  join says whom it waits for (`agents can register once every manager is
  online … waiting for peer-2 (0.79.0)`).
- **Once agents exist, never add or roll back a manager to a release from
  before agents.** A join from such a release is refused (`this cluster has
  agents, and this server's SFPanel predates them …`), but a binary swapped by
  hand cannot be stopped. Agents never send such a manager a heartbeat — theirs
  is a gRPC method it does not have, so it cannot make them voters — yet an
  operator who reaches an agent through it hands the agent a session token
  valid on every manager.
- The leader refuses registrations until five minutes after it recorded the
  switch of the internal proxy secret to the replicated one (`the cluster is
  still switching its internal proxy secret …`): an agent receives the CA
  certificate the old secret was computed from. The refusal does not spend
  the token.

On a manager, `sudo sfpanel cluster token --agent` makes an agent token and
prints the commands that use it, the leader's address and the CA
fingerprint filled in:

```bash
# On a server that already runs SFPanel
sudo sfpanel agent join <leader-ip>:3629 <token> --ca <fingerprint>

# On a new server — install and join in one step
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | sudo bash -s -- --agent <leader-ip>:3629 <token> --ca <fingerprint>
```

- `--ca` pins the cluster CA: the server verifies the leader before the
  token travels. Pointed at a follower, the join follows it to the leader.
- `--advertise IP` is the address the managers reach the agent at; without
  it, `cluster.advertise_address`, or one detected from the route to the
  manager. It must be an address of this server — the agent's certificate
  names it and its listeners bind it — so behind NAT the join fails instead
  of registering an address the agent could not serve.
- `--name NAME` registers the agent under that name instead of the server's
  host name: 1–63 letters, digits, dots, hyphens or underscores, starting
  with a letter or digit. A cluster holds each node name and each address
  once, so servers cloned from one image need a name each. A host name that
  is not a valid name stops the join before anything is sent, and the error
  says to pass `--name`.
- An agent token admits an agent only and a manager token a manager only,
  and the refusal leaves the token usable for its own kind. Like every join
  token it is single-use and lives on the leader that made it: after a
  leader change, make a new one.
- The leader registers at most `cluster.max_agents` agents (its own config,
  default 100).
- The join turns on the panel's HTTPS if it was off, writes the cluster CA
  certificate, the agent's certificate and key, and its internal proxy key
  (`agent.key`, 0600) under `cluster.cert_dir`, and restarts a running
  sfpanel, which comes back as an agent.
- Before the token travels, the join checks that the panel's certificate
  verifies against the panel's CA for the advertise IP, where the managers
  reach the panel, and for 127.0.0.1, where the agent's own gRPC server
  hands their requests on. The managed certificate names the addresses on
  the server's stable interfaces (not Docker's `br-*` and `veth*`); an
  operator certificate has to name both, or the join stops and says so.
- A server that still holds a cluster manager's files is refused: `ca.key`
  in `cluster.cert_dir`, or `raft-log.db`, `raft-stable.db`, `snapshots`,
  `tokens.json`, `proxy-secret.json` or `peers.json` in `cluster.data_dir`.
  They hold the cluster's keys, which an agent joined over them would keep.
  Remove what the refusal lists, then join again.

### Ports

| From → to | TCP port | Carries |
|---|---|---|
| Managers → agent | 3629 (`cluster.grpc_port`) | Requests, over gRPC with mTLS |
| Managers → agent | 3628 (`server.port`) | Event streams, file transfers, terminals and other WebSockets, stack migration pushes — panel HTTPS |
| Agent → managers | 3629 | Registration, heartbeats and panel-CA reports, to the leader; the other managers are tried when it cannot be reached |

An agent needs no Raft port (3630), on either side.

An agent's panel listens on its advertise address and on 127.0.0.1 only;
`server.host` is not used. It answers loopback, its managers' addresses and
`cluster.agent_allowed_sources` (IPs or CIDRs, for managers that reach
agents from an address other than the one they advertise), judged by the
connection's address, never `X-Forwarded-For`; anything else gets 403.
Loopback is what this server's own tools use — the update watchdog, the gRPC
server's hop to the panel, the installer and `sfpanel agent status`. Either
way its API takes only requests signed with the agent's key; `/api/v1/health`
is the one route open without one. The agent's gRPC server, also on the
advertise address, serves only ProxyRequest, and only to manager
certificates from its manager list.

Because its listeners bind the advertise address, an agent's boot waits up
to 60 seconds for that address to come up — a Tailscale or WireGuard
interface, or a DHCP lease, can arrive after sfpanel starts. Past that the
boot fails (`the advertise address … is not on this server after 1m0s`) and
systemd starts it again.

### Checking an agent

On the agent, `sudo sfpanel agent status` shows its cluster, node ID and
advertise address, the managers and the leader it last heard from, whether
the leader removed it, its certificate's expiry, whether its key is present
(never the key itself) and whether its panel answers on loopback. On a
manager, the node list shows it with `kind: agent` and no Raft role; it is
online while its heartbeats arrive.

The agent heartbeats to the leader every 30 seconds and keeps the leader's
address and the manager list in `{Cluster.DataDir}/agent-state.json`. Any
manager's reply can say where the leader is, but only the leader's sets the
manager list — the managers the agent takes requests from — because a
follower's may lag behind a removal. A manager that knows no leader (during
an election, cut off from the others, or a leader updating itself) is left
at once, and the next one is tried 30 seconds later. Only the leader can
tell it that it was removed: a follower that has not caught up answers that
the agent is not a member yet, and the agent keeps trying. `sfpanel cluster`
commands refuse to run on an agent, and its certificate lasts five years
like a manager's.

Keep the agent's clock in step with its managers' (NTP on both). A manager
signs each relay to the agent's panel — event streams, file transfers,
terminals and other WebSockets — with its own time, and the agent refuses a
signature more than thirty seconds off its clock: the manager then answers
`502 CLUSTER_AGENT_AUTH_FAILED` and names the clock as a likely cause.
Requests over gRPC are not affected, since the agent signs that hop to its
own panel itself. The manager an agent heartbeats to — the leader, unless it
cannot be reached — logs a warning once a ping's time is twenty seconds off
(`a node's clock is far from this one's …`), at most every ten minutes per
node; the leader watches the managers' pings the same way.

### Removing an agent

1. On a manager, remove the node: `sudo sfpanel cluster remove <node-id>` or
   `DELETE /api/v1/cluster/nodes/<node-id>`. The leader deletes the agent's
   key first, so no manager can sign for it any more, then its record and
   its panel CA. Raft's membership does not change.
2. The leader tells the agent it was removed within a few seconds while its
   heartbeat stream is open, or at its next heartbeat if it is offline at the
   time. The agent records that, closes its panel's cluster-side listener
   along with the connections open on it (a terminal included), and stops
   its gRPC server.
   Loopback keeps answering, so `sfpanel agent status` and
   `/api/v1/health` still work, and after a restart it opens loopback only.
3. On the agent, `sudo sfpanel agent reset --yes` makes it a standalone panel
   again: the config stops naming it an agent (role, node ID and managers
   cleared) and gets a new JWT key, the certificates, the key and the link's
   state are deleted, and a running service restarts. Without `--yes` it
   only lists what it would do. The installer's `uninstall` runs the reset
   itself.

An agent cannot take itself out of the cluster: a reset on its own leaves its
record on the managers, and the command says how to remove it.

### Backups and agents

Restoring a backup on an agent (`POST /api/v1/system/restore?node=<agent-id>`)
takes the archive's settings but keeps the agent's own cluster block, JWT
key, panel port and panel TLS: the managers know it by that port and that
panel's CA. Two archives are refused before anything is touched:

- a cluster manager's backup on an agent — it holds the cluster's admin
  account and JWT key, which an agent must not hold;
- an agent's backup on a manager or a standalone panel — it would start that
  server as the agent, which needs keys only the agent has.

The upload itself reaches the agent before it is refused, so select the
node with care: a manager's backup carries the cluster's JWT key.

### Advanced app installs on an agent

An advanced app install (your own compose file) asks for the operator's
password again. An agent has no accounts, so the manager the request goes
through checks it — against the cluster's account, as a login does — and the
password never reaches the agent: the manager takes it out of the body and
sends instead its word that the check passed (`X-SFPanel-Step-Up`), made
with that agent's key and bound to that one request and its body. A wrong
password is refused by the manager (`403 INVALID_PASSWORD`), as is a missing
one (`400`); five failed tries within a minute shut that address out for
five minutes (`429`), logins included. None of these reaches the agent. The
agent refuses an advanced install that carries no word from a manager, so
one sent through a manager on a release that does not send it fails until
that manager is updated.

### Why an agent's panel serves HTTPS

Managers relay event streams, file transfers and WebSockets to the agent's
panel directly, with a header signed with that agent's key. The signature
covers the method, the path with its query and the time, not the body: over
plain HTTP anyone on the path could read the files and keystrokes, or put a
body of their own under a valid signature. So the join turns HTTPS on when
it is off — the managed certificate from a local CA that a fresh install
uses — and registers the panel's CA; the managers check the agent's panel
against it, and a relay that could only go in clear is not sent
(`CLUSTER_AGENT_PANEL_INSECURE`). With an operator certificate
(`server.tls.cert_file` and `key_file`), `server.tls.ca_file` has to name its
CA.

---

## See also

- `docs/specs/api-spec.md` — `/cluster/*` REST routes
- `docs/specs/websocket-spec.md` — WS relay through cluster proxy
- `docs/specs/cluster-partition-runbook.md` — partition detection and recovery
