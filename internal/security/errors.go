package security

import "errors"

// ErrPolicyViolation is returned by VerifyImage when the operator's policy
// rejects the image (require-mode failure or no matching allowlist rule).
// Callers should treat this as a clean refusal — not a transient error.
var ErrPolicyViolation = errors.New("security: policy violation")

// ErrCosignInstallFailed is returned by EnsureCosign when both the network
// download and the /etc/sfpanel/cosign fallback fail to produce a usable
// binary. The error is wrapped with context so the operator-facing message
// can include the underlying cause.
var ErrCosignInstallFailed = errors.New("security: cosign install failed")

// ErrNoMatchingRule is returned (wrapped) by VerifyImage in require mode
// when the image does not match any allowlist rule. Distinguishable from
// signature-verification failures so the UI can show "add a rule" guidance.
var ErrNoMatchingRule = errors.New("security: no matching allowlist rule")
