package alert

import (
	"regexp"
	"strings"
	"time"
)

// matchContainerPattern matches shell-style wildcards (* and ?) against a
// container name. Returns true on a full match. Other regex specials are
// quoted as literals — operators frequently put `.` in container names
// (e.g. `db.example.com`) so we can't let `.` mean "any character".
//
// Empty pattern never matches anything (avoid accidental "match all" via
// a misconfigured rule with no pattern).
func matchContainerPattern(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

// evaluateRestartLoop returns true when at least `threshold` of the
// supplied restart timestamps fall within the last `windowSec` seconds.
// Caller fetches the recent restart timestamps from container_events;
// this function is pure logic for testability.
func evaluateRestartLoop(restartTimesMillis []int64, threshold, windowSec int) bool {
	if threshold <= 0 || len(restartTimesMillis) < threshold {
		return false
	}
	cutoff := time.Now().Add(-time.Duration(windowSec) * time.Second).UnixMilli()
	count := 0
	for _, t := range restartTimesMillis {
		if t >= cutoff {
			count++
		}
	}
	return count >= threshold
}
