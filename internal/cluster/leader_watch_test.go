package cluster

import (
	"testing"
	"time"
)

func TestLeaderWatcher_LeaderPresent_NoAlert(t *testing.T) {
	w := &LeaderWatcher{Threshold: 60 * time.Second, Repeat: 5 * time.Minute}
	now := time.Unix(1_000_000, 0)

	if alert, _ := w.Tick(now, true, ""); alert {
		t.Fatal("expected no alert when this node is leader")
	}
	if alert, _ := w.Tick(now.Add(2*time.Hour), false, "peer-1"); alert {
		t.Fatal("expected no alert when a peer is leader")
	}
}

func TestLeaderWatcher_NoLeaderUnderThreshold(t *testing.T) {
	w := &LeaderWatcher{Threshold: 60 * time.Second, Repeat: 5 * time.Minute}
	base := time.Unix(1_000_000, 0)

	// First observation of no-leader — starts the clock but no alert
	if alert, _ := w.Tick(base, false, ""); alert {
		t.Fatal("first no-leader observation should not alert")
	}
	// Still under threshold — quiet
	if alert, _ := w.Tick(base.Add(30*time.Second), false, ""); alert {
		t.Fatal("under threshold should not alert")
	}
}

func TestLeaderWatcher_NoLeaderOverThreshold_Alerts(t *testing.T) {
	w := &LeaderWatcher{Threshold: 60 * time.Second, Repeat: 5 * time.Minute}
	base := time.Unix(1_000_000, 0)

	w.Tick(base, false, "")
	alert, secs := w.Tick(base.Add(61*time.Second), false, "")
	if !alert {
		t.Fatal("expected alert once threshold exceeded")
	}
	if secs < 61 {
		t.Fatalf("expected sinceSec >= 61, got %d", secs)
	}
}

func TestLeaderWatcher_RepeatThrottle(t *testing.T) {
	w := &LeaderWatcher{Threshold: 60 * time.Second, Repeat: 5 * time.Minute}
	base := time.Unix(1_000_000, 0)

	w.Tick(base, false, "")
	alert, _ := w.Tick(base.Add(61*time.Second), false, "")
	if !alert {
		t.Fatal("expected first alert")
	}

	// Within repeat window — no second alert
	if alert, _ := w.Tick(base.Add(2*time.Minute), false, ""); alert {
		t.Fatal("repeat window should suppress")
	}
	if alert, _ := w.Tick(base.Add(5*time.Minute), false, ""); alert {
		t.Fatal("at exactly repeat window we still suppress (strict <)")
	}

	// Past repeat window from first alert — re-alert
	if alert, _ := w.Tick(base.Add(61*time.Second+5*time.Minute+1*time.Second), false, ""); !alert {
		t.Fatal("expected re-alert past repeat window")
	}
}

func TestLeaderWatcher_RecoveryResets(t *testing.T) {
	w := &LeaderWatcher{Threshold: 60 * time.Second, Repeat: 5 * time.Minute}
	base := time.Unix(1_000_000, 0)

	w.Tick(base, false, "")
	w.Tick(base.Add(61*time.Second), false, "")

	// Leader recovers
	if alert, _ := w.Tick(base.Add(2*time.Minute), false, "peer-1"); alert {
		t.Fatal("recovery tick should not alert")
	}

	// Leader lost again — clock starts fresh, no immediate alert
	t2 := base.Add(3 * time.Minute)
	if alert, _ := w.Tick(t2, false, ""); alert {
		t.Fatal("first no-leader post-recovery should not alert")
	}
	if alert, _ := w.Tick(t2.Add(30*time.Second), false, ""); alert {
		t.Fatal("under threshold after recovery should not alert")
	}
	if alert, _ := w.Tick(t2.Add(61*time.Second), false, ""); !alert {
		t.Fatal("threshold after recovery should alert again")
	}
}

func TestLeaderWatcher_ZeroValuesGetDefaults(t *testing.T) {
	w := &LeaderWatcher{} // zero value
	base := time.Unix(1_000_000, 0)

	w.Tick(base, false, "")
	if alert, _ := w.Tick(base.Add(61*time.Second), false, ""); !alert {
		t.Fatal("zero-value Threshold should default to 60s and trigger at 61s")
	}
}
