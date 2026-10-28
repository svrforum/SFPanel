package system

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// freeGRPCPort is a port on 127.0.0.1 whose successor is free too: the
// cluster gRPC port, and the Raft port a manager binds at gRPC + 1.
func freeGRPCPort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		l1, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		p := l1.Addr().(*net.TCPAddr).Port
		l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+1))
		l1.Close()
		if err != nil {
			continue
		}
		l2.Close()
		return p
	}
	t.Fatal("no free port pair")
	return 0
}

// becomeManagerNode is the agent's side of a promotion: an agent advertising
// 127.0.0.1, with its config file beside it.
func becomeManagerNode(t *testing.T) *BecomeManagerHandler {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Database.Path = filepath.Join(dir, "sfpanel.db")
	cfg.Cluster = config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent, NodeID: "a1", NodeName: "a1",
		AdvertiseAddress: "127.0.0.1", GRPCPort: freeGRPCPort(t), Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}}}
	return &BecomeManagerHandler{Config: cfg, ConfigPath: filepath.Join(dir, "config.yaml")}
}

// promoting stands in for the promotion and the restart for one test, and
// reports each run of the promotion on runs and each exit on exits.
func promoting(t *testing.T) (runs chan cluster.ConvertRequest, exits chan int) {
	t.Helper()
	runs, exits = make(chan cluster.ConvertRequest, 4), make(chan int, 4)
	oldPromote, oldExit := promoteToManager, exitAfterPromotion
	t.Cleanup(func() { promoteToManager, exitAfterPromotion = oldPromote, oldExit })
	promoteToManager = func(_ *config.Config, _ string, _ *sql.DB, req cluster.ConvertRequest) error {
		runs <- req
		return nil
	}
	exitAfterPromotion = func(code int) { exits <- code }
	return runs, exits
}

func postBecomeManager(t *testing.T, h *BecomeManagerHandler, body string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/become-manager", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := h.BecomeManager(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data  struct{ Message string }       `json:"data"`
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v (body %s)", err, rec.Body.String())
	}
	return rec.Code, out.Error.Code, out.Data.Message + out.Error.Message
}

const becomeManagerBody = `{"token":"tok-1","ca_pin":"sha256:` + "abababababababababababababababababababababababababababababababab" + `","manager":"192.0.2.1:3629"}`

// noRun fails the test if the promotion ran.
func noRun(t *testing.T, runs chan cluster.ConvertRequest) {
	t.Helper()
	select {
	case req := <-runs:
		t.Fatalf("promoted: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

// The leader's call is retried once on any error, and the agent may have
// taken the first: the same token again is answered as taken, and the agent
// is promoted once, at the address it advertises, then restarts. Another
// token is another promotion, refused.
func TestBecomeManagerPromotesOnce(t *testing.T) {
	h := becomeManagerNode(t)
	runs, exits := promoting(t)
	for i := 0; i < 2; i++ {
		if status, _, msg := postBecomeManager(t, h, becomeManagerBody); status != http.StatusAccepted {
			t.Fatalf("request %d: %d %q", i+1, status, msg)
		}
	}
	other := strings.Replace(becomeManagerBody, "tok-1", "tok-2", 1)
	if status, code, msg := postBecomeManager(t, h, other); status != http.StatusConflict || code != response.ErrClusterPromoteRefused {
		t.Fatalf("another token: %d %s %q", status, code, msg)
	}
	select {
	case req := <-runs:
		if req.Token != "tok-1" || req.Pin != "sha256:"+strings.Repeat("ab", 32) || req.Manager != "192.0.2.1:3629" || req.Advertise != "127.0.0.1" {
			t.Fatalf("promoted with %+v", req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no promotion")
	}
	select {
	case code := <-exits:
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
	noRun(t, runs)
}

// A manager binds its Raft port, the gRPC port + 1, on the address it
// advertises when it starts. Held by something else, the node would come up
// without its cluster while the leader lists it as a manager: the agent
// refuses before it answers, with nothing changed.
func TestBecomeManagerChecksTheRaftPortFirst(t *testing.T) {
	h := becomeManagerNode(t)
	held, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", h.Config.Cluster.GRPCPort+1))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	runs, _ := promoting(t)
	status, _, msg := postBecomeManager(t, h, becomeManagerBody)
	if status != http.StatusBadRequest || !strings.Contains(msg, fmt.Sprintf("Raft port %d", h.Config.Cluster.GRPCPort+1)) {
		t.Fatalf("got %d %q, want 400 naming the Raft port", status, msg)
	}
	noRun(t, runs)
}

// Only an agent is promoted: the route is an agent's, and a node that is not
// one refuses before anything runs.
func TestBecomeManagerTakesAnAgentOnly(t *testing.T) {
	h := becomeManagerNode(t)
	h.Config.Cluster.Role = ""
	runs, _ := promoting(t)
	status, code, msg := postBecomeManager(t, h, becomeManagerBody)
	if status != http.StatusConflict || code != response.ErrClusterPromoteRefused || !strings.Contains(msg, "not an agent") {
		t.Fatalf("a manager: %d %s %q", status, code, msg)
	}
	noRun(t, runs)
}

// A request without the token, the fingerprint or the leader's address
// starts nothing.
func TestBecomeManagerRefusesAnIncompleteRequest(t *testing.T) {
	h := becomeManagerNode(t)
	runs, _ := promoting(t)
	for _, body := range []string{
		strings.Replace(becomeManagerBody, `"tok-1"`, `""`, 1),
		strings.Replace(becomeManagerBody, `"192.0.2.1:3629"`, `""`, 1),
		strings.Replace(becomeManagerBody, "sha256:", "md5:", 1),
	} {
		if status, code, msg := postBecomeManager(t, h, body); status != http.StatusBadRequest || code != response.ErrInvalidRequest {
			t.Fatalf("%s: %d %s %q", body, status, code, msg)
		}
	}
	noRun(t, runs)
}
