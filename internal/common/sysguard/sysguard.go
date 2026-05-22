// Package sysguard centralises "do not touch this" deny-lists for
// destructive operations exposed via the panel API. Each predicate
// guards a different syscall surface:
//
//   - IsProtectedSystemdUnit guards systemctl operations.
//   - IsProtectedPID guards signal-sending and prevents self-targeting.
//   - IsPanelChildPID guards against killing subprocesses sfpanel
//     itself spawned (apt, docker compose, terminal PTYs, etc.) — they
//     share the panel's process group.
//
// New deny-list rules go here so every module benefits without
// duplicating logic. See docs/superpowers/plans/2026-05-22-module-hardening-followup.md
// Task 3.4 for the rollout plan.
package sysguard

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// protectedSystemdUnits is the canonical list of units that must never
// be stopped, restarted, disabled, or masked through the panel API.
//
// sfpanel.service is the obvious entry. dbus.service and systemd-journald
// are panel-critical: dbus is required for systemctl IPC, journald is the
// only path through which the panel reads service logs.
var protectedSystemdUnits = map[string]bool{
	"sfpanel.service":          true,
	"dbus.service":             true,
	"systemd-journald.service": true,
}

// IsProtectedSystemdUnit reports whether the given unit name (with or
// without the .service suffix, case-insensitive) is in the deny-list.
func IsProtectedSystemdUnit(name string) bool {
	if name == "" {
		return false
	}
	name = strings.ToLower(name)
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	return protectedSystemdUnits[name]
}

// IsProtectedPID reports whether sending a signal to pid would risk the
// host or the panel process itself. Refuses PID 0/1/2 (init/kthreadd) and
// the current process. Callers should also consult IsPanelChildPID for
// pgid-based child protection.
func IsProtectedPID(pid int) bool {
	if pid <= 2 {
		return true
	}
	if pid == os.Getpid() {
		return true
	}
	return false
}

// IsPanelChildPID reports whether the given PID is in the same process
// group as the running panel — meaning it was spawned (directly or
// transitively) by sfpanel and should not be killable through the
// generic process API. Returns an error if the PID cannot be inspected
// (does not exist, permission denied).
func IsPanelChildPID(pid int) (bool, error) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return false, fmt.Errorf("getpgid: %w", err)
	}
	return pgid == syscall.Getpgrp(), nil
}
