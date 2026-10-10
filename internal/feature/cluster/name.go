package featurecluster

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxClusterNameLen bounds the name every node shows in its sidebar.
const maxClusterNameLen = 64

var errClusterName = errors.New("cluster name must be 1-64 characters with no control characters")

// validateClusterName returns the name as it will be stored — trimmed — or
// errClusterName. Control characters are refused because the CLI prints the
// name and the logs record it, where a newline or an escape sequence would
// forge a line or repaint the operator's terminal.
func validateClusterName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxClusterNameLen {
		return "", errClusterName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errClusterName
		}
	}
	return name, nil
}
