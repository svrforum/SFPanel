//go:build linux || darwin

package system

import "syscall"

// detachAttr returns SysProcAttr that puts the spawned watchdog into its own
// session so systemctl-driven SIGTERM of the parent doesn't propagate.
// Setsid creates a new session + process group; the child becomes orphaned
// when the parent exits and is reparented to PID 1, which keeps it alive
// across the systemctl restart that immediately follows.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
