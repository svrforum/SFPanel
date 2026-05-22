package sysguard

import (
	"os"
	"testing"
)

func TestIsProtectedSystemdUnit(t *testing.T) {
	cases := []struct {
		name     string
		expected bool
	}{
		{"sfpanel.service", true},
		{"SFPANEL.service", true}, // case-insensitive
		{"sfpanel", true},         // with or without .service
		{"dbus.service", true},
		{"systemd-journald.service", true},
		{"nginx.service", false},
		{"docker.service", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsProtectedSystemdUnit(c.name); got != c.expected {
			t.Errorf("IsProtectedSystemdUnit(%q) = %v, want %v", c.name, got, c.expected)
		}
	}
}

func TestIsProtectedPID(t *testing.T) {
	cases := []struct {
		pid      int
		expected bool
	}{
		{0, true},
		{1, true},
		{2, true},
		{os.Getpid(), true},
		{99999, false},
	}
	for _, c := range cases {
		if got := IsProtectedPID(c.pid); got != c.expected {
			t.Errorf("IsProtectedPID(%d) = %v, want %v", c.pid, got, c.expected)
		}
	}
}

func TestIsPanelChildPID_SelfReturnsTrue(t *testing.T) {
	isChild, err := IsPanelChildPID(os.Getpid())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !isChild {
		t.Error("self PID should be in panel's pgid")
	}
}

func TestIsPanelChildPID_NonExistentReturnsError(t *testing.T) {
	_, err := IsPanelChildPID(99999)
	if err == nil {
		t.Error("expected error for non-existent PID")
	}
}
