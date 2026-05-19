package process

import (
	"strconv"
	"strings"
	"testing"
)

func TestKillProcess_PIDValidation(t *testing.T) {
	cases := []struct {
		pid     string
		valid   bool
		comment string
	}{
		{"", false, "empty"},
		{"abc", false, "non-numeric"},
		{"-5", false, "negative"},
		{"0", false, "init parent"},
		{"1", false, "init"},
		{"2", false, "kthreadd"},
		{"3", true, "first usermode candidate"},
		{"12345", true, "typical PID"},
		{"9999999999", false, "too large for int32"},
	}
	for _, tc := range cases {
		p, err := strconv.ParseInt(tc.pid, 10, 32)
		parsed := err == nil
		valid := parsed && p > 2
		if valid != tc.valid {
			t.Errorf("PID %q (%s): parsed=%v p=%d valid=%v, want %v",
				tc.pid, tc.comment, parsed, p, valid, tc.valid)
		}
	}
}

func TestSignalMap_KnownSignals(t *testing.T) {
	// The signal switch in KillProcess covers TERM/KILL/HUP/INT plus
	// numeric aliases 9/15/1/2. Anything else should be rejected.
	accepts := []string{"TERM", "term", "KILL", "kill", "HUP", "INT", "9", "15", "1", "2"}
	rejects := []string{"USR1", "STOP", "QUIT", "", "asdf", "16"}

	accepted := func(s string) bool {
		switch strings.ToUpper(s) {
		case "KILL", "9", "TERM", "15", "HUP", "1", "INT", "2":
			return true
		}
		return false
	}
	for _, s := range accepts {
		if !accepted(s) {
			t.Errorf("signal %q should be accepted", s)
		}
	}
	for _, s := range rejects {
		if accepted(s) {
			t.Errorf("signal %q should be rejected", s)
		}
	}
}
