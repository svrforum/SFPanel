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
| `requestVote RPC: ... connect: connection refused` repeating on one node | That node can't reach a peer; check the peer's listener (port 9444 + Raft 9445) |

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
