package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/composex"
)

// A source session is the source's side of a migration that a manager
// carries. An agent cannot send to another node — it holds no other node's
// key, and other nodes take nothing from it — so a manager pulls the bundle
// and pushes it to the target. What only the source can do stays in one
// handler here, as in Migrate: MigrateSource takes the stack's lock, stops
// and packages the stack, says it is ready, and waits for the manager's
// outcome to finalize or roll back. The bundle and the outcome come on
// requests of their own, which find the session by its ID.
//
// The routes are node to node only: on a manager they sit behind the
// operator's login like every route, so each handler checks the signature
// itself (an agent's InternalProxyOnly already did).

// sessionWait is how long a ready session waits for the manager's outcome:
// the transfer, the target's restore and its health gate fit inside it.
// A var so tests can shorten it.
var sessionWait = 2 * time.Hour

// The outcomes a manager reports.
const (
	outcomeImported = "imported" // the target runs the stack: apply the disposition
	outcomeFailed   = "failed"   // the target does not: restart the source
	outcomeUnknown  = "unknown"  // the manager cannot tell: leave the source stopped
)

const sessionInternalOnly = "migration sessions are cluster-internal only"

// sourceSession is a ready session in the handler's table.
type sourceSession struct {
	project, bundle, sha string
	bytes                int64
	// served is set once a download handed every byte of the bundle to its
	// connection: from then on the stack may be running on the target.
	served  atomic.Bool
	outcome chan string // buffered 1: the manager's one word

	// mu orders the session's end against the requests that reach it. Once
	// closed, no download starts and no outcome is taken; serving counts the
	// downloads already under way.
	mu      sync.Mutex
	closed  bool
	serving sync.WaitGroup
}

// startServing counts a download in, unless the session has ended.
func (s *sourceSession) startServing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.serving.Add(1)
	return true
}

// give hands the session the manager's word, unless it has one already or
// has ended.
func (s *sourceSession) give(result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("the migration session has ended")
	}
	select {
	case s.outcome <- result:
		return nil
	default:
		return errors.New("the migration session already has its outcome")
	}
}

// await waits for the manager's outcome, for the manager to go away (ctx), or
// for sessionWait, and then ends the session. An outcome taken before the end
// counts, even if the manager left right after giving it. Without one, it
// waits for any download still under way: whether the bundle went out whole
// is what decides whether the source may start again, and a download can
// finish after the stream that announced the bundle has gone.
func (s *sourceSession) await(ctx context.Context) string {
	var result string
	select {
	case result = <-s.outcome:
	case <-ctx.Done():
	case <-time.After(sessionWait):
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if result == "" {
		select {
		case result = <-s.outcome:
		default:
			s.serving.Wait()
		}
	}
	return result
}

// findSession is the session a request names, for the stack its path names:
// one stack's routes do not reach another's session.
func (h *Handler) findSession(c echo.Context) *sourceSession {
	v, ok := h.sessions.Load(c.QueryParam("session"))
	if !ok {
		return nil
	}
	s := v.(*sourceSession)
	if s.project != c.Param("project") {
		return nil
	}
	return s
}

// newSessionID is 16 random bytes, hex. It is not what lets a request in —
// the routes take a manager's signature — but it keeps one session's
// requests from reaching another's.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

// migrateSourceInfo is a source's half of a pre-flight, for a manager that
// carries its stack (BuildPreflightReport takes it with the target's).
// SessionVersion says this node serves migration sessions; a node from before
// them has no such route. RiskyFindings is what the stack asks for in the
// risky tier, which a target refuses from an agent without the operator's
// acknowledgement; a node from before it leaves it out.
type migrateSourceInfo struct {
	SessionVersion       int      `json:"session_version"`
	NodeID               string   `json:"node_id"`
	Arch                 string   `json:"arch"`
	Ports                []int    `json:"ports"`
	HasSystemBind        bool     `json:"has_system_bind"`
	HasExternalVolume    bool     `json:"has_external_volume"`
	HasAbsBind           bool     `json:"has_abs_bind"`
	HasDevice            bool     `json:"has_device"`
	EstimatedBytes       int64    `json:"estimated_bytes"`
	EstimatedDockerBytes int64    `json:"estimated_docker_bytes"`
	RiskyFindings        []string `json:"risky_findings,omitempty"`
}

// MigrateSourceInfo (GET /docker/compose/:project/migrate-source/info) answers
// what this node knows of its stack, for a manager's pre-flight before it
// stops anything. Cluster-internal only.
func (h *Handler) MigrateSourceInfo(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, sessionInternalOnly)
	}
	project := c.Param("project")
	if !validProjectID(project) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid project id")
	}
	in, err := h.localMigrationFacts(c.Request().Context(), project)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrComposeError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, migrateSourceInfo{
		SessionVersion:       1,
		NodeID:               in.SourceNodeID,
		Arch:                 in.SourceArch,
		Ports:                in.StackPorts,
		HasSystemBind:        in.HasSystemBind,
		HasExternalVolume:    in.HasExternalVolume,
		HasAbsBind:           in.HasAbsBind,
		HasDevice:            in.HasDevice,
		EstimatedBytes:       in.EstimatedBytes,
		EstimatedDockerBytes: in.EstimatedDockerBytes,
		RiskyFindings:        h.riskyFindings(c.Request().Context(), project),
	})
}

// riskyFindings is the risky tier of project's compose as a target checks it
// on import (checkImportCompose): the compose file the bundle carries, and the
// config resolved with its .env, which can ask for what the file does not
// show. Each finding is named once. A document that cannot be read adds
// nothing here; the import says what is wrong with it.
func (h *Handler) riskyFindings(ctx context.Context, project string) []string {
	var docs []string
	if yamlPath, _ := h.Compose.ResolveComposeFile(ctx, project); yamlPath != "" {
		if raw, err := os.ReadFile(yamlPath); err == nil {
			docs = append(docs, string(raw))
		}
	}
	if resolved, err := h.Compose.GetResolvedConfigYAML(ctx, project); err == nil {
		docs = append(docs, resolved)
	}
	var findings []string
	seen := map[string]bool{}
	for _, doc := range docs {
		report, err := composex.Analyze(doc)
		if err != nil {
			continue
		}
		for _, f := range report.Risky {
			if !seen[f.Message] {
				seen[f.Message] = true
				findings = append(findings, f.Message)
			}
		}
	}
	return findings
}

// MigrateSource (POST /docker/compose/:project/migrate-source, SSE) runs a
// session. Body: {"targetNodeId","disposition","overwriteAcked"}. Its events
// are Migrate's {phase, message, done}; once the stack is stopped and packaged
// it sends {"phase":"ready","session","sha256","bytes","done":false} and waits
// for the manager to pull the bundle (MigrateSourceBundle) and report the
// outcome (MigrateSourceOutcome). Cluster-internal only.
//
// Without an outcome — the manager went away, or sessionWait passed — the
// source starts again only if the bundle never went out whole. Once it has,
// the stack may be running on the target, and two live copies are worse than
// a stopped one: the source stays stopped, and the log says so.
func (h *Handler) MigrateSource(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, sessionInternalOnly)
	}
	project := c.Param("project")
	if !validProjectID(project) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid project id")
	}
	var req struct {
		TargetNodeID   string `json:"targetNodeId"`
		Disposition    string `json:"disposition"`
		OverwriteAcked bool   `json:"overwriteAcked"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	disp := Disposition(req.Disposition)
	if req.TargetNodeID == "" || !disp.Valid() {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "targetNodeId and a valid disposition are required")
	}
	// The stack's lock for the whole session, as Migrate holds it: two
	// managers cannot carry the same stack at once.
	if !h.tryAcquireMigration(project) {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "a migration for this stack is already in progress")
	}
	defer h.releaseMigration(project)

	username, _ := c.Get("username").(string)
	send := migrationStream(c, project, req.TargetNodeID, req.Disposition, username)
	slog.Info("stack migration session starting", "component", "compose", "stack", project,
		"target", req.TargetNodeID, "disposition", req.Disposition, "user", username)

	// Unlike Migrate's, this work follows the request: only the manager on
	// the other end can use the bundle, and a source it has left is better
	// started again now than after packaging for nobody. Rollback and the
	// disposition run on contexts of their own.
	opCtx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Hour)
	defer cancel()
	bundle, sha, tmpDir, err := h.quiesceAndPackage(opCtx, project, req.TargetNodeID, disp, req.OverwriteAcked, send)
	if err != nil {
		return nil // the source was restarted, and the stream says why
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	fi, err := os.Stat(bundle)
	if err != nil {
		h.reportRollback(send, project, "packaging failed", err.Error())
		return nil
	}

	id := newSessionID()
	s := &sourceSession{project: project, bundle: bundle, sha: sha, bytes: fi.Size(), outcome: make(chan string, 1)}
	h.sessions.Store(id, s)
	defer h.sessions.Delete(id)
	writeMigrationEvent(c, map[string]any{"phase": PhaseReady, "session": id, "sha256": sha, "bytes": fi.Size(), "done": false})

	switch s.await(c.Request().Context()) {
	case outcomeImported:
		// Its own context: the manager may close the stream as soon as it
		// has given its word, and the disposition must still be applied.
		finCtx, finCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer finCancel()
		h.reportFinalize(finCtx, send, project, disp)
	case outcomeFailed:
		h.reportRollback(send, project, "transfer/restore failed", "the manager reports that the target did not take the stack")
	case outcomeUnknown:
		send(PhaseError, "the manager could not tell whether the target runs the stack; the source was left stopped to avoid two live copies — verify the target, then start or remove the source", true)
	default:
		// The manager went away, or this panel is stopping (Streams ends the
		// stream): either way no outcome can come any more.
		why := "the stream to the manager ended"
		if c.Request().Context().Err() == nil {
			why = "no outcome came within " + sessionWait.String()
		}
		if !s.served.Load() {
			// The bundle never went out whole, so the target cannot have
			// the stack.
			h.reportRollback(send, project, "the session ended before the whole bundle went out", why)
			return nil
		}
		slog.Error("stack migration session ended without an outcome after its bundle went out whole; the source was left stopped, and the stack may be running on the target",
			"component", "compose", "stack", project, "target", req.TargetNodeID, "session", id, "user", username, "reason", why)
		send(PhaseError, why+" after the whole bundle went out: the stack may be running on the target, so the source was left stopped to avoid two live copies — verify the target, then start or remove the source", true)
	}
	return nil
}

// MigrateSourceBundle (GET /docker/compose/:project/migrate-source/bundle?session=)
// sends a ready session's bundle as migrate-import takes it: octet-stream,
// its length, and its sha256 in the migration header. Cluster-internal only.
func (h *Handler) MigrateSourceBundle(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, sessionInternalOnly)
	}
	s := h.findSession(c)
	if s == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "no such migration session for this stack")
	}
	if !s.startServing() {
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, "the migration session has ended")
	}
	defer s.serving.Done()
	f, err := os.Open(s.bundle)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "the session's bundle is gone")
	}
	defer func() { _ = f.Close() }()

	w := c.Response()
	w.Header().Set(echo.HeaderContentType, echo.MIMEOctetStream)
	w.Header().Set(echo.HeaderContentLength, strconv.FormatInt(s.bytes, 10))
	w.Header().Set(migrationShaHeader, s.sha)
	w.WriteHeader(http.StatusOK)
	n, err := io.Copy(w, f)
	if err != nil || n != s.bytes {
		// Short: the manager cannot hold the whole bundle, and a target
		// refuses one whose sha does not match.
		slog.Warn("migration session: the bundle download broke off", "component", "compose",
			"stack", s.project, "sent", n, "bytes", s.bytes, "error", err)
		return nil
	}
	// Every byte is with the connection, if not yet across it: from here the
	// manager may have the whole bundle, and the target the stack.
	s.served.Store(true)
	return nil
}

// MigrateSourceOutcome (POST /docker/compose/:project/migrate-source/outcome?session=)
// takes the manager's word on a session. Body: {"result":"imported"|"failed"|
// "unknown"}. One word per session: a second is refused (409), not taken for
// another decision. Cluster-internal only.
func (h *Handler) MigrateSourceOutcome(c echo.Context) error {
	if !auth.IsInternalProxyRequest(c.Request()) {
		return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, sessionInternalOnly)
	}
	var req struct {
		Result string `json:"result"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	switch req.Result {
	case outcomeImported, outcomeFailed, outcomeUnknown:
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "result must be imported, failed or unknown")
	}
	s := h.findSession(c)
	if s == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "no such migration session for this stack")
	}
	if err := s.give(req.Result); err != nil {
		return response.Fail(c, http.StatusConflict, response.ErrInvalidRequest, err.Error())
	}
	return response.OK(c, map[string]string{"result": req.Result})
}
