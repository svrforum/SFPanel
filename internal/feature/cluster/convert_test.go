package featurecluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// convertible gives the leader mgr a manager m2 it can convert: online, on a
// version that knows agents, labelled, in a cluster whose proxy secret
// switched long ago.
func convertible(t *testing.T, mgr *cluster.Manager) {
	t.Helper()
	data, err := json.Marshal(cluster.Node{ID: "m2", Name: "m2", Kind: cluster.KindManager, Role: cluster.RoleVoter,
		APIAddress: "192.0.2.20:3628", GRPCAddress: "192.0.2.20:3629", Labels: map[string]string{"group": "db"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: data}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	mgr.GetHeartbeat().RecordHeartbeat(&cluster.NodeMetrics{NodeID: "m2", Version: "test", Capabilities: []string{cluster.CapAgents}})
	// The FSM change installs the replicated secret process-wide.
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	if err := mgr.SetConfig("cluster_proxy_secret", "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig("cluster_proxy_secret_rotated_at", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

// nodeCall is a request ConvertNode sent a node.
type nodeCall struct {
	nodeID, method, path string
	body                 becomeAgentRequest
}

// answering is a callNode that records each call and answers status and
// answer.
func answering(calls *[]nodeCall, status int, answer string, err error) func(context.Context, string, string, string, []byte, string) (int, []byte, error) {
	return func(_ context.Context, nodeID, method, path string, body []byte, _ string) (int, []byte, error) {
		c := nodeCall{nodeID: nodeID, method: method, path: path}
		_ = json.Unmarshal(body, &c.body)
		*calls = append(*calls, c)
		return status, []byte(answer), err
	}
}

// postConvert answers POST /cluster/nodes/:id/convert and returns the
// status, the code and the message, whichever of data or error carries it.
func postConvert(t *testing.T, h *Handler, id string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/nodes/"+id+"/convert", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	if err := h.ConvertNode(c); err != nil {
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

// registerWith offers the leader a registration with token as node id at ip
// and returns its refusal. Every check that comes before the token is spent
// fails first or passes here: only the token's binding and the record still
// under m2 are left to answer.
func registerWith(mgr *cluster.Manager, token, id, ip string) error {
	_, err := mgr.HandleRegisterAgent(cluster.RegisterAgentInput{Token: token, NodeID: id, NodeName: "m2", APIAddress: ip + ":3628", GRPCAddress: ip + ":3629"})
	return err
}

// The leader gives the node a token made for it alone, at its address, and
// keeps what the node is known by for its registration.
func TestConvertNodeHandsTheNodeATokenMadeForIt(t *testing.T) {
	mgr := tokenLeader(t)
	convertible(t, mgr)
	var calls []nodeCall
	h := &Handler{Manager: mgr, callNode: answering(&calls, http.StatusAccepted, `{"success":true,"data":{"message":"leaving"}}`, nil)}
	status, _, msg := postConvert(t, h, "m2")
	if status != http.StatusAccepted || !strings.Contains(msg, "m2 is leaving the cluster and joining it again as an agent") {
		t.Fatalf("convert: %d %q", status, msg)
	}
	if len(calls) != 1 || calls[0].nodeID != "m2" || calls[0].method != http.MethodPost || calls[0].path != "/api/v1/cluster/become-agent" {
		t.Fatalf("calls %+v", calls)
	}
	key, err := mgr.CAPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if b := calls[0].body; b.CAPin != cluster.CAPin(key) || b.Manager != mgr.GetNode(mgr.LocalNodeID()).GRPCAddress || b.Token == "" {
		t.Fatalf("the node was sent %+v", b)
	}
	tok := calls[0].body.Token
	// Bound: another node, or this one elsewhere, is refused for the
	// binding; m2 at its address gets past it, to the record it still has.
	if err := registerWith(mgr, tok, "other", "192.0.2.20"); !errors.Is(err, cluster.ErrTokenBinding) {
		t.Fatalf("another node: %v", err)
	}
	if err := registerWith(mgr, tok, "m2", "198.51.100.7"); !errors.Is(err, cluster.ErrTokenBinding) {
		t.Fatalf("m2 elsewhere: %v", err)
	}
	if err := registerWith(mgr, tok, "m2", "192.0.2.20"); !errors.Is(err, cluster.ErrNodeAlreadyExists) {
		t.Fatalf("m2 at its address: %v", err)
	}
	raw, ok := mgr.GetRaft().GetFSM().GetConfigValue(cluster.ConversionIntentKey("m2"))
	var intent cluster.ConversionIntent
	if !ok || json.Unmarshal([]byte(raw), &intent) != nil || intent.Name != "m2" || intent.Labels["group"] != "db" {
		t.Fatalf("intent record %q", raw)
	}
}

// A node that refuses — a version without the route answers 404 — takes the
// conversion nowhere: the token and the intent record go again.
func TestConvertNodeTakesBackWhatTheNodeRefused(t *testing.T) {
	mgr := tokenLeader(t)
	convertible(t, mgr)
	var calls []nodeCall
	h := &Handler{Manager: mgr, callNode: answering(&calls, http.StatusNotFound, `{"message":"Not Found"}`, nil)}
	status, code, msg := postConvert(t, h, "m2")
	if status != http.StatusBadGateway || code != response.ErrClusterConvertRefused || !strings.Contains(msg, "404") || !strings.Contains(msg, "Not Found") {
		t.Fatalf("convert: %d %s %q", status, code, msg)
	}
	if len(calls) != 1 {
		t.Fatalf("calls %+v", calls)
	}
	if err := registerWith(mgr, calls[0].body.Token, "m2", "192.0.2.20"); !errors.Is(err, cluster.ErrTokenNotFound) {
		t.Fatalf("the token: %v", err)
	}
	if v, ok := mgr.GetRaft().GetFSM().GetConfigValue(cluster.ConversionIntentKey("m2")); ok {
		t.Fatalf("the intent record stayed: %s", v)
	}
}

// A node that did not answer may have taken the request and be on its way
// out, and it registers with the token: taking it back would strand it.
func TestConvertNodeKeepsTheTokenOfANodeThatDidNotAnswer(t *testing.T) {
	mgr := tokenLeader(t)
	convertible(t, mgr)
	var calls []nodeCall
	h := &Handler{Manager: mgr, callNode: answering(&calls, 0, "", errors.New("proxy to node \"m2\": connection reset"))}
	status, _, msg := postConvert(t, h, "m2")
	if status != http.StatusBadGateway || !strings.Contains(msg, "did not answer") {
		t.Fatalf("convert: %d %q", status, msg)
	}
	if err := registerWith(mgr, calls[0].body.Token, "m2", "192.0.2.20"); !errors.Is(err, cluster.ErrNodeAlreadyExists) {
		t.Fatalf("the token: %v", err)
	}
	if _, ok := mgr.GetRaft().GetFSM().GetConfigValue(cluster.ConversionIntentKey("m2")); !ok {
		t.Fatal("the intent record went")
	}
}

// The leader is not converted, and the answer says why; nothing is minted
// and no node is asked.
func TestConvertNodeRefusesTheLeader(t *testing.T) {
	mgr := tokenLeader(t)
	var calls []nodeCall
	h := &Handler{Manager: mgr, callNode: answering(&calls, http.StatusAccepted, `{}`, nil)}
	status, code, msg := postConvert(t, h, mgr.LocalNodeID())
	if status != http.StatusConflict || code != response.ErrClusterConvertRefused || !strings.Contains(msg, cluster.ErrConvertLeader.Error()) {
		t.Fatalf("convert the leader: %d %s %q", status, code, msg)
	}
	if len(calls) != 0 {
		t.Fatalf("calls %+v", calls)
	}
	if tokens, err := mgr.ListJoinTokens(); err != nil || len(tokens) != 0 {
		t.Fatalf("tokens %v %v", tokens, err)
	}
}

// One conversion at a time on the leader: two for one node would each
// record its intent, and the one the node refused would take the other's
// record away.
func TestConvertNodeStartsOneConversionAtATime(t *testing.T) {
	mgr := tokenLeader(t)
	convertible(t, mgr)
	held, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := &Handler{Manager: mgr, callNode: func(context.Context, string, string, string, []byte, string) (int, []byte, error) {
		if calls.Add(1) == 1 { // the first conversion waits for its node
			close(held)
			<-release
		}
		return http.StatusAccepted, []byte(`{"success":true,"data":{}}`), nil
	}}
	first := make(chan int, 1)
	go func() { // no t.Fatal off the test goroutine
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/api/v1/cluster/nodes/m2/convert", nil), rec)
		c.SetParamNames("id")
		c.SetParamValues("m2")
		_ = h.ConvertNode(c)
		first <- rec.Code
	}()
	<-held
	status, code, msg := postConvert(t, h, "m2")
	close(release)
	if status != http.StatusConflict || code != response.ErrClusterConvertRefused || !strings.Contains(msg, "Another conversion") {
		t.Fatalf("a second conversion meanwhile: %d %s %q", status, code, msg)
	}
	if status := <-first; status != http.StatusAccepted {
		t.Fatalf("the first: %d", status)
	}
}

// becomeAgentNode is the node side of a conversion: a manager that is not the
// leader, advertising 127.0.0.1, whose panel is to serve managed HTTPS.
func becomeAgentNode(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Server.TLS.Dir = filepath.Join(dir, "tls")
	cfg.Database.Path = filepath.Join(dir, "sfpanel.db")
	cfg.Cluster = config.ClusterConfig{Enabled: true, NodeID: "m2", NodeName: "m2", AdvertiseAddress: "127.0.0.1", GRPCPort: 3629}
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("node-key", []string{"node-key"})
	// No Raft: not the leader, and no record of its own to read the address from.
	return &Handler{Manager: cluster.NewManager(&cfg.Cluster), Config: cfg, ConfigPath: filepath.Join(dir, "config.yaml")}
}

// converting stands in for the conversion and the restart for one test, and
// reports each run of the conversion on runs and each exit on exits.
func converting(t *testing.T) (runs chan cluster.ConvertRequest, exits chan int) {
	t.Helper()
	runs, exits = make(chan cluster.ConvertRequest, 4), make(chan int, 4)
	oldConvert, oldExit := convertToAgent, exitProcess
	t.Cleanup(func() { convertToAgent, exitProcess = oldConvert, oldExit })
	convertToAgent = func(_ *cluster.Manager, _ *config.Config, _ string, _ *sql.DB, _ string, req cluster.ConvertRequest) error {
		runs <- req
		return nil
	}
	exitProcess = func(code int) { exits <- code }
	return runs, exits
}

func postBecomeAgent(t *testing.T, h *Handler, body string, signed bool) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/become-agent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signed {
		req.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(http.MethodPost, "/api/v1/cluster/become-agent"))
	}
	rec := httptest.NewRecorder()
	if err := h.BecomeAgent(echo.New().NewContext(req, rec)); err != nil {
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

const becomeAgentBody = `{"token":"tok-1","ca_pin":"sha256:` + "abababababababababababababababababababababababababababababababab" + `","manager":"192.0.2.1:3629"}`

// Only the leader starts a conversion: a request with an operator's session,
// not the cluster's signature, is refused, and nothing runs.
func TestBecomeAgentIsTheLeadersToAsk(t *testing.T) {
	h := becomeAgentNode(t)
	runs, _ := converting(t)
	status, code, msg := postBecomeAgent(t, h, becomeAgentBody, false)
	if status != http.StatusForbidden || code != response.ErrPermissionDenied || !strings.Contains(msg, "/convert") {
		t.Fatalf("unsigned: %d %s %q", status, code, msg)
	}
	select {
	case req := <-runs:
		t.Fatalf("converted: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

// The leader's call is retried once on any error, and the node may have
// taken the first one: the same token again is answered as taken, and the
// node converts once. Another token is another conversion, refused.
func TestBecomeAgentConvertsOnce(t *testing.T) {
	h := becomeAgentNode(t)
	runs, exits := converting(t)
	for i := 0; i < 2; i++ {
		if status, _, msg := postBecomeAgent(t, h, becomeAgentBody, true); status != http.StatusAccepted {
			t.Fatalf("request %d: %d %q", i+1, status, msg)
		}
	}
	other := strings.Replace(becomeAgentBody, "tok-1", "tok-2", 1)
	if status, code, msg := postBecomeAgent(t, h, other, true); status != http.StatusConflict || code != response.ErrClusterConvertRefused {
		t.Fatalf("another token: %d %s %q", status, code, msg)
	}
	select {
	case req := <-runs:
		if req.Token != "tok-1" || req.Manager != "192.0.2.1:3629" || req.Advertise != "127.0.0.1" || !strings.Contains(string(req.PanelCA), "BEGIN CERTIFICATE") || req.CheckPanelCert == nil {
			t.Fatalf("converted with %+v", req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no conversion")
	}
	select {
	case code := <-exits:
		if code != 1 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
	select {
	case req := <-runs:
		t.Fatalf("converted twice: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

// A node that leads now is not converted: the leader's own gates assumed
// another leader, and a leader's leave hands its leadership on first.
func TestBecomeAgentRefusesOnTheLeader(t *testing.T) {
	mgr := tokenLeader(t)
	h := becomeAgentNode(t)
	h.Manager = mgr
	runs, _ := converting(t)
	status, code, msg := postBecomeAgent(t, h, becomeAgentBody, true)
	if status != http.StatusConflict || code != response.ErrClusterConvertRefused || !strings.Contains(msg, cluster.ErrConvertLeader.Error()) {
		t.Fatalf("on the leader: %d %s %q", status, code, msg)
	}
	select {
	case req := <-runs:
		t.Fatalf("converted: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

// What the agent's panel needs is checked before the node answers, with
// nothing changed: once it has left, a node that cannot be an agent could
// only end standalone. The address the cluster has for it must be one of its
// own — an agent listens there — and its panel's certificate must name it.
func TestBecomeAgentChecksTheAgentsAddressFirst(t *testing.T) {
	for _, tc := range []struct{ advertise, reason string }{
		{"192.0.2.10", "not an address of this server"},
		// Loopback, but not an address the managed certificate names.
		{"127.0.0.2", "does not verify for 127.0.0.2"},
	} {
		t.Run(tc.advertise, func(t *testing.T) {
			h := becomeAgentNode(t)
			h.Config.Cluster.AdvertiseAddress = tc.advertise
			runs, _ := converting(t)
			status, _, msg := postBecomeAgent(t, h, becomeAgentBody, true)
			if status != http.StatusBadRequest || !strings.Contains(msg, tc.reason) {
				t.Fatalf("got %d %q, want 400 %q", status, msg, tc.reason)
			}
			select {
			case req := <-runs:
				t.Fatalf("converted: %+v", req)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}
