// Package safe contains process-wide helpers that wrap unsafe primitives
// (bare goroutines, panicking entrypoints) so a misbehaving handler
// can't take the panel down.
package safe

import (
	"log/slog"
	"runtime/debug"
)

// Go spawns a goroutine that recovers from any panic, logs the panic
// site with its stack trace, and returns. The bare `go func()` spawns
// scattered across the codebase had no such guard — a nil deref inside
// a background collector (monitor history, audit pruner, alert dispatch,
// terminal scrollback writer) killed the whole process instead of
// surviving as a localized failure.
//
// component identifies the caller in the structured log line so the
// operator can grep journald for the offending subsystem. The same
// recover pattern was duplicated inline in alert/manager.go, monitor/
// retention loops, and a few one-offs; this consolidates it.
func Go(component string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("background goroutine panicked",
					"component", component,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}
