# A2: Shared compose YAML validator hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` (inline). Steps use checkbox (`- [ ]`) syntax.

**Goal:** Close [R-final](../research/2026-05-18-module-hardening/R-final.md) P0-19 by hardening `composex.ValidateAdvancedCompose`. Single fix closes both compose (advanced YAML route) and appstore (Advanced install mode) because `appstore/compose_safety.go` is a 1-line delegating alias.

**Architecture:** Single source file (`internal/composex/safety.go`) + new test file (`internal/composex/safety_test.go` — module has zero coverage today). Three independent gaps; landed as one PR because they share the test fixture and reviewer context.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, standard `testing`.

**Out of scope:**
- Expanding the `cap_add` blocklist beyond CAP_-prefix handling (would touch ~5-10 additional caps; legitimate templates might be affected; left for later P1 work).
- Blocking `pid_mode: container:<id>` (joining another container's namespace) — long-form key check + value=host is the P0 scope; non-host values are a follow-up.
- Hardening `services` schema beyond what `ValidateAdvancedCompose` already covers.

---

## File structure

- **Modify** `internal/composex/safety.go` — three localized changes inside `ValidateAdvancedCompose`.
- **Create** `internal/composex/safety_test.go` — table-driven test covering every existing accept/reject + the three new reject paths + 2 happy-path samples.

---

## Task 1 — Bootstrap test file (covers existing behaviour first to prevent regression)

**Files:**
- Create: `internal/composex/safety_test.go`

- [ ] **Step 1.1: Write the table-driven test for existing PASS/REJECT cases**

Create `internal/composex/safety_test.go` with this content:

```go
package composex

import (
	"strings"
	"testing"
)

func TestValidateAdvancedCompose_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantReject  bool
		wantErrSub  string // substring of expected error message
	}{
		// --- happy path ---
		{
			name: "minimal valid service",
			yaml: `services:
  web:
    image: nginx`,
			wantReject: false,
		},
		{
			name: "named volume is fine",
			yaml: `services:
  db:
    image: postgres
    volumes:
      - dbdata:/var/lib/postgresql/data`,
			wantReject: false,
		},

		// --- already-blocked patterns (existing behaviour we MUST preserve) ---
		{
			name:       "privileged",
			yaml:       "services:\n  evil:\n    privileged: true\n",
			wantReject: true, wantErrSub: "privileged: true",
		},
		{
			name:       "pid: host short form",
			yaml:       "services:\n  evil:\n    pid: host\n",
			wantReject: true, wantErrSub: "pid: host",
		},
		{
			name:       "network: host short form",
			yaml:       "services:\n  evil:\n    network: host\n",
			wantReject: true, wantErrSub: "network: host",
		},
		{
			name:       "ipc: host short form",
			yaml:       "services:\n  evil:\n    ipc: host\n",
			wantReject: true, wantErrSub: "ipc: host",
		},
		{
			name:       "userns_mode: host",
			yaml:       "services:\n  evil:\n    userns_mode: host\n",
			wantReject: true, wantErrSub: "userns_mode: host",
		},
		{
			name:       "cap_add SYS_ADMIN unprefixed",
			yaml:       "services:\n  evil:\n    cap_add:\n      - SYS_ADMIN\n",
			wantReject: true, wantErrSub: "SYS_ADMIN",
		},
		{
			name:       "cap_add ALL",
			yaml:       "services:\n  evil:\n    cap_add:\n      - ALL\n",
			wantReject: true, wantErrSub: "ALL",
		},
		{
			name:       "security_opt apparmor:unconfined",
			yaml:       "services:\n  evil:\n    security_opt:\n      - apparmor:unconfined\n",
			wantReject: true, wantErrSub: "apparmor:unconfined",
		},
		{
			name:       "bind mount of /",
			yaml:       "services:\n  evil:\n    volumes:\n      - /:/hostfs\n",
			wantReject: true, wantErrSub: "/",
		},
		{
			name:       "bind mount of /etc",
			yaml:       "services:\n  evil:\n    volumes:\n      - /etc:/etc:ro\n",
			wantReject: true, wantErrSub: "/etc",
		},
		{
			name:       "docker socket bind",
			yaml:       "services:\n  evil:\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n",
			wantReject: true, wantErrSub: "docker.sock",
		},
		{
			name:       "devices passthrough",
			yaml:       "services:\n  evil:\n    devices:\n      - /dev/sda:/dev/sda\n",
			wantReject: true, wantErrSub: "devices",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAdvancedCompose(tc.yaml)
			if tc.wantReject {
				if err == nil {
					t.Fatalf("expected rejection, got nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error to contain %q, got %q", tc.wantErrSub, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected accept, got %v", err)
			}
		})
	}
}
```

- [ ] **Step 1.2: Run the existing-behaviour test to verify it passes**

Run: `go test ./internal/composex/ -run TestValidateAdvancedCompose_TableDriven -v`
Expected: ALL subtests PASS (we are documenting current behaviour first; nothing has been changed yet).

---

## Task 2 — Add the 3 new reject tests (which will fail against today's code)

**Files:**
- Modify: `internal/composex/safety_test.go`

- [ ] **Step 2.1: Append the new gap tests**

Append after the existing `TestValidateAdvancedCompose_TableDriven`:

```go
func TestValidateAdvancedCompose_NewGapsRejected(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantErrSub string
	}{
		// --- P0-19 gap A: long-form *_mode host ---
		{
			name:       "pid_mode: host long form",
			yaml:       "services:\n  evil:\n    pid_mode: host\n",
			wantErrSub: "pid_mode: host",
		},
		{
			name:       "network_mode: host long form",
			yaml:       "services:\n  evil:\n    network_mode: host\n",
			wantErrSub: "network_mode: host",
		},
		{
			name:       "ipc_mode: host long form",
			yaml:       "services:\n  evil:\n    ipc_mode: host\n",
			wantErrSub: "ipc_mode: host",
		},

		// --- P0-19 gap B: CAP_-prefixed cap_add ---
		{
			name:       "cap_add CAP_SYS_ADMIN canonical form",
			yaml:       "services:\n  evil:\n    cap_add:\n      - CAP_SYS_ADMIN\n",
			wantErrSub: "SYS_ADMIN",
		},
		{
			name:       "cap_add cap_sys_admin lowercase canonical",
			yaml:       "services:\n  evil:\n    cap_add:\n      - cap_sys_admin\n",
			wantErrSub: "SYS_ADMIN",
		},

		// --- P0-19 gap C: group_add joining sensitive host groups ---
		{
			name:       "group_add docker",
			yaml:       "services:\n  evil:\n    group_add:\n      - docker\n",
			wantErrSub: "docker",
		},
		{
			name:       "group_add disk",
			yaml:       "services:\n  evil:\n    group_add:\n      - disk\n",
			wantErrSub: "disk",
		},
		{
			name:       "group_add sudo",
			yaml:       "services:\n  evil:\n    group_add:\n      - sudo\n",
			wantErrSub: "sudo",
		},
		{
			name:       "group_add wheel",
			yaml:       "services:\n  evil:\n    group_add:\n      - wheel\n",
			wantErrSub: "wheel",
		},
		{
			name:       "group_add root",
			yaml:       "services:\n  evil:\n    group_add:\n      - root\n",
			wantErrSub: "root",
		},
		{
			name:       "group_add kvm",
			yaml:       "services:\n  evil:\n    group_add:\n      - kvm\n",
			wantErrSub: "kvm",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAdvancedCompose(tc.yaml)
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("expected error to contain %q, got %q", tc.wantErrSub, err.Error())
			}
		})
	}
}

func TestValidateAdvancedCompose_GroupAddBenignAllowed(t *testing.T) {
	// Non-privileged group IDs / unknown group names should NOT be rejected.
	// This guards against being too aggressive (only block known-dangerous).
	cases := []string{
		"services:\n  app:\n    image: x\n    group_add:\n      - audio\n",
		"services:\n  app:\n    image: x\n    group_add:\n      - users\n",
		"services:\n  app:\n    image: x\n    group_add:\n      - \"1234\"\n",
	}
	for i, y := range cases {
		if err := ValidateAdvancedCompose(y); err != nil {
			t.Fatalf("case %d: benign group_add should be allowed, got %v", i, err)
		}
	}
}
```

- [ ] **Step 2.2: Run the new tests to verify they FAIL**

Run: `go test ./internal/composex/ -run 'TestValidateAdvancedCompose_NewGapsRejected|TestValidateAdvancedCompose_GroupAddBenignAllowed' -v`
Expected: FAIL on every subcase of `TestValidateAdvancedCompose_NewGapsRejected` (because validator accepts them today). PASS on `TestValidateAdvancedCompose_GroupAddBenignAllowed` (because nothing inspects group_add today, so benign cases trivially pass).

---

## Task 3 — Implement the validator hardening

**Files:**
- Modify: `internal/composex/safety.go`

- [ ] **Step 3.1: Add long-form `*_mode` to the host-mode key list**

In `ValidateAdvancedCompose`, change the line:

```go
for _, hostModeKey := range []string{"pid", "network", "ipc", "uts", "userns_mode"} {
```

to:

```go
for _, hostModeKey := range []string{
	"pid", "network", "ipc", "uts",
	// Long-form aliases. Compose accepts both shapes; we must too.
	"pid_mode", "network_mode", "ipc_mode", "userns_mode",
} {
```

- [ ] **Step 3.2: Strip `CAP_` prefix in cap_add comparison**

In `ValidateAdvancedCompose`, change the cap_add loop:

```go
if caps, ok := svc["cap_add"].([]interface{}); ok {
	for _, c := range caps {
		s, _ := c.(string)
		upper := strings.ToUpper(strings.TrimSpace(s))
		if upper == "ALL" || upper == "SYS_ADMIN" || upper == "SYS_MODULE" || upper == "SYS_PTRACE" {
			return fmt.Errorf("service %q requests disallowed capability %s", svcName, upper)
		}
	}
}
```

to:

```go
if caps, ok := svc["cap_add"].([]interface{}); ok {
	for _, c := range caps {
		s, _ := c.(string)
		// Strip optional CAP_ prefix — Docker accepts both "SYS_ADMIN" and
		// "CAP_SYS_ADMIN" (the kernel-canonical form). Compare on the bare form.
		canon := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "CAP_")
		if canon == "ALL" || canon == "SYS_ADMIN" || canon == "SYS_MODULE" || canon == "SYS_PTRACE" {
			return fmt.Errorf("service %q requests disallowed capability %s", svcName, canon)
		}
	}
}
```

- [ ] **Step 3.3: Add `group_add` inspection**

In `ValidateAdvancedCompose`, immediately after the `cap_add` block (before `security_opt`), insert:

```go
if groups, ok := svc["group_add"].([]interface{}); ok {
	for _, g := range groups {
		s, _ := g.(string)
		name := strings.ToLower(strings.TrimSpace(s))
		// Reject membership in groups that gate sensitive host resources
		// (docker socket, raw disks, sudoers, kernel virtualisation).
		// Numeric GIDs and unknown names pass through.
		switch name {
		case "docker", "disk", "sudo", "wheel", "root", "kvm":
			return fmt.Errorf("service %q joins host group %q via group_add", svcName, name)
		}
	}
}
```

- [ ] **Step 3.4: Run the full validator test suite**

Run: `go test ./internal/composex/ -v`
Expected: ALL tests PASS (including all subtests of both `TestValidateAdvancedCompose_TableDriven`, `TestValidateAdvancedCompose_NewGapsRejected`, and `TestValidateAdvancedCompose_GroupAddBenignAllowed`).

---

## Task 4 — Verification

- [ ] **Step 4.1: Vet + broader package test**

Run: `go vet ./internal/composex/... && go test ./internal/...`
Expected: clean vet + all tests pass.

---

## Task 5 — Commit

- [ ] **Step 5.1: Stage and commit**

```bash
git add internal/composex/safety.go internal/composex/safety_test.go docs/superpowers/plans/2026-05-18-fix-shared-yaml-validator.md

GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git commit -m "$(cat <<'EOF'
composex: harden ValidateAdvancedCompose — long-form *_mode + CAP_ prefix + group_add

- Add pid_mode/network_mode/ipc_mode long-form keys to the host-mode reject list (previously only short pid/network/ipc were caught)
- Strip optional CAP_ prefix before comparing cap_add entries so CAP_SYS_ADMIN gets blocked the same as SYS_ADMIN
- Reject group_add membership in docker/disk/sudo/wheel/root/kvm; numeric GIDs and unknown names still pass through

Adds the module's first test file (table-driven, covers every existing reject path so the new behaviour is regression-fenced). Single fix closes the validator gap for both compose Advanced YAML and appstore Advanced install — appstore/compose_safety.go is a 1-line delegating alias.

Closes P0-19 from docs/superpowers/research/2026-05-18-module-hardening/R-final.md (2026-04-19 P0 carry-forward — partial OPEN → CLOSED).
EOF
)"
```

---

## Self-review

- [x] Spec coverage — three documented gaps in P0-19 each get a test bucket: long-form `*_mode`, `CAP_`-prefix cap_add, `group_add` sensitive groups.
- [x] No placeholders — every step has actual code.
- [x] Regression fence — table-driven existing-behaviour test runs first so the new code can't accidentally relax an already-blocked pattern.
- [x] Negative-case test — `TestValidateAdvancedCompose_GroupAddBenignAllowed` ensures the group_add check doesn't over-block.
- [x] CLAUDE.md compliance — svrforum env vars, no AI references in commit message.
