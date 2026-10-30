package system

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// An update the quorum guard refuses stops at the 409. It used to go on
// behind it — the guard returned response.Fail's error, nil once written — to
// look the release up and install it, the outage the guard is there to stop.
func TestAnUpdateTheQuorumGuardRefusesStopsThere(t *testing.T) {
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GitHubRelease{TagName: "v0.13.10"})
	}))
	t.Cleanup(srv.Close)
	withReleaseAPI(t, srv.URL)

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	raftPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"), AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1})
	if err := mgr.Init("guard"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	// A second manager voter, never heard from: updating this node would
	// leave one voter of two.
	v, _ := json.Marshal(cluster.Node{ID: "m2", Name: "m2", Kind: cluster.KindManager, Role: cluster.RoleVoter})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	h := newHandler("0.13.10")
	h.SetClusterMgr(mgr)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil), rec)
	err := h.RunUpdate(c)
	body := rec.Body.String()
	if n := lookups.Load(); n != 0 {
		t.Fatalf("the update went on past the refusal to look the release up (%d): %s", n, body)
	}
	if err == nil || rec.Code != http.StatusConflict || !strings.Contains(body, "0 of 2 voters would remain online (quorum=2)") {
		t.Fatalf("err %v, status %d, body %s", err, rec.Code, body)
	}
	// The refusal is the quorum's, by code: it shared UPDATE_IN_PROGRESS with
	// "Another update is already running", and the update card said that.
	if code := errorCode(t, rec); code != response.ErrClusterQuorum {
		t.Errorf("code %q, want %q", code, response.ErrClusterQuorum)
	}
}
