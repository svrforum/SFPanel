package compose

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/docker"
)

// sourceHandler is a node's compose handler with a demo stack and a node ID,
// answering internal requests (as the loopback hop or an agent's panel would
// have checked them).
func sourceHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	logPath := withFakeDocker(t)
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("node-key", []string{"node-key"})
	stacks := demoStack(t)
	return &Handler{Compose: docker.NewComposeManager(stacks, nil), ComposePath: stacks, NodeID: func() string { return "a1" }}, logPath
}

func internalRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2(method, r.URL.RequestURI()))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// readyEvent reads SSE events from r until "ready" or the end, sending each
// phase on phases.
func readyEvent(t *testing.T, r io.Reader, phases chan<- string) (session, sha string, size int64) {
	t.Helper()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Phase, Session, Sha256 string
			Bytes                  int64
		}
		_ = json.Unmarshal([]byte(data), &ev)
		phases <- ev.Phase
		if ev.Phase == "ready" {
			return ev.Session, ev.Sha256, ev.Bytes
		}
	}
	return "", "", 0
}

// startSession runs MigrateSource on a pipe, as a manager's request would,
// and returns once it is ready.
func startSession(t *testing.T, h *Handler, ctx context.Context) (session, sha string, size int64, phases chan string, done chan struct{}) {
	t.Helper()
	pr, pw := io.Pipe()
	rec := &pipeRecorder{ResponseRecorder: httptest.NewRecorder(), w: pw}
	req := internalRequest(http.MethodPost, "/api/v1/docker/compose/demo/migrate-source", strings.NewReader(`{"targetNodeId":"a2","disposition":"retain"}`)).WithContext(ctx)
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	phases, done = make(chan string, 64), make(chan struct{})
	go func() { _ = h.MigrateSource(c); _ = pw.Close(); close(done) }()
	session, sha, size = readyEvent(t, pr, phases)
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	return
}

// pipeRecorder streams what a handler writes, so a test can read events as
// they come.
type pipeRecorder struct {
	*httptest.ResponseRecorder
	w io.Writer
}

func (p *pipeRecorder) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRecorder) Flush()                      {}

// The whole session: the source stops and packages its stack, serves the
// bundle, hears that the target imported it, and applies the disposition.
func TestASourceSessionServesItsBundleAndFinalizes(t *testing.T) {
	h, logPath := sourceHandler(t)
	session, sha, size, phases, done := startSession(t, h, context.Background())
	if session == "" || sha == "" || size <= 0 {
		t.Fatalf("ready: %q %q %d", session, sha, size)
	}

	rec := httptest.NewRecorder()
	c := echo.New().NewContext(internalRequest(http.MethodGet, "/api/v1/docker/compose/demo/migrate-source/bundle?session="+session, nil), rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	if err := h.MigrateSourceBundle(c); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rec.Body.Bytes())
	if hex.EncodeToString(sum[:]) != sha || int64(rec.Body.Len()) != size || rec.Header().Get(migrationShaHeader) != sha {
		t.Fatalf("bundle: %d bytes, sha %x (want %d, %s)", rec.Body.Len(), sum, size, sha)
	}

	rec = httptest.NewRecorder()
	c = echo.New().NewContext(internalRequest(http.MethodPost, "/api/v1/docker/compose/demo/migrate-source/outcome?session="+session, strings.NewReader(`{"result":"imported"}`)), rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	if err := h.MigrateSourceOutcome(c); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("outcome: %v %d", err, rec.Code)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
	close(phases)
	var seen []string
	for p := range phases {
		seen = append(seen, p)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), " start") {
		t.Fatalf("a retained source was restarted:\n%s", log)
	}
	_ = seen
}

// The manager went away before the bundle went out whole: nothing can be
// running on the target, so the source starts again.
func TestASourceSessionLeftBeforeTheBundleRestartsTheSource(t *testing.T) {
	h, logPath := sourceHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, _, _, _, done := startSession(t, h, ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), " start") {
		t.Fatalf("the source was left stopped:\n%s", log)
	}
}

// After the bundle went out whole, a manager that goes away may have left the
// stack running on the target; starting the source too would make two live
// copies. It stays stopped.
func TestASourceSessionLeftAfterTheBundleKeepsTheSourceStopped(t *testing.T) {
	h, logPath := sourceHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	session, _, _, _, done := startSession(t, h, ctx)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(internalRequest(http.MethodGet, "/api/v1/docker/compose/demo/migrate-source/bundle?session="+session, nil), rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	if err := h.MigrateSourceBundle(c); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), " start") {
		t.Fatalf("the source was restarted after its bundle went out:\n%s", log)
	}
}

// The routes are cluster-internal: a browser's session does not reach them.
// Each of the four says so itself, and nothing runs before it has.
func TestASourceSessionIsInternalOnly(t *testing.T) {
	h, logPath := sourceHandler(t)
	for _, tc := range []struct {
		method, target string
		handler        func(*Handler, echo.Context) error
	}{
		{http.MethodGet, "/api/v1/docker/compose/demo/migrate-source/info", (*Handler).MigrateSourceInfo},
		{http.MethodPost, "/api/v1/docker/compose/demo/migrate-source", (*Handler).MigrateSource},
		{http.MethodGet, "/api/v1/docker/compose/demo/migrate-source/bundle?session=s1", (*Handler).MigrateSourceBundle},
		{http.MethodPost, "/api/v1/docker/compose/demo/migrate-source/outcome?session=s1", (*Handler).MigrateSourceOutcome},
	} {
		// Bounded, so that a session the guard let through cannot wait here
		// for an outcome.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(`{"targetNodeId":"a2","disposition":"retain","result":"imported"}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("project")
		c.SetParamValues("demo")
		err := tc.handler(h, c)
		cancel()
		if err != nil || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cluster-internal") {
			t.Errorf("an unsigned %s %s: %v %d %s", tc.method, tc.target, err, rec.Code, rec.Body)
		}
	}
	if log, _ := os.ReadFile(logPath); len(log) > 0 {
		t.Fatalf("an unsigned request ran docker:\n%s", log)
	}
}

// sourceCall runs one of a session's plain routes on h, signed as a manager's
// request would be, and returns the answer.
func sourceCall(t *testing.T, h *Handler, handler func(*Handler, echo.Context) error, method, project, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(internalRequest(method, target, strings.NewReader(body)), rec)
	c.SetParamNames("project")
	c.SetParamValues(project)
	if err := handler(h, c); err != nil {
		t.Fatal(err)
	}
	return rec
}

// runSession runs MigrateSource like startSession but keeps every phase it
// sends: session is the ready event's, and phases holds them all, in order,
// once done is closed.
func runSession(t *testing.T, h *Handler, ctx context.Context) (session string, phases *[]string, done chan struct{}) {
	t.Helper()
	pr, pw := io.Pipe()
	rec := &pipeRecorder{ResponseRecorder: httptest.NewRecorder(), w: pw}
	req := internalRequest(http.MethodPost, "/api/v1/docker/compose/demo/migrate-source", strings.NewReader(`{"targetNodeId":"a2","disposition":"retain"}`)).WithContext(ctx)
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	var seen []string
	ready, read := make(chan string, 1), make(chan struct{})
	go func() {
		defer close(read)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev struct{ Phase, Session string }
			_ = json.Unmarshal([]byte(data), &ev)
			seen = append(seen, ev.Phase)
			if ev.Phase == "ready" {
				ready <- ev.Session
			}
		}
	}()
	done = make(chan struct{})
	go func() { _ = h.MigrateSource(c); _ = pw.Close(); <-read; close(done) }()
	select {
	case session = <-ready:
	case <-done:
		t.Fatalf("the session ended before it was ready: %v", seen)
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not get ready")
	}
	return session, &seen, done
}

// The manager's word decides how the session ends: a target that refused the
// stack gets the source back, and one the manager could not read leaves it
// stopped — the stack may be running there — and says so.
func TestASourceSessionActsOnTheOutcome(t *testing.T) {
	for _, tc := range []struct {
		result, last string
		restarted    bool
	}{
		{"failed", PhaseRollback, true},
		{"unknown", PhaseError, false},
	} {
		t.Run(tc.result, func(t *testing.T) {
			h, logPath := sourceHandler(t)
			session, phases, done := runSession(t, h, context.Background())
			rec := sourceCall(t, h, (*Handler).MigrateSourceOutcome, http.MethodPost, "demo",
				"/api/v1/docker/compose/demo/migrate-source/outcome?session="+session, `{"result":"`+tc.result+`"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("outcome: %d %s", rec.Code, rec.Body)
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the session did not end")
			}
			log, _ := os.ReadFile(logPath)
			if strings.Contains(string(log), " start") != tc.restarted {
				t.Fatalf("restarted = %v, want %v:\n%s", !tc.restarted, tc.restarted, log)
			}
			if got := (*phases)[len(*phases)-1]; got != tc.last {
				t.Fatalf("the session ended on %q, want %q (%v)", got, tc.last, *phases)
			}
		})
	}
}

// A manager that never answers is taken for one gone: after sessionWait the
// session ends as if its stream had, and a source whose bundle never went out
// starts again.
func TestASourceSessionWithoutAnOutcomeEndsAfterItsWait(t *testing.T) {
	h, logPath := sourceHandler(t)
	defer func(d time.Duration) { sessionWait = d }(sessionWait)
	sessionWait = 50 * time.Millisecond
	_, phases, done := runSession(t, h, context.Background())
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), " start") {
		t.Fatalf("the source was left stopped:\n%s", log)
	}
	if got := (*phases)[len(*phases)-1]; got != PhaseRollback {
		t.Fatalf("the session ended on %q, want %q (%v)", got, PhaseRollback, *phases)
	}
}

// A manager that goes away while it is still pulling the bundle may yet get
// all of it, and carry it to the target with its stream to the source gone.
// The session waits for that download before it decides, so a bundle that
// went out whole keeps the source stopped.
func TestASourceSessionLeftDuringTheBundleWaitsForIt(t *testing.T) {
	h, logPath := sourceHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	session, phases, done := runSession(t, h, ctx)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() })
	rec := &pipeRecorder{ResponseRecorder: httptest.NewRecorder(), w: pw}
	c := echo.New().NewContext(internalRequest(http.MethodGet, "/api/v1/docker/compose/demo/migrate-source/bundle?session="+session, nil), rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	pulled := make(chan error, 1)
	go func() { pulled <- h.MigrateSourceBundle(c); _ = pw.Close() }()
	// One byte read: the download is under way, and the pipe holds the rest
	// until this test reads it.
	if _, err := io.ReadFull(pr, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
		t.Fatalf("the session ended while its bundle was still going out: %v", *phases)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := io.Copy(io.Discard, pr); err != nil {
		t.Fatal(err)
	}
	if err := <-pulled; err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not end")
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), " start") {
		t.Fatalf("the source was restarted although its bundle went out whole:\n%s", log)
	}
	if got := (*phases)[len(*phases)-1]; got != PhaseError {
		t.Fatalf("the session ended on %q, want %q (%v)", got, PhaseError, *phases)
	}
}

// One word per session: the first outcome decides, a second is refused
// rather than taken for another decision, and a word that is none of the
// three is refused before anything. A session is found under its own stack
// only.
func TestASourceSessionTakesOneOutcome(t *testing.T) {
	h, _ := sourceHandler(t)
	h.sessions.Store("s1", &sourceSession{project: "demo", outcome: make(chan string, 1)})
	post := func(project, result string) *httptest.ResponseRecorder {
		return sourceCall(t, h, (*Handler).MigrateSourceOutcome, http.MethodPost, project,
			"/api/v1/docker/compose/"+project+"/migrate-source/outcome?session=s1", `{"result":"`+result+`"}`)
	}
	if rec := post("demo", "maybe"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "imported, failed or unknown") {
		t.Fatalf("an unknown word: %d %s", rec.Code, rec.Body)
	}
	if rec := post("other", "imported"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "NOT_FOUND") {
		t.Fatalf("another stack's session: %d %s", rec.Code, rec.Body)
	}
	if rec := post("demo", "imported"); rec.Code != http.StatusOK {
		t.Fatalf("the first word: %d %s", rec.Code, rec.Body)
	}
	if rec := post("demo", "failed"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already has its outcome") {
		t.Fatalf("a second word: %d %s", rec.Code, rec.Body)
	}
	rec := sourceCall(t, h, (*Handler).MigrateSourceBundle, http.MethodGet, "other",
		"/api/v1/docker/compose/other/migrate-source/bundle?session=s1", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "NOT_FOUND") {
		t.Fatalf("another stack's bundle: %d %s", rec.Code, rec.Body)
	}
}

// What a manager learns of the source before it stops anything: that it
// serves sessions, this node's ID and architecture, and how much the stack
// moves.
func TestASourceSessionInfoStatesTheSourcesFacts(t *testing.T) {
	h, _ := sourceHandler(t)
	rec := sourceCall(t, h, (*Handler).MigrateSourceInfo, http.MethodGet, "demo", "/api/v1/docker/compose/demo/migrate-source/info", "")
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		t.Fatalf("info: %d %s", rec.Code, rec.Body)
	}
	for k, want := range map[string]any{"session_version": 1.0, "node_id": "a1", "arch": runtime.GOARCH, "estimated_docker_bytes": 9.0, "has_system_bind": false} {
		if resp.Data[k] != want {
			t.Errorf("%s = %v, want %v (%s)", k, resp.Data[k], want, rec.Body)
		}
	}
}

// An agent's stack that asks for host access is refused by the target unless
// the operator acknowledged it. The source names that access in its answer, so
// the pre-flight can say so before the stack is stopped: from the compose file
// as the bundle carries it, and from the resolved config the target checks
// too, where an .env can add what the file does not show.
func TestAnAgentSourceTellsThePreflightWhatIsRisky(t *testing.T) {
	for _, tc := range []struct {
		name, compose, resolved string
		want                    string // "": no risky_findings at all
	}{
		{"plain", "services:\n  web:\n    image: nginx:fake\n", "", ""},
		{"docker.sock", "services:\n  web:\n    image: nginx:fake\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n", "", "/var/run/docker.sock"},
		{"from its .env", "services:\n  web:\n    image: nginx:fake\n    privileged: ${PRIV}\n", "services:\n  web:\n    image: nginx:fake\n    privileged: true\n", "privileged: true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := sourceHandler(t)
			if err := os.WriteFile(filepath.Join(h.ComposePath, "demo", "docker-compose.yml"), []byte(tc.compose), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_RESOLVED_YAML", tc.resolved)
			rec := sourceCall(t, h, (*Handler).MigrateSourceInfo, http.MethodGet, "demo", "/api/v1/docker/compose/demo/migrate-source/info", "")
			var resp struct {
				Data map[string]any `json:"data"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil {
				t.Fatalf("info: %d %s", rec.Code, rec.Body)
			}
			findings, present := resp.Data["risky_findings"]
			if tc.want == "" {
				if present {
					t.Fatalf("a stack asking for nothing has risky_findings: %s", rec.Body)
				}
				return
			}
			list, _ := findings.([]any)
			if len(list) != 1 {
				t.Fatalf("risky_findings = %v, want one finding naming %q", findings, tc.want)
			}
			if s, _ := list[0].(string); !strings.Contains(s, tc.want) {
				t.Errorf("risky_findings[0] = %q, want it to name %q", s, tc.want)
			}
		})
	}
}
