package featureauth

import "regexp"

// Credential-field length bounds. Conservative values that fit any realistic
// admin login while making payload-stretch attacks (multi-megabyte usernames)
// fail before they reach the bcrypt verifier. Bcrypt itself caps at 72 bytes
// for the password — anything beyond that is silently truncated by golang.org/x/crypto/bcrypt.
const (
	maxUsernameBytes = 64
	maxPasswordBytes = 256
	maxTOTPCodeBytes = 16 // 6-8 digit codes are standard; allow some slack for backup codes
)

// totpCodeRe matches the standard 6-digit TOTP form. Empty TOTP is allowed
// (legitimate during pre-2FA setup); non-empty values must match.
var totpCodeRe = regexp.MustCompile(`^\d{6,8}$`)

// validCredentialBounds enforces upper-bound sizes on credential fields and
// the TOTP shape. Returns true when all fields are within bounds.
func validCredentialBounds(username, password, totp string) bool {
	if len(username) == 0 || len(username) > maxUsernameBytes {
		return false
	}
	if len(password) == 0 || len(password) > maxPasswordBytes {
		return false
	}
	if totp != "" {
		if len(totp) > maxTOTPCodeBytes || !totpCodeRe.MatchString(totp) {
			return false
		}
	}
	return true
}
