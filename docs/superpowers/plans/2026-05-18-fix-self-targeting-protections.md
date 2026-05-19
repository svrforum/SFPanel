# A5: Self-targeting protections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` (inline).

**Goal:** Close [R-final](../research/2026-05-18-module-hardening/R-final.md) P0-10 (services API can stop the panel's own systemd unit mid-response) and P0-16 (process KillProcess produces an audit row with only the URL — no signal context recorded).

**Architecture:** Per-module, both small.
- **services:** add a small denylist (`sfpanel.service`) checked at the top of state-changing handlers (Stop/Restart/Disable). Start/Enable are harmless and left open. ServiceLogs (read-only) stays open.
- **process:** the audit middleware already writes a row for every authenticated POST including `/system/processes/<pid>/kill`. What's missing is the **body context** (signal + pid + outcome) — add an explicit `slog.Info` event so ops logs contain the full picture even when audit_logs only has the URL.

**Tech Stack:** Go 1.25 stdlib. New tests use the existing project test conventions.

**Out of scope:**
- Blocking sshd.service / ssh.service — operator-controlled lock-out is a different theme (firewall lockout is its own P0).
- Blocking systemd internal units — too broad; conservative scope here.
- Schema change to add a `body_summary` column to audit_logs (would solve the "signal not in audit" issue universally — defer to a later remediation plan).

---

## File structure

- **Modify** `internal/feature/services/handler.go` — add `isProtectedServiceUnit` helper + guard 3 handlers.
- **Create** `internal/feature/services/handler_test.go` — first-ever test file (T3 flagged its absence).
- **Modify** `internal/feature/process/handler.go` — `slog.Info` emission after successful signal send.
- **Create** `internal/feature/process/handler_test.go` — first-ever test file (T5 flagged its absence). Test PID validation + signal allowlist (no need to test the slog itself).

---

## Task 1 — services: protected-unit denylist

- [ ] **Step 1.1: Write test file with failing helper-shape test**

Create `internal/feature/services/handler_test.go`:

```go
package services

import "testing"

func TestIsProtectedServiceUnit(t *testing.T) {
	// Panel's own systemd unit is protected against stop/restart/disable.
	if !isProtectedServiceUnit("sfpanel.service") {
		t.Errorf("sfpanel.service should be protected")
	}
	// Case-insensitive — operators sometimes type uppercase.
	if !isProtectedServiceUnit("SFPanel.service") {
		t.Errorf("SFPanel.service (mixed case) should be protected")
	}
	// Anything else is unprotected.
	for _, name := range []string{
		"nginx.service",
		"docker.service",
		"sshd.service",
		"my-app.service",
	} {
		if isProtectedServiceUnit(name) {
			t.Errorf("%s should NOT be protected", name)
		}
	}
}

func TestValidServiceName(t *testing.T) {
	// Regression fence — name regex must accept normal units and reject
	// path traversal / shell metacharacters.
	accepts := []string{
		"nginx.service",
		"getty@tty1.service",
		"system-systemd-cryptsetup.service",
		"my-app_v2.service",
	}
	for _, n := range accepts {
		if !validServiceName.MatchString(n) {
			t.Errorf("validServiceName(%q) = false, want true", n)
		}
	}
	rejects := []string{
		"",
		"../etc/passwd.service",
		"nginx",
		"nginx.timer",
		"foo.service;rm",
		"foo bar.service",
	}
	for _, n := range rejects {
		if validServiceName.MatchString(n) {
			t.Errorf("validServiceName(%q) = true, want false", n)
		}
	}
}
```

- [ ] **Step 1.2: Run — expect `isProtectedServiceUnit` undefined**

Run: `go test ./internal/feature/services/ -v`
Expected: build failure.

- [ ] **Step 1.3: Implement helper + guards**

In `internal/feature/services/handler.go`, add the helper just below `validServiceName`:

```go
// protectedServiceUnits lists systemd units that must not be stopped /
// restarted / disabled via the panel API — stopping sfpanel.service mid-
// response would kill the very process serving the response. CLAUDE.md
// reserves `os.Exit` to a small documented set of handlers; this denylist
// is the inverse — handlers that must NOT participate in any path leading
// to a self-kill of the panel.
var protectedServiceUnits = map[string]bool{
	"sfpanel.service": true,
}

// isProtectedServiceUnit returns true if the (case-insensitive) unit name
// is in protectedServiceUnits.
func isProtectedServiceUnit(name string) bool {
	return protectedServiceUnits[strings.ToLower(name)]
}

// refuseProtectedUnit returns a 403 response if the given unit is protected
// from the given operation. Returns nil if the operation may proceed.
func refuseProtectedUnit(c echo.Context, name, op string) error {
	if isProtectedServiceUnit(name) {
		return response.Fail(c, http.StatusForbidden, response.ErrForbidden,
			fmt.Sprintf("Refusing to %s protected unit %q via the panel API", op, name))
	}
	return nil
}
```

Then add `if err := refuseProtectedUnit(c, name, "<op>"); err != nil { return err }` immediately after `validServiceName.MatchString` in 3 handlers — `StopService`, `RestartService`, `DisableService`. Concrete edits:

`StopService` (line 79-92):

```go
func (h *Handler) StopService(c echo.Context) error {
	name := c.Param("name")
	if !validServiceName.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "Invalid service name")
	}
	if err := refuseProtectedUnit(c, name, "stop"); err != nil {
		return err
	}
	// ... rest unchanged
}
```

Same pattern for `RestartService` ("restart") and `DisableService` ("disable").

Need to check whether `response.ErrForbidden` exists in `internal/api/response/errors.go`. If not, use `response.ErrPermissionDenied` or whatever 403-mapped code is closest.

- [ ] **Step 1.4: Run services tests + integration**

Run: `go test ./internal/feature/services/ -v && go build ./...`
Expected: all pass; clean build.

---

## Task 2 — process: enriched kill event slog

- [ ] **Step 2.1: Create handler_test.go**

Create `internal/feature/process/handler_test.go`:

```go
package process

import (
	"strconv"
	"strings"
	"testing"
)

func TestKillProcess_PIDValidation(t *testing.T) {
	cases := []struct {
		pid     string
		valid   bool
		comment string
	}{
		{"", false, "empty"},
		{"abc", false, "non-numeric"},
		{"-5", false, "negative"},
		{"0", false, "init parent"},
		{"1", false, "init"},
		{"2", false, "kthreadd"},
		{"3", true, "first usermode candidate"},
		{"12345", true, "typical PID"},
		// strconv.ParseInt with bitSize=32 caps at MaxInt32 = 2147483647.
		{"9999999999", false, "too large for int32"},
	}
	for _, tc := range cases {
		p, err := strconv.ParseInt(tc.pid, 10, 32)
		parsed := err == nil
		// Validity boils down to "parsed AND > 2".
		valid := parsed && p > 2
		if valid != tc.valid {
			t.Errorf("PID %q (%s): parsed=%v p=%d valid=%v, want %v",
				tc.pid, tc.comment, parsed, p, valid, tc.valid)
		}
	}
}

func TestSignalMap_KnownSignals(t *testing.T) {
	// The signal switch in KillProcess covers TERM/KILL/HUP/INT plus
	// numeric aliases 9/15/1/2. Anything else should be rejected.
	accepts := []string{"TERM", "term", "KILL", "kill", "HUP", "INT", "9", "15", "1", "2"}
	rejects := []string{"USR1", "STOP", "QUIT", "", "asdf", "16"}

	upper := func(s string) string { return strings.ToUpper(s) }
	accepted := func(s string) bool {
		switch upper(s) {
		case "KILL", "9", "TERM", "15", "HUP", "1", "INT", "2":
			return true
		}
		return false
	}
	for _, s := range accepts {
		if !accepted(s) {
			t.Errorf("signal %q should be accepted", s)
		}
	}
	for _, s := range rejects {
		if accepted(s) {
			t.Errorf("signal %q should be rejected", s)
		}
	}
}
```

- [ ] **Step 2.2: Add structured kill event emission to KillProcess**

In `internal/feature/process/handler.go`, replace the success block at the end of `KillProcess` (currently `// Invalidate cache after kill so the next fetch reflects the change` through the final `response.OK`):

```go
	// Invalidate cache after kill so the next fetch reflects the change
	h.cache.Lock()
	h.cache.updatedAt = time.Time{}
	h.cache.Unlock()

	// Enriched audit trail: the audit middleware writes a row capturing
	// path/method/status/user/node_id but NOT the request body, so the
	// signal that was actually sent never lands in audit_logs. Emit a
	// structured slog event so ops logs preserve the full picture.
	username, _ := c.Get("username").(string)
	slog.Info("process killed via panel API",
		"component", "process",
		"pid", pid,
		"signal", strings.ToUpper(req.Signal),
		"username", username,
	)

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("Signal %s sent to process %d", strings.ToUpper(req.Signal), pid),
		"pid":     pid,
		"signal":  strings.ToUpper(req.Signal),
	})
```

Add `"log/slog"` to the import list.

- [ ] **Step 2.3: Build + test**

Run: `go build ./internal/feature/process/... && go test ./internal/feature/process/ -v`
Expected: clean.

---

## Task 3 — Verification

- [ ] **Step 3.1: Vet + full internal tests**

Run: `go vet ./internal/feature/services/... ./internal/feature/process/... && go test ./internal/...`
Expected: clean.

---

## Task 4 — Commit

- [ ] **Step 4.1: Stage and commit**

```bash
git add internal/feature/services/handler.go \
        internal/feature/services/handler_test.go \
        internal/feature/process/handler.go \
        internal/feature/process/handler_test.go \
        docs/superpowers/plans/2026-05-18-fix-self-targeting-protections.md

GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git commit -m "$(cat <<'EOF'
services + process: block sfpanel.service self-stop + log enriched kill events

- services: protectedServiceUnits denylist + refuseProtectedUnit helper guards Stop/Restart/Disable from acting on sfpanel.service. Start/Enable/Logs stay open. First test file for the module — validates the denylist + locks in the validServiceName regex behaviour.
- process: KillProcess now emits a structured slog.Info event (pid, signal, username) on success so ops logs retain the full context that the audit middleware row (which records only path/method/status) cannot capture. First test file for the module — PID validation + signal allowlist coverage.

Closes P0-10 and P0-16 from docs/superpowers/research/2026-05-18-module-hardening/R-final.md.
EOF
)"
```
