package response

import (
	"regexp"
	"strings"
)

var (
	// ansiPattern matches the common subset of ANSI escape sequences emitted
	// by command-line tools (CSI colour/cursor codes + the OSC title-set
	// sequence terminated by BEL or ESC-backslash). Stripping these keeps
	// JSON responses safe to render in a browser without leaking raw
	// terminal control bytes back to the operator.
	ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	pathPattern = regexp.MustCompile(`/home/[^\s:]+`)

	// assignPattern hides the value of a secret-named setting: DB_PASSWORD=x,
	// "password": "x", x-api-key: x, ?token=x. flagPattern hides the value of
	// a secret-named command-line flag: --password x, --authkey x.
	//
	// The name used to match anywhere — any word ending in "key" followed by a
	// space — so "Container npg-valkey Started" printed as "npg-valkey=***",
	// and monkey, hotkey and the like hid the next word. A value now needs an
	// = or : (or a flag's dash) in front of it, and a name counts as a secret
	// by a whole part of it (DB_PASSWORD, SECRET_KEY_BASE) or by how it ends
	// (dbpassword, clientsecret, authtoken). "key" is too common an ending to
	// trust, so it counts only as a whole part or after a word that makes it a
	// credential (apikey, privatekey).
	assignPattern = regexp.MustCompile(`(?i)\b(` + secretName + `["']?)(\s*[=:]\s*)\S+`)
	flagPattern   = regexp.MustCompile(`(?im)(^|\s)(--?` + secretName + `)(\s+)\S+`)
)

const secretName = `(?:[a-z0-9]+[_.-])*` +
	`(?:[a-z0-9]*(?:password|passwd|secret|token)|(?:api|access|private|secret|auth|master|signing|encryption)?key|passphrase)` +
	`(?:[_.-][a-z0-9]+)*`

func redactSecrets(s string) string {
	s = assignPattern.ReplaceAllString(s, "${1}${2}***")
	return flagPattern.ReplaceAllString(s, "${1}${2}${3}***")
}

func SanitizeOutput(output string) string {
	result := output
	result = ansiPattern.ReplaceAllString(result, "")
	result = pathPattern.ReplaceAllString(result, "/home/***")
	result = redactSecrets(result)
	if len(result) > 500 {
		result = result[:500] + "... (truncated)"
	}
	return strings.TrimSpace(result)
}

// SanitizeLog strips ANSI escapes and redacts obvious secrets
// (password/token/secret/key assignments) from multi-line log output, WITHOUT
// the 500-char cap SanitizeOutput applies — log views are intentionally long.
// It keeps filesystem paths intact (logs are more useful with them, and an
// authenticated admin can already read them elsewhere).
func SanitizeLog(output string) string {
	result := ansiPattern.ReplaceAllString(output, "")
	result = redactSecrets(result)
	return strings.TrimSpace(result)
}
