package alert

import (
	"testing"
	"time"
)

func TestMatchContainerPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*", "anything", true},
		{"nginx-*", "nginx-app", true},
		{"nginx-*", "nginx-", true},
		{"nginx-*", "apache-app", false},
		{"nginx-app", "nginx-app", true},
		{"nginx-app", "nginx-app-2", false},
		{"*-prod", "myapp-prod", true},
		{"*-prod", "myapp-dev", false},
		{"foo?bar", "fooXbar", true},
		{"foo?bar", "fooXYbar", false},
		// Regex special characters treated as literals.
		{"a.b", "a.b", true},
		{"a.b", "axb", false},
		// Empty pattern never matches anything.
		{"", "x", false},
	}
	for _, c := range cases {
		got := matchContainerPattern(c.pattern, c.name)
		if got != c.want {
			t.Errorf("match(%q, %q) = %v; want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestRestartLoopEvaluator(t *testing.T) {
	now := time.Now().UnixMilli()
	cases := []struct {
		name         string
		restartTimes []int64
		threshold    int
		windowSec    int
		want         bool
	}{
		{
			"3 restarts in 5min triggers",
			[]int64{now - 60_000, now - 120_000, now - 180_000},
			3, 300, true,
		},
		{
			"2 restarts in 5min does not trigger",
			[]int64{now - 60_000, now - 120_000},
			3, 300, false,
		},
		{
			"3 restarts spread over 6min does not trigger",
			[]int64{now - 60_000, now - 200_000, now - 360_000},
			3, 300, false,
		},
		{
			"empty list does not trigger",
			[]int64{},
			3, 300, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evaluateRestartLoop(c.restartTimes, c.threshold, c.windowSec)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
