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
	if body := rec.Body.String(); !strings.Contains(body, "CLUSTER_QUORUM") || !strings.Contains(body, "0 of 2 voters would remain online (quorum=2)") {
		t.Fatalf("refused for another reason: %d %s", rec.Code, rec.Body)
	}
}

// A voter that stopped a moment ago still has its last ping inside the
// heartbeat timeout, and was counted among the survivors: of three voters,
// one just stopped and this one updating would leave one. Its stream ended,
// and the guard asks whether it is up now.
func TestTheQuorumGuardDoesNotCountAVoterWhoseStreamEnded(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	raftPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"), AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1})
	if err := mgr.Init("live"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	hb := mgr.GetHeartbeat()
	for _, id := range []string{"m2", "m3"} {
		v, _ := json.Marshal(cluster.Node{ID: id, Name: id, Kind: cluster.KindManager, Role: cluster.RoleVoter})
		if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		hb.RecordHeartbeat(&cluster.NodeMetrics{NodeID: id})
		hb.StreamOpened(id)
	}
	h := newHandler("0.80.0")
	h.SetClusterMgr(mgr)
	guard := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		_ = h.clusterUpdateQuorumGuard(echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil), rec))
		return rec
	}
	if rec := guard(); rec.Body.Len() != 0 {
		t.Fatalf("with every voter up: %d %s", rec.Code, rec.Body)
	}
	hb.StreamEnded("m3")
	if body := guard().Body.String(); !strings.Contains(body, "CLUSTER_QUORUM") || !strings.Contains(body, "1 of 3 voters would remain online (quorum=2)") {
		t.Fatalf("with m3 stopped a moment ago: %s", body)
	}
}
