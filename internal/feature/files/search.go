package files

import (
	"path/filepath"
	"strings"
)

// Matcher decides whether a filename is a search hit.
type Matcher func(name string) bool

// NewMatcher builds the matcher for a query.
//
// Search matched a lowercased substring and nothing else, which answers the
// wrong question for the most common case. Looking for compose files means
// typing "yml" and getting every path with those three letters anywhere in it,
// while the thing an operator actually wants to write — *.yml — matched
// nothing at all, because the asterisk was compared literally.
//
// A query containing a glob metacharacter is treated as a glob; everything
// else keeps the substring behaviour, which is the right default for typing
// half a name. Deciding by content rather than by a mode switch means the
// operator never has to know which kind of search they are doing.
func NewMatcher(query string) Matcher {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return func(string) bool { return false }
	}

	if !strings.ContainsAny(needle, "*?[") {
		return func(name string) bool {
			return strings.Contains(strings.ToLower(name), needle)
		}
	}

	// A malformed pattern — an unclosed bracket, say — makes filepath.Match
	// return ErrBadPattern for every candidate, which would silently return no
	// results for a query the operator can see is reasonable. Fall back to
	// substring so a typo degrades instead of disappearing.
	if _, err := filepath.Match(needle, "probe"); err != nil {
		return func(name string) bool {
			return strings.Contains(strings.ToLower(name), needle)
		}
	}

	return func(name string) bool {
		matched, err := filepath.Match(needle, strings.ToLower(name))
		return err == nil && matched
	}
}
