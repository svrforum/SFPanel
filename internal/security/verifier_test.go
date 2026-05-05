package security

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	sfdb "github.com/svrforum/SFPanel/internal/db"
)

// newTestDB opens a fresh on-disk SQLite database with all migrations
// applied. We use an on-disk file (not :memory:) for parity with the
// other feature tests in this repo and so MaxOpenConns=1 doesn't have
// to fight an in-memory shared-cache URI.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := sfdb.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

// dockerInspectDigestOutput shapes a `docker image inspect <ref>` reply
// down to just the field VerifyImage cares about. The real CLI returns
// a JSON array; we mimic that here.
func dockerInspectDigestOutput(digest string) string {
	return `[{"Id":"` + digest + `"}]`
}

// staticPolicy returns a LoadPolicy func that always yields the given p.
func staticPolicy(p Policy) func() (Policy, error) {
	return func() (Policy, error) { return p, nil }
}

// TestVerifier_OffModeIsNoOp — mode=off must short-circuit before any
// docker exec, DB read, or cosign work.
func TestVerifier_OffModeIsNoOp(t *testing.T) {
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		t.Fatalf("unexpected exec in off mode: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	v := &Verifier{
		Cmd:        cmd,
		DB:         nil, // off-mode must not touch the DB either
		LoadPolicy: staticPolicy(Policy{Mode: ModeOff}),
	}
	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatalf("off mode should be no-op, got %v", err)
	}
}

// TestVerifier_RequireMode_NoMatchingRule — mode=require with no rules,
// digest resolves; verifier must wrap ErrPolicyViolation AND ErrNoMatchingRule.
func TestVerifier_RequireMode_NoMatchingRule(t *testing.T) {
	db := newTestDB(t)
	digest := "sha256:" + repeat("a", 64)
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if name == "docker" && len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
			return dockerInspectDigestOutput(digest), nil
		}
		t.Fatalf("unexpected exec: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	v := &Verifier{
		Cmd:        cmd,
		DB:         db,
		LoadPolicy: staticPolicy(Policy{Mode: ModeRequire}),
	}
	err := v.VerifyImage(context.Background(), "ghcr.io/unmatched/img:1")
	if err == nil {
		t.Fatal("require + no matching rule must error")
	}
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("err: want wraps ErrPolicyViolation, got %v", err)
	}
	if !errors.Is(err, ErrNoMatchingRule) {
		t.Fatalf("err: want wraps ErrNoMatchingRule, got %v", err)
	}
}

// TestVerifier_CacheHitVerified — pre-INSERT a verified row that has
// not yet expired; verifier must not call cosign.
func TestVerifier_CacheHitVerified(t *testing.T) {
	db := newTestDB(t)
	digest := "sha256:" + repeat("b", 64)
	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	if _, err := db.Exec(`INSERT INTO image_signatures (digest, ref, status, identity_subject, identity_issuer, error_message, verified_at, expires_at)
		VALUES (?, ?, 'verified', ?, ?, NULL, ?, ?)`,
		digest, "ghcr.io/foo/bar:1", "https://github.com/foo/bar/.github/workflows/release.yaml@refs/tags/v", "https://token.actions.githubusercontent.com", now, expires); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cosignCalls := 0
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if name == "docker" && len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
			return dockerInspectDigestOutput(digest), nil
		}
		// Anything else is a cosign call we should not have made.
		cosignCalls++
		return "", errors.New("unexpected exec")
	}}
	v := &Verifier{
		Cmd: cmd,
		DB:  db,
		LoadPolicy: staticPolicy(Policy{
			Mode: ModeRequire,
			Rules: []Rule{{
				Pattern:  "ghcr.io/foo/*",
				Identity: Identity{SubjectPrefix: "https://github.com/foo/bar/.github/workflows/release.yaml@refs/tags/v", Issuer: "https://token.actions.githubusercontent.com"},
			}},
		}),
	}
	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatalf("verified cache hit must pass: %v", err)
	}
	if cosignCalls != 0 {
		t.Fatalf("cosign should not be invoked on verified cache hit (got %d calls)", cosignCalls)
	}
}

// TestVerifier_CacheHitUnsigned_RequireMode — pre-INSERT status=unsigned;
// require mode must reject without re-running cosign.
func TestVerifier_CacheHitUnsigned_RequireMode(t *testing.T) {
	db := newTestDB(t)
	digest := "sha256:" + repeat("c", 64)
	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	if _, err := db.Exec(`INSERT INTO image_signatures (digest, ref, status, identity_subject, identity_issuer, error_message, verified_at, expires_at)
		VALUES (?, ?, 'unsigned', NULL, NULL, 'no allowlist rule matched', ?, ?)`,
		digest, "ghcr.io/foo/bar:1", now, expires); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if name == "docker" && len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
			return dockerInspectDigestOutput(digest), nil
		}
		t.Fatalf("unexpected exec on unsigned cache hit: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	v := &Verifier{
		Cmd:        cmd,
		DB:         db,
		LoadPolicy: staticPolicy(Policy{Mode: ModeRequire}),
	}
	err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1")
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("err: want wraps ErrPolicyViolation, got %v", err)
	}
}

// repeat avoids importing "strings" just for this helper at test scope.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// queryStatus reads back the status (and error_message, for failure
// cases) for a given digest. Used to assert cache writes after a
// cosign exec.
func queryStatus(t *testing.T, db *sql.DB, digest string) (string, string) {
	t.Helper()
	var (
		status string
		errMsg sql.NullString
	)
	row := db.QueryRow(`SELECT status, error_message FROM image_signatures WHERE digest = ?`, digest)
	if err := row.Scan(&status, &errMsg); err != nil {
		t.Fatalf("query status for %s: %v", digest, err)
	}
	return status, errMsg.String
}

// cosignTestRule is reused across the Task 8 cases — a plausible-looking
// GitHub Actions release identity for a fictitious org/repo.
var cosignTestRule = Rule{
	Pattern: "ghcr.io/foo/*",
	Identity: Identity{
		SubjectPrefix: "https://github.com/foo/bar/.github/workflows/release.yaml@refs/tags/v",
		Issuer:        "https://token.actions.githubusercontent.com",
	},
}

// stubCosignBinary writes a sham cosign binary to a temp dir and returns
// its path. The Installer's `cosign version` check will then succeed
// because (a) the file exists and (b) our fakeCmd routes the version
// call to a v2.x string. The actual *contents* of the file are never
// executed by the test because RunWithTimeout goes through fakeCmd.
func stubCosignBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cosign")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binPath
}

// TestVerifier_CosignSuccess — cosign exits 0; verifier caches verified
// and returns nil. Asserts on DB row contents post-call.
func TestVerifier_CosignSuccess(t *testing.T) {
	db := newTestDB(t)
	digest := "sha256:" + repeat("d", 64)
	cosignPath := stubCosignBinary(t)
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		switch {
		case name == "docker" && len(args) >= 2 && args[0] == "image" && args[1] == "inspect":
			return dockerInspectDigestOutput(digest), nil
		case name == cosignPath && len(args) >= 1 && args[0] == "version":
			return "cosign version v2.4.1\n", nil
		case name == cosignPath && len(args) >= 1 && args[0] == "verify":
			// Sanity-check the args carry the rule's identity.
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--certificate-oidc-issuer="+cosignTestRule.Identity.Issuer) {
				t.Fatalf("missing OIDC issuer flag in args: %v", args)
			}
			if !strings.Contains(joined, "--certificate-identity-regexp=") {
				t.Fatalf("missing identity-regexp flag in args: %v", args)
			}
			return "Verification for ghcr.io/foo/bar:1 -- OK\n", nil
		}
		t.Fatalf("unexpected exec: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	v := &Verifier{
		Cmd:        cmd,
		DB:         db,
		LoadPolicy: staticPolicy(Policy{Mode: ModeRequire, Rules: []Rule{cosignTestRule}}),
		Cosign:     &Installer{Cmd: cmd, Path: cosignPath},
	}
	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	status, _ := queryStatus(t, db, digest)
	if status != "verified" {
		t.Fatalf("status: got %q want verified", status)
	}
}

// TestVerifier_CosignFailure_RequireMode — cosign exits non-zero in
// require mode → wrapped ErrPolicyViolation + DB row failed.
func TestVerifier_CosignFailure_RequireMode(t *testing.T) {
	db := newTestDB(t)
	digest := "sha256:" + repeat("e", 64)
	cosignPath := stubCosignBinary(t)
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		switch {
		case name == "docker" && len(args) >= 2 && args[0] == "image" && args[1] == "inspect":
			return dockerInspectDigestOutput(digest), nil
		case name == cosignPath && len(args) >= 1 && args[0] == "version":
			return "cosign version v2.4.1\n", nil
		case name == cosignPath && len(args) >= 1 && args[0] == "verify":
			return "no matching signatures found\n", errors.New("exit status 1")
		}
		t.Fatalf("unexpected exec: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	v := &Verifier{
		Cmd:        cmd,
		DB:         db,
		LoadPolicy: staticPolicy(Policy{Mode: ModeRequire, Rules: []Rule{cosignTestRule}}),
		Cosign:     &Installer{Cmd: cmd, Path: cosignPath},
	}
	err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1")
	if !errors.Is(err, ErrPolicyViolation) {
		t.Fatalf("err: want wraps ErrPolicyViolation, got %v", err)
	}
	status, errMsg := queryStatus(t, db, digest)
	if status != "failed" {
		t.Fatalf("status: got %q want failed", status)
	}
	if errMsg == "" {
		t.Fatal("error_message should be populated on failure")
	}
}

// TestVerifier_CosignFailure_WarnMode — same exit non-zero, mode=warn →
// returns nil + DB row failed.
func TestVerifier_CosignFailure_WarnMode(t *testing.T) {
	db := newTestDB(t)
	digest := "sha256:" + repeat("f", 64)
	cosignPath := stubCosignBinary(t)
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		switch {
		case name == "docker" && len(args) >= 2 && args[0] == "image" && args[1] == "inspect":
			return dockerInspectDigestOutput(digest), nil
		case name == cosignPath && len(args) >= 1 && args[0] == "version":
			return "cosign version v2.4.1\n", nil
		case name == cosignPath && len(args) >= 1 && args[0] == "verify":
			return "no matching signatures found\n", errors.New("exit status 1")
		}
		t.Fatalf("unexpected exec: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	v := &Verifier{
		Cmd:        cmd,
		DB:         db,
		LoadPolicy: staticPolicy(Policy{Mode: ModeWarn, Rules: []Rule{cosignTestRule}}),
		Cosign:     &Installer{Cmd: cmd, Path: cosignPath},
	}
	if err := v.VerifyImage(context.Background(), "ghcr.io/foo/bar:1"); err != nil {
		t.Fatalf("warn mode must not block, got %v", err)
	}
	status, _ := queryStatus(t, db, digest)
	if status != "failed" {
		t.Fatalf("status: got %q want failed", status)
	}
}
