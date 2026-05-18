# A4: Symlink-leaf validators Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` (inline).

**Goal:** Close [R-final](../research/2026-05-18-module-hardening/R-final.md) P0-11 + P0-12 + P0-13 in a single PR. Adds symlink-leaf resolution to `files.validatePathForWrite` (closes UploadFile + MkDir bypass) and to `logs.AddCustomSource` (closes custom-source bypass), plus the missing `isCriticalPath` regression-fence tests.

**Architecture:** Per-module changes; no shared helper because the two validators have different allow sets (files: deny-list `criticalPaths`; logs: allow-list `/var/log` and `/opt`). Pattern is identical though: `os.Lstat` the supplied path; if it's a symlink, `EvalSymlinks` and re-run the same validation predicate on the resolved target.

**Tech Stack:** Go 1.25 stdlib only (`os`, `path/filepath`).

**Out of scope:**
- Refactoring `/opt/` out of the logs allowlist (T5 P1 finding; deferred).
- Tightening `criticalPaths` further (already comprehensive for write-side).
- Symlink-leaf handling for files.WriteFile (already safe — the rename-into-place pattern replaces the symlink rather than following it; only UploadFile and MkDir need the fix).

---

## File structure

- **Modify** `internal/feature/files/handler.go` — extend `validatePathForWrite` with `Lstat`-symlink check + re-validate resolved target.
- **Modify** `internal/feature/files/handler_test.go` — add `isCriticalPath` table-driven test (P0-12) + symlink-leaf bypass test (P0-11).
- **Modify** `internal/feature/logs/handler.go` — in `AddCustomSource`, after the allowlist check, `EvalSymlinks` if file exists and re-validate.
- **Modify** `internal/feature/logs/handler_test.go` — add symlink-bypass test.

---

## Task 1 — files: regression-fence `isCriticalPath` + symlink-leaf bypass tests

- [ ] **Step 1.1: Append tests to files/handler_test.go**

```go
func TestIsCriticalPath_TableDriven(t *testing.T) {
	// Regression fence for the 2026-04-19 P0 R3 N-01 fix that switched
	// isCriticalPath from exact-match to prefix-match. Any future
	// "optimization" that re-introduces exact-match must fail these.
	rejects := []string{
		// Exact matches of every entry in criticalPaths
		"/", "/etc", "/usr", "/bin", "/sbin", "/var", "/boot",
		"/proc", "/sys", "/dev", "/home", "/root", "/lib",
		"/lib64", "/opt", "/run", "/srv",
		// 2026-04-19 attack vectors (must be rejected via prefix)
		"/etc/cron.d/backdoor",
		"/etc/sudoers.d/zz_pwn",
		"/etc/systemd/system/evil.service",
		"/usr/local/bin/sfpanel",
		"/etc/init.d/evil",
		"/etc/profile.d/evil.sh",
		"/root/.ssh/authorized_keys",
	}
	for _, p := range rejects {
		if !isCriticalPath(p) {
			t.Errorf("isCriticalPath(%q) = false, want true", p)
		}
	}
	accepts := []string{
		"/tmp/file",
		"/tmp",
		"/var/lib/sfpanel/data", // /var/lib/sfpanel is NOT in criticalPaths (it's our own data dir)
		"/mnt/storage/x",
		"/data/x",
	}
	for _, p := range accepts {
		// Note: /var/lib/sfpanel IS protected via the /var prefix actually.
		// Skip the /var/lib check — it IS critical via /var prefix.
		if p == "/var/lib/sfpanel/data" {
			continue
		}
		if isCriticalPath(p) {
			t.Errorf("isCriticalPath(%q) = true, want false", p)
		}
	}
}

func TestValidatePathForWrite_RejectsSymlinkLeafToCritical(t *testing.T) {
	// P0-11: UploadFile + MkDir pass destDir as the validated path. If
	// destDir IS a symlink to /etc/cron.d, validatePathForWrite must reject.
	tmp := t.TempDir()
	link := filepath.Join(tmp, "sneaky")
	if err := os.Symlink("/etc/cron.d", link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	err := validatePathForWrite(link)
	if err == nil {
		t.Fatalf("validatePathForWrite(%q) accepted symlink to /etc/cron.d; want rejection", link)
	}
}

func TestValidatePathForWrite_AllowsSymlinkLeafToBenign(t *testing.T) {
	// Symlink to a non-critical target should still be allowed.
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	link := filepath.Join(tmp, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if err := validatePathForWrite(link); err != nil {
		t.Fatalf("validatePathForWrite(%q) rejected benign symlink: %v", link, err)
	}
}
```

- [ ] **Step 1.2: Run tests — expect symlink-leaf bypass test to FAIL, others to PASS**

Run: `go test ./internal/feature/files/ -run 'TestIsCriticalPath_TableDriven|TestValidatePathForWrite_Rejects|TestValidatePathForWrite_Allows' -v`
Expected: PASS for table-driven + AllowsBenign; FAIL for RejectsSymlinkLeafToCritical.

---

## Task 2 — files: implement the fix

- [ ] **Step 2.1: Extend `validatePathForWrite`**

In `internal/feature/files/handler.go`, replace the existing `validatePathForWrite` body's final return with a symlink-leaf check:

```go
// validatePathForWrite checks symlink resolution for write/delete operations.
func validatePathForWrite(p string) error {
	if err := validatePath(p); err != nil {
		return err
	}
	parentDir := filepath.Dir(p)
	realDir, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Parent doesn't exist yet — MkdirAll will create it; validate the literal path
			realDir = filepath.Clean(parentDir)
		} else {
			return fmt.Errorf("cannot resolve parent directory: %w", err)
		}
	}
	resolved := filepath.Join(realDir, filepath.Base(p))
	if isCriticalPath(resolved) {
		return fmt.Errorf("access to critical system path is not allowed")
	}
	if isCriticalPath(realDir) {
		return fmt.Errorf("writing inside critical system directory is not allowed")
	}
	// Leaf-symlink check: if p itself is a symlink (e.g. /tmp/sneaky ->
	// /etc/cron.d), MkdirAll/os.Create would follow it into a critical path
	// even though parent + literal-resolved checks above pass. Resolve the
	// symlink chain and re-check the final target.
	if info, lerr := os.Lstat(p); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, terr := filepath.EvalSymlinks(p)
		if terr != nil {
			return fmt.Errorf("cannot resolve symlink target: %w", terr)
		}
		if isCriticalPath(target) {
			return fmt.Errorf("path resolves to a critical system path via symlink")
		}
	}
	return nil
}
```

- [ ] **Step 2.2: Run files tests**

Run: `go test ./internal/feature/files/ -v 2>&1 | tail -30`
Expected: ALL pass, including the new symlink-leaf test.

---

## Task 3 — logs: symlink-bypass test + fix

- [ ] **Step 3.1: Append test to logs/handler_test.go**

```go
func TestAddCustomSource_RejectsSymlinkToSensitive(t *testing.T) {
	// P0-13: allowlist requires /var/log or /opt prefix, but a symlink
	// inside an allowlisted dir pointing at /etc/shadow passes the literal
	// prefix check today. The fix EvalSymlinks-and-rechecks before storing.
	//
	// We can't easily create a symlink inside /var/log (test isn't root in
	// CI) — so we test the helper directly via the validator helper that
	// the implementation factors out.
	tmp := t.TempDir()
	sensitive := filepath.Join(tmp, "sensitive.txt")
	if err := os.WriteFile(sensitive, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(tmp, "allowed")
	if err := os.MkdirAll(allowed, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "alias.log")
	if err := os.Symlink(sensitive, link); err != nil {
		t.Fatal(err)
	}
	// validateCustomSourcePath is the helper to be extracted; the
	// allowlist needs to include `allowed/` for this test. We pass it
	// explicitly via the helper signature.
	err := validateCustomSourcePath(link, []string{allowed + "/"})
	if err == nil {
		t.Fatalf("validateCustomSourcePath accepted symlink leaving allowlisted dir; want rejection")
	}
}

func TestValidateCustomSourcePath_AllowsBenignSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real.log")
	if err := os.WriteFile(target, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	allowed := tmp + "/"
	link := filepath.Join(tmp, "alias.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomSourcePath(link, []string{allowed}); err != nil {
		t.Fatalf("validateCustomSourcePath rejected benign in-allowlist symlink: %v", err)
	}
}
```

- [ ] **Step 3.2: Run tests — expect build failure (`validateCustomSourcePath` undefined)**

- [ ] **Step 3.3: Extract validateCustomSourcePath in logs/handler.go**

Add a helper above `AddCustomSource`:

```go
// validateCustomSourcePath enforces the absolute-path + traversal-free + allowlist
// rules on a candidate log source path. After the literal-path check, the path is
// EvalSymlinks'd (if the target exists) and the resolved target is re-validated
// against the same allowlist — this closes the symlink bypass where an attacker
// places a symlink inside an allowlisted dir pointing at /etc/shadow.
func validateCustomSourcePath(p string, allowedPrefixes []string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute")
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("path must not contain '..'")
	}
	clean := filepath.Clean(p)
	if !hasAllowedPrefix(clean, allowedPrefixes) {
		return fmt.Errorf("path not in allowlist")
	}
	if _, err := os.Lstat(clean); err == nil {
		resolved, rerr := filepath.EvalSymlinks(clean)
		if rerr != nil {
			return fmt.Errorf("cannot resolve symlink: %w", rerr)
		}
		if !hasAllowedPrefix(resolved, allowedPrefixes) {
			return fmt.Errorf("path resolves outside allowlist via symlink")
		}
	}
	return nil
}

func hasAllowedPrefix(p string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}
```

Then replace the inline validation block in `AddCustomSource` (currently around lines 487-507) with a single call:

```go
allowedPrefixes := []string{"/var/log/", "/opt/"}
if err := validateCustomSourcePath(req.Path, allowedPrefixes); err != nil {
	return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
}
cleanPath := filepath.Clean(req.Path)
```

- [ ] **Step 3.4: Run tests**

Run: `go test ./internal/feature/logs/ -v 2>&1 | tail -30`
Expected: all pass.

---

## Task 4 — Verification

- [ ] **Step 4.1: Vet + broader test**

Run: `go vet ./internal/feature/files/... ./internal/feature/logs/... && go test ./internal/...`
Expected: clean.

---

## Task 5 — Commit

- [ ] **Step 5.1: Stage and commit**

```bash
git add internal/feature/files/handler.go \
        internal/feature/files/handler_test.go \
        internal/feature/logs/handler.go \
        internal/feature/logs/handler_test.go \
        docs/superpowers/plans/2026-05-18-fix-symlink-leaf-validators.md

GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git commit -m "$(cat <<'EOF'
files + logs: close symlink-leaf bypass + add isCriticalPath regression tests

- files.validatePathForWrite: Lstat the supplied path; if it's a symlink, EvalSymlinks and re-check isCriticalPath on the resolved target. Closes the UploadFile + MkDir leaf bypass where parent resolution caught multi-level symlinks but a one-level leaf symlink (e.g. /tmp/sneaky -> /etc/cron.d) was followed by MkdirAll/os.Create.
- files: first-ever isCriticalPath table-driven tests covering every entry + the 2026-04-19 R3 N-01 attack vectors (/etc/cron.d, /etc/sudoers.d, /usr/local/bin, etc.).
- logs.AddCustomSource: extract validateCustomSourcePath helper; EvalSymlinks the candidate path and re-check against the allowlist so a symlink inside /var/log pointing at /etc/shadow no longer passes the literal prefix check.

Closes P0-11, P0-12, P0-13 from docs/superpowers/research/2026-05-18-module-hardening/R-final.md.
EOF
)"
```
