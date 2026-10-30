package compose

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	echoMw "github.com/labstack/echo/v4/middleware"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/docker"
)

// brokerBundle is what the fake source serves as its bundle: past the gzip
// middleware's MinLength, and compressible.
var brokerBundle = bytes.Repeat([]byte("a stack on an agent, carried by a manager "), 2048)

func brokerBundleSha() string {
	sum := sha256.Sum256(brokerBundle)
	return hex.EncodeToString(sum[:])
}

// signedWith reports whether r carries an internal-proxy header made with key
// for the method and request-URI it arrived with (the v2 format documented at
// auth.InternalProxyHeaderV2): what an agent checks of its managers.
func signedWith(key string, r *http.Request) bool {
	parts := strings.SplitN(r.Header.Get(auth.InternalProxyHeaderV2), ":", 3)
	if len(parts) != 3 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(parts[0] + ":" + parts[1] + ":" + r.Method + ":" + r.URL.RequestURI()))
	return hmac.Equal([]byte(parts[2]), []byte(hex.EncodeToString(mac.Sum(nil))))
}

func writeTestJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeSource is the source agent's panel: a migration session of its stack
// "demo" as MigrateSource and its three routes serve one, behind the panel's
// own gzip middleware. A session request not signed with the agent's key is
// refused, as an agent refuses it.
type fakeSource struct {
	*httptest.Server
	key     string
	outcome chan string // what the session waits for
	ended   chan string // how each session ended: its outcome, "left" or "timeout"

	mu            sync.Mutex
	info          echo.HandlerFunc // nil: session_version 1
	served        []byte           // the bundle served; nil: brokerBundle, as announced
	servedSha     string           // its sha header; "": brokerBundle's
	outcomeStatus int              // the outcome's answer; 0: 200, and the session hears it
	mute          bool             // the session ends on its outcome without a word, as a source dying then would
	sessions      int              // session POSTs
	session       []byte           // the last session POST's body
	encodings     []string         // each bundle request's Accept-Encoding
	outcomes      []string
}

func newFakeSource(t *testing.T) *fakeSource {
	t.Helper()
	s := &fakeSource{key: "a1-key", outcome: make(chan string, 1), ended: make(chan string, 4)}
	e := echo.New()
	// The router's own (router.go): a client that takes gzip gets the bundle
	// compressed, and its length dropped.
	e.Use(echoMw.GzipWithConfig(echoMw.GzipConfig{Level: 5, MinLength: 1024}))
	g := e.Group("/api/v1/docker/compose/:project")
	g.GET("/migrate-source/info", s.serveInfo)
	g.POST("/migrate-source", s.signed(s.serveSession))
	g.GET("/migrate-source/bundle", s.signed(s.serveBundle))
	g.POST("/migrate-source/outcome", s.signed(s.serveOutcome))
	s.Server = httptest.NewTLSServer(e)
	t.Cleanup(s.Close)
	return s
}

func (s *fakeSource) signed(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !signedWith(s.key, c.Request()) {
			return c.JSON(http.StatusUnauthorized, map[string]any{"success": false, "error": map[string]string{"code": "MISSING_TOKEN", "message": "This node accepts only requests its managers sign"}})
		}
		return next(c)
	}
}

// serveInfo is reached through the agent's cluster port, as ProxyToNode goes.
func (s *fakeSource) serveInfo(c echo.Context) error {
	s.mu.Lock()
	info := s.info
	s.mu.Unlock()
	if info != nil {
		return info(c)
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "data": map[string]any{"session_version": 1, "node_id": "a1", "arch": runtime.GOARCH}})
}

func (s *fakeSource) serveSession(c echo.Context) error {
	body, _ := io.ReadAll(c.Request().Body)
	s.mu.Lock()
	s.sessions++
	s.session = body
	s.mu.Unlock()
	w := c.Response()
	w.Header().Set(echo.HeaderContentType, "text/event-stream")
	w.WriteHeader(http.StatusOK)
	event := func(ev map[string]any) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		w.Flush()
	}
	event(map[string]any{"phase": PhaseQuiesce, "message": "Stopping source stack...", "done": false})
	event(map[string]any{"phase": PhasePackage, "message": "Packaging stack (data + images)...", "done": false})
	event(map[string]any{"phase": PhaseReady, "session": "s1", "sha256": brokerBundleSha(), "bytes": len(brokerBundle), "done": false})
	select {
	case result := <-s.outcome:
		s.mu.Lock()
		mute := s.mute
		s.mu.Unlock()
		switch {
		case mute:
		case result == outcomeImported:
			event(map[string]any{"phase": PhaseFinalize, "message": "Applying source disposition (retain)...", "done": false})
			event(map[string]any{"phase": PhaseDone, "message": "Migration complete.", "done": true})
		case result == outcomeFailed:
			event(map[string]any{"phase": PhaseRollback, "message": "transfer/restore failed; source restarted: the manager reports that the target did not take the stack", "done": true})
		default:
			event(map[string]any{"phase": PhaseError, "message": "the source was left stopped", "done": true})
		}
		s.ended <- result
	case <-c.Request().Context().Done():
		s.ended <- "left"
	case <-time.After(30 * time.Second):
		s.ended <- "timeout"
	}
	return nil
}

// sessionEnd is how the session ended, once it has.
func (s *fakeSource) sessionEnd(t *testing.T) string {
	t.Helper()
	select {
	case how := <-s.ended:
		return how
	case <-time.After(10 * time.Second):
		t.Fatal("the source's session did not end")
		return ""
	}
}

func (s *fakeSource) serveBundle(c echo.Context) error {
	s.mu.Lock()
	s.encodings = append(s.encodings, c.Request().Header.Get("Accept-Encoding"))
	bundle, sha := s.served, s.servedSha
	s.mu.Unlock()
	if bundle == nil {
		bundle = brokerBundle
	}
	if sha == "" {
		sha = brokerBundleSha()
	}
	if c.QueryParam("session") != "s1" {
		return c.JSON(http.StatusNotFound, map[string]any{"success": false, "error": map[string]string{"code": "NOT_FOUND", "message": "no such migration session for this stack"}})
	}
	w := c.Response()
	w.Header().Set(echo.HeaderContentType, echo.MIMEOctetStream)
	w.Header().Set(echo.HeaderContentLength, strconv.Itoa(len(bundle)))
	w.Header().Set(migrationShaHeader, sha)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(bundle)
	return err
}

func (s *fakeSource) serveOutcome(c echo.Context) error {
	var req struct {
		Result string `json:"result"`
	}
	if err := c.Bind(&req); err != nil || c.QueryParam("session") != "s1" {
		return c.JSON(http.StatusBadRequest, map[string]any{"success": false, "error": map[string]string{"code": "INVALID_REQUEST", "message": "bad outcome"}})
	}
	s.mu.Lock()
	s.outcomes = append(s.outcomes, req.Result)
	status := s.outcomeStatus
	s.mu.Unlock()
	if status != 0 {
		return c.JSON(status, map[string]any{"success": false, "error": map[string]string{"code": "INVALID_REQUEST", "message": "the migration session has ended"}})
	}
	select {
	case s.outcome <- req.Result:
	default:
	}
	return c.JSON(http.StatusOK, map[string]any{"success": true, "data": map[string]string{"result": req.Result}})
}

func (s *fakeSource) seen() (sessions int, session []byte, encodings, outcomes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions, s.session, append([]string(nil), s.encodings...), append([]string(nil), s.outcomes...)
}

// fakeTarget is the target agent's panel: its pre-flight facts, and its
// migrate-import, which takes a bundle signed for it whose sha matches the
// header — what the real one checks before it writes anything.
type fakeTarget struct {
	*httptest.Server
	key string

	mu      sync.Mutex
	policy  bool  // target-info states import_policy 1
	ports   []int // target-info's ports in use
	status  int   // migrate-import's answer; 0 is 200
	imports int
	got     []byte
	length  int64 // the import request's Content-Length
	header  http.Header
}

func newFakeTarget(t *testing.T, policy bool) *fakeTarget {
	t.Helper()
	d := &fakeTarget{key: "a2-key", policy: policy}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/docker/compose/migrate/target-info", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		data := map[string]any{"arch": runtime.GOARCH, "freeBytes": 1 << 40, "dockerFreeBytes": 1 << 40, "portsInUse": d.ports}
		if d.policy {
			data["import_policy"] = 1
		}
		d.mu.Unlock()
		writeTestJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
	})
	mux.HandleFunc("POST /api/v1/docker/compose/migrate-import", func(w http.ResponseWriter, r *http.Request) {
		if !signedWith(d.key, r) {
			writeTestJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": map[string]string{"code": "MISSING_TOKEN", "message": "This node accepts only requests its managers sign"}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		d.mu.Lock()
		d.imports++
		d.got, d.length, d.header = body, r.ContentLength, r.Header.Clone()
		status := d.status
		d.mu.Unlock()
		switch {
		case hex.EncodeToString(sum[:]) != r.Header.Get(migrationShaHeader):
			writeTestJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": map[string]string{"code": "COMPOSE_ERROR", "message": "bundle: bundle hash mismatch"}})
		case status != 0:
			writeTestJSON(w, status, map[string]any{"success": false, "error": map[string]string{"code": "COMPOSE_ERROR", "message": "the target refused the stack"}})
		default:
			writeTestJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]string{"status": "ok", "stackId": "demo"}})
		}
	})
	d.Server = httptest.NewTLSServer(mux)
	t.Cleanup(d.Close)
	return d
}

func (d *fakeTarget) seen() (imports int, got []byte, length int64, header http.Header) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.imports, d.got, d.length, d.header
}

// panelHop is a fake agent panel as its node's cluster port reaches it: the
// loopback hop of a request a manager proxies there (ProxyToNode).
type panelHop struct{ srv *httptest.Server }

func (p panelHop) URL(path string) string { return p.srv.URL + path }
func (p panelHop) HTTPClient(timeout time.Duration) (*http.Client, error) {
	return &http.Client{Transport: p.srv.Client().Transport, Timeout: timeout}, nil
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// addAgent records agent id in the managers' state with panel as its panel,
// whose CA and key the managers hold, and a cluster port of its own whose
// loopback hop reaches that panel.
func addAgent(t *testing.T, mgr *cluster.Manager, id string, panel *httptest.Server, key string) {
	t.Helper()
	srv, err := cluster.NewGRPCServer(mgr, 0, panelHop{panel})
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	if err := srv.Start(addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	v, _ := json.Marshal(cluster.Node{ID: id, Name: id, Kind: cluster.KindAgent, APIAddress: panel.Listener.Addr().String(), GRPCAddress: addr})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetPanelCA(id, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: panel.Certificate().Raw}))); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig(cluster.AgentKeyConfigKey(id), key); err != nil {
		t.Fatal(err)
	}
}

// brokerCluster is migrationManager's cluster with both ends of the
// migration agents: a1 the source, a2 the target.
func brokerCluster(t *testing.T, src *fakeSource, dst *fakeTarget) *cluster.Manager {
	t.Helper()
	mgr := migrationManager(t, src.Server, true, true)
	addAgent(t, mgr, "a1", src.Server, src.key)
	addAgent(t, mgr, "a2", dst.Server, dst.key)
	return mgr
}

// brokerHandler is the manager's compose handler. Its stacks root holds no
// stack, and docker is the logging fake: the stack is on the agent, and the
// manager has no docker of its own to run for it.
func brokerHandler(t *testing.T, mgr *cluster.Manager) (*Handler, string) {
	t.Helper()
	dockerLog := withFakeDocker(t)
	stacks := t.TempDir()
	h := &Handler{Compose: docker.NewComposeManager(stacks, nil), ComposePath: stacks}
	h.SetClusterMgr(mgr)
	return h, dockerLog
}

type operatorEvent struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
	Done    bool   `json:"done"`
}

// brokeredCall runs handler on the manager as the cluster proxy hands it a
// request for a1's stack (?node=a1, a1 an agent).
func brokeredCall(t *testing.T, h *Handler, handler func(*Handler, echo.Context) error, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	c.Set("username", "alice")
	c.Set(MigrateSourceKey, "a1")
	if err := handler(h, c); err != nil {
		t.Fatal(err)
	}
	return rec
}

// runBrokered is a migration of a1's stack, and the events the operator saw.
func runBrokered(t *testing.T, h *Handler, body string) []operatorEvent {
	t.Helper()
	rec := brokeredCall(t, h, (*Handler).Migrate, "/api/v1/docker/compose/demo/migrate", body)
	var events []operatorEvent
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev operatorEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("an event that does not parse: %q", data)
			}
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		t.Fatalf("no events: %d %s", rec.Code, rec.Body)
	}
	return events
}

// brokeredReport is the pre-flight of a1's stack, as the dialog asks for it.
func brokeredReport(t *testing.T, h *Handler, body string) PreflightReport {
	t.Helper()
	rec := brokeredCall(t, h, (*Handler).MigratePreflight, "/api/v1/docker/compose/demo/migrate/preflight", body)
	var resp struct {
		Data PreflightReport `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		t.Fatalf("pre-flight: %d %s", rec.Code, rec.Body)
	}
	return resp.Data
}

func phasesOf(events []operatorEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Phase)
	}
	return out
}

// A stack on an agent goes to its target through a manager: the source's
// session stops and packages it, the manager pulls the bundle and pushes it to
// the target under the operator's policy, marked as an agent's, and the source
// hears that the target took it. The manager runs no docker of its own.
func TestAManagerCarriesAnAgentsStackToTheTarget(t *testing.T) {
	for _, tc := range []struct {
		overwrite bool
		risks     []string
	}{{true, nil}, {false, []string{`service "x" sets privileged: true`}}} {
		t.Run(fmt.Sprintf("overwrite=%v,risks=%d", tc.overwrite, len(tc.risks)), func(t *testing.T) {
			src, dst := newFakeSource(t), newFakeTarget(t, true)
			h, dockerLog := brokerHandler(t, brokerCluster(t, src, dst))

			risks, _ := json.Marshal(tc.risks)
			events := runBrokered(t, h, fmt.Sprintf(`{"targetNodeId":"a2","disposition":"retain","overwriteAcked":%v,"acknowledgedRisks":%s}`, tc.overwrite, risks))

			_, got, _, header := dst.seen()
			if !bytes.Equal(got, brokerBundle) {
				t.Fatalf("the target received %d bytes, want the source's %d (events %v)", len(got), len(brokerBundle), events)
			}
			for k, want := range map[string]string{
				migrationShaHeader:        brokerBundleSha(),
				migrationPolicyHeader:     "1",
				migrationSourceKindHeader: "agent",
				migrationOverwriteHeader:  strconv.FormatBool(tc.overwrite),
				migrationAckRisksHeader:   encodeAckedRisks(tc.risks),
			} {
				if header.Get(k) != want {
					t.Errorf("%s = %q, want %q", k, header.Get(k), want)
				}
			}
			sessions, session, _, outcomes := src.seen()
			if sessions != 1 || len(outcomes) != 1 || outcomes[0] != outcomeImported {
				t.Fatalf("the source had %d sessions and heard %v, want one and [imported]", sessions, outcomes)
			}
			// What the source's manifest names comes from the operator's request.
			var asked map[string]any
			if err := json.Unmarshal(session, &asked); err != nil || asked["targetNodeId"] != "a2" || asked["disposition"] != "retain" || asked["overwriteAcked"] != tc.overwrite {
				t.Fatalf("the session was asked for %s", session)
			}
			if last := events[len(events)-1]; last.Phase != PhaseDone || !last.Done {
				t.Fatalf("the operator's stream ended on %+v (%v)", last, phasesOf(events))
			}
			// The source's own phases reach the operator; its ready is the
			// manager's to act on.
			seen := strings.Join(phasesOf(events), " ")
			for _, p := range []string{PhasePreflight, PhaseQuiesce, PhasePackage, PhaseTransfer, PhaseFinalize, PhaseDone} {
				if !strings.Contains(seen, p) {
					t.Errorf("the operator did not see %q: %s", p, seen)
				}
			}
			if strings.Contains(seen, PhaseReady) {
				t.Errorf("the source's ready reached the operator: %s", seen)
			}
			if log, _ := os.ReadFile(dockerLog); len(log) > 0 {
				t.Fatalf("the manager ran docker:\n%s", log)
			}
		})
	}
}

// A target from before import policies would take an agent's bundle on the
// bundle's word and by a manager's rules, so an agent's stack does not go
// there — and the source is never asked to stop.
func TestABrokeredMigrationStopsNothingForATargetWithoutPolicies(t *testing.T) {
	src, dst := newFakeSource(t), newFakeTarget(t, false)
	h, _ := brokerHandler(t, brokerCluster(t, src, dst))

	if report := brokeredReport(t, h, `{"targetNodeId":"a2"}`); !hasBlock(report, "target-version") {
		t.Fatalf("the pre-flight does not block: %+v", report)
	}
	events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain"}`)
	last := events[len(events)-1]
	if last.Phase != PhaseError || !last.Done || !strings.Contains(last.Message, "pre-flight blocked") || !strings.Contains(last.Message, "cannot take a migration from an agent") {
		t.Fatalf("the operator's stream ended on %+v", last)
	}
	if sessions, _, _, _ := src.seen(); sessions != 0 {
		t.Fatalf("the source was asked to stop its stack %d times", sessions)
	}
	if imports, _, _, _ := dst.seen(); imports != 0 {
		t.Fatalf("the target was sent %d imports", imports)
	}
}

// An agent from before migration sessions has no route to serve one: the
// manager says to upgrade it, before anything stops.
func TestABrokeredMigrationRefusesASourceWithoutSessions(t *testing.T) {
	for _, tc := range []struct {
		name string
		info echo.HandlerFunc
	}{
		{"no such route", func(c echo.Context) error {
			return c.JSON(http.StatusNotFound, map[string]any{"success": false, "error": map[string]string{"code": "NOT_FOUND", "message": "No such endpoint"}})
		}},
		{"no session version", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]any{"success": true, "data": map[string]any{"node_id": "a1", "arch": runtime.GOARCH}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := newFakeSource(t), newFakeTarget(t, true)
			src.info = tc.info
			h, _ := brokerHandler(t, brokerCluster(t, src, dst))

			if report := brokeredReport(t, h, `{"targetNodeId":"a2"}`); !hasBlock(report, "source-version") {
				t.Fatalf("the pre-flight does not block: %+v", report)
			}
			events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain"}`)
			last := events[len(events)-1]
			if last.Phase != PhaseError || !last.Done || !strings.Contains(last.Message, "pre-flight blocked") || !strings.Contains(last.Message, "upgrade it") {
				t.Fatalf("the operator's stream ended on %+v", last)
			}
			if sessions, _, _, _ := src.seen(); sessions != 0 {
				t.Fatalf("the source was asked to stop its stack %d times", sessions)
			}
		})
	}
}

// A target that refused the stack runs nothing of it: the source hears
// "failed" and starts its stack again, and the operator sees why.
func TestABrokeredMigrationThatFailsOnTheTargetTellsTheSource(t *testing.T) {
	src, dst := newFakeSource(t), newFakeTarget(t, true)
	dst.status = http.StatusBadRequest
	h, _ := brokerHandler(t, brokerCluster(t, src, dst))

	events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain"}`)
	if _, _, _, outcomes := src.seen(); len(outcomes) != 1 || outcomes[0] != outcomeFailed {
		t.Fatalf("the source heard %v, want [failed]", outcomes)
	}
	if last := events[len(events)-1]; last.Phase != PhaseRollback || !last.Done {
		t.Fatalf("the operator's stream ended on %+v (%v)", last, phasesOf(events))
	}
	var why bool
	for _, ev := range events {
		why = why || strings.Contains(ev.Message, "the target refused the stack")
	}
	if !why {
		t.Fatalf("the operator was not told what the target said: %+v", events)
	}
}

// The panel's gzip middleware compresses whatever a client will take, and Go's
// transport asks for gzip whenever a request names no encoding: a bundle
// pulled that way arrives with no length (and the source spends gigabytes of
// gzip on it). The pull asks for no encoding, so through that middleware the
// bundle keeps its length and its bytes.
func TestTheBrokerPullsTheBundleUncompressed(t *testing.T) {
	src, dst := newFakeSource(t), newFakeTarget(t, true)
	h, _ := brokerHandler(t, brokerCluster(t, src, dst))

	events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain"}`)
	_, _, encodings, outcomes := src.seen()
	if len(encodings) != 1 || encodings[0] != "" {
		t.Fatalf("the bundle was asked for with Accept-Encoding %q", encodings)
	}
	_, got, length, _ := dst.seen()
	if length != int64(len(brokerBundle)) || !bytes.Equal(got, brokerBundle) {
		t.Fatalf("the target took %d bytes, Content-Length %d, want %d (events %v)", len(got), length, len(brokerBundle), events)
	}
	if len(outcomes) != 1 || outcomes[0] != outcomeImported {
		t.Fatalf("the source heard %v, want [imported]", outcomes)
	}
}

// The pre-flight of a stack on an agent is the agent's facts against the
// target's: the manager holds no such stack, and its own architecture is not
// the source's.
func TestABrokeredPreflightIsTheSourcesFacts(t *testing.T) {
	src, dst := newFakeSource(t), newFakeTarget(t, true)
	other := "s390x"
	if runtime.GOARCH == other {
		other = "arm64"
	}
	src.info = func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"success": true, "data": map[string]any{"session_version": 1, "node_id": "a1", "arch": other, "ports": []int{8080}}})
	}
	dst.ports = []int{8080}
	h, _ := brokerHandler(t, brokerCluster(t, src, dst))

	report := brokeredReport(t, h, `{"targetNodeId":"a2"}`)
	if !hasBlock(report, "arch-mismatch") || !hasBlock(report, "port-conflict") {
		t.Fatalf("the report is not the source's: %+v", report)
	}
}

// An agent's stack that asks for host access is refused by the target unless
// the operator acknowledged it, and by then the stack has been stopped. The
// pre-flight names that access as a warning, so the operator can acknowledge
// it first; it does not block, as the acknowledgement lifts it.
func TestABrokeredPreflightWarnsOfARiskyAgentStack(t *testing.T) {
	src, dst := newFakeSource(t), newFakeTarget(t, true)
	src.info = func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"success": true, "data": map[string]any{
			"session_version": 1, "node_id": "a1", "arch": runtime.GOARCH,
			"risky_findings": []string{`service "web" binds sensitive host path "/var/run/docker.sock"`, `service "web" sets privileged: true`},
		}})
	}
	h, _ := brokerHandler(t, brokerCluster(t, src, dst))

	report := brokeredReport(t, h, `{"targetNodeId":"a2"}`)
	if hasBlock(report, "compose-risky") {
		t.Fatalf("the acknowledgement lifts a risky stack, yet it blocks: %+v", report)
	}
	var risky *PreflightFinding
	for i := range report.Warnings {
		if report.Warnings[i].Code == "compose-risky" {
			risky = &report.Warnings[i]
		}
	}
	if risky == nil {
		t.Fatalf("no compose-risky warning: %+v", report)
	}
	for _, want := range []string{"/var/run/docker.sock", "privileged: true"} {
		if !strings.Contains(risky.Message, want) {
			t.Errorf("the warning %q does not name %q", risky.Message, want)
		}
	}
}

// An agent's stack that binds the panel's own secrets is refused by every
// target, acknowledged or not, and without this the refusal comes only after
// the source has stopped and packaged it. The pre-flight blocks it instead, so
// the migration ends before the source is touched.
func TestABrokeredPreflightBlocksAForbiddenAgentStack(t *testing.T) {
	src, dst := newFakeSource(t), newFakeTarget(t, true)
	src.info = func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"success": true, "data": map[string]any{
			"session_version": 1, "node_id": "a1", "arch": runtime.GOARCH,
			"forbidden_findings": []string{`service "web" binds sensitive host path "/etc/sfpanel"`},
		}})
	}
	h, _ := brokerHandler(t, brokerCluster(t, src, dst))

	report := brokeredReport(t, h, `{"targetNodeId":"a2"}`)
	var forbidden *PreflightFinding
	for i := range report.Blocks {
		if report.Blocks[i].Code == "compose-forbidden" {
			forbidden = &report.Blocks[i]
		}
	}
	if forbidden == nil {
		t.Fatalf("no compose-forbidden block, so the source would stop before the target refuses: %+v", report)
	}
	if !strings.Contains(forbidden.Message, "/etc/sfpanel") {
		t.Errorf("the block %q does not name /etc/sfpanel", forbidden.Message)
	}
}

// What the source serves must be the bundle it announced: the target is sent
// the announced sha with every byte, and the length is what goes out. A bundle
// that is not that one goes nowhere, and the source hears "failed".
func TestABrokerPushesOnlyTheBundleTheSourceAnnounced(t *testing.T) {
	for _, tc := range []struct {
		name   string
		served []byte
		sha    string // "": the announced one
	}{
		{"another sha", brokerBundle, strings.Repeat("0", 64)},
		{"another length", brokerBundle[:1000], ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := newFakeSource(t), newFakeTarget(t, true)
			src.served, src.servedSha = tc.served, tc.sha
			h, _ := brokerHandler(t, brokerCluster(t, src, dst))

			events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain"}`)
			if imports, _, _, _ := dst.seen(); imports != 0 {
				t.Fatalf("the target was sent %d imports of a bundle the source did not announce", imports)
			}
			if _, _, _, outcomes := src.seen(); len(outcomes) != 1 || outcomes[0] != outcomeFailed {
				t.Fatalf("the source heard %v, want [failed]", outcomes)
			}
			var why bool
			for _, ev := range events {
				why = why || strings.Contains(ev.Message, "it announced")
			}
			if last := events[len(events)-1]; last.Phase != PhaseRollback || !why {
				t.Fatalf("the operator's stream: %+v", events)
			}
		})
	}
}

// The source must hear the outcome to finish its side. When it does not take
// it, the operator is told what is known — the target's side, and what the
// source does without an outcome — and the session ends with the stream: the
// source then decides alone, by whether its bundle went out whole. A source
// that took the outcome and then went silent is reported as unconfirmed.
func TestABrokeredMigrationWhoseOutcomeIsLostSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name          string
		outcomeStatus int
		mute          bool
		targetStatus  int
		last, says    string
		ended         string
	}{
		// The target took the whole bundle, so the source keeps it stopped.
		{"imported, refused", http.StatusConflict, false, 0, PhaseDone,
			"the source did not take the outcome (the migration session has ended), so its stack stays stopped", "left"},
		{"failed, refused", http.StatusConflict, false, http.StatusBadRequest, PhaseError,
			"the source did not take the outcome (the migration session has ended), so it starts its stack again only if its bundle never went out whole", "left"},
		{"imported, then silent", 0, true, 0, PhaseDone,
			"the source took the outcome but has not confirmed its disposition (retain)", outcomeImported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := newFakeSource(t), newFakeTarget(t, true)
			src.outcomeStatus, src.mute = tc.outcomeStatus, tc.mute
			dst.status = tc.targetStatus
			h, _ := brokerHandler(t, brokerCluster(t, src, dst))

			events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain"}`)
			last := events[len(events)-1]
			if last.Phase != tc.last || !last.Done || !strings.Contains(last.Message, tc.says) {
				t.Fatalf("the operator's stream ended on %+v", last)
			}
			if how := src.sessionEnd(t); how != tc.ended {
				t.Fatalf("the source's session ended on %q, want %q", how, tc.ended)
			}
		})
	}
}

// The whole path against the source side as an agent runs it: Task 4's
// session handlers behind the panel's gzip middleware, packaging the agent's
// own stack (the logging fake docker). The target receives the bundle the
// agent packaged — its manifest names the agent as the source — and the
// session applies the disposition once it hears the outcome.
func TestABrokeredMigrationOfARealSourceSession(t *testing.T) {
	dockerLog := withFakeDocker(t)
	stacks := demoStack(t)
	agent := &Handler{Compose: docker.NewComposeManager(stacks, nil), ComposePath: stacks, NodeID: func() string { return "a1" }}
	e := echo.New()
	e.Use(echoMw.GzipWithConfig(echoMw.GzipConfig{Level: 5, MinLength: 1024}))
	g := e.Group("/api/v1/docker/compose/:project")
	g.GET("/migrate-source/info", agent.MigrateSourceInfo)
	g.POST("/migrate-source", agent.MigrateSource)
	g.GET("/migrate-source/bundle", agent.MigrateSourceBundle)
	g.POST("/migrate-source/outcome", agent.MigrateSourceOutcome)
	panel := httptest.NewTLSServer(e)
	defer panel.Close()
	dst := newFakeTarget(t, true)

	mgr := migrationManager(t, panel, true, true)
	addAgent(t, mgr, "a1", panel, "a1-key")
	addAgent(t, mgr, "a2", dst.Server, dst.key)
	// The agent's panel takes what is signed with its key (the session) and,
	// as its own loopback hop, the managers' (the pre-flight's query): one
	// process plays both, so both are accepted here.
	auth.SetProxySecrets("managers-secret", []string{"managers-secret", "a1-key"})
	h := &Handler{ComposePath: t.TempDir()}
	h.SetClusterMgr(mgr)

	events := runBrokered(t, h, `{"targetNodeId":"a2","disposition":"retain","overwriteAcked":false}`)
	if last := events[len(events)-1]; last.Phase != PhaseDone || last.Message != "Migration complete." {
		t.Fatalf("the operator's stream ended on %+v (%v)", last, events)
	}
	_, got, length, header := dst.seen()
	if int64(len(got)) != length || header.Get(migrationSourceKindHeader) != "agent" {
		t.Fatalf("the target took %d bytes of %d, source kind %q", len(got), length, header.Get(migrationSourceKindHeader))
	}
	m, files, _, err := receiveBundle(bytes.NewReader(got), header.Get(migrationShaHeader), t.TempDir())
	if err != nil {
		t.Fatalf("the bundle the target took: %v", err)
	}
	if m.StackID != "demo" || m.Source.NodeID != "a1" || m.Target.NodeID != "a2" || len(m.Images) != 1 || files["compose/"+m.ComposeFile] == nil {
		t.Fatalf("the bundle is not the agent's stack: %+v", m)
	}
	log, _ := os.ReadFile(dockerLog)
	if !strings.Contains(string(log), " stop") || strings.Contains(string(log), " start") {
		t.Fatalf("a retained stack is stopped on the agent and not started again:\n%s", log)
	}
}
