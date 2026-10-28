package featurecluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/config"
)

// A leave that no leader took says so in its answer — the UI and the CLI
// with the service running show only the answer. The others still list the
// node, and in a cluster of two the one left has no quorum until someone
// removes it there.
func TestALeaveNoLeaderTookSaysSo(t *testing.T) {
	exited := make(chan struct{}, 1)
	defer func(old func(int)) { exitProcess = old }(exitProcess)
	exitProcess = func(int) { exited <- struct{}{} }

	mgr := tokenLeader(t)
	// A second voter that is not there: the leader steps down, and no
	// leader can be elected to take this node out.
	_ = mgr.GetRaft().PromoteToVoter("ghost", "127.0.0.1:1")
	for deadline := time.Now().Add(15 * time.Second); mgr.IsLeader() || mgr.GetRaft().LeaderID() != ""; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the leader did not step down")
		}
	}

	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Cluster.Enabled = true
	cfg.Cluster.DataDir = filepath.Join(dir, "data")
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	h := &Handler{Manager: mgr, Config: cfg, ConfigPath: filepath.Join(dir, "config.yaml")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/leave?force=true", nil)
	rec := httptest.NewRecorder()
	if err := h.LeaveCluster(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct{ Message string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v (body %s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !strings.Contains(out.Data.Message, "no leader took this node out") ||
		!strings.Contains(out.Data.Message, "sfpanel cluster remove") {
		t.Fatalf("leave: %d %q", rec.Code, out.Data.Message)
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the process was not restarted")
	}
}
