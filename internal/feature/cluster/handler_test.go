package featurecluster

import "testing"

func TestCheckQuorumAfterRemoval(t *testing.T) {
	cases := []struct {
		voters     int
		wantBlocks bool
		label      string
	}{
		{1, true, "1-voter cluster: removing the only voter destroys the cluster"},
		{2, true, "2-voter cluster: dropping to 1 voter loses any fault tolerance and only the next click bricks the cluster"},
		{3, false, "3-voter cluster: 2 remaining still has quorum (2 of 3 is N/2+1) — no fault tolerance left, but not below quorum"},
		{4, false, "4-voter cluster: 3 remaining still has quorum (3 of 4)"},
		{5, false, "5-voter cluster: 4 remaining still has quorum (3 of 5)"},
		{0, false, "no voters at all — nothing to enforce, fail open"},
	}
	for _, c := range cases {
		msg, blocks := checkQuorumAfterRemoval("test-node", c.voters)
		if blocks != c.wantBlocks {
			t.Errorf("%s: got blocks=%v want %v (msg=%q)", c.label, blocks, c.wantBlocks, msg)
		}
		if blocks && msg == "" {
			t.Errorf("%s: blocked without a message", c.label)
		}
	}
}
