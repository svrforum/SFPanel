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
	"github.com/svrforum/SFPanel/internal/cluster"
)

// disbandHandler is a handler on mgr whose disband changes nothing here:
// performDisband wipes the node's cluster files and exits the process.
func disbandHandler(t *testing.T, mgr *cluster.Manager) *Handler {
	t.Helper()
	h := &Handler{Manager: mgr, ConfigPath: filepath.Join(t.TempDir(), "config.yaml")}
	h.disbandOnce.Do(func() {})
	return h
}

// postDisband answers POST /cluster/disband with query, and returns the
// status and the message, whichever of data or error carries it.
func postDisband(t *testing.T, h *Handler, query string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/disband"+query, nil)
	rec := httptest.NewRecorder()
	if err := h.DisbandCluster(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data  struct{ Message string } `json:"data"`
		Error struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v (body %s)", err, rec.Body.String())
	}
	return rec.Code, out.Data.Message + out.Error.Message
}

func addAgentRecord(t *testing.T, mgr *cluster.Manager, id, name string) {
	t.Helper()
	data, err := json.Marshal(cluster.Node{ID: id, Name: name, Kind: cluster.KindAgent})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: data}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

// An agent the leader could not tell keeps trying a cluster that is gone
// until someone resets it, so the answer names it.
func TestDisbandNamesTheAgentsItCouldNotTell(t *testing.T) {
	mgr := tokenLeader(t)
	addAgentRecord(t, mgr, "a-1", "edge-1")
	code, msg := postDisband(t, disbandHandler(t, mgr), "")
	if code != http.StatusOK || !strings.Contains(msg, "need 'sudo sfpanel agent reset --yes' on each: edge-1") {
		t.Fatalf("disband: %d %q", code, msg)
	}
	if mgr.GetNode("a-1") != nil {
		t.Fatal("the agent is still a member")
	}
}

// A cluster whose quorum is gone for good has no leader to forward a disband
// to, and a forced one is the way out the partition runbook gives: it runs
// on the node it was sent to, which can tell none of the agents.
func TestAForcedDisbandWithNoLeaderRunsWhereItIsSent(t *testing.T) {
	mgr := tokenLeader(t)
	addAgentRecord(t, mgr, "a-1", "edge-1")
	// A second voter that is not there: the leader loses its quorum and
	// steps down, and no leader can be elected.
	_ = mgr.GetRaft().PromoteToVoter("ghost", "127.0.0.1:1")
	for deadline := time.Now().Add(15 * time.Second); mgr.IsLeader() || mgr.GetRaft().LeaderID() != ""; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the leader did not step down")
		}
	}
	h := disbandHandler(t, mgr)
	if code, msg := postDisband(t, h, ""); code != http.StatusServiceUnavailable || !strings.Contains(msg, "No cluster leader available") {
		t.Fatalf("unforced: %d %q", code, msg)
	}
	code, msg := postDisband(t, h, "?force=true")
	if code != http.StatusOK || !strings.Contains(msg, "Forced local-only disband") || !strings.Contains(msg, "on each: edge-1") {
		t.Fatalf("forced: %d %q", code, msg)
	}
}
