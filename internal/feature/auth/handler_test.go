package featureauth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// newAuthHandlerForTest returns a Handler with a temp DB and a sensible
// Config. ClusterMgr and ClusterAccountsFn stay nil — callers wire in the
// FSM stub when they need to exercise the cluster-admin guard.
func newAuthHandlerForTest(t *testing.T) *Handler {
	t.Helper()
	db := openTestDB(t)
	return &Handler{
		DB:     db,
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "test-secret-not-for-prod", TokenExpiry: "1h"}},
	}
}

// withClusterAdminStub installs a ClusterAccountsFn that returns a single
// fake admin account. The concrete *cluster.Manager would require a running
// Raft to seed an account through SetAccount, so we inject the lookup at the
// handler seam instead — same code path the production manager would hit.
func withClusterAdminStub(h *Handler, username, passwordHash string) {
	h.ClusterAccountsFn = func() map[string]*cluster.AdminAccount {
		return map[string]*cluster.AdminAccount{
			username: {
				Username: username,
				Password: passwordHash,
			},
		}
	}
}

// TestSetupAdmin_RefusesWhenClusterFSMHoldsAdmin pins C2: a node joined to
// an existing cluster has an empty local admin table (admin lives in the
// FSM). Before this fix, /auth/setup would happily create a "second admin"
// row that the next leadership term would replicate as the authoritative
// account.
func TestSetupAdmin_RefusesWhenClusterFSMHoldsAdmin(t *testing.T) {
	h := newAuthHandlerForTest(t)
	withClusterAdminStub(h, "admin", "hash")

	body := strings.NewReader(`{"username":"intruder","password":"verylongpassword12345!"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/setup", body)
	req.RemoteAddr = "127.0.0.1:12345" // setup is restricted to loopback/LAN sources
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.SetupAdmin(c); err != nil {
		t.Fatalf("SetupAdmin returned err: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409 (cluster admin already exists); body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Cluster admin already configured")) {
		t.Errorf("response body lacks cluster-admin message: %s", rec.Body.String())
	}

	// And the local admin table stays empty — the handler must not have
	// reached the INSERT.
	var n int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM admin`).Scan(&n); err != nil {
		t.Fatalf("count admin: %v", err)
	}
	if n != 0 {
		t.Errorf("local admin rows=%d, want 0 (handler must short-circuit before INSERT)", n)
	}
}

// TestSetupAdmin_RejectsPublicSourceIP pins the first-run land-grab guard: a
// fresh install bound to 0.0.0.0 must refuse /auth/setup from a non-private
// source IP so a remote attacker can't claim the admin before the operator.
func TestSetupAdmin_RejectsPublicSourceIP(t *testing.T) {
	h := newAuthHandlerForTest(t)
	body := strings.NewReader(`{"username":"intruder","password":"verylongpassword12345!"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/setup", body)
	req.RemoteAddr = "203.0.113.7:40000" // TEST-NET-3, public
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if err := h.SetupAdmin(c); err != nil {
		t.Fatalf("SetupAdmin returned err: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403 (public setup blocked); body=%s", rec.Code, rec.Body.String())
	}
	var n int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM admin`).Scan(&n); err != nil {
		t.Fatalf("count admin: %v", err)
	}
	if n != 0 {
		t.Errorf("local admin rows=%d, want 0 (gate must block before INSERT)", n)
	}
}

func TestIsLoopbackOrPrivate(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "172.16.0.1": true,
		"192.168.1.5": true, "169.254.1.1": true, "fd00::1": true,
		"203.0.113.7": false, "8.8.8.8": false, "1.1.1.1": false, "": false, "nope": false,
	}
	for ip, want := range cases {
		if got := isLoopbackOrPrivate(ip); got != want {
			t.Errorf("isLoopbackOrPrivate(%q)=%v, want %v", ip, got, want)
		}
	}
}

// TestGetSetupStatus_FalseWhenClusterFSMHoldsAdmin pins the same invariant
// on the status endpoint that the UI polls. With FSM admin present the
// endpoint must report setup_required=false so the UI doesn't render the
// bootstrap form.
func TestGetSetupStatus_FalseWhenClusterFSMHoldsAdmin(t *testing.T) {
	h := newAuthHandlerForTest(t)
	withClusterAdminStub(h, "admin", "hash")

	req := httptest.NewRequest("GET", "/api/v1/auth/setup-status", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.GetSetupStatus(c); err != nil {
		t.Fatalf("GetSetupStatus returned err: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			SetupRequired bool `json:"setup_required"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if body.Data.SetupRequired {
		t.Errorf("setup_required=true returned even though FSM admin exists; body=%s", rec.Body.String())
	}
}

// TestGetSetupStatus_TrueWhenNoLocalNoCluster exercises the single-node
// first-boot path: no cluster, empty local admin table, setup_required must
// be true so the UI renders the bootstrap form.
func TestGetSetupStatus_TrueWhenNoLocalNoCluster(t *testing.T) {
	h := newAuthHandlerForTest(t)

	req := httptest.NewRequest("GET", "/api/v1/auth/setup-status", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := h.GetSetupStatus(c); err != nil {
		t.Fatalf("GetSetupStatus returned err: %v", err)
	}
	var body struct {
		Data struct {
			SetupRequired bool `json:"setup_required"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if !body.Data.SetupRequired {
		t.Errorf("setup_required=false on fresh single-node install; body=%s", rec.Body.String())
	}
}
