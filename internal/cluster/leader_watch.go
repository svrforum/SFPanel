package cluster

import "time"

// LeaderWatcher tracks how long the cluster has been without a leader and
// decides when to escalate operational logs from WARN-style raft noise up to
// an ERROR-level "no leader" alert that external monitoring can hook.
//
// The struct is intentionally pure — Manager pumps state in via Tick on a
// timer and acts on the returned advice. This keeps the threshold logic
// trivially testable without spinning up Raft.
type LeaderWatcher struct {
	// Threshold is how long the cluster must be without a leader before the
	// first ERROR-level alert fires. Defaults to 60 s.
	Threshold time.Duration
	// Repeat is the minimum spacing between successive alerts while the
	// no-leader condition persists. Defaults to 5 min.
	Repeat time.Duration

	noLeaderSince time.Time
	lastAlertAt   time.Time
}

// Tick is called periodically with the current Raft role info. Returns
// (true, secondsSinceLeaderLost) when an ERROR-level log should be emitted,
// or (false, 0) otherwise.
//
//   - isLeader=true OR leaderID!="" → healthy, resets the no-leader clock.
//   - First no-leader observation starts the clock but does not alert.
//   - Subsequent observations alert once Threshold is exceeded, then again
//     every Repeat to avoid spamming the journal during long outages.
func (w *LeaderWatcher) Tick(now time.Time, isLeader bool, leaderID string) (alert bool, sinceSec int64) {
	if w.Threshold <= 0 {
		w.Threshold = 60 * time.Second
	}
	if w.Repeat <= 0 {
		w.Repeat = 5 * time.Minute
	}

	if isLeader || leaderID != "" {
		// Reset both clocks — a fresh outage after recovery should be able
		// to alert on its own merits, not be throttled by the previous one.
		w.noLeaderSince = time.Time{}
		w.lastAlertAt = time.Time{}
		return false, 0
	}

	if w.noLeaderSince.IsZero() {
		w.noLeaderSince = now
		return false, 0
	}

	elapsed := now.Sub(w.noLeaderSince)
	if elapsed < w.Threshold {
		return false, 0
	}

	if w.lastAlertAt.IsZero() || now.Sub(w.lastAlertAt) > w.Repeat {
		w.lastAlertAt = now
		return true, int64(elapsed.Seconds())
	}
	return false, 0
}
