package system

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// A node that became a cluster member without a restart — a live init or
// join from the UI — kept its boot-time nil manager, and its update quorum
// guard let anything through until it restarted.
func TestTheQuorumGuardSeesAManagerSetAfterBoot(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	raftPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"), AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1})
	if err := mgr.Init("live"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	// A second manager voter, never heard from: updating this node would
	// leave one voter of two.
	v, _ := json.Marshal(cluster.Node{ID: "m2", Name: "m2", Kind: cluster.KindManager, Role: cluster.RoleVoter})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	h := newHandler("0.80.0") // booted standalone
	h.SetClusterMgr(mgr)      // then joined, live
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil), rec)
	if err := h.clusterUpdateQuorumGuard(c); err == nil && rec.Code == http.StatusOK {
		t.Fatalf("the guard let an update through: %d %s", rec.Code, rec.Body)
	}
	// The guard's code is shared with "Another update is already running";
	// the message is what says it was the quorum.
	if body := rec.Body.String(); !strings.Contains(body, "UPDATE_IN_PROGRESS") || !strings.Contains(body, "0 of 2 voters would remain online (quorum=2)") {
		t.Fatalf("refused for another reason: %d %s", rec.Code, rec.Body)
	}
}
