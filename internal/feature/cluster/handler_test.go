package featurecluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

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

// newNilOverviewStub returns a *cluster.Manager wired up enough to satisfy
// GetStatus's call sites but with no raft node attached. With raft==nil:
//   - GetOverview() returns nil (the bug we're guarding against)
//   - IsLeader() returns false → handler takes the follower branch
//   - GetLeaderGRPCAddress() returns "" → proxyToLeader fails fast with
//     "no leader" so the handler falls through to the stale fallback
//
// This lets us drive GetStatus into the post-proxy path where it dereferences
// the nil overview, without depending on a hand-rolled interface seam.
func newNilOverviewStub(t *testing.T) *cluster.Manager {
	t.Helper()
	return cluster.NewManager(&config.ClusterConfig{NodeID: "stub-node"})
}

func TestGetStatus_NilOverviewReturnsStaleEnabled(t *testing.T) {
	h := &Handler{}
	h.setManager(newNilOverviewStub(t))
	defer h.setManager(nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.GetStatus(c); err != nil {
		t.Fatalf("GetStatus returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Data struct {
			Enabled bool   `json:"enabled"`
			Stale   bool   `json:"stale"`
			LocalID string `json:"local_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if !body.Data.Enabled {
		t.Errorf("enabled=false, want true (manager is set)")
	}
	if !body.Data.Stale {
		t.Errorf("stale=false, want true (overview unavailable)")
	}
}
