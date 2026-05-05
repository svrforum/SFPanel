package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// Cache TTLs for image_signatures rows. Successes (verified or
// policy-rejected unsigned) get the long TTL; failures get a short TTL
// so a transient network glitch doesn't block deploys for a day.
const (
	cacheTTL     = 24 * time.Hour
	failCacheTTL = 30 * time.Second

	// dockerInspectTimeout caps the local docker query. The CLI is
	// near-instant for already-pulled images; 5s is generous.
	dockerInspectTimeout = 5 * time.Second
)

// status values stored in image_signatures.status. Centralised here so
// typos surface at compile time.
const (
	statusVerified = "verified"
	statusUnsigned = "unsigned"
	statusFailed   = "failed"
)

// Verifier gates image pulls against the cluster's signing policy. It
// caches results in the image_signatures table so repeat deploys don't
// re-shell-out to cosign.
//
// LoadPolicy is injected as a func (not a *cluster.Manager) so tests can
// swap policies without spinning up a Raft cluster. Production wiring
// closes over the cluster.Manager: see internal/api/router.go.
//
// Cosign is optional — when nil, the cosign step is skipped and any
// non-cached image in a non-off policy is treated as a verification
// failure (require → ErrPolicyViolation; warn → log + nil). Tests that
// only exercise off-mode or pure cache paths can leave it nil.
type Verifier struct {
	Cmd        exec.Commander
	DB         *sql.DB
	LoadPolicy func() (Policy, error)
	Cosign     *Installer
}

// VerifyImage decides whether ref is allowed to deploy under the
// current policy. Returns nil on accept, ErrPolicyViolation (wrapped
// with context) on policy refusal, or another error only for genuine
// infrastructure failures the caller should not silently ignore.
//
// Policy load errors are logged and ignored — a transient FSM hiccup
// must not block deploys; the next attempt re-evaluates.
func (v *Verifier) VerifyImage(ctx context.Context, ref string) error {
	policy, err := v.LoadPolicy()
	if err != nil {
		slog.Warn("security: load policy failed; allowing deploy",
			"component", "security", "ref", ref, "error", err)
		return nil
	}
	if policy.IsOff() {
		return nil
	}

	digest, err := v.resolveDigest(ctx, ref)
	if err != nil {
		// Image isn't local yet (likely about to be pulled by docker);
		// let docker surface its own error rather than fabricating one.
		slog.Debug("security: digest unresolved; skipping verify",
			"component", "security", "ref", ref, "error", err)
		return nil
	}

	if rec, ok := v.cacheLookup(digest); ok {
		switch rec.status {
		case statusVerified:
			return nil
		case statusUnsigned:
			return v.handleUnsigned(policy, ref, rec.errMsg)
		case statusFailed:
			return v.handleFailed(policy, ref, rec.errMsg)
		}
	}

	rule, ok := policy.MatchRule(ref)
	if !ok {
		v.cacheStore(digest, ref, statusUnsigned, "", "", "no allowlist rule matched", failCacheTTL)
		if policy.Mode == ModeRequire {
			return fmt.Errorf("%w: %w (ref=%s)", ErrPolicyViolation, ErrNoMatchingRule, ref)
		}
		slog.Warn("security: image has no matching allowlist rule; allowing under warn mode",
			"component", "security", "ref", ref)
		return nil
	}

	return v.runCosignVerify(ctx, policy, rule, digest, ref)
}

// runCosignVerify is the placeholder for Task 7. Task 8 replaces this
// stub with a real cosign exec. Documenting it as a separate method
// keeps the surrounding flow readable.
func (v *Verifier) runCosignVerify(_ context.Context, policy Policy, _ Rule, digest, ref string) error {
	v.cacheStore(digest, ref, statusFailed, "", "", "cosign integration not wired", failCacheTTL)
	if policy.Mode == ModeRequire {
		return fmt.Errorf("%w: cosign not wired (ref=%s)", ErrPolicyViolation, ref)
	}
	slog.Warn("security: cosign not wired; allowing under warn mode",
		"component", "security", "ref", ref)
	return nil
}

// handleUnsigned dispatches a cached "unsigned" record (no matching
// rule, or cosign reported the image carries no signature).
func (v *Verifier) handleUnsigned(policy Policy, ref, errMsg string) error {
	if policy.Mode == ModeRequire {
		if errMsg == "no allowlist rule matched" {
			return fmt.Errorf("%w: %w (ref=%s)", ErrPolicyViolation, ErrNoMatchingRule, ref)
		}
		return fmt.Errorf("%w: image unsigned (ref=%s)", ErrPolicyViolation, ref)
	}
	slog.Warn("security: cached unsigned; allowing under warn mode",
		"component", "security", "ref", ref, "reason", errMsg)
	return nil
}

// handleFailed dispatches a cached "failed" record (cosign verify exited
// non-zero on a previous attempt, still inside the TTL).
func (v *Verifier) handleFailed(policy Policy, ref, errMsg string) error {
	if policy.Mode == ModeRequire {
		return fmt.Errorf("%w: previous verification failed (ref=%s): %s", ErrPolicyViolation, ref, errMsg)
	}
	slog.Warn("security: cached failure; allowing under warn mode",
		"component", "security", "ref", ref, "reason", errMsg)
	return nil
}

// resolveDigest runs `docker image inspect <ref>` and pulls .[0].Id.
// docker's Id field is the local manifest digest (sha256:…) for images
// whose pull source agrees on the layout — it's the value cosign
// signs against.
func (v *Verifier) resolveDigest(ctx context.Context, ref string) (string, error) {
	_ = ctx // RunWithTimeout uses its own context; preserved for future plumbing.
	out, err := v.Cmd.RunWithTimeout(dockerInspectTimeout, "docker", "image", "inspect", ref)
	if err != nil {
		return "", err
	}
	var inspected []struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal([]byte(out), &inspected); err != nil {
		return "", fmt.Errorf("parse docker inspect: %w", err)
	}
	if len(inspected) == 0 || inspected[0].ID == "" {
		return "", errors.New("docker inspect: empty result")
	}
	return inspected[0].ID, nil
}

// cacheRecord is the in-memory shape of a row read from image_signatures.
type cacheRecord struct {
	status string
	errMsg string
}

// cacheLookup reads the row for digest, honouring expires_at. Expired
// rows are reported as miss; they get overwritten by the next
// cacheStore via INSERT … ON CONFLICT(digest) DO UPDATE.
func (v *Verifier) cacheLookup(digest string) (cacheRecord, bool) {
	if v.DB == nil {
		return cacheRecord{}, false
	}
	row := v.DB.QueryRow(`SELECT status, COALESCE(error_message, ''), expires_at FROM image_signatures WHERE digest = ?`, digest)
	var (
		status    string
		errMsg    string
		expiresAt int64
	)
	if err := row.Scan(&status, &errMsg, &expiresAt); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("security: cache lookup failed",
				"component", "security", "digest", digest, "error", err)
		}
		return cacheRecord{}, false
	}
	if expiresAt <= time.Now().UnixMilli() {
		return cacheRecord{}, false
	}
	return cacheRecord{status: status, errMsg: errMsg}, true
}

// cacheStore upserts a row into image_signatures. We use ON CONFLICT
// (digest) DO UPDATE so concurrent verifies of the same digest don't
// race-fail on the unique key.
func (v *Verifier) cacheStore(digest, ref, status, subject, issuer, errMsg string, ttl time.Duration) {
	if v.DB == nil {
		return
	}
	now := time.Now()
	verifiedAt := now.UnixMilli()
	expiresAt := now.Add(ttl).UnixMilli()
	var (
		subjectVal sql.NullString
		issuerVal  sql.NullString
		errMsgVal  sql.NullString
	)
	if subject != "" {
		subjectVal = sql.NullString{String: subject, Valid: true}
	}
	if issuer != "" {
		issuerVal = sql.NullString{String: issuer, Valid: true}
	}
	if errMsg != "" {
		errMsgVal = sql.NullString{String: errMsg, Valid: true}
	}
	_, err := v.DB.Exec(`
		INSERT INTO image_signatures (digest, ref, status, identity_subject, identity_issuer, error_message, verified_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(digest) DO UPDATE SET
			ref              = excluded.ref,
			status           = excluded.status,
			identity_subject = excluded.identity_subject,
			identity_issuer  = excluded.identity_issuer,
			error_message    = excluded.error_message,
			verified_at      = excluded.verified_at,
			expires_at       = excluded.expires_at`,
		digest, ref, status, subjectVal, issuerVal, errMsgVal, verifiedAt, expiresAt)
	if err != nil {
		slog.Warn("security: cache store failed",
			"component", "security", "digest", digest, "ref", ref, "error", err)
	}
}
