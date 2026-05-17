# Cluster partition runbook

Operator playbook for detecting and recovering from a network partition in
SFPanel's Raft + mTLS cluster. Companion to `cluster-ops.md`, which covers
day-to-day cluster operations.

---

## What can go wrong

A partition cuts the cluster into two (or more) groups that can't reach each
other over the gRPC + Raft transport. Three scenarios to recognise:

1. **Minority partition (no leader).** The smaller side has no quorum, so
   no leader can be elected. Reads served by the local FSM continue (UI
   shows last-known data with no fresh metrics); writes return
   `ErrNotLeader` or hang to the 5 s Apply timeout.
2. **Majority partition (still has a leader).** The larger side has quorum.
   It will elect a new leader within the 2 s `LeaderLeaseTimeout` and
   continue serving writes. The minority side becomes "isolated".
3. **Stale-leader window (≤ 2 s).** Right at the moment the partition
   forms, the old leader on the minority side hasn't realised it lost
   quorum yet. Writes started in this window will hang to the Apply
   timeout, NOT silently succeed — Raft refuses to commit without majority
   acknowledgement. SFPanel's added Barrier check (D.1) shortens this
   window for HandleJoin specifically.

---

## Detection

```
sudo sfpanel cluster status
```

Compare across nodes. Symptoms:

| Symptom | Likely diagnosis |
|---|---|
| Some nodes report `leader: <id>` and others report `leader: ""` | Partition; the empty-leader side is the minority |
| All nodes report different `node_count` for the same cluster | Replicated FSM diverged → split-brain (rare; see Recovery) |
| `journalctl -u sfpanel \| grep "stale-leader suspect"` shows entries | Discarded Apply errors (D.3 logging) — leadership flipped during a write |
| `requestVote RPC: ... connect: connection refused` repeating on one node | That node can't reach a peer; check the peer's listener (port 3629 + Raft 3630) |

Healthy state: every node sees the same `leader`, the same `node_count`,
and `cluster status` returns within ~50 ms.

---

## Recovery — minority partition isolated

Goal: rejoin the minority to the majority.

1. Restore network connectivity between the two sides (firewall rule,
   route, DNS — whichever caused the partition).
2. Within ~10 s the minority side observes the majority leader's
   heartbeat and steps down. Local Raft replays any committed log
   entries it missed.
3. Verify with `sfpanel cluster status` on each node — all should report
   the same leader.

If the minority node was the originator of `cluster disband`, see the
**Forced local disband** section below — its local state may be wiped
while the majority continues.

---

## Recovery — split-brain (FSM diverged)

True split-brain in Raft is rare because both sides need to elect leaders,
which requires quorum on each side. The only case where this happens in
SFPanel is operator-forced: someone ran `cluster disband ?force=true` on a
minority leader that had lost quorum, accepted the split, and the majority
elected its own leader.

Once it has happened:

1. Pick the side you want to keep (usually the side with more nodes / more
   recent data).
2. On every node of the LOSING side: `sudo sfpanel cluster leave` then
   wipe state — `rm -rf /var/lib/sfpanel/cluster/` and the `cluster:`
   block in `/etc/sfpanel/config.yaml`.
3. Restart sfpanel on those nodes; they come back as standalone.
4. Re-issue join tokens from the surviving leader and re-add the now-
   standalone nodes via the normal join flow.

There is no automated split-brain detector — the operator runbook is the
contract. Adding one would require pinging Sigstore-style external
witnesses to break ties, which is out of scope.

---

## Recovery — deadlock from log divergence (2-voter clusters)

When two voters each accept candidate-state log entries during overlapping
elections but neither succeeds in committing (because 2-voter quorum needs
both votes), they end up with diverged uncommitted entries. Pre-vote
rejects each other's log as "older" and the cluster oscillates
Follower → Candidate → Follower indefinitely. Signals:

- Periodic `WARN raft: rejecting pre-vote request since our last term is
  greater: ...` lines on both nodes
- `heartbeat send failed component=cluster error=EOF` every ~60 s
- `ERROR cluster has no leader seconds_without_leader=N` from the
  `LeaderWatcher` once `N` crosses 60 s (logs again every 5 min while the
  condition persists)
- Raft term increments slowly (~1/min) but no `election won` ever lands

No data is lost — quorum-of-2 means none of the diverged entries can have
applied to either FSM. Recovery:

1. Identify which node has the newer log: in either node's journal find
   `last-term=X` (local) and `last-candidate-term=Y` (peer) on the
   pre-vote rejection line. The higher number wins.
2. Stop both `sfpanel` services.
3. Wait 2 s for clean shutdown.
4. Start the newer-log node first. It is briefly a single-node candidate.
5. Start the older-log node within ~10 s. It joins, sees a higher leader
   term, truncates its diverged entries via `appendEntries rejected,
   sending older logs: next=N`, then `pipelining replication`.
6. Confirm with `sfpanel cluster status` — `Role: Leader` on one and
   `Role: Follower` with the leader ID on the other.

Total downtime: ~10–15 s. If you cannot tell which log is newer, picking
either side is acceptable — the chosen side becomes leader and its
diverged entries (if any) become canonical. Since none committed, this
does not change observable FSM state.

---

## Stale-leader writes during the 2 s window

For most write paths the Apply timeout (5 s) plus Raft's quorum requirement
makes stale writes fail loudly — the request hangs and returns an error.
Two paths got specific fences:

- **HandleJoin** (D.1): runs `raft.Barrier(2s)` before issuing the node
  cert. A stale leader can't hand out a cert that the surviving majority
  doesn't accept.
- **PromoteOnHeartbeatIfPending** (D.5): rate-limited per nodeID at one
  attempt per 5 s. A flood of forged heartbeats can no longer churn
  config changes.

Reads (`GetStatus`, `GetNodes`) still serve from local FSM state. A
partitioned follower's UI will show "last good" data until reconnect; we
explicitly do not hide it because that would make the panel unusable
during real but brief network blips.

---

## Forced local disband (`?force=true`)

`POST /api/v1/cluster/disband?force=true` is the escape hatch when the
broadcast Apply fails (typically because the originator lost majority
mid-Apply). Without `force`, the handler now refuses the local-only
fallback and returns `503` with a clear message — the operator must
opt in to creating the split.

Use `force` ONLY when:

- The cluster is unrecoverable (e.g. enough voter nodes wiped that quorum
  can never form again), AND
- You accept that the surviving nodes (if any) will keep running and
  require manual `cluster leave` to clean up.

After a forced disband, treat the surviving side per the split-brain
recovery flow above.

---

## Port migration on a live cluster

Use case: the operator wants to move HTTP / cluster gRPC / Raft ports
to different numbers on an already-running cluster. The default trio
moved from 19443 / 9444 / 9445 to 3628 / 3629 / 3630 in v0.13.4; the
same procedure works for any operator-chosen ports.

The work splits cleanly along risk lines. Run them as two separate
phases, NOT one combined window — Phase 1 has a rollback path that
Phase 2 doesn't.

### Phase 1 — HTTP port (rolling, low risk)

Cluster gRPC + Raft stay on their current ports. HTTP can be flipped
one node at a time; the cluster keeps a leader throughout.

```bash
# on each node, one at a time, with ≥ 10s gap between nodes:
sudo cp /etc/sfpanel/config.yaml /etc/sfpanel/config.yaml.bak-portmig-$(date +%Y%m%d-%H%M%S)
sudo sed -i 's/^\(\s*\)port: <OLD>\b/\1port: <NEW>/' /etc/sfpanel/config.yaml
sudo systemctl restart sfpanel
```

After each node restarts, the FSM-stored `api_address` (used by other
nodes when proxying back to this one) is stale. `verifySelfAddress()`
only auto-corrects on the leader (see `internal/cluster/CLAUDE.md`),
so a follower's address must be PATCHed explicitly from the current
leader:

```bash
JWT=<bearer-from-fresh-login>
LEADER_ID=<current-leader-uuid>
NODE_ID=<this-follower-uuid>
CSRF_VAL=$(openssl rand -hex 16)
curl -X PATCH "http://<leader-host>:<leader-port>/api/v1/cluster/nodes/${NODE_ID}/address" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF_VAL" \
  -H "Cookie: sfpanel_csrf=$CSRF_VAL" \
  -d '{"api_address":"<follower-ip>:<NEW>","grpc_address":"<follower-ip>:<grpc-port>"}'
```

### Phase 2 — cluster gRPC + Raft (synchronised, higher risk)

Raft transport auto-binds to `cluster.grpc_port + 1`, so flipping the
gRPC port also moves the Raft port. The Raft membership (BoltDB log,
in `/var/lib/sfpanel/cluster/raft-log.db`) holds peer addresses
pinned at the old Raft port — those need to be rewritten via
`peers.json` + `RecoverCluster()`, which is the same mechanism
documented for quorum-loss recovery. The difference: every node must
read peers.json at the same boot, so every node needs the file
dropped before its restart.

1. **Prepare** (sfpanel can stay running for this step):
   ```bash
   # on every node:
   sudo cp /etc/sfpanel/config.yaml /etc/sfpanel/config.yaml.bak-portmig2-$(date +%Y%m%d-%H%M%S)
   sudo sed -i 's/^\(\s*\)grpc_port: <OLD>\b/\1grpc_port: <NEW>/' /etc/sfpanel/config.yaml
   # write peers.json with the NEW Raft port for every voter:
   sudo tee /var/lib/sfpanel/cluster/peers.json > /dev/null <<'EOF'
   [
     {"id":"<node-uuid-1>","address":"<ip-1>:<NEW_RAFT_PORT>","non_voter":false},
     {"id":"<node-uuid-2>","address":"<ip-2>:<NEW_RAFT_PORT>","non_voter":false}
   ]
   EOF
   ```

2. **Restart every node within a few seconds** (rolling does NOT work —
   if one node is on the new gRPC port and the other is on the old,
   mTLS handshake fails on both sides):
   ```bash
   # in parallel, e.g. via parallel ssh:
   sudo systemctl restart sfpanel
   ```

3. **Watch for the recovery trace** on each node:
   ```
   Raft peers.json detected — running RecoverCluster
   Raft RecoverCluster complete; peers.json renamed to peers.info
   gRPC server listening addr=0.0.0.0:<NEW>
   ```

4. Leader election runs against the new Raft port. Within ~5 s a
   leader appears (`election won: term=<N> tally=2`).

5. **PATCH every node's `grpc_address`** in the FSM (same PATCH as
   Phase 1, this time setting `grpc_address`):
   ```bash
   curl -X PATCH ... -d '{"api_address":"<ip>:<HTTP>","grpc_address":"<ip>:<NEW_GRPC>"}'
   ```
   Repeat for every node ID. After this the `/cluster/nodes` FSM
   view shows the new gRPC ports and cross-node `?node=<peer>` proxy
   works again.

### What can go wrong

- **Asymmetric Phase 2 restart**: if one node restarts with the new
  gRPC port and another hasn't, they cannot dial each other. Symptom:
  `dial tcp <peer>:<NEW>: connect: connection refused` and the node
  with the old port reports `leader_id=""`. Recovery: bring the
  laggard up on the new port and the cluster re-converges.
- **peers.json on only one node**: only that node rewrites its Raft
  membership. The other still has the old peer addresses. Symptom:
  one side becomes leader of a single-node cluster; the other side
  can't establish quorum. Recovery: drop the matching peers.json on
  the other node and restart it too.
- **Forgetting the FSM PATCH after Phase 2**: cross-node proxy
  continues to dial the old gRPC port and 504s. The `/cluster/nodes`
  view shows the cause (`grpc_address` still pointing at the old
  port). Run the PATCH.

Backups created during the procedure (`config.yaml.bak-portmig*`,
`peers.info`) are kept as the rollback path.

---

## Useful commands

```
sudo sfpanel cluster status                    # one-line per-node state
sudo journalctl -u sfpanel -f --grep cluster   # live cluster log stream
sudo journalctl -u sfpanel \| grep -i raft     # raw HashiCorp Raft messages
sudo sfpanel cluster reissue-cert              # leader-only; rotates this node's cert
```

For deeper introspection, the FSM state is at `/var/lib/sfpanel/cluster/`
in BoltDB form (HashiCorp's `bolt` CLI can dump). Touching it manually
voids the Raft consistency guarantees — use `cluster leave` + rejoin
instead of editing.
