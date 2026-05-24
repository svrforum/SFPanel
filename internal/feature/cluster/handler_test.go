package featurecluster

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
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

// TestRollbackInit_FlipsConfigDisabledAndReturns500 covers the post-Init
// failure helper directly. We cannot drive a real SetConfig failure into the
// rollback without a live mgr.Init() (CA gen + Raft bootstrap + filesystem),
// which is far too heavy/stateful for a unit test and there's no Raft unit
// harness. So we exercise the helper in isolation with mgr=nil (Shutdown must
// be nil-safe) and assert the two contracts that matter:
//   (a) HTTP 500 with code INTERNAL_ERROR
//   (b) the on-disk config now has Cluster.Enabled=false
func TestRollbackInit_FlipsConfigDisabledAndReturns500(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &config.Config{}
	cfg.Cluster.Enabled = true
	cfg.Cluster.DataDir = filepath.Join(dir, "data")
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")

	// Seed the on-disk config so we can prove rollbackInit rewrites it.
	seed, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}
	if err := os.WriteFile(cfgPath, seed, 0600); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	h := &Handler{Config: cfg, ConfigPath: cfgPath}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/cluster/init", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// mgr=nil exercises the nil-safe Shutdown guard.
	if err := h.rollbackInit(c, nil, errors.New("boom")); err != nil {
		t.Fatalf("rollbackInit returned error: %v", err)
	}

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body.Success {
		t.Errorf("success=true, want false")
	}
	if body.Error.Code != "INTERNAL_ERROR" {
		t.Errorf("error code = %q, want INTERNAL_ERROR", body.Error.Code)
	}

	// In-memory config flipped to disabled.
	if h.Config.Cluster.Enabled {
		t.Errorf("in-memory Cluster.Enabled=true, want false")
	}

	// On-disk config flipped to disabled.
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	var got config.Config
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got.Cluster.Enabled {
		t.Errorf("on-disk Cluster.Enabled=true, want false")
	}
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
