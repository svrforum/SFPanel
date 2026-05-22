package packages

import (
	"errors"
	"os"
	"syscall"
)

// dpkgFrontendLockPath is where apt acquires its package-manager mutex.
// Concurrent apt invocations contend on this file; we check it up front
// to give the user a fast 409 instead of letting apt block on the lock
// for the full streaming timeout.
const dpkgFrontendLockPath = "/var/lib/dpkg/lock-frontend"

// dpkgLockHeld reports whether another process currently holds the dpkg
// front-end lock. False positives are possible (race between this check
// and apt-get start), but the cost is just a noisier-than-necessary
// error message — apt itself will retry/fail cleanly.
//
// Implementation: try to acquire an exclusive flock with LOCK_NB. If the
// flock fails with EWOULDBLOCK/EAGAIN, the lock is held; release and
// return true. If we successfully acquire it, release immediately and
// return false. Other errors (file missing on non-Debian systems) return
// false — apt will fail more usefully than our pre-check would.
func dpkgLockHeld() bool {
	return dpkgLockHeldAt(dpkgFrontendLockPath)
}

// dpkgLockHeldAt is the testable form of dpkgLockHeld — it takes the
// path explicitly so tests can point at a tempfile.
func dpkgLockHeldAt(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false // missing or unreadable — let apt produce the real error
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// EWOULDBLOCK/EAGAIN → held by another process. Any other error
		// (e.g., the filesystem doesn't support flock) we treat as
		// "unknown" → let apt try.
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
