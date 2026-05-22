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
	ansiPattern      = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	pathPattern      = regexp.MustCompile(`/home/[^\s:]+`)
	sensitivePattern = regexp.MustCompile(`(?i)(password|secret|token|key)[=:\s]+\S+`)
)

func SanitizeOutput(output string) string {
	result := output
	result = ansiPattern.ReplaceAllString(result, "")
	result = pathPattern.ReplaceAllString(result, "/home/***")
	result = sensitivePattern.ReplaceAllString(result, "$1=***")
	if len(result) > 500 {
		result = result[:500] + "... (truncated)"
	}
	return strings.TrimSpace(result)
}
