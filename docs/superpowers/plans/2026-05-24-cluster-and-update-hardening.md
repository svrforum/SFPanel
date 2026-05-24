# Cluster + Update Hardening Implementation Plan (2026-05-24)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the 9 Critical and 12 High findings from the 2026-05-24 four-domain review (fresh install / single-node update / cluster lifecycle / cluster runtime+orchestrator), in order of operator blast radius, without regressing the existing v0.15.0-v0.15.2 work.

**Architecture:** Six sequential release phases (`v0.15.3` → `v0.16.x`), each independently shippable. Phase 1 lands the 4 most user-impacting Critical fixes as a hot-fix release; Phase 2 closes the remaining 5 Critical issues; Phases 3-5 work through the High items per domain; Phase 6 is a convention sweep. Every behavioural fix gets a TDD-pinning test, and several fixes share a single helper so the same invariant only has one home.

**Tech Stack:** Go (echo, database/sql + SQLite WAL, gRPC mTLS via hashicorp/raft, cosign go-lib), unchanged. New tests use `httptest`, `exec.MockCommander`, and stub Raft managers (the existing pattern from the v0.15.0 work).

---

## Scope check & ground rules

This plan touches all four domains the review covered. It is **not** a single PR — each phase is shippable as its own release. The phasing is deliberate:

- **Phase 1 (v0.15.3)** = security hot-fix. Lands within days. Limited to issues that have a direct external attack surface or break common operator flows.
- **Phase 2 (v0.15.4)** = data integrity / quorum safety. One week behind Phase 1.
- **Phase 3 (v0.15.5)** = single-node update hardening. Watchdog, ctx propagation, .bak rotation.
- **Phase 4 (v0.16.0)** = cluster lifecycle hardening. Join/leave/remove/transfer correctness.
- **Phase 5 (v0.16.1)** = orchestrator improvements. Cross-version graceful, SSE error detection, version skip.
- **Phase 6 (v0.16.2)** = convention sweep. `log/slog` migration in main+CLI, raw-error sanitisation.

**Stability principles applied throughout:**

1. **TDD-first**: every behavioural change has a failing test before code, so the test pins the intent and a later refactor can't silently undo it.
2. **Single-file blast radius per task** (mostly). Where a fix spans two files (e.g., header signer + verifier), the task still scopes to one logical concern.
3. **Backwards-compatible**: no API/route renaming, no breaking response-shape changes. Wire-format additions only (e.g., new `step:"skipped"` SSE event in Phase 5).
4. **Pre-merge gate**: `make ci` (lint + test + build) green before every commit. Race tests (`go test -race`) for any concurrency-sensitive change (Phase 2 C6, Phase 4 JoinCluster mutex).
5. **Production deploy cadence**: ship one phase per release, monitor `/var/log/sfpanel/sfpanel.log` for 24h before the next. The user runs the panel on this same host (port 3628), so the churn cost of a bad release is high.
6. **Commit author**: every commit uses `GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com` (same for committer). No AI references in messages, no `--no-verify`.
7. **Cross-version aware**: the cluster currently contains two nodes both on v0.15.2 (verified via `sfpanel cluster list`). Any fix that touches the inter-node wire (Phase 1 C1, Phase 5 orchestrator) must remain compatible with the prior version until a release deprecates support.

---

## File structure overview

| Phase | Files created | Files modified |
|-------|---------------|----------------|
| 1.1 | none | `internal/api/middleware/proxy.go`, `internal/api/middleware/proxy_test.go` |
| 1.2 | none | `internal/feature/auth/handler.go`, `internal/feature/auth/handler_test.go` |
| 1.3 | none | `internal/feature/system/handler.go`, `internal/release/cosign.go`, related tests |
| 1.4 | none | `internal/feature/system/handler.go`, `internal/feature/system/handler_test.go` |
| 2.1 | none | `internal/feature/system/handler.go` |
| 2.2 | none | `internal/cluster/manager.go` (Leave quorum guard), `internal/feature/cluster/handler.go` (LeaveCluster) |
| 2.3 | none | `internal/feature/cluster/handler.go` (InitCluster SetConfig errs) |
| 2.4 | none | `cmd/sfpanel/cluster_commands.go` (CLI init sync) |
| 2.5 | none | `cmd/sfpanel/main.go` (bgCtx propagation to sync goroutine) |
| 3.x | none | `internal/feature/system/handler.go`, `cmd/sfpanel/watchdog.go` |
| 4.x | none | `internal/cluster/manager.go`, `internal/feature/cluster/handler.go`, `cmd/sfpanel/cluster_commands.go` |
| 5.x | none | `internal/feature/cluster/handler.go` (ClusterUpdate orchestrator) |
| 6.x | none | `cmd/sfpanel/main.go`, `cmd/sfpanel/cluster_commands.go`, others as touched |

---

# Phase 1 — Security hot-fix (v0.15.3) ✅ SHIPPED 2026-05-24

The 4 most user-impacting Critical issues. Each is its own commit; the release is cut after all four merge.

> **Status:** Complete. Commits `a8d6f7f` (C1), `bfcbf46`+`b504f52` (C2), `3e509ca` (C3), `002c244` (C4). Tagged `v0.15.3` and pushed to origin. CHANGELOG updated.

---

## Task 1.1 — Fix `setAuthHeaders` v2 sig path-vs-RequestURI mismatch (C1)

**Files:**
- Modify: `internal/api/middleware/proxy.go:141`
- Test: `internal/api/middleware/proxy_test.go` (extend if exists, create if not)

**Why:** `setAuthHeaders` signs the v2 HMAC over `origReq.URL.Path` (path only). The receiver in `internal/auth/proxyauth.go:91` verifies against `r.URL.RequestURI()` (path + query). Every cross-node HTTP relay carrying a query string fails the v2 check, and because the receiver short-circuits v1 fallback when v2 is present, the request is rejected with 401. Operator-visible breakage: `?node=N1 /api/v1/files/download?path=...`, `/api/v1/files/upload?path=...`, `/api/v1/system/restore` query-bearing paths, `/api/v1/logs/read?source=...`.

**Regression risk:** Very low. Aligning the signer with the verifier removes false-negatives; nothing was relying on the broken behaviour because nothing worked.

- [x] **Step 1.1.1: Write the failing test**

Append to `internal/api/middleware/proxy_test.go` (create the file if it doesn't yet exist):

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/svrforum/SFPanel/internal/auth"
)

func TestSetAuthHeaders_V2SignsPathPlusQuery(t *testing.T) {
	auth.SetClusterProxySecret("test-secret-32-bytes-long-enough!!")
	defer auth.SetClusterProxySecret("")

	// Inbound request carries a query string — typical of cross-node file
	// download, log read, etc.
	orig := httptest.NewRequest("GET", "/api/v1/logs/read?source=syslog&lines=8", nil)

	// setAuthHeaders mutates this outbound request.
	out, _ := http.NewRequest("GET", "http://peer:3628/api/v1/logs/read?source=syslog&lines=8", nil)

	setAuthHeaders(out, orig, nil)

	v2 := out.Header.Get(auth.InternalProxyHeaderV2)
	if v2 == "" {
		t.Fatal("v2 header not set")
	}
	// Replay the outbound on the receiver — IsInternalProxyRequest reads
	// r.URL.RequestURI() (path+query) for validation. If the signer used
	// path-only, validation fails.
	if !auth.IsInternalProxyRequest(out) {
		t.Errorf("receiver rejected v2 sig — signer used path-only but verifier uses path+query")
	}
}
```

- [x] **Step 1.1.2: Run, confirm FAIL**

```bash
cd /opt/stacks/SFPanel
go test ./internal/api/middleware/ -run TestSetAuthHeaders_V2SignsPathPlusQuery -v
```

Expected: FAIL — receiver rejects v2.

- [x] **Step 1.1.3: Apply the fix**

In `internal/api/middleware/proxy.go:141`, change:

```go
		if v2 := authpkg.SignProxyRequestV2(origReq.Method, origReq.URL.Path); v2 != "" {
```

to:

```go
		if v2 := authpkg.SignProxyRequestV2(origReq.Method, origReq.URL.RequestURI()); v2 != "" {
```

`RequestURI()` returns `path + "?" + rawQuery` exactly as the receiver consumes it.

- [x] **Step 1.1.4: Re-run, confirm PASS**

```bash
go test ./internal/api/middleware/ -run TestSetAuthHeaders_V2SignsPathPlusQuery -v
```

- [x] **Step 1.1.5: Run full middleware + auth suites**

```bash
go test ./internal/api/middleware/... ./internal/auth/... -count=1
PATH=/home/devuser/go/bin:$PATH golangci-lint run ./...
```

- [x] **Step 1.1.6: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
add internal/api/middleware/proxy.go internal/api/middleware/proxy_test.go && \
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
commit -m "proxy: sign v2 over path+query so cross-node relay with query string passes"
```

---

## Task 1.2 — Refuse `/auth/setup` when cluster FSM already holds an admin (C2)

**Files:**
- Modify: `internal/feature/auth/handler.go:591-660` (`GetSetupStatus`, `SetupAdmin`)
- Test: `internal/feature/auth/handler_test.go`

**Why:** Both endpoints decide "setup required" from the local SQLite `admin` table only. A node that joined an existing cluster has an empty local table (admin lives in the Raft FSM); the bootstrap endpoint stays open and accepts a new admin row. If that node ever wins a leadership term, FSM admin is overwritten by attacker data. Single-node installs never see this surface, so the bug can sit hidden forever.

**Regression risk:** Low — adding a more restrictive condition only refuses cases that were previously broken. Existing successful single-node `/auth/setup` flows still pass because cluster manager is nil and the cluster check short-circuits.

- [x] **Step 1.2.1: Write the failing test**

In `internal/feature/auth/handler_test.go`, add:

```go
func TestSetupAdmin_RefusesWhenClusterFSMHoldsAdmin(t *testing.T) {
	h := newTestAuthHandler(t) // existing helper
	// Local DB is empty (no admin row). Inject a cluster manager whose FSM
	// reports an existing admin so GetSetupStatus / SetupAdmin can consult it.
	h.SetClusterManagerForTest(stubClusterManagerWithAdmin("admin", "hash"))

	rec := h.do(t, "POST", "/auth/setup", `{"username":"intruder","password":"verylongpassword12345"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("SetupAdmin: status=%d, want 409 (cluster admin already exists)", rec.Code)
	}
}

func TestGetSetupStatus_FalseWhenClusterFSMHoldsAdmin(t *testing.T) {
	h := newTestAuthHandler(t)
	h.SetClusterManagerForTest(stubClusterManagerWithAdmin("admin", "hash"))

	rec := h.do(t, "GET", "/auth/setup-status", nil)
	// Body should report setup_required: false.
	var body struct {
		Data struct {
			SetupRequired bool `json:"setup_required"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Data.SetupRequired {
		t.Error("setup_required: true returned even though FSM admin exists")
	}
}
```

`stubClusterManagerWithAdmin` and `SetClusterManagerForTest` may need to be added or extended. Match the existing test scaffolding pattern (`refresh_test.go` uses similar seams).

- [x] **Step 1.2.2: Run, confirm FAIL**

```bash
go test ./internal/feature/auth/ -run "TestSetupAdmin_RefusesWhenClusterFSMHoldsAdmin|TestGetSetupStatus_FalseWhenClusterFSMHoldsAdmin" -v
```

Expected: FAIL — current code returns 200 + setup_required:true / accepts SetupAdmin.

- [x] **Step 1.2.3: Apply the fix**

In `GetSetupStatus`:

```go
func (h *Handler) GetSetupStatus(c echo.Context) error {
	if mgr := h.getClusterMgr(); mgr != nil {
		if accounts := mgr.GetAccounts(); len(accounts) > 0 {
			return response.OK(c, map[string]bool{"setup_required": false})
		}
	}
	var count int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM admin").Scan(&count); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Database error")
	}
	return response.OK(c, map[string]bool{"setup_required": count == 0})
}
```

In `SetupAdmin`, before the existing `SELECT COUNT(*) FROM admin` check inside the tx, add the same cluster pre-check:

```go
if mgr := h.getClusterMgr(); mgr != nil {
	if accounts := mgr.GetAccounts(); len(accounts) > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists,
			"Cluster admin already configured; this node cannot bootstrap a separate admin")
	}
}
```

The exact accessor (`mgr.GetAccounts()`) may be named differently in the real code (`mgr.ListAdmins`, `mgr.AdminAccounts`, etc.). Use whatever the existing FSM accessor is — read `internal/cluster/raft_fsm.go` first to confirm the method name and return shape.

- [x] **Step 1.2.4: Re-run, confirm PASS**

- [x] **Step 1.2.5: Full auth suite + race + lint**

```bash
go test -race ./internal/feature/auth/... -count=1
PATH=/home/devuser/go/bin:$PATH golangci-lint run ./internal/feature/auth/...
```

- [x] **Step 1.2.6: Commit**

```bash
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
add internal/feature/auth/handler.go internal/feature/auth/handler_test.go && \
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
commit -m "auth: refuse /auth/setup when cluster FSM already holds an admin"
```

---

## Task 1.3 — Enforce Sigstore signature on update (close cosign optional bypass) (C3)

**Files:**
- Modify: `internal/feature/system/handler.go:248-269` (the `if sigURL != "" && certURL != ""` block)
- Modify: `internal/release/release.go` or `internal/release/version.go` — add a `SignatureRequiredSince` constant
- Test: `internal/feature/system/handler_test.go`

**Why:** Today, if the release JSON omits `.sig`/`.pem`, the verifier falls through to SHA-256-only. An attacker who controls the GitHub release page just deletes those two asset entries and uploads a malicious tarball + matching checksums.txt. Cutoff is needed: any release at or after a known signing baseline must produce a signature, otherwise abort. The repository has been signing every release since v0.13.0 (per CHANGELOG), so `v0.13.0` is the natural cutoff.

**Regression risk:** Very low for forward updates (every release the codebase ever shipped at v0.13.0+ carries a signature). For pre-v0.13.0 → v0.15.3 first updates, the fallback still applies and SHA-256 governs.

- [x] **Step 1.3.1: Add the cutoff constant**

In `internal/release/release.go` (or wherever `CompareVersions` lives — read to confirm):

```go
// SignatureRequiredSince is the first SFPanel release that ships a Sigstore
// signature. Updates targeting this version or later MUST carry both
// checksums.txt.sig and checksums.txt.pem; missing them aborts the update
// to prevent supply-chain downgrade of the verification policy. Older
// targets fall back to SHA-256 only (preserves the one-time upgrade path
// from pre-signed releases).
const SignatureRequiredSince = "0.13.0"
```

- [x] **Step 1.3.2: Write the failing test**

```go
func TestRunUpdate_RejectsMissingSignatureForRecentTarget(t *testing.T) {
	// Stand up an httptest server that serves a release manifest declaring
	// v0.15.4 as latest, with checksums.txt and the tarball, but NO sig/pem.
	server := newFakeReleaseServer(t, releaseFixture{
		Version: "v0.15.4",
		Assets:  []string{"sfpanel_0.15.4_linux_amd64.tar.gz", "checksums.txt"},
		// no .sig, no .pem
	})
	defer server.Close()

	h := &Handler{
		Cmd:               exec.NewMockCommander(),
		ReleaseAPIURLBase: server.URL,
		// other fields as the existing test scaffold sets them
	}

	rec := postUpdate(t, h)
	if rec.Code != http.StatusOK { // SSE stream begins 200 then emits step:error
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"step":"error"`) {
		t.Errorf("expected step:error in SSE body, got %q", body)
	}
	if !strings.Contains(body, "signature required") {
		t.Errorf("expected refusal message mentioning 'signature required', got %q", body)
	}
}
```

If `newFakeReleaseServer` / `releaseFixture` / `postUpdate` don't exist, add minimal scaffolding (≤30 LOC).

- [x] **Step 1.3.3: Run, confirm FAIL**

- [x] **Step 1.3.4: Apply the fix**

In `internal/feature/system/handler.go` around line 248-269, replace the `if sigURL != "" && certURL != ""` block + `else` branch with:

```go
sigURL := release.FindAssetURL(ghRelease.Assets, "checksums.txt.sig")
certURL := release.FindAssetURL(ghRelease.Assets, "checksums.txt.pem")

requireSig := release.CompareVersions(latest, release.SignatureRequiredSince) >= 0
if sigURL == "" || certURL == "" {
	if requireSig {
		sendEvent("error", fmt.Sprintf(
			"Signature required: release %s is missing checksums.txt.sig or checksums.txt.pem", latest))
		return nil
	}
	sendEvent("verifying", "Release predates Sigstore signing; falling back to SHA-256 only")
} else {
	sendEvent("verifying", "Verifying release signature (Sigstore keyless)...")
	sigBytes, sigErr := fetchBytes(dlClient, sigURL)
	if sigErr != nil {
		sendEvent("error", fmt.Sprintf("Signature download failed: %v", sigErr))
		return nil
	}
	certBytes, certErr := fetchBytes(dlClient, certURL)
	if certErr != nil {
		sendEvent("error", fmt.Sprintf("Cert download failed: %v", certErr))
		return nil
	}
	if vErr := release.VerifyCosignBlob(checksumBody, sigBytes, certBytes, release.SFPanelReleaseIdentity()); vErr != nil {
		sendEvent("error", fmt.Sprintf("Signature verification failed: %v", vErr))
		return nil
	}
}
```

- [x] **Step 1.3.5: Re-run, confirm PASS**

- [x] **Step 1.3.6: Lint + run system tests including race**

```bash
go test -race ./internal/feature/system/... ./internal/release/... -count=1
PATH=/home/devuser/go/bin:$PATH golangci-lint run ./...
```

- [x] **Step 1.3.7: Commit**

```bash
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
add internal/feature/system/handler.go internal/release/release.go internal/feature/system/handler_test.go && \
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
commit -m "system: require Sigstore signature for releases at or after v0.13.0"
```

---

## Task 1.4 — Cap tar entry size to prevent gzip-bomb OOM (C4)

**Files:**
- Modify: `internal/feature/system/handler.go:340-345` (the `io.ReadAll(tr)` for the binary entry; also the equivalent in `RestoreBackup` if not already capped — verify)
- Test: `internal/feature/system/handler_test.go`

**Why:** The on-wire archive is capped at 200 MiB, but `tr` (decompressed) is read with `io.ReadAll` and no size limit. A malicious release tarball with high gzip compression can decompress to many GB and OOM the host. RestoreBackup already streams to disk under a per-entry cap (added in v0.15.0 Task 2.5); the update path was missed.

**Regression risk:** Very low. Legitimate sfpanel binaries are ~40-50 MB; a 256 MiB cap is 5× headroom.

- [x] **Step 1.4.1: Failing test**

```go
func TestRunUpdate_RejectsOversizedBinaryEntry(t *testing.T) {
	// Build a tar.gz whose single sfpanel entry decompresses to 300 MiB
	// (just a long run of zeros — gzip compresses these to ~300 KiB).
	server := newFakeReleaseServerWithCustomTarball(t, "0.15.4", makeOversizedTarball(t, 300*1024*1024))
	defer server.Close()

	h := &Handler{Cmd: exec.NewMockCommander(), ReleaseAPIURLBase: server.URL}
	rec := postUpdate(t, h)
	body := rec.Body.String()
	if !strings.Contains(body, `"step":"error"`) {
		t.Fatalf("expected step:error, got %q", body)
	}
	if !strings.Contains(body, "binary exceeds size cap") {
		t.Errorf("expected cap-rejection message, got %q", body)
	}
}
```

- [x] **Step 1.4.2: Run, confirm FAIL or OOM in test (be ready to kill)**

If the test machine has limited RAM, this could OOM. Run with `GOMAXPROCS=1 ulimit -v 524288 go test ...` to bound it, or skip running until Step 1.4.3 is applied (and confirm by reading code that it would fail).

- [x] **Step 1.4.3: Apply the cap**

Near the top of `handler.go`, add a constant:

```go
// maxBinaryEntryBytes caps the decompressed size of the sfpanel binary inside
// the update tarball. A malicious archive can compress 1000:1 (mostly zeros),
// so the on-wire 200 MiB cap doesn't bound decompression. 256 MiB is 5× the
// real binary size and well below typical free RAM.
const maxBinaryEntryBytes int64 = 256 * 1024 * 1024
```

Replace the `io.ReadAll(tr)` call at line 341:

```go
limited := io.LimitReader(tr, maxBinaryEntryBytes+1)
binaryData, err = io.ReadAll(limited)
if err != nil {
	sendEvent("error", fmt.Sprintf("Extract failed: %v", err))
	return nil
}
if int64(len(binaryData)) > maxBinaryEntryBytes {
	sendEvent("error", fmt.Sprintf("binary exceeds size cap (%d bytes); aborting", maxBinaryEntryBytes))
	return nil
}
```

(`maxBinaryEntryBytes+1` allows reading one byte past the cap so we can detect overflow.)

- [x] **Step 1.4.4: Re-run, confirm PASS**

- [x] **Step 1.4.5: Commit**

```bash
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
add internal/feature/system/handler.go internal/feature/system/handler_test.go && \
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
commit -m "system: cap decompressed binary entry at 256 MiB to prevent gzip-bomb OOM"
```

---

## Phase 1 release ✅ DONE

After Tasks 1.1-1.4 merge, cut **v0.15.3**:

```bash
git -c user.name=svrforum -c user.email=svrforum.com@gmail.com \
tag -a v0.15.3 -m "$(cat <<'EOF'
v0.15.3 — security hot-fix

- proxy: sign v2 over path+query so cross-node relay with query string passes (C1)
- auth: refuse /auth/setup when cluster FSM already holds an admin (C2)
- system: require Sigstore signature for releases at or after v0.13.0 (C3)
- system: cap decompressed binary entry at 256 MiB to prevent gzip-bomb OOM (C4)
EOF
)" HEAD && git push origin main && git push origin v0.15.3
```

---

# Phase 2 — Data integrity & quorum safety (v0.15.4)

The 5 remaining Critical items, focused on operations that can permanently damage state. All are TDD'd; the tests use the same scaffolding as Phase 1 where possible.

---

## Task 2.1 — Abort update when `.bak` backup fails (C5)

**Files:** `internal/feature/system/handler.go:363-365`, plus test.

**Risk:** Low. The change converts silent failure into a hard abort; legitimate runs always succeed at backup.

- [ ] **Step 2.1.1: Failing test** — make `os.WriteFile(bakPath, …)` fail (e.g., point `execPath` at a path whose parent directory is read-only) and assert the update returns `step:"error"` with "backup failed" in the message.

- [ ] **Step 2.1.2: Apply** — replace the silent `_ =` with:

```go
if data, readErr := os.ReadFile(execPath); readErr == nil {
	if err := os.WriteFile(backupPath, data, info.Mode().Perm()); err != nil {
		sendEvent("error", fmt.Sprintf("backup failed: %v", err))
		return nil
	}
} else {
	sendEvent("error", fmt.Sprintf("cannot read current binary for backup: %v", readErr))
	return nil
}
```

- [ ] **Step 2.1.3: Test passes, commit** as `system: abort update when binary backup fails (no silent fallthrough)`.

---

## Task 2.2 — Quorum guard on `LeaveCluster` (C6)

**Files:** `internal/cluster/manager.go:593-661` (`Leave`), `internal/feature/cluster/handler.go:901` (`LeaveCluster` HTTP handler).

**Risk:** Medium. Adds rejection for ops that today succeed (and break the surviving peer). The `?force=true` escape preserves the existing behaviour for emergency drains.

- [ ] **Step 2.2.1: Failing test** in `internal/feature/cluster/handler_test.go`:

```go
func TestLeaveCluster_RefusesWhenWouldBreakQuorum(t *testing.T) {
	h := newTestClusterHandler(t)
	h.setManager(stubManagerWith2VotersOnePeerOffline())

	rec := h.do(t, "POST", "/cluster/leave", nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409 (quorum guard)", rec.Code)
	}
}

func TestLeaveCluster_ForceTrueBypassesGuard(t *testing.T) {
	h := newTestClusterHandler(t)
	h.setManager(stubManagerWith2VotersOnePeerOffline())

	rec := h.do(t, "POST", "/cluster/leave?force=true", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("force=true bypass: status=%d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2.2.2: Apply** — in `LeaveCluster` handler, before calling `mgr.Leave()`:

```go
if c.QueryParam("force") != "true" {
	if reason, blocked := mgr.WouldDropBelowQuorumOnLeave(); blocked {
		return response.Fail(c, http.StatusConflict, response.ErrClusterQuorum,
			"leave refused: "+reason+"; pass ?force=true to override")
	}
}
```

Add `WouldDropBelowQuorumOnLeave() (string, bool)` to `internal/cluster/manager.go`. Returns true and a human-readable reason when removing self would leave the cluster below quorum based on **live heartbeat health** of the remaining voters (not stale FSM status — see Task 4.5 below for the same upgrade applied to `TransferLeadership`).

`response.ErrClusterQuorum` — add to `internal/api/response/errors.go` with `StatusCode() = 409` if not present.

- [ ] **Step 2.2.3: Commit** as `cluster: refuse Leave when remaining voters would lose quorum (force=true to override)`.

---

## Task 2.3 — Check `SetConfig` errors in `InitCluster` and roll back (C7)

**Files:** `internal/feature/cluster/handler.go:225-228`.

**Risk:** Low. Previously errors silently caused permanent FSM inconsistency; now they cause the init to abort cleanly with a clear error.

- [ ] **Step 2.3.1: Failing test** — inject a stub manager whose `SetConfig` returns an error for `"jwt_secret"`, call `InitCluster`, assert 500 with rollback (config.yaml restored to `Enabled=false`, manager shut down).

- [ ] **Step 2.3.2: Apply** — replace the silent calls:

```go
if err := mgr.SetConfig("jwt_secret", h.Config.Auth.JWTSecret); err != nil {
	return h.rollbackInit(c, mgr, fmt.Errorf("persist jwt secret: %w", err))
}
if err := mgr.SetConfig("raft_tls", "true"); err != nil {
	return h.rollbackInit(c, mgr, fmt.Errorf("persist raft_tls flag: %w", err))
}
```

Add `rollbackInit(c, mgr, err) error` helper that:
1. Calls `mgr.Shutdown()` (best-effort).
2. Rewrites `config.yaml` with `Cluster.Enabled = false`.
3. Returns `response.Fail(c, 500, response.ErrInternalError, "cluster init failed: "+err.Error())`.
4. Logs `slog.Error("init rolled back", "component", "cluster", "err", err)`.

- [ ] **Step 2.3.3: Commit** as `cluster: check SetConfig errors in InitCluster and roll back config on failure`.

---

## Task 2.4 — Inline JWT/admin FSM sync in CLI `cluster init` direct mode (C8)

**Files:** `cmd/sfpanel/cluster_commands.go:208-279` (`clusterInit`).

**Risk:** Low. CLI direct mode (used when no server is running) currently relies on the 30-second sync goroutine after systemd restart. Doing the sync inline closes the race.

- [ ] **Step 2.4.1: Failing test** in `cmd/sfpanel/cluster_commands_test.go`:

```go
func TestClusterInit_DirectMode_SyncsAdminAndSecretInline(t *testing.T) {
	// Set up a fresh tempdir config + a local admin row in a temp SQLite DB.
	// Call clusterInit (direct mode). Assert mgr.FSMHasAdmin() returns true
	// before clusterInit returns.
}
```

- [ ] **Step 2.4.2: Apply** — after `mgr.Init()` succeeds and before `mgr.Shutdown()`, call the same `syncAdminToFSM` / `syncJWTSecretToFSM` helpers that `main.go`'s background goroutine uses. Extract those helpers into a `cluster.SyncBootstrapState(mgr, db, jwtSecret)` function so CLI and HTTP paths share one implementation.

- [ ] **Step 2.4.3: Commit** as `cluster: inline FSM bootstrap sync in CLI cluster init direct mode`.

---

## Task 2.5 — Propagate shutdown context to admin/JWT sync goroutine (C9)

**Files:** `cmd/sfpanel/main.go:213-241` (the polling goroutine).

**Risk:** Very low. Changes a sleep loop to a select-on-ctx loop. Behaviour identical except graceful shutdown actually stops the goroutine.

- [ ] **Step 2.5.1: Failing test** — start the goroutine with a closed context, assert it exits within 200ms (vs the current 30s polling deadline).

- [ ] **Step 2.5.2: Apply** — replace the `time.Now().Before(deadline)` loop with:

```go
go safe.Go("cluster-bootstrap-sync", func() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-bgCtx.Done():
			return
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			return
		}
		// existing sync attempt body
	}
})
```

- [ ] **Step 2.5.3: Commit** as `main: bind cluster-bootstrap sync goroutine to shutdown context`.

---

## Phase 2 release

After Tasks 2.1-2.5 merge, cut **v0.15.4**.

---

# Phase 3 — Single-node update hardening (v0.15.5)

Resolves the Update High items.

## Task 3.1 — Propagate `c.Request().Context()` into update download path
- Wrap all `http.NewRequest` (release-api, asset download, checksum, sig, cert) calls in `http.NewRequestWithContext(c.Request().Context(), ...)`.
- Add a test (mock release server) that cancels the request and asserts the SSE stream terminates within 1s.
- Commit: `system: propagate request context to update downloads`.

## Task 3.2 — Watchdog liveness probe before commit
- Modify `internal/feature/system/handler.go:413-431`: after `exec.Command(bakPath, "watchdog-update", ...).Start()`, send `process.Signal(syscall.Signal(0))` 200ms later. If signal fails (process gone), abort with "watchdog failed to start; aborting update".
- Test: replace `.bak` with `/bin/false` and assert update aborts cleanly.
- Commit: `system: verify watchdog process is alive before committing update`.

## Task 3.3 — Use `Run()` for `systemctl restart`, not `Start()`
- Modify `internal/feature/system/handler.go:443-454,460-469`: change the systemd-active branch to `h.Cmd.Run("systemctl", "restart", "sfpanel")` so the call is synchronous. systemd will SIGTERM us during the call; the explicit `os.Exit(0)` afterward is unreachable but kept as a safety net for non-systemd hosts.
- Commit: `system: invoke systemctl restart synchronously so restart failures surface`.

## Task 3.4 — Rotate `.bak` to `.bak.previous` for 2-stage rollback
- Modify `internal/feature/system/handler.go:362-364`: before writing `backupPath`, rename existing `backupPath` to `backupPath + ".previous"` (best-effort). Watchdog tries `.bak` first, then `.bak.previous`.
- Modify `cmd/sfpanel/watchdog.go` to chain rollback targets.
- Commit: `system: preserve previous .bak as .bak.previous for two-stage rollback`.

## Task 3.5 — Sanitise SSE error messages
- Modify `internal/feature/system/handler.go:194,464`: wrap every `sendEvent("error", fmt.Sprintf(...))` body through `response.SanitizeOutput`.
- Commit: `system: sanitize update SSE error messages`.

## Task 3.6 — Restrict HTTP redirects to GitHub hosts
- Modify the HTTP client construction (around `internal/feature/system/handler.go:107,155,213`): set `CheckRedirect` to allow only `*.github.com` and `*.githubusercontent.com` hosts.
- Test: a fake server that redirects to `evil.example.com` aborts.
- Commit: `system: restrict update redirects to GitHub hosts`.

## Phase 3 release

Cut **v0.15.5** after Tasks 3.1-3.6.

---

# Phase 4 — Cluster lifecycle hardening (v0.16.0)

Resolves the cluster lifecycle High items. This is a minor version bump because some semantics change (force flags, leader-only commands).

## Task 4.1 — Hold `configMu` during `JoinCluster` mutations
- Modify `internal/feature/cluster/handler.go:307-334`: wrap `engine.Execute()` in a write-locked region on `h.configMu`. Verify with race-test.
- Commit: `cluster: hold configMu during JoinCluster engine execution`.

## Task 4.2 — `reissue-cert` CLI refuses on followers
- Modify `cmd/sfpanel/cluster_commands.go:171-206` (`clusterReissueCert`): before `IssueNodeCert`, check `ca.key` exists. If absent, exit with "reissue-cert must run on the leader; ca.key not present here".
- Commit: `cluster: clusterReissueCert refuses cleanly on followers (no ca.key)`.

## Task 4.3 — `cluster remove` CLI gains quorum guard + `--force`
- Modify `cmd/sfpanel/cluster_commands.go:649-686` (`clusterRemove`): reuse the new `WouldDropBelowQuorumOnLeave`-style helper (or add `WouldDropBelowQuorumOnRemove(nodeID)` mirror) and gate the remove behind `--force` if it would break quorum.
- Commit: `cluster: clusterRemove CLI requires --force when remove would break quorum`.

## Task 4.4 — `InitCluster` rolls back `Enabled=true` on `LiveActivate` failure
- Modify `internal/feature/cluster/handler.go:142-282` (`InitCluster`): on `LiveActivate` failure, rewrite config.yaml with `Cluster.Enabled = false` and return 500 (not 200 with `live:false`). Combines with Phase 2 Task 2.3 rollback helper.
- Commit: `cluster: roll back Enabled=true on LiveActivate failure`.

## Task 4.5 — `TransferLeadership` uses live heartbeat health, not FSM status
- Modify `internal/cluster/manager.go:1011-1034`: replace `target.Status != StatusOnline` with `heartbeat.CheckHealth()[targetID] != StatusOnline`, and add a `RoleVoter` filter so non-voter join-in-progress nodes can't receive leadership.
- Commit: `cluster: TransferLeadership consults live heartbeat health and voter role`.

## Task 4.6 — `admin DB` lookup failure no longer silent in `InitCluster`
- Modify `internal/feature/cluster/handler.go:229-239`: wrap the `QueryRow` in a switch on `errors.Is(err, sql.ErrNoRows)` (continue) vs other errors (log WARN + proceed; or fail init — operator preference, default WARN + proceed).
- Commit: `cluster: log admin DB lookup failures during cluster init`.

## Phase 4 release

Cut **v0.16.0** after Tasks 4.1-4.6. Note in CHANGELOG: `cluster remove` and `cluster leave` now refuse to break quorum unless `--force`/`?force=true` is passed — this is intentional, document the escape clearly.

---

# Phase 5 — Orchestrator improvements (v0.16.1)

Resolves the orchestrator High items. Each task improves cluster update UX without changing the wire format.

## Task 5.1 — `/cluster/update` skips followers already at target version
- Modify `internal/feature/cluster/handler.go:1056-1066, 1090-1168` (`updateNode`): compare `ni.Version` against the orchestrator-side `latest`. If equal, emit `{step:"skipped", reason:"same_version"}` SSE event and proceed to next node.
- Commit: `cluster: skip orchestrator update for followers already at target version`.

## Task 5.2 — Detect `step:"error"` in follower's `/system/update` SSE body
- Modify `internal/feature/cluster/handler.go:1122-1146`: instead of treating `resp.StatusCode >= 400` as the only failure signal, parse the SSE body for `"step":"error"` tokens and fail the orchestrator step with the detail message.
- Commit: `cluster: orchestrator detects step:error events in follower update SSE body`.

## Task 5.3 — Re-sign v2 sig on gRPC retry
- Modify `internal/api/middleware/proxy.go:576-597` and `internal/feature/cluster/handler.go:870-895`: on retry path, call `setAuthHeaders` again (or its v2-only equivalent) so the retry carries a fresh nonce. Prevents idle-pool 100ms+ reconnect from tripping the replay window.
- Commit: `cluster: re-sign v2 nonce on gRPC proxy retry`.

## Task 5.4 — Switch `/cluster/update` orchestrator from gRPC `ProxyRequest` to direct HTTP relay
- Modify `internal/feature/cluster/handler.go:1090-1168`: call `setAuthHeaders` + direct HTTP POST to peer's panel (over mTLS, same secret) instead of gRPC `ProxyRequest`. This single change closes the cross-version orchestrator block we hit on v0.13.x peers, because the follower never sees a re-validated v2 nonce.
- Test: spin up a fake peer that exposes a SSE update endpoint; assert orchestrator drives it to completion with `step:"complete"` events.
- Commit: `cluster: orchestrator uses direct HTTP relay to follower /system/update (not gRPC ProxyRequest)`.

## Phase 5 release

Cut **v0.16.1**.

---

# Phase 6 — Convention sweep (v0.16.2)

Low-priority, ships when convenient.

- **Task 6.1** — Replace `log.Fatal*` / `log.Printf` in `cmd/sfpanel/main.go` and `cmd/sfpanel/cluster_commands.go` with `slog.Error` + `os.Exit(1)`. Echo logger adapter (one line, internal-only) excepted.
- **Task 6.2** — Atomicize `Manager.version` field with `atomic.Pointer[string]` or `sync.RWMutex`.
- **Task 6.3** — Config persist log line uses DEBUG (not INFO) for the auto-generated-secrets case.
- **Task 6.4** — `install.sh`:
  - `--force` flag to bypass the version-check
  - Validate `SFPANEL_DB_PATH` and `SFPANEL_PORT` ranges before persisting
  - Explicit message on `uninstall` that config + cluster keys are preserved
  - WAL checkpoint before `backup_db`
- **Task 6.5** — `release.parseSemver` rejects `..` in segments.

All six grouped into one commit: `cleanup: convention sweep (log/slog migration, atomic version, install.sh polish)`.

---

# Operator deployment cadence

| Release | Tag | Soak | Notes |
|---------|-----|------|-------|
| v0.15.3 | hot-fix | 24h | Security only; deploy to both nodes (sequential, leader-transfer between) |
| v0.15.4 | stability | 48h | Watch Leave/Disband logs |
| v0.15.5 | update path | 72h | Trigger one self-update on local to exercise new ctx + watchdog code |
| v0.16.0 | cluster lifecycle | 1w | Document `?force=true` changes for operators |
| v0.16.1 | orchestrator | 48h | Confirm rolling update via UI works on next minor release |
| v0.16.2 | convention | as merged | non-functional |

Each release uses the same flow we exercised for v0.15.0-v0.15.2:
1. Commit to main with svrforum env vars.
2. `git tag -a v0.x.y -m "..."`.
3. `git push origin main && git push origin v0.x.y`.
4. GitHub Release workflow auto-publishes tarball + cosign keyless signature.
5. Local: `gh release download v0.x.y`, verify checksum + cosign, atomic binary swap, `systemctl restart sfpanel`.
6. Peer: same. If `/cluster/update` UI button now works (Phase 5 ships), use the UI instead.

---

## Self-review checklist

1. **Spec coverage** —
   - 9 Critical findings: C1 → Task 1.1; C2 → 1.2; C3 → 1.3; C4 → 1.4; C5 → 2.1; C6 → 2.2; C7 → 2.3; C8 → 2.4; C9 → 2.5. ✅
   - 12 High findings: Update High (5) → Tasks 3.1-3.6; Cluster lifecycle High (6) → Tasks 4.1-4.6; Orchestrator High (3) → Tasks 5.1-5.3 (+5.4 architectural improvement). ✅
   - Convention violations (4) → Task 6.1-6.5. ✅
2. **Placeholders** — every Phase 1-2 task lists exact file:line, exact code blocks, exact commit messages. Phase 3-5 outline tasks name the file:line and the change shape; engineer fills in test helpers based on existing scaffolding (acceptable per the plan's "outlined" framing — same convention as the prior plan worked well).
3. **Type consistency** — `WouldDropBelowQuorumOnLeave` (Task 2.2) is reused by `WouldDropBelowQuorumOnRemove` (Task 4.3). `rollbackInit` helper (Task 2.3) is reused by Task 4.4 LiveActivate failure path. `SyncBootstrapState` (Task 2.4) is the shared CLI+HTTP entry point. `SignatureRequiredSince` (Task 1.3) is reused if a future signing policy needs cutoff. ✅
4. **Order matters** — Phase 1 (security) → Phase 2 (data integrity) → 3 (update) → 4 (cluster lifecycle) → 5 (orchestrator) → 6 (convention). Each phase deploys to production and soaks before the next. ✅
5. **Stability** — every task has TDD-first, single-file blast radius (Phase 1-2 strictly, Phase 3+ mostly), backwards-compatible wire format, race-test gate on concurrency changes (2.2, 4.1, 2.5), explicit `?force=true` / `--force` escape on quorum-affecting changes. ✅
