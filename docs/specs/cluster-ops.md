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
UI's cluster update flow uses this pattern by default (`mode=rolling`). If
you fan out manually, wait for `sfpanel cluster status` to report a leader
before continuing.

`mode=simultaneous` is hard-blocked when it would take all voters offline at
once — see `internal/feature/cluster/handler.go` `ClusterUpdate`. The guard
allows simultaneous restart of a single-node cluster (nothing to lose) but
refuses 2+-voter clusters with a clear SSE error.

---

## Join token persistence

Tokens are persisted to `{Cluster.DataDir}/tokens.json` (mode 0600). A leader
restart no longer invalidates pending invites. The file holds the HMAC secret
plus the token map; protect it like any other cluster credential. If the file
is corrupted or deleted, all pending invites become invalid — issue new
tokens with `sfpanel cluster token`.

---

## TLS material

| Material | Lifetime | Rotation | Notes |
|---|---|---|---|
| Cluster CA | 10 years (`tls.go:49`) | Manual, coordinated restart | No in-place rotation — every node must trust the new CA simultaneously |
| Node cert | 5 years (`tls.go:97`) | `sudo sfpanel cluster reissue-cert` | Hot-reload within ≤ 60 s; debounced re-stat at `tls.go:184` |

Both `node.crt` and `node.key` live in `{Cluster.CertDir}` and are written
0600. The cert is technically public material in mTLS, but locking it down
matches the rest of the trust material on the host (config, DB, key).

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
   new CA → start sfpanel.
5. **Verify** with `sfpanel cluster status` after every voter is back. If any
   node is stuck, copy the new CA cert manually and restart that node.

`cachedCAPool` is loaded once at startup (`tls.go:165-180`), so step 4
implicitly requires a restart — there is no hot-reload path for the CA.

---

## Update orchestration

`POST /api/v1/cluster/update` runs from the leader and fans out
`/api/v1/system/update` to every healthy follower in `rolling` (default) or
`simultaneous` mode.

- The simultaneous mode quorum guard refuses to take all voters offline at
  once.
- `/api/v1/system/update` itself is serialised by an in-process mutex; a
  second concurrent call returns `409 UPDATE_IN_PROGRESS`.
- The downgrade guard (`internal/release.IsForwardUpdate`) refuses any
  same-or-older target version. Bypass requires editing the binary.
- Archives are streamed to a temp file (`os.MkdirTemp`) and hashed via
  `TeeReader` so memory-constrained nodes (256 MB cluster nodes) can still
  receive a 200 MiB tarball without OOM.
- `checksums.txt` is verified against a Sigstore keyless cosign signature
  (`checksums.txt.sig` + `checksums.txt.pem`) before any hash inside it is
  trusted. The Fulcio cert's SAN URI must start with
  `https://github.com/svrforum/SFPanel/.github/workflows/release.yml@refs/tags/v`
  and the OIDC issuer extension must be GitHub Actions
  (`https://token.actions.githubusercontent.com`). See
  `internal/release/cosign.go` for the verification path. Releases that
  predate the signing pipeline (no .sig/.pem assets) fall back to plain
  SHA-256 with a notice in the SSE stream.

### Auto-rollback watchdog

Self-update spawns a detached watchdog process from the BACKUP binary
(`/usr/local/bin/sfpanel.bak`) before triggering `systemctl restart`. The
watchdog polls `http://127.0.0.1:<port>/api/v1/system/info` for 90 s. If
the new binary never responds, the watchdog `rename(2)`s `.bak` back over
`sfpanel` and restarts the service. See `cmd/sfpanel/watchdog.go`.

This is a forward-only feature: pre-watchdog binaries used as `.bak`
don't know the `watchdog-update` subcommand and exit 2 immediately. Once
a node has updated *to* a watchdog-aware binary, every subsequent update
inherits the rollback safety net.

If a follower fails its update, rolling mode stops and reports the failed
node. Simultaneous mode is best-effort — failed nodes are reported but the
remaining nodes continue.

---

## Disband and leave

- `cluster leave` removes the calling node from the cluster and exits the
  process (`os.Exit(1)`). systemd's `Restart=always` brings it back up in
  standalone mode.
- `cluster disband` runs from any voter, removes Raft state and TLS material
  cluster-wide via the FSM, and exits each node's process. The local SQLite
  DB is **not** wiped — audit logs, monitor history, cron, and Docker state
  are local-only and persist after the cluster goes away.

If you want a clean wipe after disband, manually delete `{Cluster.DataDir}`,
`{Cluster.CertDir}`, and the `cluster:` block in `config.yaml`.

---

## See also

- `docs/specs/api-spec.md` — `/cluster/*` REST routes
- `docs/specs/websocket-spec.md` — WS relay through cluster proxy
- `docs/superpowers/specs/2026-04-13-cluster-join-redesign.md` — design intent
  for the current join flow
