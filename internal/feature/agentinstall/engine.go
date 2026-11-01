package agentinstall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

func logger() *slog.Logger { return slog.With("component", "agentinstall") }

// Request is an install as the operator asked for it: the servers, one
// account for all of them, and how to go about it.
type Request struct {
	Servers []Server
	Account Account
	// Review stops every server after its pre-flight until Proceed.
	Review bool
	// Mode is ModeAuto (""), ModeOnline or ModeOffline.
	Mode string
	// Concurrency is how many servers are worked on at once: 1 to 5, 3
	// when 0.
	Concurrency int
	StartedBy   string
}

// Server is one server to install: Host as the operator typed it (an IP or
// a name the leader resolves), its SSH port (22 when 0), and the node name
// (the server's hostname when "") and group label asked for.
type Server struct {
	Host  string
	Port  int
	Name  string
	Group string
}

// Account is how to log in to every server. Method is "password", "key" or
// "none" (Tailscale SSH). The sudo password is SudoPassword, or the login
// password when SudoSame. The engine takes the byte slices over: it zeroes
// them when it drops the credentials, and at once when Start refuses.
type Account struct {
	User         string
	Method       string
	Password     []byte
	Key          []byte
	Passphrase   []byte
	SudoPassword []byte
	SudoSame     bool
}

func (a *Account) wipe() {
	clear(a.Password)
	clear(a.Key)
	clear(a.Passphrase)
	clear(a.SudoPassword)
}

// Env is the cluster as an install sees it from its leader, and the
// installer's own inputs, so the engine can be tested without either.
type Env interface {
	IsLeader() bool
	// Version is this leader's version, the one an install pins.
	Version() string
	// ManagerVersions are the other managers' names with the version each
	// last reported on its heartbeat ("" when not heard from).
	ManagerVersions() map[string]string
	// ActiveRun is the ID of a run under way, a task's or an install's, or "".
	ActiveRun() string
	Nodes() []cluster.Node
	ReadyForAgentRegistration(name string, ip net.IP) error
	JoinAddressFor(target net.IP) (string, error)
	// CAPin is the cluster CA's pin, what a joining agent checks.
	CAPin() (string, error)
	// CreateAgentToken mints a one-use agent token bound to ip, for
	// cluster.AgentInstallTokenTTL; RevokeToken takes it back.
	CreateAgentToken(ip net.IP) (string, error)
	RevokeToken(token string) error
	// Heartbeat is what this leader's heartbeat knows of node id: whether
	// it has been heard from, whether it is online now (LiveHealth), and the
	// version it reports.
	Heartbeat(id string) (heard, online bool, version string)
	// ProxyHealth is the status of GET /api/v1/health relayed to node id
	// (ProxyToNode, over its cluster port), PanelHealth that of the same GET
	// over HTTPS on its panel port (PanelEndpointChecked).
	ProxyHealth(ctx context.Context, id string) (int, error)
	PanelHealth(ctx context.Context, id string) (int, error)
	RemoveNode(id string) error
	// UpdateNodeLabels replaces node id's labels with labels.
	UpdateNodeLabels(id string, labels map[string]string) error
	PutRun(r cluster.RunRecord) error
	PutRunNode(runID, key string, rec cluster.RunNodeRecord) error
	// Audit records an audit row under user.
	Audit(user, method, path string, status int)
	// Checksums is version's checksums.txt, its signature verified;
	// Archive the release archive for arch, checked against checksums.
	Checksums(ctx context.Context, version string) ([]byte, error)
	Archive(ctx context.Context, version, arch string, checksums []byte) ([]byte, error)
	// Archives is SFPANEL_AGENT_ARCHIVES: a directory holding an unreleased
	// version's archives and checksums.txt. Only an unreleased version asks.
	Archives() string
	// InstallScript is the install.sh to upload: this binary's own.
	InstallScript() []byte
	Now() time.Time
}

// Refusal is an install request or an operator's answer the engine turns
// down: the HTTP status and code to answer with, never 401.
type Refusal struct {
	Status  int
	Code    string
	Message string
	Details map[string]string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

func refusal(status int, code, message string, details ...string) *Refusal {
	r := &Refusal{Status: status, Code: code, Message: message}
	if len(details) > 0 {
		r.Details = map[string]string{}
		for i := 0; i+1 < len(details); i += 2 {
			r.Details[details[i]] = details[i+1]
		}
	}
	return r
}

// Limits of a request.
const (
	maxServers         = 50
	defaultConcurrency = 3
	maxConcurrency     = 5
)

// installTaskName is the name an install's run record carries, the
// built-in's in the task list.
const installTaskName = "에이전트 설치 (SSH)"

// releaseVersion is a released version's form; anything else (a describe
// of a dev build) is unreleased and installs only from SFPANEL_AGENT_ARCHIVES.
var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// timings are the engine's waits, fields so tests can shorten them.
type timings struct {
	// poll is how often the install's log and exit status are read, and
	// installLimit how long install.sh may run.
	poll, installLimit time.Duration
	// verifyEvery is how often the three online checks run, verifyLimit how
	// long a registered agent has to pass them.
	verifyEvery, verifyLimit time.Duration
	// checkWait bounds a login Tailscale SSH holds for a browser check;
	// answerWait any other wait on the operator.
	checkWait, answerWait time.Duration
	// keep is how long the credentials stay after an install ends with a
	// registered row failed, sweep how often that is looked at.
	keep, sweep time.Duration
	// cleanup bounds the clean-up at a row's end.
	cleanup time.Duration
	// call bounds one online check's request.
	call time.Duration
	// tmpBase is where the per-row directory is made on the target.
	tmpBase string
}

func defaultTimings() timings {
	return timings{
		poll: 2 * time.Second, installLimit: 30 * time.Minute,
		verifyEvery: 3 * time.Second, verifyLimit: 3 * time.Minute,
		checkWait: 10 * time.Minute, answerWait: 30 * time.Minute,
		keep: 10 * time.Minute, sweep: 5 * time.Second,
		cleanup: time.Minute, call: 10 * time.Second,
		tmpBase: "/tmp",
	}
}

// Engine runs agent installs on this leader: one at a time, each in a
// session in memory with its credentials. It is the task engine's
// InstallRuns.
type Engine struct {
	env     Env
	dialer  Dialer
	resolve func(ctx context.Context, host string) (net.IP, error)
	t       timings

	mu  sync.Mutex
	cur *session
}

// New returns an Engine over env that reaches servers over SSH.
func New(env Env) *Engine {
	return &Engine{env: env, dialer: sshDialer{}, resolve: resolveHost, t: defaultTimings()}
}

// resolveHost is host's address: host itself when it is one, else the first
// IPv4 address the resolver gives (MagicDNS names included), else the first.
func resolveHost(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s has no address", host)
	}
	return ips[0], nil
}

// Active reports whether an install is under way: its rows, or a recheck.
func (e *Engine) Active() bool {
	e.mu.Lock()
	s := e.cur
	e.mu.Unlock()
	return s != nil && s.active()
}

// Owns reports whether runID is this engine's session's: from before its
// record is first written until another install replaces it.
func (e *Engine) Owns(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cur != nil && e.cur.id == runID
}

// Start starts an install and returns its run's ID once the run is
// recorded; the servers are worked on in the background. It refuses with a
// *Refusal: 400 for a request that is not one, 409 AGENT_INSTALL_ACTIVE
// while another install is under way, 409 TASK_RUN_ACTIVE while a task run
// is, 409 AGENT_INSTALL_MIXED_VERSIONS when the managers run different
// versions, and 409 AGENT_INSTALL_UNRELEASED for a version that is no
// release without SFPANEL_AGENT_ARCHIVES. Off the leader it is
// cluster.ErrNotLeader. Whatever it answers, the account's bytes are the
// engine's: zeroed on a refusal, and when the session drops them.
func (e *Engine) Start(ctx context.Context, r Request) (runID string, err error) {
	handed := false
	defer func() {
		if !handed {
			r.Account.wipe()
		}
	}()
	if err := normalise(&r); err != nil {
		return "", err
	}
	if !e.env.IsLeader() {
		return "", cluster.ErrNotLeader
	}
	version, archives, err := e.pinVersion()
	if err != nil {
		return "", err
	}

	s := newSession(e, r, version, archives)
	e.mu.Lock()
	if e.cur != nil && e.cur.active() {
		e.mu.Unlock()
		return "", refusal(http.StatusConflict, response.ErrAgentInstallActive, "Another agent install is under way", "run_id", e.cur.id)
	}
	if id := e.env.ActiveRun(); id != "" {
		e.mu.Unlock()
		return "", refusal(http.StatusConflict, response.ErrTaskRunActive, "A run is under way", "run_id", id)
	}
	// The session owns its run before the record exists: the supervisor
	// ends an install under way that no session here owns.
	old := e.cur
	e.cur = s
	e.mu.Unlock()
	handed = true
	if old != nil {
		old.drop()
	}

	if err := s.record(); err != nil {
		e.mu.Lock()
		if e.cur == s {
			e.cur = nil
		}
		e.mu.Unlock()
		s.drop()
		return "", err
	}
	e.env.Audit(s.head.StartedBy, http.MethodPost, auditPath(s.id)+"#start", http.StatusAccepted)
	if archives != "" {
		logger().Warn("installing an unreleased version from SFPANEL_AGENT_ARCHIVES without a signature check",
			"run", s.id, "version", version, "dir", archives)
	}
	s.launch()
	return s.id, nil
}

// auditPath is where an install's audit rows sit: under the task runs, as
// its record is one.
func auditPath(id string) string { return "/api/v1/cluster/task-runs/" + id }

// normalise checks a request and fills in its defaults.
func normalise(r *Request) error {
	bad := func(msg string, details ...string) error {
		return refusal(http.StatusBadRequest, response.ErrInvalidRequest, msg, details...)
	}
	if len(r.Servers) == 0 || len(r.Servers) > maxServers {
		return bad(fmt.Sprintf("An install takes 1 to %d servers", maxServers), "max", strconv.Itoa(maxServers))
	}
	switch {
	case r.Concurrency == 0:
		r.Concurrency = defaultConcurrency
	case r.Concurrency < 1 || r.Concurrency > maxConcurrency:
		return bad(fmt.Sprintf("Concurrency is 1 to %d", maxConcurrency), "field", "concurrency")
	}
	switch r.Mode {
	case "":
		r.Mode = ModeAuto
	case ModeAuto, ModeOnline, ModeOffline:
	default:
		return bad("The mode is auto, online or offline", "field", "mode")
	}
	a := r.Account
	if a.User == "" {
		return bad("The account needs a user", "field", "user")
	}
	switch a.Method {
	case "password":
		if len(a.Password) == 0 {
			return bad("The password is missing", "field", "password")
		}
	case "key":
		if len(a.Key) == 0 {
			return bad("The private key is missing", "field", "key")
		}
	case "none":
	default:
		return bad("The login method is password, key or none", "field", "method")
	}
	for i := range r.Servers {
		srv := &r.Servers[i]
		key := rowKey(i)
		srv.Host = strings.TrimSpace(srv.Host)
		if srv.Host == "" || strings.ContainsAny(srv.Host, " \t/") {
			return bad("A server's host is missing or malformed", "server", key, "field", "host")
		}
		if srv.Port == 0 {
			srv.Port = 22
		}
		if srv.Port < 1 || srv.Port > 65535 {
			return bad("A server's SSH port is 1 to 65535", "server", key, "field", "port")
		}
		if srv.Name != "" {
			if err := cluster.ValidNodeName(srv.Name); err != nil {
				return bad(err.Error(), "server", key, "field", "name")
			}
		}
		if srv.Group != "" {
			if err := cluster.ValidateLabels(map[string]string{groupLabel: srv.Group}); err != nil {
				return bad(err.Error(), "server", key, "field", "group")
			}
		}
	}
	return nil
}

// groupLabel is the label a server's group is kept under, the one task
// targets select by.
const groupLabel = "group"

func rowKey(i int) string { return "s" + strconv.Itoa(i+1) }

// pinVersion is the version to install, the leader's, which every manager
// must run too, and, for a version that is no release, the directory its
// archives come from. A released version never reads SFPANEL_AGENT_ARCHIVES.
func (e *Engine) pinVersion() (version, archives string, err error) {
	version = strings.TrimPrefix(e.env.Version(), "v")
	var mixed []string
	for name, v := range e.env.ManagerVersions() {
		if v != "" && strings.TrimPrefix(v, "v") != version {
			mixed = append(mixed, name+" ("+v+")")
		}
	}
	if len(mixed) > 0 {
		sort.Strings(mixed)
		return "", "", refusal(http.StatusConflict, response.ErrAgentInstallMixedVersions,
			"The managers run different versions: update the cluster first",
			"version", version, "managers", strings.Join(mixed, ", "))
	}
	if releaseVersion.MatchString(version) {
		return version, "", nil
	}
	dir := e.env.Archives()
	if dir == "" {
		return "", "", refusal(http.StatusConflict, response.ErrAgentInstallUnreleased,
			"This version is no release, and SFPANEL_AGENT_ARCHIVES names no directory of its archives", "version", version)
	}
	if st, err := os.Stat(filepath.Join(dir, "checksums.txt")); err != nil || !st.Mode().IsRegular() {
		return "", "", refusal(http.StatusConflict, response.ErrAgentInstallUnreleased,
			"SFPANEL_AGENT_ARCHIVES holds no checksums.txt", "version", version)
	}
	return version, dir, nil
}

// session is runID's session, or a 404 AGENT_INSTALL_NOT_FOUND refusal.
func (e *Engine) session(runID string) (*session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur == nil || e.cur.id != runID {
		return nil, refusal(http.StatusNotFound, response.ErrAgentInstallNotFound, "No install session on this leader holds this run")
	}
	return e.cur, nil
}

func (e *Engine) row(runID, key string) (*session, *row, error) {
	s, err := e.session(runID)
	if err != nil {
		return nil, nil, err
	}
	r := s.byKey[key]
	if r == nil {
		return nil, nil, refusal(http.StatusNotFound, response.ErrAgentInstallNotFound, "The install has no such server")
	}
	return s, r, nil
}

func notWaiting(msg string) error {
	return refusal(http.StatusConflict, response.ErrAgentInstallNotWaiting, msg)
}

// ApproveHostKey answers row key's host key question. Accepting needs the
// fingerprint the operator was shown, as the row records it ("ED25519
// SHA256:…", or the SHA256 part alone): another is 409
// AGENT_INSTALL_HOST_KEY_CHANGED. Declining stops that server only.
func (e *Engine) ApproveHostKey(runID, key, fingerprint string, accept bool) error {
	s, r, err := e.row(runID, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.rec.Awaiting != awaitHostKey {
		return notWaiting("This server is not waiting for its host key to be approved")
	}
	if accept && (fingerprint == "" || (fingerprint != r.rec.HostKey && fingerprint != r.fetched.Fingerprint)) {
		return refusal(http.StatusConflict, response.ErrAgentInstallHostKeyChanged,
			"The fingerprint approved is not the server's", "host_key", r.rec.HostKey)
	}
	r.answerLocked(accept)
	return nil
}

// ConfirmStandalone answers whether row key's standalone panel may become
// an agent; declining skips that server.
func (e *Engine) ConfirmStandalone(runID, key string, accept bool) error {
	s, r, err := e.row(runID, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.rec.Awaiting != awaitStandalone {
		return notWaiting("This server is not waiting for the standalone panel to be confirmed")
	}
	r.answerLocked(accept)
	return nil
}

// Proceed lets every server that waits after its pre-flight in a review
// go on to install.
func (e *Engine) Proceed(runID string) error {
	s, err := e.session(runID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.rows {
		if r.rec.Awaiting == awaitReview {
			r.answerLocked(true)
			n++
		}
	}
	if n == 0 {
		return notWaiting("No server waits for the install to go on")
	}
	return nil
}

// Stop stops the install for the operator by: every row under way ends
// stopped, cleaned up as far as a short wait allows, its token revoked; then
// the credentials go. On an install that has ended it only drops the
// credentials it kept.
func (e *Engine) Stop(runID, by string) error {
	s, err := e.session(runID)
	if err != nil {
		return err
	}
	s.stop(by)
	return nil
}

// StepOutput is row key's phase step's output from byte skip on, as a
// step's output route answers it. An error says the session has no such
// output.
func (e *Engine) StepOutput(runID, key string, step int, skip int64) (tasks.JobView, error) {
	s, r, err := e.row(runID, key)
	if err != nil {
		return tasks.JobView{}, err
	}
	if step < 0 || step >= phases {
		return tasks.JobView{}, errors.New("an install has five phases")
	}
	s.mu.Lock()
	st := r.rec.Steps[step]
	s.mu.Unlock()
	out, next := r.logs[step].from(skip)
	return tasks.JobView{
		ID: st.JobID, RunID: runID, Kind: cluster.RunKindAgentInstall, Status: st.Status,
		Output: out, NextSkip: next, Done: stepEnded(st.Status),
		StartedAt: st.StartedAt, EndedAt: st.EndedAt,
	}, nil
}

func stepEnded(status string) bool {
	switch status {
	case cluster.RunStepPending, cluster.RunStepStarting, cluster.RunStepRunning:
		return false
	}
	return true
}
