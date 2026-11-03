package agentinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	featurecluster "github.com/svrforum/SFPanel/internal/feature/cluster"
)

// The agent install API (design §7, §8), under the manager-only /cluster
// group. An install's session lives in the memory of the leader that
// started it, so every route here is the leader's and a follower sends it
// on (forward): the start, and the answers to the questions its rows ask.
// Progress, history, a phase's output and stopping are the task runs'
// routes, as an install's record is a run.
//
// No answer here is a 401, whatever a server's SSH login said: the browser
// takes a 401 for its own session ending. A login a server refuses is that
// row's reason (AGENT_INSTALL_AUTH_FAILED).

// Handler serves the agent install API on a manager.
type Handler struct {
	engine *Engine
	// where is where this manager stands in its cluster, read per request.
	where func() place
	// startGate is the cluster handler's StartGate, and toLeader sends a
	// request on to the leader (middleware.ProxyToLeader): both replaced in
	// tests.
	startGate func(claim func(featurecluster.Busy) error) error
	toLeader  func(c echo.Context) (handled bool, err error)
}

// place is where a manager stands: in a cluster or not, its leader or not,
// and as its leader whether its FSM holds every committed entry.
type place struct{ inCluster, leader, caughtUp bool }

// caughtUpWait bounds the wait for the barrier a new leader's first look
// takes, as the task API's does.
const caughtUpWait = 2 * time.Second

// NewHandler returns the API over engine, for the cluster manager getMgr
// returns (nil outside a cluster) and the cluster handler's start gate.
func NewHandler(engine *Engine, getMgr func() *cluster.Manager, gate *featurecluster.Handler) *Handler {
	h := &Handler{engine: engine}
	h.where = func() place {
		m := getMgr()
		switch {
		case m == nil:
			return place{}
		case !m.IsLeader():
			return place{inCluster: true}
		}
		return place{inCluster: true, leader: true, caughtUp: m.CaughtUpLeader(caughtUpWait)}
	}
	h.startGate = func(claim func(featurecluster.Busy) error) error {
		if gate == nil {
			return claim(featurecluster.Busy{})
		}
		return gate.StartGate(claim)
	}
	h.toLeader = func(c echo.Context) (bool, error) { return middleware.ProxyToLeader(c, getMgr()) }
	return h
}

// forward answers a request this manager cannot take: outside a cluster
// 400; on a follower it sends it on to the leader, body unread; on a leader
// that has not caught up 503, as a write judged from an FSM short of the
// previous leader's last entries could miss a run under way. handled says
// the request was answered.
func (h *Handler) forward(c echo.Context) (handled bool, err error) {
	p := h.where()
	switch {
	case !p.inCluster:
		return true, response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	case !p.leader:
		return h.toLeader(c)
	case !p.caughtUp:
		return true, response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "The cluster leader is catching up: retry in a few seconds")
	}
	return false, nil
}

// answerErr answers err: a refusal as itself; a node that is not the
// leader (the leader changed under a forward) 503; a body over the limit as
// the limit says; anything else 500.
func answerErr(c echo.Context, err error) error {
	var r *Refusal
	var he *echo.HTTPError
	switch {
	case errors.As(err, &r):
		return response.FailWith(c, r.Status, r.Code, r.Message, r.Details)
	case errors.Is(err, cluster.ErrNotLeader):
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "This node is not the cluster leader: retry in a few seconds")
	case errors.As(err, &he):
		return he
	}
	logger().Error("agent install API", "path", c.Path(), "error", err)
	return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "The install could not be recorded")
}

func invalid(msg string, details ...string) error {
	return refusal(http.StatusBadRequest, response.ErrAgentInstallInvalid, msg, details...)
}

func operator(c echo.Context) string {
	username, _ := c.Get("username").(string)
	return username
}

// readJSON decodes the request's body into v, refusing a field v has none
// of, so a misspelt one is not quietly left out. The body's bytes are
// zeroed once decoded: a start's carries the secrets.
func readJSON(c echo.Context, v any) error {
	raw, err := io.ReadAll(c.Request().Body)
	defer clear(raw)
	if err != nil {
		return err // over the body limit: echo's 413
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("The request body is not one this route takes", "field", "body")
	}
	return nil
}

// startBody is POST /cluster/agent-installs' body. encoding/json hands the
// secrets over as strings, which cannot be zeroed: readStart copies them
// into the Account's byte slices and drops the strings at once, so they
// live only as long as the request's decoding.
type startBody struct {
	Servers     []serverBody `json:"servers"`
	Account     accountBody  `json:"account"`
	Review      bool         `json:"review"`
	Mode        string       `json:"mode"`
	Concurrency int          `json:"concurrency"`
}

type serverBody struct {
	Host string `json:"host"`
	// Port is 22 when left out; one given is 1 to 65535.
	Port  *int   `json:"port"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// accountBody is the account to log in with: typed in the form, or Login,
// the ID of a saved login, alone.
type accountBody struct {
	Login        string `json:"login"`
	User         string `json:"user"`
	Method       string `json:"method"`
	Password     string `json:"password"`
	Key          string `json:"key"`
	Passphrase   string `json:"passphrase"`
	SudoPassword string `json:"sudo_password"`
	SudoSame     bool   `json:"sudo_same"`
}

// methods are the API's login methods as the engine names them.
var methods = map[string]string{"password": "password", "key": "key", "tailscale": "none"}

// readStart reads a start's body into a Request whose secrets are byte
// slices, and checks what only the body can say: the method's name, or a
// saved login named with no other account field, and a port given. The
// rest is normalise's. On a refusal the secrets are zeroed. login is the
// saved login's ID, for the leader to read (useLogin).
func readStart(c echo.Context) (r Request, login string, err error) {
	var b startBody
	if err := readJSON(c, &b); err != nil {
		return Request{}, "", err
	}
	a := &b.Account
	r = Request{
		Review: b.Review, Mode: b.Mode, Concurrency: b.Concurrency,
		Account: Account{
			User: a.User, SudoSame: a.SudoSame,
			Password: secret(a.Password), Key: secret(a.Key), Passphrase: secret(a.Passphrase), SudoPassword: secret(a.SudoPassword),
		},
	}
	a.Password, a.Key, a.Passphrase, a.SudoPassword = "", "", "", ""
	if a.Login != "" {
		// A saved login is the whole account: a field beside it would
		// leave which one counts to a guess.
		ra := &r.Account
		if a.User != "" || a.Method != "" || a.SudoSame || len(ra.Password)+len(ra.Key)+len(ra.Passphrase)+len(ra.SudoPassword) > 0 {
			ra.wipe()
			return Request{}, "", invalid("A saved login is the whole account: no other account field goes with it", "field", "login")
		}
	} else {
		method, ok := methods[a.Method]
		if !ok {
			r.Account.wipe()
			return Request{}, "", invalid("The login method is password, key or tailscale", "field", "method")
		}
		r.Account.Method = method
	}
	for i, s := range b.Servers {
		port := 22
		if s.Port != nil {
			port = *s.Port
			if port < 1 || port > 65535 {
				r.Account.wipe()
				return Request{}, "", invalid("A server's SSH port is 1 to 65535", "row", rowKey(i), "field", "port")
			}
		}
		r.Servers = append(r.Servers, Server{Host: s.Host, Port: port, Name: s.Name, Group: s.Group})
	}
	return r, a.Login, nil
}

// secret is s as bytes the engine can zero, nil for none.
func secret(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}

// busyRefusal is the start gate's refusal of a start, as a task run's: a
// cluster update, a CA rotation, a conversion or a promotion restarts or
// re-keys the nodes an install registers with.
func busyRefusal(b featurecluster.Busy) error {
	switch {
	case b.UpdateRunning:
		return refusal(http.StatusConflict, response.ErrUpdateInProgress, "A cluster update is under way")
	case b.CARotation:
		return refusal(http.StatusConflict, response.ErrClusterCARotating, cluster.ErrCARotationInProgress.Error())
	case b.Converting:
		return refusal(http.StatusConflict, response.ErrTaskClusterChanging, "A manager is being converted to an agent", "what", "conversion")
	case b.Promoting:
		return refusal(http.StatusConflict, response.ErrTaskClusterChanging, "An agent is being promoted to a manager", "what", "promotion")
	}
	return nil
}

// Start answers POST /cluster/agent-installs (leader): the request is
// checked, then judged and recorded in one StartGate claim, as a task run
// is, so an update, a CA rotation, a conversion, a promotion or a task run
// that starts next sees the install under way; 202 {run_id}. The servers are
// worked on in the background, and what becomes of each is its row in the
// run's record. The request's secrets are the engine's from the claim on,
// and zeroed here when it never gets them. The start is audited by the
// engine, without the body. Recording the run asks nothing slow of anyone,
// so the start stays well inside the thirty seconds a follower's forward
// of it gets. An account that is a saved login is read here, on the leader,
// before the claim: a follower forwards the body as it came, and never
// reads a saved secret.
func (h *Handler) Start(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	req, login, err := readStart(c)
	if err != nil {
		return answerErr(c, err)
	}
	req.StartedBy = operator(c)
	handed := false
	defer func() {
		if !handed {
			req.Account.wipe()
		}
	}()
	if login != "" {
		if err := h.engine.useLogin(&req, login); err != nil {
			return answerErr(c, err)
		}
	}
	// Checked before the gate, so a request that is not one is told so
	// whatever is under way; Start checks again, finding it as it is.
	if err := normalise(&req); err != nil {
		return answerErr(c, err)
	}
	var runID string
	err = h.startGate(func(b featurecluster.Busy) error {
		if err := busyRefusal(b); err != nil {
			return err
		}
		handed = true
		var err error
		runID, err = h.engine.Start(c.Request().Context(), req)
		return err
	})
	if err != nil {
		return answerErr(c, err)
	}
	return response.Accepted(c, map[string]string{"run_id": runID})
}

// answerBody is the body of an answer to a row's question.
type answerBody struct {
	Fingerprint string `json:"fingerprint"`
	Accept      *bool  `json:"accept"`
}

// readAnswer reads an answer, which must say yes or no.
func readAnswer(c echo.Context) (answerBody, error) {
	var b answerBody
	if err := readJSON(c, &b); err != nil {
		return b, err
	}
	if b.Accept == nil {
		return b, invalid("The answer says neither yes nor no", "field", "accept")
	}
	return b, nil
}

// ApproveHostKey answers POST /cluster/agent-installs/:id/servers/:key/host-key
// {fingerprint, accept} (leader): yes with the fingerprint the row showed,
// or no, which stops that server. Another fingerprint is 409
// AGENT_INSTALL_HOST_KEY_CHANGED.
func (h *Handler) ApproveHostKey(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	b, err := readAnswer(c)
	if err != nil {
		return answerErr(c, err)
	}
	if *b.Accept && b.Fingerprint == "" {
		return answerErr(c, invalid("Approving a host key takes the fingerprint shown", "field", "fingerprint"))
	}
	if err := h.engine.ApproveHostKey(c.Param("id"), c.Param("key"), b.Fingerprint, *b.Accept); err != nil {
		return answerErr(c, err)
	}
	return response.OK(c, nil)
}

// ConfirmStandalone answers POST
// /cluster/agent-installs/:id/servers/:key/standalone {accept} (leader):
// whether the server's standalone panel may become an agent, losing its
// local sign-in; no skips that server.
func (h *Handler) ConfirmStandalone(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	b, err := readAnswer(c)
	if err != nil {
		return answerErr(c, err)
	}
	if err := h.engine.ConfirmStandalone(c.Param("id"), c.Param("key"), *b.Accept); err != nil {
		return answerErr(c, err)
	}
	return response.OK(c, nil)
}

// Proceed answers POST /cluster/agent-installs/:id/proceed (leader): every
// server waiting after its pre-flight in a review goes on to install.
func (h *Handler) Proceed(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	if err := h.engine.Proceed(c.Param("id")); err != nil {
		return answerErr(c, err)
	}
	return response.OK(c, nil)
}

// Recheck answers POST /cluster/agent-installs/:id/servers/:key/recheck
// (leader): a registered agent that did not come online is checked again.
func (h *Handler) Recheck(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	if err := h.engine.Recheck(c.Param("id"), c.Param("key")); err != nil {
		return answerErr(c, err)
	}
	return response.OK(c, nil)
}

// Revert answers POST /cluster/agent-installs/:id/servers/:key/revert
// (leader): a registered agent that did not come online, or whose row was
// stopped, is removed from the cluster, and what this run installed on the
// server goes while the credentials are still held.
func (h *Handler) Revert(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	if err := h.engine.Revert(c.Param("id"), c.Param("key"), operator(c)); err != nil {
		return answerErr(c, err)
	}
	return response.OK(c, nil)
}

// Tailnet answers GET /cluster/agent-installs/tailnet (leader): the Linux
// devices of the leader's tailnet, for the install form to pick servers
// from, as the leader is the node that reaches them. A leader not on
// Tailscale, or whose tailscale reaches no tailscaled, is 200 with
// on_tailscale false and the reason; a `tailscale status` that fails
// otherwise is 500 AGENT_INSTALL_TAILNET_FAILED, its output sanitized.
func (h *Handler) Tailnet(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	ans, err := h.engine.Tailnet(c.Request().Context())
	var te *TailnetError
	if errors.As(err, &te) {
		return response.Fail(c, http.StatusInternalServerError, response.ErrAgentInstallTailnetFailed, te.Error())
	}
	if err != nil {
		return answerErr(c, err)
	}
	return response.OK(c, ans)
}
