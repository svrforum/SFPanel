package cluster

import "testing"

// TestWouldDropBelowQuorumOnLeave covers the pure quorum-on-leave decision.
// The function takes selfID plus a map of every voter ID to its live health
// status (self included) and decides whether self departing would leave the
// surviving voters unable to form quorum. Only StatusOnline counts as healthy;
// suspect/offline/joining/missing all count as unreachable.
func TestWouldDropBelowQuorumOnLeave(t *testing.T) {
	cases := []struct {
		name       string
		selfID     string
		voters     map[string]NodeStatus
		wantBlock  bool
		wantReason bool // expect a non-empty reason when blocked
	}{
		{
			// 2 voters total, peer offline: remaining=1, quorum=1, healthy=0 → BLOCK.
			name:       "2 voters peer offline (orphan case)",
			selfID:     "self",
			voters:     map[string]NodeStatus{"self": StatusOnline, "peer": StatusOffline},
			wantBlock:  true,
			wantReason: true,
		},
		{
			// 2 voters total, peer online: remaining=1, quorum=1, healthy=1 → allow.
			name:      "2 voters peer online",
			selfID:    "self",
			voters:    map[string]NodeStatus{"self": StatusOnline, "peer": StatusOnline},
			wantBlock: false,
		},
		{
			// 3 voters total, both peers online: remaining=2, quorum=2, healthy=2 → allow.
			name:      "3 voters both peers online",
			selfID:    "self",
			voters:    map[string]NodeStatus{"self": StatusOnline, "p1": StatusOnline, "p2": StatusOnline},
			wantBlock: false,
		},
		{
			// 3 voters total, one peer offline: remaining=2, quorum=2, healthy=1 → BLOCK.
			name:       "3 voters one peer offline",
			selfID:     "self",
			voters:     map[string]NodeStatus{"self": StatusOnline, "p1": StatusOnline, "p2": StatusOffline},
			wantBlock:  true,
			wantReason: true,
		},
		{
			// 1 voter total (self only): remaining=0, quorum=1, healthy=0 → BLOCK (degenerate).
			name:       "1 voter self only",
			selfID:     "self",
			voters:     map[string]NodeStatus{"self": StatusOnline},
			wantBlock:  true,
			wantReason: true,
		},
		{
			// suspect peer is NOT healthy — only StatusOnline counts.
			name:       "2 voters peer suspect counts as unhealthy",
			selfID:     "self",
			voters:     map[string]NodeStatus{"self": StatusOnline, "peer": StatusSuspect},
			wantBlock:  true,
			wantReason: true,
		},
		{
			// joining peer is NOT healthy either.
			name:       "2 voters peer joining counts as unhealthy",
			selfID:     "self",
			voters:     map[string]NodeStatus{"self": StatusOnline, "peer": StatusJoining},
			wantBlock:  true,
			wantReason: true,
		},
		{
			// 5 voters total, self + 2 online + 2 offline: remaining=4, quorum=3,
			// healthy=2 → BLOCK (not enough reachable survivors).
			name:   "5 voters two survivors offline",
			selfID: "self",
			voters: map[string]NodeStatus{
				"self": StatusOnline, "p1": StatusOnline, "p2": StatusOnline,
				"p3": StatusOffline, "p4": StatusOffline,
			},
			wantBlock:  true,
			wantReason: true,
		},
		{
			// 5 voters total, self + 3 online + 1 offline: remaining=4, quorum=3,
			// healthy=3 → allow.
			name:   "5 voters three survivors online",
			selfID: "self",
			voters: map[string]NodeStatus{
				"self": StatusOnline, "p1": StatusOnline, "p2": StatusOnline,
				"p3": StatusOnline, "p4": StatusOffline,
			},
			wantBlock: false,
		},
		{
			// self's own status is irrelevant — it is leaving regardless. Even if
			// self shows suspect, an online remaining quorum allows the leave.
			name:      "self suspect does not affect remaining-voter count",
			selfID:    "self",
			voters:    map[string]NodeStatus{"self": StatusSuspect, "p1": StatusOnline, "p2": StatusOnline},
			wantBlock: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, blocked := wouldDropBelowQuorumOnLeave(c.selfID, c.voters)
			if blocked != c.wantBlock {
				t.Fatalf("blocked=%v want %v (reason=%q)", blocked, c.wantBlock, reason)
			}
			if c.wantReason && reason == "" {
				t.Fatalf("blocked but reason was empty")
			}
			if !blocked && reason != "" {
				t.Fatalf("not blocked but got reason=%q", reason)
			}
		})
	}
}

// TestWouldDropBelowQuorumOnLeave_SelfNotInMap guards the case where selfID is
// not present among the voters (e.g. self is a non-voter or unknown). The
// function should treat every entry as a remaining voter.
func TestWouldDropBelowQuorumOnLeave_SelfNotInMap(t *testing.T) {
	// self is not in the voter set; two online voters remain → quorum 2,
	// healthy 2 → allow.
	if reason, blocked := wouldDropBelowQuorumOnLeave("self", map[string]NodeStatus{
		"p1": StatusOnline, "p2": StatusOnline,
	}); blocked {
		t.Fatalf("expected allow when self is not a voter, got blocked (reason=%q)", reason)
	}
	// self not in set, one online + one offline → remaining 2, quorum 2,
	// healthy 1 → BLOCK.
	if _, blocked := wouldDropBelowQuorumOnLeave("self", map[string]NodeStatus{
		"p1": StatusOnline, "p2": StatusOffline,
	}); !blocked {
		t.Fatalf("expected block when only 1 of 2 remaining voters is reachable")
	}
}
