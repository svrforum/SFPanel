//go:build linux || darwin

package system

import (
	"os"
	"syscall"
)

// detachAttr returns SysProcAttr that puts the spawned watchdog into its own
// session so systemctl-driven SIGTERM of the parent doesn't propagate.
// Setsid creates a new session + process group; the child becomes orphaned
// when the parent exits and is reparented to PID 1, which keeps it alive
// across the systemctl restart that immediately follows.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// watchdogAlive probes whether the spawned watchdog process is still running.
//
// The watchdog is a direct child of this process (Setsid changes its session,
// not its parent), so a watchdog that exits before we probe becomes a ZOMBIE
// until reaped — and signal 0 to a zombie still succeeds, which would falsely
// report a dead watchdog as alive. So we first do a non-blocking Wait4(WNOHANG):
// a returned pid means the child has already exited (dead, and now reaped); a
// returned pid of 0 means it's still running. Signal 0 is the fallback for the
// (not-expected here) case where the process isn't our child.
//
// A nil proc means Start() never produced a process handle — treat as dead.
func watchdogAlive(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(proc.Pid, &ws, syscall.WNOHANG, nil)
	if err == nil && wpid == proc.Pid {
		// Child has already exited and we just reaped it.
		return false
	}
	// wpid == 0 (still running) or Wait4 failed (e.g. not our child) — fall back
	// to signal 0: nil error means the process exists, an error means it's gone.
	return proc.Signal(syscall.Signal(0)) == nil
}
