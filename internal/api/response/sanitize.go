package response

import (
	"regexp"
	"strings"
)

var (
	pathPattern      = regexp.MustCompile(`/home/[^\s:]+`)
	sensitivePattern = regexp.MustCompile(`(?i)(password|secret|token|key)[=:\s]+\S+`)
)

func SanitizeOutput(output string) string {
	result := output
	result = pathPattern.ReplaceAllString(result, "/home/***")
	result = sensitivePattern.ReplaceAllString(result, "$1=***")
	if len(result) > 500 {
		result = result[:500] + "... (truncated)"
	}
	return strings.TrimSpace(result)
}
