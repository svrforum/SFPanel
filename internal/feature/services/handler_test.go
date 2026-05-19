package services

import "testing"

func TestIsProtectedServiceUnit(t *testing.T) {
	// Panel's own systemd unit is protected against stop/restart/disable.
	if !isProtectedServiceUnit("sfpanel.service") {
		t.Errorf("sfpanel.service should be protected")
	}
	// Case-insensitive — operators sometimes type uppercase.
	if !isProtectedServiceUnit("SFPanel.service") {
		t.Errorf("SFPanel.service (mixed case) should be protected")
	}
	// Anything else is unprotected.
	for _, name := range []string{
		"nginx.service",
		"docker.service",
		"sshd.service",
		"my-app.service",
	} {
		if isProtectedServiceUnit(name) {
			t.Errorf("%s should NOT be protected", name)
		}
	}
}

func TestValidServiceName(t *testing.T) {
	// Regression fence — name regex must accept normal units and reject
	// path traversal / shell metacharacters.
	accepts := []string{
		"nginx.service",
		"getty@tty1.service",
		"system-systemd-cryptsetup.service",
		"my-app_v2.service",
	}
	for _, n := range accepts {
		if !validServiceName.MatchString(n) {
			t.Errorf("validServiceName(%q) = false, want true", n)
		}
	}
	rejects := []string{
		"",
		"../etc/passwd.service",
		"nginx",
		"nginx.timer",
		"foo.service;rm",
		"foo bar.service",
	}
	for _, n := range rejects {
		if validServiceName.MatchString(n) {
			t.Errorf("validServiceName(%q) = true, want false", n)
		}
	}
}
