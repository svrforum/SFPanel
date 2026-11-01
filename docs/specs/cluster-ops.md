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

A conversion or a promotion (below) hands its node a token bound to that
node: it admits that node ID only, with the node's panel and gRPC addresses
on the IP the cluster has for it, and lasts 30 minutes. Whoever else holds it
can join neither as another node nor from another address — the leader
answers `this join token was not made for this node at this address` — and
the refusal does not spend it. An unbound token brings no node back under an
ID the cluster knows: an agent registering with one cannot ask for an ID,
and a manager joining with one under an ID in use is refused. Bound tokens
are listed and revoked like any other, and live only on the leader that made
them. The binding is two fields of the token in `tokens.json`; a leader from
before them drops both when it loads the file, and the token then admits any
node of its kind.

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
leader able to reach its Raft port (the gRPC port + 1)?`. A joiner whose
Raft port was closed from the start never learns from Raft which manager
leads, so it sends no heartbeat at all: the node list shows it offline, and
only Raft's errors name it. Open the port, and once its Raft has caught up
the next heartbeat promotes it. A manager on
a release that does not report its applied index is promoted at its first
heartbeat.

---

## TLS material

| Material | Lifetime | Rotation | Notes |
|---|---|---|---|
| Cluster CA | 10 years (`InitCA`; a rotated CA from its rotation) | `sudo sfpanel cluster rotate ca` | No restart: every node trusts both CAs before any certificate moves (§ Rotating the cluster's secrets) |
| Node cert | 5 years (`IssueNodeCert`) | `sudo sfpanel cluster reissue-cert`; a CA rotation reissues every node certificate | Hot-reload within ≤ 60 s; debounced re-stat in `getNodeCert` |

Both `node.crt` and `node.key` live in `{Cluster.CertDir}` and are written
0600. The cert is technically public material in mTLS, but locking it down
matches the rest of the trust material on the host (config, DB, key).

A node certificate carries its role in the subject's OU: `sfpanel-manager`
or `sfpanel-agent`. The Raft port refuses an agent's certificate, and of the
cluster gRPC methods that need a certificate an agent may call only
AgentHeartbeat, ReportPanelCA, RenewCertificate and RotateAgentKey, each for
itself — the last two answer only while a rotation asks for them; a manager
heartbeats on Heartbeat, which refuses an agent. A manager whose certificate
predates roles reissues it with the manager role by itself, on its first
leader-watch tick after the upgrade, keeping its node ID and addresses and
signing with the CA key it holds through replicated state; `reissue-cert`
issues with the manager role too. A manager's certificate names every
address the cluster records for it, a Tailscale address among them, and is
reissued with the same key when one is added (§ Managers on more than one
network).

A CA past its `NotAfter` fails every gRPC handshake, and the cluster cannot
recover from that by itself: rotate it well before (§ Rotating the cluster's
secrets).

---

## Rotating the cluster's secrets

Four secrets can be replaced on a live cluster, without restarting a node,
each with one command on a manager whose service is running:
`sudo sfpanel cluster rotate jwt`, `rotate proxy`, `rotate agent-keys` and
`rotate ca`. The command asks the panel, which hands the request to the
leader. Before it writes anything the leader checks that every node the
rotation changes can follow it; a refusal is `409 CLUSTER_ROTATION_REFUSED`,
nothing having changed, and names whom it waits for and why
(`… waiting for peer-2 (offline), web-03 (0.79.0)`). A manager that stops
is offline to a rotation at once, as its heartbeat stream to the leader ends,
while the node list keeps it online until its last ping is three minutes old.
Converting or promoting a node, a leave from the leader and a panel update
judge the voters the same way (§ Disband and leave says how a follower's
leave does).
`sudo sfpanel cluster rotate status` (`GET /api/v1/cluster/rotation`) shows
where each node stands in each of them, by generation and fingerprint — never
a secret.

Rotate after converting a manager to an agent or taking one out of the
cluster, after a backup has left the cluster's servers, and whenever a secret
may have leaked. A manager holds all four — in memory, on disk and in its
copy of the Raft log — and keeps them when it goes. § In what order says how
to go about it.

### The JWT key

`sudo sfpanel cluster rotate jwt` (`POST /api/v1/cluster/rotate/jwt`) gives
the cluster a new JWT key. Every manager, the leader too, applies it as soon
as its replicated state holds it, and ends every session it holds: the
refresh tokens are deleted before the key changes, and the WebSocket tickets
go after it. Every access token, refresh token and WebSocket ticket issued
before the rotation is then refused on every manager, the operator's own
session included: the answer is the last one it gets, and everyone signs in
again.

- **What it does not end:** terminals, log streams and other connections
  that were open already. They were authenticated when they opened, and
  nothing checks them again. If someone may be in one, restart the managers
  one at a time after the rotation.
- **Gate:** every other manager online, on a release that follows a
  rotation. One that does not would go on taking the retired key.
- **`SFPANEL_JWT_SECRET`:** a manager whose environment sets it runs that
  key at every start instead of the cluster's, so a rotation is undone on it
  at its next restart: it takes the retired key again, and the others refuse
  the tokens it issues. It warns at boot. Unset the variable on cluster
  managers.
- **A manager restored from a backup** restarts with the key, and the
  generation, it had when the backup was taken. By its first leader-watch
  tick (15 seconds) it finds the cluster's key newer and applies it, which
  ends the sessions the restore brought back.
- An agent holds no JWT key, and a JWT rotation changes nothing on it.

### The managers' internal proxy secret

`sudo sfpanel cluster rotate proxy` (`POST /api/v1/cluster/rotate/proxy`,
`202`) replaces the secret the managers sign their relays to each other with.
Rotate it whenever the JWT key is: a header signed with it passes on any
manager's panel without a JWT, as any user. Nobody is signed out. It goes in
three steps, each on the leader's 15-second tick:

1. **next** — every manager accepts the new secret beside the one it signs
   with. The leader waits until every manager is online and says, on its
   heartbeat, that it accepts the new secret. This step has no time limit:
   nobody signs with the new secret yet, so waiting opens nothing.
2. **switched** — every manager signs with the new secret, and accepts the
   old one for two more minutes by its own clock, for a peer whose replicated
   state lags. The leader waits five minutes.
3. **done** — the leader takes the old secret out of replicated state.

No session, terminal or stream is cut at any step.

- **Gate:** every other manager online, on a release that follows a
  rotation, and five minutes past the switch of the internal proxy secret to
  the replicated one, which the leader records by itself once every manager
  accepts the replicated secret.
- **A rotation held in next** waits for a manager that is offline, not heard
  from or on an older release; `rotate status` names it (`waiting`). Wait
  for it, or remove it. Started again in next, a rotation replaces only the
  next secret, which nobody has signed with. A start during switched is
  refused (`… is still accepted until <time>`): start again after that time.
- **A manager rolled back to an older release** follows
  `cluster_proxy_secret`, the plain copy of the signing secret, alone. It
  never says it accepts a next secret, so a rotation's switch waits for it
  until it is updated again or removed.
- No node goes back to an older secret: each keeps the last step it applied
  in `proxy-secret.json`, and after a restart goes by the newer of that and
  its replicated state.

### The agents' keys

`sudo sfpanel cluster rotate agent-keys`
(`POST /api/v1/cluster/rotate/agent-keys`, `202`) gives every agent a new
key, the one the managers sign their requests to it with, over the agent's
own mTLS connection to the leader (RotateAgentKey):

1. At the agent's next heartbeat the leader tells it a rotation is due. The
   agent fetches its new key with a proof made with the key it holds, writes
   it to `<cert_dir>/agent-key-rotation.json` and accepts both keys.
2. It confirms with a proof made with the new key. Only then does the leader
   make the new key the one the managers sign with, and the old one leaves
   replicated state.
3. The agent's `agent.key` becomes the new key, and it accepts the old one
   for two more minutes, for a manager whose replicated state lags.

Until the confirm the managers sign with the old key, which the agent still
accepts: a rotation cut off half-way breaks no relay, terminal or stream.

- **An offline agent does not hold the rotation up.** It goes on with its
  old key, and moves at its first heartbeat once it is back; `rotate status`
  lists it by name as `pending` meanwhile. Remove an agent that will not come
  back (`sudo sfpanel cluster remove <node-id>`).
- **Gate:** every other manager online, on a release that follows a
  rotation, and no online agent on a release that does not — the refusal
  names it and its version. Update it first.
- **Started again,** a rotation replaces the last one: an agent that has not
  confirmed takes the new rotation's key. One whose confirm the leader took
  but whose answer it never got moves to the key it confirmed first, then to
  the new one.
- `agent.key` is always the key the managers sign with, as far as the agent
  knows; the key on its way is in `agent-key-rotation.json`. An agent rolled
  back to an older release reads `agent.key` alone and keeps working, on its
  old key: it never confirms, so the managers stay on that key too.
- **An agent that lost its disk after confirming** — restored from an image
  taken before, say — holds a key the managers no longer sign with, and the
  leader tells it so (`the managers sign for this agent with a key it does
  not hold …`). Remove the node on a manager, run
  `sudo sfpanel agent reset --yes` on it, and join it again.

### The cluster CA

`sudo sfpanel cluster rotate ca [--phase-timeout 30m]`
(`POST /api/v1/cluster/rotate/ca`, `202`) replaces the cluster CA. The
leader makes the next CA, which lasts ten years, and moves every node to it a
step at a time. Each step waits for every node, on evidence the leader sees
itself:

1. **trust** — every node trusts both CAs. The leader waits until every node
   says, on its heartbeat, that it trusts both.
2. **reissue** — the next CA signs. Every manager reissues its own
   certificate with it, keeping its key and names, and every agent asks the
   leader for one (RenewCertificate, with a proof made with the agent's
   key). The leader waits until every node's heartbeat connection has
   presented a certificate from the next CA.
3. **promote** — the next CA is the cluster's, and every node drops the old
   one. The leader waits until every manager, and every agent online, says it
   trusts the next CA alone. An agent offline by then holds its new
   certificate already, and catches up at its first heartbeat back.
4. **done**.

- **Time limit:** trust and reissue may each take `--phase-timeout`
  (default 30 minutes, from 5 minutes to 24 hours). Past it the rotation
  goes back by itself, keeping the current CA, and records which nodes held
  it (`reason`). From trust it drops the next CA (drop → aborted); from
  reissue every certificate moves back to the current CA first (rollback →
  drop → aborted). The way back has no time limit: rollback waits for every
  node, and drop for every manager and every agent online.
- **`sudo sfpanel cluster rotate abort`** stops it the same way. From
  promote on there is no way back — a node may have dropped the old CA — and
  the stop is refused; another rotation is the way to move again.
- **Every node must be online**, agents included, on a release that follows
  a rotation, before it starts. An offline node holds trust and reissue until
  their time sends the rotation back, and an offline manager holds promote
  until it is back. Wait for it, or remove it, rotate, and join its
  replacement with the new fingerprint.
- **Also refused at the start:** a CA rotation under way, a cluster update
  running (`409 UPDATE_IN_PROGRESS`), a group task run under way
  (`409 TASK_RUN_ACTIVE`: its calls ride on the certificates a rotation
  reissues), and a switch of the internal proxy secret to the replicated one
  less than five minutes old — the secret it replaced was derived from
  `ca.crt`.
- **While it runs, nothing joins or updates.** Join tokens, cluster updates
  and group task runs are refused with `409 CLUSTER_CA_ROTATING`,
  conversions and promotions with their own `409`, and joins and agent
  registrations with the rotation's message. `sfpanel cluster reissue-cert`
  refuses on a node whose `ca.crt` holds two CAs.
- **After it,** make new join commands (`sudo sfpanel cluster token`). Every
  one printed before carries the old CA's fingerprint, and a join with it is
  refused (`the CA fingerprint does not match …`).
- **`raft_tls: false`:** a cluster whose Raft port is not under TLS rotates
  all the same, but the rotation does not reach that port; `rotate status`
  shows `raft_tls`.
- The old CA's key stays in the managers' Raft logs and snapshots until Raft
  compacts them. Once no node trusts that CA, no node accepts a new
  connection made with a certificate it signs.
- **Connections made under the old CA end with it.** A connection is
  verified once, when it is made. When a node stops trusting a CA — every
  node at promote, and at the drop on the way back — it closes every
  connection its gRPC port and its Raft port accepted under that CA, however
  long ago it was made. Members reconnect at once with the certificate they
  hold now; a call in flight on such a connection fails. A node does not
  close the connections it dialed itself: the peer at the other end closes
  one it no longer trusts, and the rest, dialed to a member's address, last
  until they next reconnect.

### In what order

After converting a manager to an agent or taking one out, or to start over
after a suspected leak:

1. Update every node to this release, the agents first.
2. Choose the managers to keep, and convert the others one at a time
   (§ Converting a manager to an agent).
3. `sudo sfpanel cluster rotate status`: every node is online and can
   rotate (`can_rotate`).
4. `rotate jwt`, and sign in again.
5. `rotate proxy` and `rotate agent-keys`; then `rotate status` until the
   proxy secret is `done` and the only agents still `pending` are offline.
6. `rotate ca`; then `rotate status` until it is `done`. It needs every node
   online, so an agent still pending after step 5 has come back and moved by
   then, or been removed.
7. Make new join commands.
8. Deal with the old backups: the `config.yaml.bak` and `sfpanel.db.bak` an
   update leaves, and the scheduled ones in `/var/lib/sfpanel/backups`. Those
   taken before step 4 hold the retired JWT key and the old account.

- **Why the keys go before the CA:** a CA rotation's certificate renewal is
  proved with the agent's key. Rotated first, the keys a former manager held
  prove nothing: it would have had to act during the key rotation too.
  The other way round, one CA rotation with the old CA key and the old agent
  keys is enough to get certificates from the next CA.
- **A manager converted, left or removed after a rotation started** saw the
  new secrets: run steps 4 to 6 again.
- **If an attacker may have acted during the rotations** — a former manager
  holding both the old CA key and the agents' keys — rotating does not shut
  them out. Remove each agent, reset it (`sudo sfpanel agent reset --yes`)
  and join it again with the new fingerprint.
- **On production servers,** a Proxmox host among them, approve each step as
  it comes, and rehearse the whole sequence on a test cluster first.

### What is not rotated

The account's secrets: the admin's password hash, TOTP secret and recovery
codes, which a former manager held too. Replace them after step 4, as each
needs a fresh sign-in: change the password (it replicates to every manager),
turn 2FA off and enroll it again, and make new recovery codes.

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
  `409 UPDATE_IN_PROGRESS`. None starts while a CA rotation is under way
  (`409 CLUSTER_CA_ROTATING`), and a CA rotation does not start during an
  update: a node an update restarts would come back on whatever its disk
  held. None starts while a group task run is under way either
  (`409 TASK_RUN_ACTIVE`, § Group tasks), and no run starts during an update.
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
  another manager voter — one the run has just updated or one already on the
  release. It tries the one that reaches the most agents first, so the
  agents keep a leader they can reach (§ Managers on more than one
  network), then the rest; ties go by name, and a manager whose reach is
  unknown comes last. If no handover works, the run stops and the leader is
  left as it is (`Leadership could not be handed over …: the leader was not
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
  leadership to another online manager voter — the one that reaches the
  most agents (§ Managers on more than one network) — and then asks it the
  same way,
  so the new leader removes it from Raft and deletes its record.
- When no leader took the node out — none was known or could be reached, or
  it refused — the leave goes on and says so (`no leader took this node out
  of the cluster: the others may still list it …`): remove the node on the
  leader with `sudo sfpanel cluster remove <node-id>`. The CLI prints this
  when the service is stopped; through the running service — the UI, or
  the CLI, which hands the leave to it — the answer's message ends with the
  same warning and this node's ID.
- A leave through the running service is refused (`409 CLUSTER_QUORUM`,
  `only 1 of 2 remaining voters reachable (need 2 for quorum)`) when the
  managers left behind could not form a quorum. The leader judges the
  others by their heartbeat streams to it; a follower, which hears from no
  other manager, by the status the leader keeps in each node's record — as
  fresh as the node list, so a manager stopped a moment ago still counts
  for a few minutes there. `sudo sfpanel cluster leave --force` (the API's
  `?force=true`, which the UI offers after the refusal) leaves anyway. A
  leave with the service stopped has no such check.
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
  each manager's process. It stays until a majority of the managers have
  taken the disband in — one that only stored it would wait for a leader
  that is gone — and then up to five seconds more, until each other manager
  has stopped and closed its heartbeat stream; meanwhile one that lagged can
  still catch up. Its log names any that had not
  (`these managers had not stopped when the leader did`): each needs a
  forced leave (`sudo sfpanel cluster leave --force`) or a forced disband
  (below) on its own server.
  The local SQLite DB is **not** wiped — audit logs, monitor history, cron,
  and Docker state are local-only and persist after the cluster goes away.
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
management (`/cluster/*`) stays on the managers, and a manager carries the
agent's stacks when they move to another node (§ Stack migrations).

What an agent holds is no credential anywhere else: its certificate
(OU `sfpanel-agent`) gets it nothing on a manager's gRPC port but its own
heartbeat, panel-CA report, and certificate renewal and key during a
rotation, its key is accepted by no other node, and the managers' internal
proxy secret never reaches it.

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
- `--token-file PATH` in the token's place, for both commands: the token is
  read from that file (at most 4 KiB, surrounding whitespace dropped), so it
  appears on no command line. A token given both ways is refused, as is an
  empty or unreadable file; the error names the path, never the content.
- `install.sh` alone also takes `--version V`, `--checksums FILE` and
  `--archive FILE`, and only with `--agent`. They are what the SSH install
  below passes, and they work by hand too:
  - `--version V` pins the release to install, ahead of `SFPANEL_VERSION`
    (which `sudo` drops), and asks GitHub for no latest version.
  - `--checksums FILE` is a `checksums.txt` whose signature the caller has
    verified. The archive is checked against it instead of the one published
    beside it, and cosign is not run, `SFPANEL_REQUIRE_COSIGN` included.
  - `--archive FILE` installs that release archive instead of downloading
    one, so the server needs no internet and no `curl`. It needs both
    `--checksums` and `--version`. A hash that does not match stops the
    install (`Checksum verification failed`) before the running panel is
    touched.
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
  (`agent.key`, 0600) under `cluster.cert_dir` — removing the key rotation's
  state an earlier membership left there (`agent-key-rotation.json`) — and
  restarts a running sfpanel, which comes back as an agent.
- The agent removes the panel's local `admin` account when it boots as an
  agent: an agent has no login, and on a server that was a manager — after
  `cluster leave`, or a conversion that stopped short — the account is the
  cluster admin's, its password hash, TOTP seed and recovery codes with it.
  Not before: until sfpanel restarts — after `--no-restart`, or a restart that
  failed — the standalone panel goes on serving, and its account with it.
- Before the token travels, the join checks that the panel's certificate
  verifies against the panel's CA for the advertise IP, where the managers
  reach the panel, and for 127.0.0.1, where the agent's own gRPC server
  hands their requests on. The managed certificate names the addresses on
  the server's stable interfaces (not Docker's `br-*` and `veth*`); an
  operator certificate has to name both, or the join stops and says so.
- A server that still holds a cluster manager's files is refused: `ca.key`
  in `cluster.cert_dir`, or `raft-log.db`, `raft-stable.db`, `snapshots`,
  `tokens.json`, `proxy-secret.json`, `ca-rotation.json`, `peers.json` or
  `peers.info` in `cluster.data_dir`. They hold the cluster's keys, which an
  agent joined over them would keep. Remove what the refusal lists, then join
  again.

### Ports

| From → to | TCP port | Carries |
|---|---|---|
| Managers → agent | 3629 (`cluster.grpc_port`) | Requests, over gRPC with mTLS |
| Managers → agent | 3628 (`server.port`) | Event streams, file transfers, terminals and other WebSockets, stack migrations (a manager pushes stacks to the agent, and pulls the agent's own from it) — panel HTTPS |
| Agent → managers | 3629 | Registration, heartbeats, panel-CA reports, and a rotation's certificate renewal and key, to the leader — at each of its addresses, the agent's own network first; the other managers are tried when it cannot be reached |

An agent needs no Raft port (3630), on either side.

An agent's panel listens on its advertise address and on 127.0.0.1 only;
`server.host` is not used. It answers loopback, every address of its
managers — the one each advertises and the others the leader lists for it,
a Tailscale address among them — and `cluster.agent_allowed_sources` (IPs
or CIDRs, for managers that reach agents from an address they do not
report, behind NAT for instance), judged by the
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

### Installing an agent over SSH

The leader can log in to the servers over SSH and run the join itself. In
the panel this is Cluster › Tasks › "Agent install (SSH)"
(`/cluster/tasks/builtin%2Fagent-install`); the Add agent dialog links to it.
The API is `POST /api/v1/cluster/agent-installs`, described in `api-spec.md`
("에이전트 설치 API"). There is no CLI. One install takes 1 to 50 servers and
works on 1 to 5 of them at a time (3 by default). Each server becomes one row
of a group-task run record (`kind: "agent-install"`), so the run's stream,
history and Stop serve it as they serve a task run.

**What it needs**

- **The leader must reach each server over SSH**, on the port given (22 by
  default). The leader opens every connection, whichever manager the browser
  is attached to. A server the leader cannot reach fails
  `AGENT_INSTALL_UNREACHABLE`. For such a server, use the commands in
  § Joining on the server itself.
- **The server must reach the leader's cluster port** (3629). The pre-flight
  checks this from the server.
- **The managers must reach the agent's 3629 and 3628** (§ Ports). Nothing
  checks this before the install; the connection check after registration
  finds out.
- **One account for every server: root, or a user who can sudo.** The sudo
  password can be:
  - the login password (the form's default for a password login);
  - a password of its own;
  - none, for passwordless sudo. The installer then runs `sudo -n`.

  The password reaches sudo on its standard input (`sudo -S`), never on a
  command line. A root login uses no sudo.
- **Every manager runs the leader's version.** The install pins the leader's
  version; mixed managers are refused (`409 AGENT_INSTALL_MIXED_VERSIONS`).
  Update the cluster first.
- **The leader must reach GitHub.** In every mode the leader downloads the
  version's `checksums.txt` and verifies its signature, as the self-update
  does, before the server installs against it (`--checksums`). The one
  exception is a build that is not a release (below).

**How the leader logs in**

There are three methods. One is chosen for every server.

- **Password.** It also answers keyboard-interactive prompts.
- **Private key**, with its passphrase if it has one.
- **Tailscale SSH.** The leader logs in with SSH `none` authentication: no
  password and no key. The tailnet's SSH rules decide whether the user may
  log in, so the leader has to reach the server over the tailnet.
  - When a rule is in `check` mode, the server holds the login and sends an
    approval address to open in a browser. The row then waits with
    `awaiting: "check"` and shows that address for up to 10 minutes.
  - The screen links the address only when it is `https:`, and shows its
    host beside the link.
  - A non-root user still needs sudo, as above.

**Addresses**

A host is an IP or a name the leader resolves (a Tailscale MagicDNS name
works); a name resolves to its first IPv4 address, or to its first address
when it has no IPv4 one. That address has three
roles:

- it is the agent's advertise address, and the server must hold it
  (`AGENT_INSTALL_IP_NOT_LOCAL`);
- it is the address the one-time join token is bound to;
- it decides where the agent joins. A server in 100.64.0.0/10 joins at the
  leader's Tailscale address; any other server joins at one of the leader's
  addresses outside that range (§ Managers on more than one network). The
  port is the leader's `cluster.grpc_port`.

**Host keys**

The first connection to each server only reads its host key, then closes.
The row waits with `awaiting: "host_key"` and shows the key as
`ED25519 SHA256:…`.

- Nothing runs on the server, and no login is tried, until an operator
  approves that fingerprint.
- Compare it with what `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`
  prints on the server. For another key type, use that type's key file.
- The approval carries the fingerprint the operator was shown. From then on
  the leader accepts that key and no other. A key that changes between the
  approval and the login fails the row (`AGENT_INSTALL_HOST_KEY_CHANGED`)
  before anything is sent. The row's details name the key the server
  presented (`host_key`) beside the one approved (`approved`).
- Declining stops that server only.
- A fingerprint is remembered for that install only.

Any question the operator leaves unanswered for 30 minutes ends its row
`AGENT_INSTALL_FAILED`. That covers the host key, a standalone panel and a
review; the Tailscale check has its own limit of 10 minutes.

**Online and offline**

- `auto` (the default) installs online when the pre-flight finds that the
  server can download the version's release from GitHub, and offline
  otherwise.
- **Online:** the server downloads the archive for its CPU and checks it
  against the leader's verified `checksums.txt`.
- **Offline:** the leader downloads the archive, checks its hash against the
  verified `checksums.txt`, and uploads it over SSH (`--archive`). The server
  needs no internet; the leader still needs GitHub.
- **A build that is not a release** (a version other than `X.Y.Z`) cannot be
  verified this way.
  - Such an install is refused (`409 AGENT_INSTALL_UNRELEASED`) unless the
    leader's environment sets `SFPANEL_AGENT_ARCHIVES` to a directory that
    holds `checksums.txt` and `sfpanel_<version>_linux_<arch>.tar.gz`.
  - Those files are used as they are, without a signature check, and always
    uploaded.
  - It exists for test rigs. A release build never reads the variable.

The install script is the `install.sh` built into the leader's own binary,
uploaded with the rest; the server downloads none from `main`. The
version's archive, a 0600 file holding the join token, and the script go
into a `mktemp -d` directory under `/tmp`. The install then runs as root
under `systemd-run --unit=sfpanel-agent-install-<run>-<row>`, so it survives
a dropped SSH connection. The leader reads the log and exit code every 2
seconds, for 30 minutes at most. When a read cannot run because the
connection broke, the leader logs in again at the next read, accepting the
approved host key only, and follows the install from there.

**Pre-flight**

Each server is checked after login and before anything is installed. The
first problem found stops that server alone, with a reason the screen words.
The reasons are listed in `api-spec.md` under "행 이유 코드".

- **The server:** Ubuntu or Debian; amd64 or arm64; systemd; the tools the
  install needs; root, or sudo that works with the password given.
- **SFPanel already on the server:** an agent or a cluster manager is
  refused, and so are a cluster manager's files left behind and a panel
  serving the operator's own certificate (`server.tls.cert_file` or
  `key_file` set), with or without `server.tls.ca_file`
  (`AGENT_INSTALL_OPERATOR_TLS`). The join requires the panel's certificate
  to verify for 127.0.0.1 and the advertise address, and would refuse it
  only after `install.sh` had stopped the panel. An SFPanel newer than the
  version being installed is refused too: the install never downgrades.
- **The row:** a valid node name (the server's hostname when none is given),
  and the IP given held by the server.
- **The server, continued:**
  - the panel and cluster ports free (3628 and 3629, or the ones its config
    names);
  - a clock within 30 seconds of the leader's;
  - a TCP connection to the leader's join address;
  - in `online` mode, `curl` and access to GitHub;
  - at least 256 MiB free under `/tmp`, `/usr/local/bin` and `/var/lib`.
- **The cluster:** the registration gates of § Joining are checked here, so
  a server they would refuse has nothing installed. They are:
  - every manager online and knowing agents;
  - five minutes passed since the proxy-secret switch;
  - no CA rotation under way;
  - an agent slot free;
  - the CA key present;
  - a name and an address no node holds, nor another server of the same
    install.

A server that **had SFPanel before** waits with `awaiting: "standalone"`
until an operator confirms the conversion: its local login goes away and
its panel switches to HTTPS. Declining skips that server. SFPanel before
means its binary, or only the data `install.sh uninstall` keeps without
`--purge` (`/etc/sfpanel`, `/var/lib/sfpanel` or `/var/log/sfpanel`); the
agent takes that data over. The row records it as `sfpanel_before`, and
nothing purges such a server.

With **Preview** (`review: true`), every server stops after its host key
and its pre-flight, waiting with `awaiting: "review"`. One **Continue
install** sends on the servers that passed.

**Checks after the install**

The leader finds the new agent in the cluster by the bound IP: a node that
is no manager at that address. The pre-flight's gates refused any node
there, so one found there after the install started is the row's.
`install.sh` registers the agent inside its join and has steps after it,
so a row whose `install.sh` exits non-zero, or runs out of time, after an
agent registered at its IP goes on to the checks all the same. The leader
then waits up to 3 minutes, checking every 3 seconds, for all three of
these:

- the leader's heartbeat shows the agent online at the installed version;
- `GET /api/v1/health`, relayed over the cluster port, answers 200;
- `GET /api/v1/health` on the agent's panel answers 200 over HTTPS.

The `group` label given is then merged into the node's labels.

**Failure, recheck and revert**

- **Before registration:** a row whose install had started and that ends
  short of a registration (a failure, a Stop, a timeout) first has its
  token revoked, so no join can come after, and the leader looks for an
  agent at the row's IP.
  - An agent found there is the row's: the row keeps it as `node_id`,
    nothing is purged, and the install unit, past its join, is left to
    finish.
  - With none, the leader stops the install unit if it is still running,
    and looks once more: a join admitted before the token was revoked may
    land during the stop.
  - Only a row with no agent then has its server cleaned, over the same SSH
    connection. If the server had no SFPanel before, it runs
    `install.sh uninstall --purge` and removes the empty `/opt/stacks` the
    run created.
  - A server that had SFPanel before is not purged. `install.sh` stops its
    panel to upgrade it; if the panel's service was active at the
    pre-flight, the leader starts it again (`systemctl start sfpanel`,
    best effort), and the row's `reason_details.panel` says `restarted` or
    `restart_failed`.
  - The temporary directory is always removed.
- **After registration, offline after 3 minutes:** the row fails
  `AGENT_INSTALL_OFFLINE` with a guess at the cause:
  - no heartbeat: the agent cannot reach the leader's 3629;
  - a heartbeat but no relay or no panel: the managers cannot reach the
    agent's 3629 or 3628, so check the firewall;
  - a heartbeat at another version.

  Two actions follow:
  - **Recheck** runs the three checks again. It needs no credentials.
  - **Revert** removes the node from the cluster.
    - On a server with no SFPanel before, it also purges SFPanel and the
      empty `/opt/stacks` over SSH, in the background, while the
      credentials last. The row shows `removing`, then `purged` or
      `cleanup_failed`.
    - A server that had SFPanel before keeps it, with or without the
      credentials, and its row keeps its reason (`reverted: removed`,
      `standalone: "1"`). Reverting cannot give it back its standalone
      panel, because the install already removed the local account. So
      that server loses only its node record.
- **Stopped after registration:** a row stopped once its agent registered,
  during the install, the checks or a recheck, ends `stopped` with
  `AGENT_INSTALL_STOPPED` (`registered: "1"`) and keeps its `node_id`. The
  node stays in the cluster until the row is reverted. A Stop drops the
  credentials, so such a revert removes the node only, and the row reads
  `AGENT_INSTALL_CREDENTIALS_GONE` (`reverted: removed`), unless the server
  had SFPanel before (above). There is no recheck after a Stop.

  Revert never leaves a panel without an administrator.
- **Credentials.** The SSH password, private key, passphrase and sudo
  password are never stored anywhere: not on disk, in Raft, in a run
  record, a log, an audit row or a response. They live only in the leader's
  memory, as bytes that are zeroed when they go.
  - They go as soon as an install succeeds.
  - When a registered server fails, they stay for 10 minutes after the
    install ends, so Revert can still purge it. `credentials_until` in the
    run record holds that deadline. They go sooner once every such server
    has been reverted.
  - A Stop wipes them at once.
  - They never leave the leader that started the install, so after a
    leader change nothing can use them.
- **Cleanup by hand.** A Revert once the credentials are gone removes the
  node only. The screen then shows the command to clean the server, as it
  does after `cleanup_failed`, or after a leader change that cut short an
  install already under way. To clean such a server by hand, run on it:

  ```bash
  curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/v<version>/scripts/install.sh | sudo bash -s -- uninstall --purge
  ```

  `<version>` is the installed version (the run's `inputs.version`); a build
  that is not a release uses `main` in place of `v<version>`. `--purge`
  removes the configuration, the data and the logs as well. On a server
  that had SFPanel before the install (`sfpanel_before`), that is the
  former panel's data, which the installer never purges: do not run it
  there. Make sure the node is no longer in the cluster first: a removal
  has to come from a manager (§ Removing an agent).
- **Join token.** Each row gets its own token: one-time, agent-only, valid
  for 30 minutes and bound to the row's IP. It travels as a file, never as
  an argument (`--token-file`). However the row ends, the token is revoked
  and its directory is deleted.

**One install at a time, and the leader**

- While an install runs, the following are refused with
  `409 AGENT_INSTALL_ACTIVE`:
  - a second install;
  - a group-task run, or a rerun;
  - a manual leader transfer.
- While an install runs, the leader does not move to the preferred leader.
- An install counts as a run under way: a cluster update, a CA rotation, a
  conversion or a promotion waits with `409 TASK_RUN_ACTIVE`.
- The session lives in memory on the leader that started it. A new leader
  does not take it over. It ends the run `failed` with
  `AGENT_INSTALL_LEADER_CHANGED`:
  - a server that had registered keeps its node, to check on the node page.
    A row whose install had begun and that names no node is given the
    agent at its IP, if there is one;
  - the other rows are cut off, and may need the cleanup command above.
- A Stop (`POST …/task-runs/<id>/stop`) halts every row and revokes its
  token. A row whose agent registered keeps its node (above); one with no
  agent has its install unit stopped and its server cleaned. The
  credentials go. An install cannot be rerun
  (`409 AGENT_INSTALL_NOT_RERUNNABLE`); start a new one.

### Checking an agent

On the agent, `sudo sfpanel agent status` shows its cluster, node ID and
advertise address, the managers and the leader it last heard from, whether
the leader removed it, the CAs it trusts (by fingerprint — two while a CA
rotation is under way), its certificate's expiry and the CA that signed it,
whether its key is present (never the key itself) and whether its panel
answers on loopback. On a
manager, the node list shows it with `kind: agent` and no Raft role; it is
online while its heartbeats arrive.

The agent heartbeats to the leader every 30 seconds and keeps the leader's
address and the manager list in `{Cluster.DataDir}/agent-state.json`, with
every address of each manager. It dials a manager at the address that worked
last time first (`reached`, per manager ID), then at the addresses on its own
network — those in Tailscale's range when its advertise address is in it —
then at the rest (§ Managers on more than one network). Any
manager's reply can say where the leader is, but only the leader's sets the
manager list — the managers the agent takes requests from — because a
follower's may lag behind a removal. The one entry a follower adds is the
leader it names, when the agent has no manager at that address: a leader
elected while the agent was not listening is reached that way, and its own
reply then sets the list. A manager that knows no leader (during
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
   cleared) and gets a new JWT key, the certificates, the key, a key
   rotation's state (`agent-key-rotation.json`) and the link's state are
   deleted, and a running service restarts. Without `--yes` it
   only lists what it would do. The installer's `uninstall` runs the reset
   itself. The panel has no admin account then — the agent removed it at
   boot — so it asks for one as a fresh install does, from loopback or a
   private network: create it straight away. The reset says so too.

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

## Managers on more than one network

A manager may be reachable on more than one network: the LAN address it
advertises, and a Tailscale tailnet. Agents can sit on either — a server
reachable only over Tailscale, or one on a LAN with no tailnet — and a
manager relays to an agent only over a network of its own: there is no hop
through a second manager. So the cluster records every address of each
manager, agents dial the one on their own network, and the leader role goes
to a manager that reaches the agents, since they heartbeat to the leader.
None of it needs configuring.

### A manager's addresses

- A manager's addresses are the one it advertises
  (`cluster.advertise_address`, or else the host of its recorded gRPC
  address), then its Tailscale addresses — at most eight. Its gRPC server
  answers at each of them, on the one port.
- A Tailscale address is an IPv4 address on an interface that is up, not
  loopback, and not one Docker creates with a stack (`br-*`, `veth*`): any
  address in Tailscale's range, 100.64.0.0/10 — Tailscale's interface is not
  always `tailscale0` — and any address on an interface named `tailscale*`,
  which a self-hosted control server may number from another range. A
  Docker bridge an operator numbered from 100.64.0.0/10 is listed too. That
  is harmless: a dial to it fails and the next address is tried, and the
  certificate names one address more.
- Each follower reports its addresses on every heartbeat ping. The leader
  writes them, and its own, into the node records on its 15-second tick, at
  most once a minute per node, so an interface that flaps does not cost a
  Raft entry per ping. `addresses` in `GET /api/v1/cluster/nodes` and
  `/cluster/overview` lists every node's addresses, the one it is
  registered at first, for example `["192.0.2.10", "100.64.0.10"]`. An
  agent, and a manager with no Tailscale address, has its registered
  address alone; a manager registered at a host name keeps that one
  address, since the list holds IPs only.
- A node whose address is changed (`PATCH /cluster/nodes/<id>/address`, or
  the check a manager makes of its own address at boot) is listed at the new
  address first at once; the rest of its list follows on the leader's next
  tick.
- The leader writes lists only while every manager is online and runs a
  release that knows them: a manager from before drops the list from each
  record it applies, and a snapshot it takes would keep it dropped after its
  upgrade. Until then records keep what they have, and each node is dialled
  at its registered address as before. During a rolling update the lists
  start once the last manager is on the new release.

### Certificates

Each manager's node certificate names every address the manager finds for
itself and every address the cluster records for it. The manager checks on
its 15-second tick, and when one is missing it reissues the certificate with
the same key, node ID and manager role, keeping every name the old one had:
the list never shrinks, so a node still dialling an address the manager no
longer has is not cut off. The new certificate is presented at once. Nothing
is reissued while a CA rotation is under way — the rotation moves every
certificate itself — and the first tick after it catches up. To see what a
certificate names:

```bash
sudo openssl x509 -in {Cluster.CertDir}/node.crt -noout -ext subjectAltName
```

`sudo sfpanel cluster reissue-cert` names the advertise address, this host's
Tailscale addresses and every name of the certificate it replaces, and makes
a new key. It reads no replicated state, so an address only the cluster
records for the manager is added by the running service on its next tick.

### How an agent reaches its managers

- The leader's replies carry every manager's addresses, and the agent keeps
  them with the manager list in `agent-state.json`.
- The agent dials a manager at the address that worked last time first
  (`reached` in `agent-state.json`, per manager ID), as long as the manager
  still lists it; then at the addresses on its own network — those in
  100.64.0.0/10 when its advertise address is in that range, the others when
  it is not — then at the rest. The agent goes by the range alone: it knows
  none of the manager's interfaces. It saves the address a session went
  through, and writes the file only when that changes.
- It follows the leader by node ID: a reply that names the leader sends the
  agent to that manager's addresses in the same order.
- A state file from an earlier release has neither addresses nor `reached`:
  the agent dials as before, at each manager's gRPC address, until the
  leader's first reply fills them in. An agent from before addresses keeps
  dialling the gRPC addresses only.
- Join a server that is only on the tailnet through a manager's Tailscale
  address: `sudo sfpanel agent join 100.64.0.10:3629 <token> --ca
  <fingerprint>`. `cluster token --agent` prints the leader's advertised
  address; any address of any manager works. Without `--advertise`, the
  agent's own address is detected from the route to the manager, so this
  agent advertises its Tailscale address. Pointed at a follower, the join
  dials the leader at each of its addresses in the order above, and moves
  on from one whose server fails the pinned CA's check — anyone can hand
  out the CA's public key; only the leader holds a certificate it signed.
  When it reaches the leader at none, the join fails, and the token is not
  spent. A follower names those addresses to any caller, before a token is
  checked — only the leader holds tokens — so whoever reaches a follower's
  gRPC port learns the leader's Tailscale addresses as well as the LAN one
  it always learned.
- An agent's panel takes requests from every address of its managers — a
  manager's Tailscale address among them — with nothing in
  `cluster.agent_allowed_sources`. That setting stays for a manager whose
  traffic reaches the agent from an address it does not report, behind NAT
  for instance.

### Which manager reaches which agent

Every manager probes every agent in the cluster, online or not, every 60
seconds: a bare TCP connect to the agent's gRPC port and close, without TLS,
three seconds per dial, eight agents at once. The agent's log stays quiet.
While an agent is out of reach the next probe comes after 10 seconds, so one
back from a restart is found within seconds. A manager reports the agents it
reached on its heartbeat ping, and a probe counts for three minutes.

A request for an agent that this manager's fresh probe could not reach is
refused at once with `502 CLUSTER_NODE_UNREACHABLE`, naming the agent, instead
of timing out after thirty seconds over gRPC or minutes over a relay. That
holds for every relayed request — event streams, file transfers and
WebSockets included, a WebSocket before its upgrade. The panel says to open
it on a manager on the agent's network (LAN or Tailscale). Without a fresh
probe of that agent, the relay is sent and the dial decides, as before. An
agent that has just come back can be refused this way for up to the 10
seconds until the next probe.

### The leader goes to the manager that reaches the most agents

Agents heartbeat to the leader, and one the leader cannot reach looks offline
to it even while another manager reaches it. So the leader weighs, on its
15-second tick, whether to hand its role over. These rules apply in order;
the first that matches keeps the leader where it is:

1. Something holds the role: a cluster update is running, or a CA rotation
   is under way. Both stop if the leader changes.
2. It has led for less than 30 seconds.
3. It handed its role over by itself in the last 10 minutes, whether or not
   the hand-over worked. This is kept in memory: a restart forgets it.
4. Its own reach is unknown: no probe within three minutes, or one with no
   verdict on an agent registered since.
5. It reaches every agent, or there are none.

Otherwise it hands the role to the manager voter, online by live heartbeat,
that reaches strictly more agents than it does — as many is no reason, or two
managers that reach different agents would trade the role back and forth —
the one that reaches the most, then the first by name. A manager whose reach
is unknown — a stale probe, or a release from before probes — is never
chosen, and a leader from before them never hands over. Before handing over,
the leader probes again: an agent that was restarting during its last probe
does not move the role to a manager that only probed later.

The leader logs the hand-over (`leadership handed to a manager that reaches
more agents`, with `target` and a `reason` such as `peer-2 reaches 3 of 3
agents, this leader 2`), or why it failed (`could not hand leadership to a
manager that reaches more agents`).

A manual transfer (`sudo sfpanel cluster leader-transfer <node-id>`,
`POST /api/v1/cluster/leader-transfer`) does not start the 10-minute wait. A
transfer to a manager that reaches fewer agents is therefore undone within
about a minute: once that manager has led for 30 seconds, it hands the role
on at its next tick — unless it handed the role over by itself within the
last 10 minutes, in which case the role moves on when those 10 minutes are
up. While it leads, the agents it cannot reach show offline, since agents
heartbeat to the leader; they are back within a minute of the hand-over.

The leader's other hand-overs pick by the same measure: on shutdown, on
`cluster leave`, and at the end of a cluster update (§ Update orchestration)
the role goes to the manager voter online by live heartbeat that reaches the
most agents. Ties go by name, and a manager whose reach is unknown comes
last.

### Not covered

- Tailscale's IPv6 addresses are neither detected nor recorded.
- No manager relays through another. An agent only some managers reach is
  opened from a panel on one of them.

---

## Stack migrations

A stack moves to another node cold. The source stops it and packages its
definition, `.env`, volumes, bind data and images into one bundle; the target
restores the bundle, starts the stack and waits for it to be healthy; only
then does the source apply its disposition — `retain` leaves it stopped,
`delete` removes it, `clone` starts it again. The operator runs
`POST /api/v1/docker/compose/<project>/migrate/preflight` and then
`…/migrate`, with `?node=` naming the node the stack is on (`api-spec.md`).

- **A stack on a manager** is migrated by that manager, which sends the
  bundle to the target itself; from another manager, the request is relayed
  to it like any other.
- **A stack on an agent** is carried by the manager the request comes to —
  the one the browser is connected to — since an agent holds no other node's
  key and no node takes anything from it. `/migrate` and `/migrate/preflight`
  sent with `?node=<agent>` are not relayed: they run on that manager, as
  `/auth/*` does. It drives the agent's migration session (below): the agent
  stops and packages its stack, the manager pulls the bundle over the
  agent's panel HTTPS and pushes it to the target as it arrives, writing none
  of it to disk, and then tells the agent how the target took it. The Docker
  work is the source's and the target's, so a manager without Docker carries
  an agent's stack all the same.

Either way the run is detached from the browser: closing the tab does not
stop it.

**Nothing stops before the target is checked.** The source stops only once
the pre-flight has no block, the target has answered it, and the migrating
manager can push to it: it knows the node, can reach an agent target over
HTTPS (its panel CA is in replicated state) and holds the key it signs for it
with. A target that did not answer the pre-flight (`target-unreachable`) is
refused rather than gone on with, as its disk, ports and existing stacks went
unchecked. Carrying an agent's stack, the manager must also reach that agent
the same way. Each refusal ends the stream with `pre-flight blocked: …` and
leaves the source running.

**The target takes the import's terms from the sending manager**, never from
the bundle, which is the source's: whether to replace a stack or a volume it
already has (the operator's overwrite acknowledgement), which risky findings
the operator was shown and acknowledged, and whether the bundle came from an
agent. They travel as headers of the import request (`X-SFPanel-Migration-*`),
which only a manager can send: the request is signed with the target's key,
and an agent holds no other node's. A bundle's own overwrite flag counts only
when no such header came, from a manager on a release from before them.

**An agent's bundle meets stricter rules**, so that root on an agent does not
become root on the node its stack moves to:

- A compose file in the risky tier — `privileged`, host namespaces, the
  Docker socket and the like — needs the operator's acknowledgement of each
  finding: `"acknowledgedRisks"` in the migration's body lists the sentences
  the pre-flight showed, and the target refuses (`COMPOSE_RISKY`), naming
  them, every risky finding of the compose file or of its resolved form that
  is not on the list. The list the dialog shows is the agent's own word; an
  agent that leaves a finding out gets it refused, not acknowledged. A
  manager's stack is checked for the forbidden tier only, as before: it
  passed the risky tier when it was created. The forbidden tier — binds of
  `/etc/sfpanel`, `/var/lib/sfpanel`, `/root/.ssh` or `/etc/sudoers.d` — is
  refused for every source, and the pre-flight blocks it
  (`compose-forbidden`) before the source stops, from the compose file and
  its resolved form. A stack whose resolved form `docker compose config`
  cannot produce on the target is refused too (`COMPOSE_ERROR`): what it
  binds is unknown.
- Bind data is restored into the stack's own directory only, and a stack
  that binds a path outside it is refused. A manager's absolute binds are
  still written to the same paths on the target, outside the protected
  system trees.
- An image loads only under a tag the target does not have, or over the same
  image. A tag the target holds as another image is refused (`409`): the
  stacks that use it would run the agent's image at their next restart.

For every source, an image archive may set only the tag the stack runs
(`docker load` sets every tag an archive names), and a bundle's small entries
— manifest, compose file, `.env` and the other definition files — may not
exceed 64 MiB together.

**Mixed versions.** Carrying an agent's stack needs both ends on a release
that knows how. An agent from before migration sessions is a `source-version`
block, and a target that states no import policy — it would take the agent's
bundle on the bundle's word, by a manager's rules — is a `target-version`
block. The pre-flight reports both before anything stops. A manager's stack
still goes to an older target, which reads the bundle's overwrite flag as
before.

### The source's session

Every node, an agent too, serves a migration of its own stacks as a session
that a manager drives, on four node-to-node routes under
`/api/v1/docker/compose/<project>/migrate-source` that take only a manager's
signature (`api-spec.md`). The session holds the stack's migration lock —
two managers cannot carry the same stack at once — stops and packages the
stack, announces the bundle, and waits up to two hours for the manager's word
on how the target took it:

- `imported`: the target runs the stack. The source applies the disposition.
- `failed`: the target refused the bundle or could not bring the stack up.
  The source starts its stack again.
- `unknown`: the connection to the target broke, and the target was running
  the stack when asked, before its import had answered. The source leaves its
  stack stopped.

Without a word — the manager went away, its stream broke, this panel stopped,
or the two hours passed — the source starts its stack again only if the
bundle never went out whole. Once every byte has been handed to the manager's
connection, the stack may be running on the target, and two live copies are
worse than a stopped one: the source leaves its stack stopped and logs an
error. In both stopped cases, check the target, then start or remove the
source.

If the manager cannot hand the source its word, or the source's stream ends
before it says how it finished, the manager ends the operator's stream with
what it knows: `done` when the target took the stack — a source that never
got the word leaves its stack stopped and its disposition unapplied — and
`error` otherwise. Both messages end with "check the source".

---

## Converting a manager to an agent

`POST /api/v1/cluster/nodes/<node-id>/convert`, or
`sudo sfpanel cluster convert <node-id>` on a manager whose service is
running, turns a manager into an agent under its own node ID, with its name,
labels and join time. Alert rules scoped to nodes and the audit log name a
node by its ID, so they go on pointing at it, and its local database stays as
it was but for the admin account. Any manager takes the request to the
leader, which runs it.

**Gates.** Nothing changes until every one of these holds; a refusal is
`409 CLUSTER_CONVERT_REFUSED` with the reason.

- The node is a manager other than the leader, online, on a release that
  knows agents. Hand a leader's role to another manager first
  (`sudo sfpanel cluster leader-transfer <node-id>`); a one-manager cluster
  has nothing to convert.
- The voters left keep their quorum without it, counted by heartbeat as a
  leave's guard counts them. A two-manager cluster can convert its follower:
  the leader alone is a quorum of one.
- An agent could register now: five minutes have passed since the leader
  recorded the switch of the internal proxy secret, every other manager is
  online on a release that knows agents, and there is room for another agent
  (`cluster.max_agents`).
- The agent's identity is free: the node's name is one an agent may have
  (1–63 letters, digits, dots, hyphens or underscores, starting with a letter
  or digit), the cluster has the node at an IP and a port, and no other node
  has that name or IP. Nodes cannot be renamed, so a manager that fails here
  — one of two servers cloned under one host name, say — leaves the cluster
  (`sfpanel cluster leave`) and joins with `sfpanel agent join --name`, as a
  new node.
- The leader can sign certificates: it holds the CA key, on disk or in
  replicated state.
- No CA rotation is under way.
- No group task run is under way (`409 TASK_RUN_ACTIVE`, with the run's ID:
  the node would leave Raft and restart under the run's steps). While a
  conversion is being started, no run starts (`409 TASK_CLUSTER_CHANGING`).

A leader starts one conversion at a time.

**Steps.**

1. The leader makes an agent token bound to the node, records the node's
   name, labels and join time in replicated state, and asks the node to go
   (`POST /api/v1/cluster/become-agent`, which takes only a call signed node
   to node).
2. While a refusal still changes nothing, the node checks what the agent it
   becomes needs. The address the cluster has for it must be an IP of this
   server, since an agent listens where it registers; correct a wrong one
   with `PATCH /api/v1/cluster/nodes/<node-id>/address`. Its panel must serve
   HTTPS that verifies for 127.0.0.1 and that IP: HTTPS that was off is
   turned on with the managed certificate, as `agent join` does, and an
   operator certificate needs `server.tls.ca_file`. Then the node answers,
   and the leader answers the operator `202`: the conversion has begun.
3. The node makes the cluster's admin account its local one — password, 2FA
   and recovery codes — as a leave does: from the leave on, a stop leaves a
   standalone panel, which signs in with that account.
4. The node leaves the cluster, and the leader removes its record and its
   Raft membership.
5. It saves its config as a standalone panel's, with a JWT key of its own.
6. It removes what a manager keeps and an agent must not hold: `ca.key`, and
   its own `node.crt` and `node.key` — a manager's certificate, which leaving
   does not revoke — in `cluster.cert_dir`; `raft-log.db`, `raft-stable.db`,
   `snapshots`, `tokens.json`, `proxy-secret.json`, `ca-rotation.json`,
   `peers.json` and `peers.info` in `cluster.data_dir`; and the
   `config.yaml.bak` and `sfpanel.db.bak` an update leaves.
7. It registers as an agent under its own ID with the token, and the leader
   gives the new record the name, labels and join time it recorded.
8. It restarts as an agent (`Restart=always` starts it again), and the agent
   removes the local `admin` account as it boots: an agent has no login.

It is over when the node list shows the node as an agent. The node may be the
one the browser is connected to — the CLI on that node needs it — but the
browser's session ends with the restart, as an agent has no login or UI:
carry on from another manager.

**When it fails.**

- A gate refuses: nothing has been made or changed.
- The node refuses (`502 CLUSTER_CONVERT_REFUSED`, with its reason): nothing
  has changed, and the leader takes back the token and the record it made.
- The node does not answer the leader (`502 INTERNAL_ERROR`): it may have
  taken the request and be on its way out — its leave drops the connection
  the answer would come back on — so the token is kept, and admits that node
  alone, at its IP. Look for the node in the node list a minute later, and if
  it is still a manager there, convert it again.
- The node cannot make the cluster's account its own, or the leave fails:
  nothing else changes on the node, which restarts as the manager it was. Its
  log says why.
- A step after the standalone config is saved fails — a file that will not
  go, or a registration the leader refuses (a leader elected since the
  conversion began does not have its token): the node restarts as a
  standalone panel, as a leave leaves one. Sign in to it with the cluster's
  account. After a refused registration it holds none of the cluster's keys
  and no manager certificate; after a file that would not go, it holds
  exactly the files its log names. On the managers the node has only gone
  from the node list. Remove any file the log names, then join it with
  `sudo sfpanel agent join` and a token from
  `sudo sfpanel cluster token --agent`. It comes back as a new node, with a
  new ID and without its labels, and removes the account when it boots as an
  agent.

**Backups.** The scheduled backups beside the database
(`/var/lib/sfpanel/backups`) are the operator's and stay where they are; when
there are any, the answer's message ends with a warning, and the node logs
one. Those taken while the node was a manager hold the cluster's JWT key and
admin account, usable until the JWT key is rotated (`sfpanel cluster rotate
jwt`) and the admin's password and 2FA are replaced, and the agent refuses to
restore them (§ Backups and agents). Move them off the node, or delete them.
The node itself held the managers' internal proxy secret and the agents' keys
as well: rotate those too (`rotate proxy`, `rotate agent-keys`), in the order
§ Rotating the cluster's secrets gives.

---

## Promoting an agent to a manager

`POST /api/v1/cluster/nodes/<node-id>/promote`, or
`sudo sfpanel cluster promote <node-id>` on a manager whose service is
running, turns an agent into a manager under its own node ID, with its name,
labels and join time. Any manager takes the request to the leader.

**Gates.** The node is an agent and online, as it joins again itself; the
cluster has room for another manager (32); the leader can sign certificates;
the cluster has the agent at an IP and a port, which the token is bound to;
and no CA rotation is under way. A refusal is `409 CLUSTER_PROMOTE_REFUSED`, nothing having changed.
No group task run may be under way either (`409 TASK_RUN_ACTIVE`, with the
run's ID: the agent restarts as a manager), and while a promotion is being
started no run starts (`409 TASK_CLUSTER_CHANGING`).

**Steps.**

1. The leader makes a manager token bound to the agent and asks it to join
   (`POST /api/v1/system/become-manager`, signed with the agent's key: an
   agent serves no `/cluster` route).
2. The agent checks that it can listen on its Raft port — the cluster gRPC
   port + 1, 3630 by default — at its advertise address, where the manager it
   becomes binds it, and answers `202`: the promotion has begun.
3. It removes whatever an earlier membership as a manager left — `ca.key`,
   the Raft stores and snapshots, `tokens.json`, `proxy-secret.json`,
   `ca-rotation.json`, `peers.json` and `peers.info` — while it is still an
   agent and uses none of them. One that will not go stops the promotion
   there.
4. It joins as a manager under its own ID and name, at its config's
   `advertise_address`. The leader turns the agent's record into a manager's,
   a Raft non-voter, keeping its name, labels and join time, and deletes the
   agent's key from replicated state, as the managers sign for a manager
   with their own secret; its panel CA stays. The join writes the manager's
   certificate, its config — no role or manager list, the cluster's JWT key —
   and the cluster's admin account.
5. It removes `agent.key`, `agent-key-rotation.json` and `agent-state.json`.
6. It restarts as a manager, with the login and web UI a manager has.

The agent restarts whatever the outcome, and one whose join the leader
refused comes back as the agent it was. The token is revoked whenever the
agent refuses (`502 CLUSTER_PROMOTE_REFUSED`) or does not answer
(`502 INTERNAL_ERROR`): an agent that took the request and joined first is a
manager by then, and one that did not can no longer join with it. Look in the
node list a minute later, and promote it again if it is still an agent. The
token is bound to the IP the cluster has for the agent, so an agent whose
`advertise_address` differs from it — one changed by hand — is refused at
the join and stays an agent.

**Becoming a voter.** A promoted manager is a Raft non-voter until its Raft
has caught up, as any joining manager is (§ Joining a manager), and that
needs its Raft port reachable from the leader. An agent needs no Raft port
(§ Ports), so a firewall in front of one may well close it: open it to the
managers before the promotion. The agent holds the port open from step 2
until its promotion has run, and the leader dials it before it turns the
record into a manager's. A port the leader cannot reach refuses the join,
and the agent comes back as the agent it was; its log and the leader's say
why (`cannot reach … Raft port`). Admitted with the port closed, the node's
Raft would never hear from the leader, it would send no manager heartbeat,
and every agent registration and conversion would wait for it. A port that
closes after the join does just that: the node stays a non-voter, the
leader's log shows Raft's own errors for its Raft address
(`failed to heartbeat to`, `failed to appendEntries to`), and agent
registrations and conversions are refused, naming it, until the port is
open or the node is removed. Convert or promote one node at a time, and let
each come back before the next.

**A join that the leader admitted and the server could not finish** — the
leader's answer failed the CA pin, or a file could not be written — leaves
the leader listing a manager the server is not. Remove the node on a manager
(`sudo sfpanel cluster remove <node-id>`); on the server,
`sudo sfpanel agent reset --yes` makes it a standalone panel, which can then
join again.

---

## Group tasks

A group task runs the same steps on a set of nodes: refresh and upgrade
packages, restart a service, run a command, write a file, deploy a stack,
reboot when one is needed. The leader runs it, node by node, and keeps the
record in replicated state, so a run goes on when the browser that started it
closes and when the leader changes. The screen is Cluster › Tasks
(`/cluster/tasks`); there is no CLI. The API is the `/api/v1/cluster/tasks`
and `/api/v1/cluster/task-runs` routes in `api-spec.md` ("그룹 작업 API").

### The task document

A task is one YAML document, checked in full when it is saved or validated:
every problem comes back with its line.

```yaml
name: web security updates        # required; no other task, stored or built in, may have it
description: refresh, upgrade, restart nginx, reboot if needed
inputs:                           # asked for when the task is run
  service: { label: Service to restart, default: nginx, required: true }
targets: { groups: [web], kinds: [agent], nodes: [] }   # defaults; a run may replace them
batch: 2                          # agents at once; managers always go one at a time
stop_on_failure: true             # the default
steps:
  - packages: { update: true, upgrade: all }
  - name: restart the web server
    service: { name: "{{ service }}", action: restart }  # start|stop|restart|enable|disable
  - command: { run: "nginx -t", timeout: 2m }             # or script: | for several lines
    continue_on_error: true
  - file: { path: /etc/motd, content: "managed by {{ node.name }}\n", mode: "0644", owner: root }
  - stack: { name: status, compose: "…", up: true }
  - reboot: { only_if_required: true, wait: 10m }
```

- A step is exactly one of `packages`, `service`, `command`, `file`, `stack`,
  `reboot`, with an optional `name` and `continue_on_error` (off by default).
  A `packages` step takes `update` and at most one of `upgrade` (`all` or a
  list), `install` or `remove`.
- **Targets** are the union of the nodes whose `group` label is one of
  `groups` (one group per node: set it with
  `PATCH /api/v1/cluster/nodes/<node-id>/labels`), of the node kinds in
  `kinds` (`manager`, `agent`) and of the node IDs in `nodes`.
- **References.** `{{ input }}`, `{{ node.name }}` and `{{ node.address }}`
  (the first address the cluster has for the node) are filled in for each
  node. In a command each value is shell-quoted unless its input says
  `raw: true`. The quoting makes the value one word whatever it holds, but
  only *where the shell reads a word*, so a command's reference must stand
  there: unquoted, at the top level, as in `echo {{ x }}`, `--opt={{ x }}` or
  `X={{ x }}`. A task that puts one inside single or double quotes, `$( )`,
  `$(( ))`, backticks, `${ }`, a heredoc body or a comment is refused at its
  line, because the quotes Render adds would open the value to the shell
  there. To use a value inside a string, assign it first (`X={{ x }}`) and
  write `"$X"`. The check reads the text the way `/bin/sh` does and refuses
  whatever it cannot follow (`$'…'`, `$[…]`, quotes inside `${…}`, `case`
  inside `( )` or `$( )`), so a reference after such syntax is refused too;
  assign it near the top of the script instead. A `raw` input is exempt: it
  is spliced as written, wherever it stands. In a service name, a file's
  path, content and owner, and a stack's name and compose document a value
  goes in as typed, unquoted. A reference in a package list may hold several
  names, split on spaces and commas. `{{.Names}}`-style Go templates for
  docker and similar tools are left alone.
- **Built-in tasks**, read-only and carried by every manager's binary:
  `builtin/package-update`, `builtin/package-install`,
  `builtin/service-restart`, `builtin/command` (its one input is `raw`: the
  whole command line, as typed) and `builtin/reboot` (only when required).
  The list also carries `builtin/agent-install`, which has no document: it
  opens the agent install (§ Installing an agent over SSH). A preview or a
  start of it is `404 TASK_NOT_FOUND`, and changing or deleting it is
  `409 TASK_READ_ONLY`.
- **Limits:** 64 KiB per document, 50 steps per task, 200 targets per run
  (counted before any is left out), a command's time limit 10 minutes unless
  the step says otherwise, a reboot's wait 10 minutes. An input value is at
  most 16 KiB and a run's values 64 KiB together (`400 TASK_TOO_LARGE`,
  `details.input` the input, or `*` for the total): they are kept in the run
  record every manager holds. A request body to the task routes is at most
  1 MiB (`413`).

### Importing tasks from GitHub

Task documents kept in a GitHub repository can be imported and fetched again
later (`POST /api/v1/cluster/tasks/import/preview`, then
`POST /api/v1/cluster/tasks/import`). Only GitHub is supported, through its
REST API; nothing is cloned.

- **What is read.** A folder (or the repository root, or one file) on a
  branch, `main` if none is given. Its `.yaml` and `.yml` files are the
  candidates, one level deep: subfolders are not entered. At most 50 per
  folder.
- **Pinned to a commit.** The preview resolves the branch to its commit and
  lists the folder at that commit; the import reads each picked file at the
  same commit. A push between the two cannot bring in content the operator
  did not see. Each imported task records the repository, branch, file path
  and that commit as its `source`.
- **Fetching again.** The same repository (case aside), branch and file path
  is the same task: a fresh import changes it in place and keeps its ID. The
  preview compares each file's git blob SHA with the stored document, so a
  refresh where nothing changed costs two GitHub calls, and lists the tasks
  whose file is gone as `removed` (deleting them is the operator's call). An
  imported task is read-only (`PUT` is `409 TASK_READ_ONLY`); it may be
  deleted.
- **All or nothing from GitHub, file by file for the documents.** Every
  picked file is read before anything is written: a refused token, a used-up
  allowance (`429 GIT_RATE_LIMITED`; 60 calls an hour without a token) or a
  missing repository writes nothing. A file that is not a task — not UTF-8,
  a document with problems, over 64 KiB, a name taken — is listed in the
  answer's `failed` with its code, and the others are written.
- **The token** is optional, for private repositories. It is sent to GitHub
  in the `Authorization` header and is never stored: not in the task, Raft,
  the panel's log or an audit row. A private repository asks for it again on
  each refresh.
- **Where it runs.** The leader calls GitHub for both routes, so one address
  spends the allowance and the preview compares against the store the import
  writes to; a follower forwards them (five minutes). The leader logs one line
  per import, `tasks imported from git`, with the repository, branch, commit
  and counts.

### The preview and the start

`POST /api/v1/cluster/task-runs/plan` is the preview: the order the run would
take, the targets it would leave out and why, and each planned node's own
verdict on each step, filled in for that node. A node answers from the same
guards its pages use — a protected service unit, a protected file path, a
forbidden compose document, a Proxmox reboot — with the refusal's code, or
says a step needs confirming (a service the Services page asks about, a risky
compose document). The preview asks up to 16 nodes at once, 10 seconds each,
under the name of the operator who asked; a node that does not answer is
shown with why. It also names the managers a start would wait for
(`managers_not_ready`, § Mixed versions), and carries a `plan_id`: the
SHA-256 of the document's revision, the task's own inputs as given and the
targets as given or the task's own.

`POST /api/v1/cluster/task-runs` starts the run and answers `202` with its ID.
It does not ask the nodes again, so a step the preview showed refused fails
on that node when its turn comes (a Proxmox reboot is skipped instead, below).
A step that needs confirming runs with the confirmation only when the
operator listed its number (0-based) in `confirmed`. A confirmation is a yes
to what the preview showed, so a start with `confirmed` must send the
preview's `plan_id` back (`400 INVALID_REQUEST` without it), and the leader
refuses it when the task, the inputs or the targets have changed since
(`409 TASK_PLAN_CHANGED`: preview again) — else a step number confirmed for
one step could unlock another one, or the same step filled in with another
unit. A start with no confirmation needs no `plan_id`.

The run is recorded in one write, its head. A node's own record is written
when its turn comes; until then the API and the stream show it `pending`.
A write that fails leaves nothing recorded, so the `503` that asks the
operator to retry is true.

A target is left out, with the reason in the plan, when it is offline by the
leader's live heartbeats (`offline`), reports no `tasks-v1`
(`outdated`), is an agent this leader's probe does not reach
(`unreachable`), is held by another run under way (`busy`), or when a node ID
in `targets.nodes` names no node (`unknown`).

### In what order

1. **Agents first, `batch` at a time** (the run's `batch`, else the task's,
   else 1). The agents of a wave run together; the next wave starts once every
   one of them has finished.
2. **Every other manager, one at a time.**
3. **The leader, last**, after it hands the leadership to another manager
   voter: the one online by live heartbeat that reaches the most agents, as
   at the end of a cluster update (§ The leader goes to the manager that
   reaches the most agents). The new leader takes the run over and runs the
   old leader's steps as any manager's.

Within each, nodes go by name, then ID, so a run takes the same order every
time. Each node runs its steps in order, one at a time. A leader with no
other manager voter online to hand over to — a lone manager, or one whose
fellow voters are offline — runs its own steps; a reboot step among them
still passes the quorum check below first. A handover that fails fails the
leader's part of the run (`leadership could not be handed over to …`) and the
run goes on without it: running steps that may restart the leader would leave
the managers without one.

The leader's turn is whichever wave holds the manager leading when that wave
comes. Leadership may move during a run for other reasons — the leader goes
to the manager that reaches the most agents — and a run does not hold it
back: the new leader takes the run over (§ A new leader takes over).

### Failure and stop

- **A failed step stops its node**, unless the step says
  `continue_on_error`. A step fails when its job fails or the node refuses it;
  the step's summary carries the node's code and reason
  (`INVALID_PATH: …`, `exit 3`, `timed out after 10m`).
- **`stop_on_failure`** (the default) starts no later wave once a wave has a
  failed node: the wave under way runs to its end, then every node not yet
  started is recorded `stopped` and the run `failed`. Without it every wave
  runs, and the run still ends `failed`.
- **A node busy with another run's job** (`409 TASK_NODE_BUSY` from the node)
  fails, its reason naming that job, run and task.
- **Unknown.** A node that did not answer the job's start (posted three
  times, two seconds apart), stopped answering about it for five minutes, or
  no longer has it, ends `unknown`, never `succeeded`. Unknown counts as failed
  for `stop_on_failure`.
- **Stop** (`POST …/task-runs/<id>/stop`) starts no new step and lets the
  jobs running end on their nodes: a node part-way through stops after the
  step it is on, the nodes not yet started are recorded `stopped`, and the
  run ends `stopped` (`stopped by <operator>`).
- **A forced stop** (`{"force": true}`, also on a run already stopping) also
  cancels the command jobs under way — their transient unit is stopped — and
  calls off a reboot not yet under way (its timer is stopped). A reboot whose
  timer has already fired (`systemctl is-system-running` says `stopping`)
  cannot be called off: its job stays `rebooting` and the next boot settles
  it by its boot ID. Package,
  service, file and stack steps always run to their end: cutting apt short can
  leave dpkg broken.

### Quorum and Proxmox VE

- **Before a manager's reboot step** the leader checks that the other manager
  voters online by live heartbeat are still a majority of all manager voters.
  If not, the step and the node are recorded `skipped` (`quorum`) and the run
  ends `stopped` (`quorum: rebooting <name> would leave the managers without a
  quorum`). Two voters therefore never reboot each other through a task. The
  only manager voter is rebooted: it has no other manager to keep a quorum
  for, and the run goes on once it is back.
- **One run at a time reboots managers.** The check above sees the managers
  online, and a manager whose reboot another run has just scheduled is still
  online for a few seconds; two runs rebooting different managers could each
  pass it. So a run whose document has a reboot step and whose nodes include
  a manager is refused while another such run is under way
  (`409 TASK_REBOOT_BUSY`, `details.run_id`). Runs that reboot agents only
  are not held back.
- **A Proxmox VE host refuses reboot steps** (`TASK_REBOOT_PVE_HOST`): the
  panel does not know the guests running on it, which may be this cluster's
  own nodes. The preview shows the refusal, and the run records the step
  `skipped` (`Proxmox host`) and goes on to the node's next step.

### A new leader takes over

The runs are in Raft, so whichever manager leads takes them over. Every five
seconds the leader, once its state holds every committed entry, starts a
runner for each run in progress that has none; a manager that no longer leads
stops its runners, leaving each run as recorded. A run left `handing_over`
goes back to `running`. The API's writes wait for the same thing: until a
new leader has caught up, the task and task-run writes and the preview
answer `503 INTERNAL_ERROR` (`The cluster leader is catching up: retry in a
few seconds`) and write nothing, as a stop judged from a state that misses
the run's end would undo it.

No job is started twice. Each step's job ID (`<run-id>-<step>`) is recorded
before the job is posted, and a node keeps one job per ID: posted again, it
answers with the job it has. For a step recorded `starting` a new leader asks
the node for that ID first: a job the node has is followed, never posted
again; one it has no record of means the post never arrived, and the step is
a new one again — a stop recorded since applies to it, and so does the
quorum check of a manager's reboot — posted with the recorded ID; a node that
does not answer gets the post again with that ID. A step recorded `running`
is only asked about. A job the node no longer has is `unknown`, and is never
started again; rerun the node to run it.

A leader records a step's end and its node's end in two writes, so a leader
lost between them leaves the node `running` with the step ended. The leader
that takes over closes such a node from its own record before running
anything: the first step recorded `failed` (without `continue_on_error`)
ends it `failed`, `unknown` ends it `unknown`, `cancelled` ends it `stopped`;
a node whose every step has ended otherwise ends `succeeded`, whatever its
health, as no step is left to post.

A run already `stopping` when a leader takes it over ends only once the
nodes the former leader left `running` are closed: a step in flight is
followed to its end, or cancelled when the stop was forced, no new step
starts, and a node whose steps had all ended is closed from its record as
above. An ended run never lists a node as `running`.

A job does not depend on the leader. Command and package jobs run in
transient units that a panel restart on the node does not stop either: the
node takes them back at boot. A stack step runs inside the panel, and a panel
restart in the middle of it leaves the job `interrupted`, which the run
records as unknown.

### Running failed nodes again

`POST …/task-runs/<id>/rerun` starts a new run of the same document snapshot
inputs and confirmations — not the task as it is now, which may have changed
or been deleted — over the nodes chosen. The nodes are ordered anew over the
cluster as it is now, as for a new run: agents in batches, then the other
managers one at a time, then the leader, so a leadership that moved puts the
new leader last and a node promoted since gets a wave of its own; a node
offline, outdated, out of reach or held by another run is left out, and with
none left the rerun is `400 TASK_NO_TARGETS` (`409 TASK_TARGET_BUSY` when one
is held). `?only=failed` takes
every node that did not succeed (failed, unknown, skipped, stopped, never
started); without it the body's `{"nodes": […]}` names them. Each node starts
at its first step that did not succeed, and the steps before it read
`succeeded in run <id>`; a node whose every step succeeded starts from the
first. The run being rerun must have ended (`409 TASK_RUN_ACTIVE`). The new
run passes the same start checks as any, `TASK_REBOOT_BUSY` included; a
chosen node that goes offline before its turn comes fails.

### What a run waits for, and what waits for a run

A task run, a cluster update, a CA rotation, a conversion and a promotion
are judged at their start under one lock on the leader, so two arriving
together cannot both pass:

- A run is refused while a cluster update is running
  (`409 UPDATE_IN_PROGRESS`), a CA rotation is under way
  (`409 CLUSTER_CA_ROTATING`), or a manager is being converted or an agent
  promoted (`409 TASK_CLUSTER_CHANGING`, `details.what` `conversion` or
  `promotion`).
- A cluster update, a CA rotation, a conversion and a promotion are refused
  while any run is under way, whatever its nodes
  (`409 TASK_RUN_ACTIVE`, `details.run_id`): each restarts or re-keys nodes
  under the run's steps. The runs are read from replicated state, so this
  holds across a change of leader.
- Two runs may run at once when their nodes do not overlap. A node held by a
  run under way is left out of the next run's plan (`busy`); a start that
  would leave no node, or that loses a race for a node to a run recorded
  first, is `409 TASK_TARGET_BUSY` (`details.node_id`, `run_id`).
- Two runs that both reboot a manager do not run at once
  (`409 TASK_REBOOT_BUSY`, § Quorum and Proxmox VE).

### Mixed versions

Every node whose code runs task jobs says `tasks-v1` in its heartbeat. A node
that does not is left out of a run (`outdated`: update it). A run — and a
rerun — starts only when every manager other than the leader is online and
says it: `409 TASK_MANAGERS_NOT_READY`, whose `details.managers` names each
one with why (`(not heard from)`, `(offline)`, or the release it runs).
Definitions and run records are written with the config commands every
manager release applies; there is no new Raft command.

### Audit

The leader writes two rows to its own audit log for each run, under the name
of the operator who started it:

- `POST /api/v1/cluster/task-runs/<id>#start`, status 202, when the run is
  recorded;
- `TASK /api/v1/cluster/task-runs/<id>#end:<status>`, status 200, written by
  the leader that ends it — after a change of leader, another manager than
  the one holding the start row.

Each node's audit log has the job starts (`POST /api/v1/task-jobs`) under the
same operator name, the preview's checks under the operator who asked for
the preview, and a forced stop's cancels under the operator who stopped the
run or forced the stop (the run's `stopped_by`, else who started it). Inputs are
never written to an audit row or the panel's log. They are kept in the run's
record, which every manager holds and the API returns, and in the job specs
on each node: do not pass secrets as inputs.

### Storage and retention

| What | Where | Kept |
|------|-------|------|
| Task definitions | Raft FSM `task/<id>` (JSON) | until deleted |
| The document a run started from | `taskrev/<sha256 of the YAML>` | while a kept run uses it — one copy however many runs use it |
| A run's head | `taskrun/<run-id>` (JSON), written once at the start | the 200 newest runs, and every run in progress |
| One node's part of a run | `taskrun/<run-id>/n/<node-id>` (JSON), written from the node's turn on; none before (read as `pending`) | with its run |
| A node's jobs | that node's SQLite, table `task_jobs` | the node's 200 newest jobs, none older than 30 days; a job running or waiting on its reboot is kept |
| A job's output | that node's `/var/log/sfpanel/task-jobs/<job-id>.log` (directory `0700`, file `0600`) | with its job |

The leader prunes the runs as each one ends; a node sweeps its jobs as it
starts one and at boot, file and row together. A step's summary in the run
record is cut to 200 bytes; the output stays on the node, and
`GET …/task-runs/<id>/nodes/<node-id>/steps/<n>/output?skip=<bytes>` asks the
node for it in pieces of at most 256 KiB, sanitized as logs are. A node not
reached (`502 CLUSTER_NODE_UNREACHABLE`) or one that has swept the job
(`404 TASK_JOB_NOT_FOUND`) is answered as such instead. Run IDs are
`YYYYMMDD-HHMMSS-<6 hex>` in UTC, so they sort by start.

### The node's side: one job at a time

Each step reaches its node as a job on `/api/v1/task-jobs`, on managers and
agents alike. Those routes take the task engine's signed calls only; a
browser's `?node=` request for one is refused by the relaying manager
(`403 TASK_JOB_CLUSTER_ONLY`), so no step runs past the run's gates and
audit.

- **One job at a time per node.** A job running or waiting on its reboot
  holds the node; another is refused with `409 TASK_NODE_BUSY`, naming the
  job that holds it. The hold is the job's row in `task_jobs`, so it survives
  a panel restart.
- **Command** steps run as root in a transient unit,
  `sfpanel-task-<job-id>.service`, with `RuntimeMaxSec` set to the step's time
  limit. The script is written beside the log (`<job-id>.sh`, `0700`) and
  removed when the command ends; its output is appended to the job's log, and
  the last line, `[sfpanel-task] exit=<n>`, says how it ended. A command always
  counts as a change. On a host without systemd the panel runs it as its own
  child, and a panel restart ends it.
- **Packages** steps are the Packages page's own transactions, behind its
  guards: the lists refresh first when `update` is set, then the upgrade,
  install or removal in an `sfpanel-apt-<id>.service` unit, which a panel
  restart does not stop. The leader never cancels one; a node asked to with
  force lets the transaction under way run to its end and starts none after
  it.
- **Service** steps run `systemctl <action> <unit>` behind the Services
  page's guard; a unit the page would ask about runs only when the step was
  confirmed.
- **File** steps write behind the file manager's write guard (so
  `/etc/sfpanel`, `/root/.ssh` and `/etc/sudoers.d` are refused), at most
  10 MiB, by an atomic rename and without a `.bak`. The guard follows every
  symlink on the way, a linked parent of a file not created yet included,
  and the step checks its path again once it has made its directories. A
  file that already holds the content, mode and owner is left alone and
  reads `unchanged`.
- **Stack** steps create the stack, or update its compose file when it
  differs, behind the compose endpoints' name rule and safety tiers
  (forbidden always refused, risky only when confirmed); with `up` they run
  `docker compose up -d` for at most 30 minutes (enough to pull images),
  each line of its output reaching the job's log as compose prints it.
- **Reboot** steps with `only_if_required` look for `/var/run/reboot-required`
  and, without it, succeed at once as `not required`. Otherwise the reboot is
  scheduled five seconds out (`systemd-run --on-active=5
  --unit=sfpanel-task-reboot-<job-id> systemctl reboot`) and the job waits,
  `rebooting`, until a panel starts in another boot — the kernel's boot ID
  changed — and settles it `succeeded`. If the step's `wait` runs out first,
  it fails. A node without systemd refuses a reboot it would have to schedule
  (`TASK_REBOOT_UNSUPPORTED`).

### Following a run

`GET …/task-runs/<id>/stream` is a server-sent event stream that whichever
manager receives it answers from its own replicated state; it is never sent
to the leader, so it follows a run across a change of leader. It sends a
`snapshot` of the run first, then a `node` event for each node record that
changes and a `run` event when the head does, `: keepalive` after 15 seconds
of quiet, and `end` once the run has ended. A client that comes back starts
again from a snapshot. `GET …/task-runs/<id>` answers the same records once.
See `websocket-spec.md`.

---

## See also

- `docs/specs/api-spec.md` — `/cluster/*` REST routes
- `docs/specs/websocket-spec.md` — WS relay through cluster proxy
- `docs/specs/cluster-partition-runbook.md` — partition detection and recovery
