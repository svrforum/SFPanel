package compose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// A brokered migration is one this manager carries for a stack on an agent.
// An agent cannot send to another node — it holds no other node's key, and
// other nodes take nothing from it — so the stack goes source → manager →
// target. The source's session (migration_source.go) stops and packages the
// stack; this manager pulls the bundle over the source's panel HTTPS and
// pushes it to the target's migrate-import as it arrives, with no disk and in
// constant memory, and then tells the source how the target took it. The
// source finalizes or rolls back itself, with its own docker and stack
// directory, and its events reach the operator as they are.

// MigrateSourceKey marks, on a request's context, that the stack it migrates
// is on an agent, and names that agent. The cluster proxy sets it on
// /migrate and /migrate/preflight sent with ?node=<agent>, and runs them here
// instead of relaying them to an agent that cannot send a stack anywhere. The
// middleware's migrateSourceKey is the same string: that package imports no
// feature package.
const MigrateSourceKey = "sfpanel.migrateSource"

// maxSourceAnswer bounds what this manager reads of a source's JSON answer or
// of one line of its session's stream: the agent could otherwise make it
// buffer whatever it sends.
const maxSourceAnswer = 1 << 20

// errSessionEnded is a session stream that ended without saying so.
var errSessionEnded = errors.New("the stream ended")

// brokeredPreflight is gatherPreflight for a stack on agent src: the source's
// half is the agent's own answer (MigrateSourceInfo), the target's the same as
// for any migration. Two more things block, as neither can be helped once the
// source has stopped: a source from before migration sessions, which cannot be
// carried, and a target from before import policies, which would take an
// agent's bundle on the bundle's word and by a manager's rules.
func (h *Handler) brokeredPreflight(ctx context.Context, src, project, targetNodeID, username string, overwriteAcked bool) (PreflightReport, error) {
	mgr := h.clusterManager()
	if mgr == nil {
		return PreflightReport{}, fmt.Errorf("cluster is not enabled")
	}
	status, body, err := mgr.ProxyToNode(ctx, src, http.MethodGet, sessionURI(project, "/info"), nil, username)
	if err != nil {
		return PreflightReport{}, fmt.Errorf("could not query the source node: %w", err)
	}
	var wrapper struct {
		Data migrateSourceInfo `json:"data"`
	}
	switch {
	case status == http.StatusNotFound:
		return sourceVersionBlocked(), nil
	case status != http.StatusOK:
		return PreflightReport{}, fmt.Errorf("the source could not report its stack: %s", answerError(status, body))
	case json.Unmarshal(body, &wrapper) != nil:
		return PreflightReport{}, fmt.Errorf("the source's answer could not be read")
	case wrapper.Data.SessionVersion < 1:
		return sourceVersionBlocked(), nil
	}
	info := wrapper.Data
	in := PreflightInput{
		SourceNodeID:         src,
		TargetNodeID:         targetNodeID,
		SourceArch:           info.Arch,
		StackPorts:           info.Ports,
		HasSystemBind:        info.HasSystemBind,
		HasExternalVolume:    info.HasExternalVolume,
		HasAbsBind:           info.HasAbsBind,
		HasDevice:            info.HasDevice,
		EstimatedBytes:       info.EstimatedBytes,
		EstimatedDockerBytes: info.EstimatedDockerBytes,
		OverwriteAcked:       overwriteAcked,
		Disposition:          DispositionRetain, // as gatherPreflight
	}
	target, reached := addTargetFacts(ctx, mgr, &in, project, username)
	report := BuildPreflightReport(in)
	switch {
	case !reached:
		report.Warnings = append(report.Warnings, targetUnreachable)
	case target.ImportPolicy < 1:
		report.Blocks = append(report.Blocks, PreflightFinding{Code: "target-version", Message: "the target runs a version that cannot take a migration from an agent; upgrade it"})
	}
	return report, nil
}

// sourceVersionBlocked is the whole report for a source that serves no
// migration sessions: without its facts, no other check means anything.
func sourceVersionBlocked() PreflightReport {
	return PreflightReport{
		Blocks:   []PreflightFinding{{Code: "source-version", Message: "the source agent runs a version without migration sessions; upgrade it"}},
		Warnings: []PreflightFinding{},
	}
}

// blocksDetail is blocksSummary with each block's reason: a block here may be
// a version to upgrade, which its code alone does not say.
func blocksDetail(b []PreflightFinding) string {
	parts := make([]string, 0, len(b))
	for _, f := range b {
		parts = append(parts, f.Code+": "+f.Message)
	}
	return strings.Join(parts, "; ")
}

// answerError is what a node's error answer says: the message in its
// envelope, or the status when it has none.
func answerError(status int, body []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
}

// brokeredMigrate is Migrate for a stack on agent src, which this manager
// carries: the same checks before anything stops, the source's session in
// place of its own quiesce and package, then the transfer and the outcome.
func (h *Handler) brokeredMigrate(c echo.Context, mgr *cluster.Manager, src, project string, req migrateRequest, disp Disposition, rateBytes int64) error {
	// This manager's own lock for carrying src's stack, keyed apart from the
	// stack ID: MigrateImport takes that one when this manager is also the
	// target. The source holds its stack's lock for the whole session, so two
	// managers cannot carry the same stack at once either.
	lock := "brokered:" + src + "/" + project
	if !h.tryAcquireMigration(lock) {
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, "a migration for this stack is already in progress")
	}
	defer h.releaseMigration(lock)

	username, _ := c.Get("username").(string)
	stream := migrationStream(c, project, req.TargetNodeID, req.Disposition, username, slog.String("source", src))
	// pushBundle reports progress from the goroutine that writes its request
	// body, which net/http may leave running after the target has answered.
	var sendMu sync.Mutex
	send := func(phase, msg string, done bool) {
		sendMu.Lock()
		defer sendMu.Unlock()
		stream(phase, msg, done)
	}
	slog.Info("stack migration starting", "component", "compose", "stack", project,
		"source", src, "target", req.TargetNodeID, "disposition", req.Disposition, "user", username,
		"rate_limit_mibps", req.RateLimitMbps, "brokered", true)

	// Detached from the operator's request, as in Migrate: a browser that
	// leaves must not cut the transfer, nor the session — whose end, without
	// an outcome, leaves the source to decide alone. 2h is Migrate's bound.
	opCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	// 1. preflight — abort on any block, and on what Migrate refuses.
	send(PhasePreflight, "Running pre-flight checks...", false)
	report, err := h.brokeredPreflight(opCtx, src, project, req.TargetNodeID, username, req.OverwriteAcked)
	if err != nil {
		send(PhaseError, response.SanitizeOutput("pre-flight failed: "+err.Error()), true)
		return nil
	}
	if len(report.Blocks) > 0 {
		send(PhaseError, response.SanitizeOutput("pre-flight blocked: "+blocksDetail(report.Blocks)), true)
		return nil
	}
	if err := h.checkPushTarget(req.TargetNodeID); err != nil {
		send(PhaseError, response.SanitizeOutput("pre-flight blocked: "+err.Error()), true)
		return nil
	}
	for _, w := range report.Warnings {
		if w.Code == targetUnreachable.Code {
			send(PhaseError, "pre-flight blocked: "+w.Message, true)
			return nil
		}
	}
	// The session, the bundle and the outcome go to the source's panel: this
	// manager must reach it over HTTPS and sign for it before it stops.
	node := mgr.GetNode(src)
	if node == nil {
		send(PhaseError, response.SanitizeOutput(fmt.Sprintf("pre-flight blocked: unknown source node %q", src)), true)
		return nil
	}
	source, err := newSourceClient(mgr, node, username)
	if err != nil {
		send(PhaseError, response.SanitizeOutput("pre-flight blocked: "+err.Error()), true)
		return nil
	}

	// 2-3. quiesce and package, on the source, in its session.
	resp, err := source.openSession(opCtx, project, req)
	if err != nil {
		send(PhaseError, response.SanitizeOutput("could not start the source's migration session: "+err.Error()), true)
		return nil
	}
	// Closing the stream ends the session if it is still open: the source
	// then decides alone, by whether its bundle went out whole.
	defer func() { _ = resp.Body.Close() }()
	events := newSessionEvents(resp.Body)
	var ready sessionEvent
	for ready.Phase != PhaseReady {
		ev, err := events.next()
		if err != nil {
			send(PhaseError, response.SanitizeOutput("the source's session ended before its bundle was ready ("+err.Error()+"); a source whose bundle never went out starts its stack again — check the source"), true)
			return nil
		}
		if ev.Phase == PhaseReady {
			ready = ev
			continue
		}
		send(ev.Phase, response.SanitizeOutput(ev.Message), ev.Done)
		if ev.Done {
			return nil // the source ended its session, and said how
		}
	}

	// 4. transfer — pull the bundle and push it to the target as it comes.
	send(PhaseTransfer, "Transferring to target...", false)
	result, cause := h.carryBundle(opCtx, source, project, ready, req, username, transferProgress(send), rateBytes)
	if cause != "" {
		slog.Warn("stack migration transfer failed", "component", "compose", "stack", project,
			"source", src, "target", req.TargetNodeID, "result", result, "cause", cause)
		send(PhaseTransfer, response.SanitizeOutput(cause), false)
	}

	// 5. the source hears how the target took it, and finishes its side.
	if err := source.giveOutcome(project, ready.Session, result); err != nil {
		slog.Error("stack migration: the source did not take the outcome", "component", "compose", "stack", project,
			"source", src, "target", req.TargetNodeID, "result", result, "error", err)
		sourceUnconfirmed(send, result, disp, false, err.Error())
		return nil
	}
	for {
		ev, err := events.next()
		if err != nil {
			sourceUnconfirmed(send, result, disp, true, "its stream ended before it said how ("+err.Error()+")")
			return nil
		}
		if ev.Phase == PhaseReady {
			continue
		}
		send(ev.Phase, response.SanitizeOutput(ev.Message), ev.Done)
		if ev.Done {
			return nil
		}
	}
}

// carryBundle pulls a ready session's bundle from the source and pushes it to
// the target as it arrives. It returns the outcome the source is to hear and,
// for anything but imported, why.
func (h *Handler) carryBundle(ctx context.Context, source *sourceClient, project string, ready sessionEvent, req migrateRequest, username string, onProg func(sent, total int64), rateBytes int64) (result, cause string) {
	resp, err := source.do(ctx, http.MethodGet, sessionURI(project, "/bundle?session="+url.QueryEscape(ready.Session)), nil)
	if err != nil {
		return outcomeFailed, "transfer failed: could not pull the bundle from the source: " + err.Error()
	}
	// Closed without being read whole, the download is cut short, and the
	// source counts its bundle as not gone out.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxSourceAnswer))
		return outcomeFailed, "transfer failed: the source did not serve its bundle: " + answerError(resp.StatusCode, answer)
	}
	// The target checks the sha it is sent against every byte, so the one
	// announced must also be the one served; and pushBundle needs the length.
	if resp.ContentLength != ready.Bytes || resp.Header.Get(migrationShaHeader) != ready.Sha256 {
		return outcomeFailed, fmt.Sprintf("transfer failed: the source served %d bytes with sha %q, not the %d bytes with sha %q it announced",
			resp.ContentLength, resp.Header.Get(migrationShaHeader), ready.Bytes, ready.Sha256)
	}
	// A pull that breaks off half way cuts the push short: the target finds
	// the sha does not match and writes nothing.
	policy := importPolicy{Overwrite: req.OverwriteAcked, AckRisks: req.AcknowledgeRisks, AgentSource: true}
	status, body, terr := h.pushBundle(ctx, req.TargetNodeID, username, resp.Body, resp.ContentLength, ready.Sha256, policy, onProg, rateBytes)
	switch {
	case terr == nil && status == http.StatusOK:
		return outcomeImported, ""
	case terr == nil:
		// The target answered: it ran its own cleanup, and runs nothing.
		return outcomeFailed, "transfer/restore failed: target status " + http.StatusText(status) + ": " + string(body)
	}
	// On a connection error the target may have the whole bundle and be
	// bringing the stack up, detached from the connection. As in Migrate,
	// probe on a fresh context: if it runs there, the source must not start.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer probeCancel()
	if h.targetStackRunning(probeCtx, req.TargetNodeID, project, username) {
		return outcomeUnknown, "transfer connection lost but the stack is RUNNING on the target (" + terr.Error() + ")"
	}
	return outcomeFailed, "transfer failed: " + terr.Error()
}

// sourceUnconfirmed ends the operator's stream when the source's own last word
// cannot be had. What the target did, this manager can say; what the source
// did, only the source can. told says whether the source took the outcome.
// One that did not decides alone once its stream ends, by whether its bundle
// went out whole — which it did, for a target that took the stack.
func sourceUnconfirmed(send func(string, string, bool), result string, disp Disposition, told bool, why string) {
	switch {
	case result == outcomeImported && told:
		// The stack runs on the target: as in Migrate, a source that needs
		// attention afterwards does not undo the migration.
		send(PhaseDone, response.SanitizeOutput("migrated to target (running); the source took the outcome but has not confirmed its disposition ("+string(disp)+"): "+why+" — check the source"), true)
	case result == outcomeImported:
		send(PhaseDone, response.SanitizeOutput("migrated to target (running); the source did not take the outcome ("+why+"), so its stack stays stopped and its disposition ("+string(disp)+") is not applied — check the source"), true)
	case told:
		send(PhaseError, response.SanitizeOutput(targetSide(result)+"; the source took the outcome but has not confirmed how it ended its side: "+why+" — check the source"), true)
	default:
		send(PhaseError, response.SanitizeOutput(targetSide(result)+"; the source did not take the outcome ("+why+"), so it starts its stack again only if its bundle never went out whole — check the source"), true)
	}
}

// targetSide is what an outcome other than imported says of the target.
func targetSide(result string) string {
	if result == outcomeUnknown {
		return "whether the target runs the stack is unknown"
	}
	return "the target does not run the stack"
}

// sessionURI is a source session route of project's: "" for the session
// itself, "/info", "/bundle?…", "/outcome?…".
func sessionURI(project, route string) string {
	return "/api/v1/docker/compose/" + url.PathEscape(project) + "/migrate-source" + route
}

// sourceClient reaches a source agent's session routes over its panel HTTPS,
// each request signed with the agent's key and naming the operator.
type sourceClient struct {
	mgr      *cluster.Manager
	node     *cluster.Node
	base     string
	username string
	client   *http.Client
}

// newSourceClient is a client for node's panel, or why this manager cannot
// talk to it: an agent it cannot reach over HTTPS or holds no key for.
//
// DisableCompression: Go's transport asks for gzip whenever a request names
// no encoding, and the panel's gzip middleware takes it at its word — the
// source spends gigabytes of gzip on the bundle, which arrives inflated on
// the fly with no length, and pushBundle has none to send. No keep-alives: a
// connection left idle after the run would stay open on both nodes. No client
// timeout: a transfer can outlast any fixed one, so each request's context
// bounds it.
func newSourceClient(mgr *cluster.Manager, node *cluster.Node, username string) (*sourceClient, error) {
	ep, err := mgr.PanelEndpointChecked(node)
	if err != nil {
		return nil, err
	}
	if err := mgr.CanSignFor(node); err != nil {
		return nil, err
	}
	return &sourceClient{
		mgr:      mgr,
		node:     node,
		base:     ep.BaseURL,
		username: username,
		client: &http.Client{Transport: &http.Transport{
			TLSClientConfig:    ep.TLSConfig.Clone(),
			DisableCompression: true,
			DisableKeepAlives:  true,
		}},
	}, nil
}

// do sends one request to the source's panel, signed for the source over the
// exact request-URI it goes to.
func (s *sourceClient) do(ctx context.Context, method, uri string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+uri, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	sig, err := s.mgr.SignProxyRequestFor(s.node, method, req.URL.RequestURI())
	if err != nil {
		return nil, err
	}
	req.Header.Set(auth.InternalProxyHeaderV2, sig)
	if s.username != "" {
		req.Header.Set("X-SFPanel-Original-User", s.username)
	}
	return s.client.Do(req)
}

// openSession starts the source's session for the operator's request: the
// source takes its stack's lock, stops and packages it, and streams its phases
// until the outcome. An answer that is not a stream is the source refusing,
// before it stopped anything.
func (s *sourceClient) openSession(ctx context.Context, project string, req migrateRequest) (*http.Response, error) {
	body, _ := json.Marshal(map[string]any{"targetNodeId": req.TargetNodeID, "disposition": req.Disposition, "overwriteAcked": req.OverwriteAcked})
	resp, err := s.do(ctx, http.MethodPost, sessionURI(project, ""), body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get(echo.HeaderContentType), "text/event-stream") {
		defer func() { _ = resp.Body.Close() }()
		answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxSourceAnswer))
		return nil, fmt.Errorf("the source refused it: %s", answerError(resp.StatusCode, answer))
	}
	return resp, nil
}

// giveOutcome tells the source how the target took its stack, on a short
// context of its own: a source that stopped answering must not hold the run
// for what is left of its two hours.
func (s *sourceClient) giveOutcome(project, session, result string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"result": result})
	resp, err := s.do(ctx, http.MethodPost, sessionURI(project, "/outcome?session="+url.QueryEscape(session)), body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxSourceAnswer))
	if resp.StatusCode != http.StatusOK {
		return errors.New(answerError(resp.StatusCode, answer))
	}
	return nil
}

// sessionEvent is one event of a source session: Migrate's {phase, message,
// done}, and on "ready" the bundle the session serves.
type sessionEvent struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
	Done    bool   `json:"done"`
	Session string `json:"session"`
	Sha256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

// sessionEvents reads a session's stream, a line at most maxSourceAnswer long.
type sessionEvents struct{ sc *bufio.Scanner }

func newSessionEvents(r io.Reader) *sessionEvents {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxSourceAnswer)
	return &sessionEvents{sc: sc}
}

// next is the stream's next event, or why there is none: it ended, broke, or
// sent a line past the bound. A line that is no event is skipped.
func (s *sessionEvents) next() (sessionEvent, error) {
	for s.sc.Scan() {
		data, ok := strings.CutPrefix(s.sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev sessionEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		return ev, nil
	}
	if err := s.sc.Err(); err != nil {
		return sessionEvent{}, err
	}
	return sessionEvent{}, errSessionEnded
}
