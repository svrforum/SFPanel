package featurecluster

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
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
//
//	(a) HTTP 500 with code INTERNAL_ERROR
//	(b) the on-disk config now has Cluster.Enabled=false
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

// The read-path breaker exists so an unreachable leader costs one dial per
// cooldown instead of one per request: the sidebar polls status + nodes +
// overview together every 15s, and each was independently paying the full
// leader-proxy timeout while the peer was down.
func TestLeaderReadBreaker(t *testing.T) {
	const addr = "10.0.0.9:3629"
	base := time.Unix(1700000000, 0)
	h := &Handler{}

	if h.leaderReadBlocked(addr, base) {
		t.Fatal("a handler that has never proxied must not block the first attempt")
	}

	// A success must leave the breaker closed.
	h.recordLeaderRead(addr, nil, base)
	if h.leaderReadBlocked(addr, base) {
		t.Fatal("a successful read must not open the breaker")
	}

	h.recordLeaderRead(addr, errors.New("deadline exceeded"), base)
	if !h.leaderReadBlocked(addr, base.Add(leaderReadCooldown-time.Millisecond)) {
		t.Fatal("a repeat attempt inside the cooldown must be short-circuited")
	}
	if h.leaderReadBlocked(addr, base.Add(leaderReadCooldown)) {
		t.Fatal("the breaker must reopen once the cooldown elapses, so a recovered leader is noticed")
	}

	// A new leader address deserves its own probe rather than inheriting the
	// previous leader's failure.
	h.recordLeaderRead(addr, errors.New("deadline exceeded"), base)
	if h.leaderReadBlocked("10.0.0.10:3629", base) {
		t.Fatal("a failure against one leader must not block a different leader address")
	}

	// Recovery clears the record entirely.
	h.recordLeaderRead(addr, nil, base)
	if h.leaderReadBlocked(addr, base) {
		t.Fatal("a success must clear a previously-opened breaker")
	}
}

// A member whose cluster did not start at boot has no manager but still says
// Enabled in its config, and the UI used to offer it Init and Join. Init
// failed there with "already initialized" and its cleanup then deleted the
// node's Raft and certificate folders. Both must refuse, say why, and leave
// the folders alone; the status must report the node as a stopped member.
func TestInitAndJoinRefuseAMemberWhoseClusterDidNotStart(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Cluster.Enabled = true
	cfg.Cluster.NodeID = "existing-node"
	cfg.Cluster.DataDir = filepath.Join(dir, "data")
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	for _, d := range []string{cfg.Cluster.DataDir, cfg.Cluster.CertDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "keep"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{Config: cfg, ConfigPath: filepath.Join(dir, "config.yaml")}
	e := echo.New()

	for _, tc := range []struct {
		path string
		body string
		call func(echo.Context) error
	}{
		{"/cluster/init", `{"name":"fresh","advertise_address":"127.0.0.1"}`, h.InitCluster},
		{"/cluster/join", `{"leader_address":"127.0.0.1:3629","token":"t"}`, h.JoinCluster},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		if err := tc.call(e.NewContext(req, rec)); err != nil {
			t.Fatalf("%s returned error: %v", tc.path, err)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusConflict || body.Error.Code != response.ErrClusterNotRunning {
			t.Fatalf("%s: status %d code %q, want 409 %s (body %s)", tc.path, rec.Code, body.Error.Code, response.ErrClusterNotRunning, rec.Body.String())
		}
	}
	for _, d := range []string{cfg.Cluster.DataDir, cfg.Cluster.CertDir} {
		if _, err := os.Stat(filepath.Join(d, "keep")); err != nil {
			t.Fatalf("%s was cleaned up: %v", d, err)
		}
	}
	if !cfg.Cluster.Enabled || cfg.Cluster.NodeID != "existing-node" {
		t.Fatalf("in-memory membership was reset: %+v", cfg.Cluster)
	}

	req := httptest.NewRequest(http.MethodGet, "/cluster/status", nil)
	rec := httptest.NewRecorder()
	if err := h.GetStatus(e.NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Data struct {
			Enabled    bool `json:"enabled"`
			Configured bool `json:"configured"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if status.Data.Enabled || !status.Data.Configured {
		t.Fatalf("status = %s, want enabled:false configured:true", rec.Body.String())
	}
}
