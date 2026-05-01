package release

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions compares two MAJOR.MINOR.PATCH version strings.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
// A leading "v" on either side is tolerated. Pre-release / build suffixes
// are not supported because release.yml emits plain numeric tags.
func CompareVersions(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", a, err)
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", b, err)
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1, nil
		}
		if pa[i] > pb[i] {
			return 1, nil
		}
	}
	return 0, nil
}

// IsForwardUpdate reports whether `latest` is strictly newer than `current`.
// Equal versions or downgrades return (false, nil). Parse failures return
// (false, error).
func IsForwardUpdate(current, latest string) (bool, error) {
	cmp, err := CompareVersions(current, latest)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

func parseSemver(v string) ([3]int, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return [3]int{}, fmt.Errorf("empty version")
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("expected MAJOR.MINOR.PATCH, got %d segments", len(parts))
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, fmt.Errorf("segment %d (%q) not numeric", i, p)
		}
		if n < 0 {
			return [3]int{}, fmt.Errorf("segment %d negative", i)
		}
		out[i] = n
	}
	return out, nil
}
