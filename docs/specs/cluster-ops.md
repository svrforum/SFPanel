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
issues with the manager role too.

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
  running (`409 UPDATE_IN_PROGRESS`), and a switch of the internal proxy
  secret to the replicated one less than five minutes old — the secret it
  replaced was derived from `ca.crt`.
- **While it runs, nothing joins or updates.** Join tokens and cluster
  updates are refused with `409 CLUSTER_CA_ROTATING`, conversions and
  promotions with their own `409`, and joins and agent registrations with
  the rotation's message. `sfpanel cluster reissue-cert` refuses on a node
  whose `ca.crt` holds two CAs.
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
  held.
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
| Agent → managers | 3629 | Registration, heartbeats, panel-CA reports, and a rotation's certificate renewal and key, to the leader; the other managers are tried when it cannot be reached |

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
the leader removed it, the CAs it trusts (by fingerprint — two while a CA
rotation is under way), its certificate's expiry and the CA that signed it,
whether its key is present (never the key itself) and whether its panel
answers on loopback. On a
manager, the node list shows it with `kind: agent` and no Raft role; it is
online while its heartbeats arrive.

The agent heartbeats to the leader every 30 seconds and keeps the leader's
address and the manager list in `{Cluster.DataDir}/agent-state.json`. Any
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
agent.
They travel as headers of the import request (`X-SFPanel-Migration-*`),
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
  agent that leaves a finding out gets it refused, not acknowledged. A manager's stack is checked for the forbidden tier
  only, as before: it passed the risky tier when it was created. The
  forbidden tier — binds of `/etc/sfpanel`, `/var/lib/sfpanel`, `/root/.ssh`
  or `/etc/sudoers.d` — is refused for every source.
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

## See also

- `docs/specs/api-spec.md` — `/cluster/*` REST routes
- `docs/specs/websocket-spec.md` — WS relay through cluster proxy
- `docs/specs/cluster-partition-runbook.md` — partition detection and recovery
