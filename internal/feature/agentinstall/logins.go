package agentinstall

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// Saved logins: SSH logins an operator named and the cluster keeps, so that
// servers sharing one initial account are installed without typing it each
// time. The store is the cluster manager's (cluster.InstallLogin, in the
// replicated config map, on managers only); here are its routes, under the
// manager-only /cluster group, and an install's use of a saved login.
//
// They are an exception the operator made to the install's rule that no
// secret is kept: a saved login's secrets are plaintext in every manager's
// replicated state, as jwt_secret is. The rest of the rule holds. No route
// answers a saved secret (LoginView says only whether each is set); an
// install's run record names the login it used, never its secrets; and the
// install holds its own copy of the account as zeroed bytes, as it does a
// typed one. A saved login also lets anyone with a panel session start an
// install with it, so its secrets go only to the servers savedLoginAllowed
// lets them reach.
//
// Every route here is the leader's, as the install's are: a follower sends
// it on (forward), and the leader answers from its own state, so a page
// reads its own write.

// LoginView is a saved login as every route answers it: whether each secret
// is set, never the secret. Method is the API's name: password, key or
// tailscale.
type LoginView struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	User            string    `json:"user"`
	Method          string    `json:"method"`
	HasPassword     bool      `json:"has_password"`
	HasKey          bool      `json:"has_key"`
	HasPassphrase   bool      `json:"has_passphrase"`
	SudoSame        bool      `json:"sudo_same"`
	HasSudoPassword bool      `json:"has_sudo_password"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	UpdatedBy       string    `json:"updated_by"`
}

func loginView(l cluster.InstallLogin) LoginView {
	return LoginView{
		ID: l.ID, Name: l.Name, User: l.User, Method: apiMethod(l.Method),
		HasPassword: l.Password != "", HasKey: l.Key != "", HasPassphrase: l.Passphrase != "",
		SudoSame: l.SudoSame, HasSudoPassword: l.SudoPassword != "",
		CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt, UpdatedBy: l.UpdatedBy,
	}
}

// apiMethod is the engine's login method as the API names it.
func apiMethod(method string) string {
	for api, m := range methods {
		if m == method {
			return api
		}
	}
	return method
}

// maxLoginName is the most characters a saved login's name has.
const maxLoginName = 64

// loginBody is the body of a saved login's create and change. As a start's
// account, its secrets are strings only while the body is decoded: readLogin
// copies them into an Account's byte slices and drops them.
type loginBody struct {
	Name         string `json:"name"`
	User         string `json:"user"`
	Method       string `json:"method"`
	Password     string `json:"password"`
	Key          string `json:"key"`
	Passphrase   string `json:"passphrase"`
	SudoSame     bool   `json:"sudo_same"`
	SudoPassword string `json:"sudo_password"`
}

// readLogin reads a saved login's body: its name, trimmed, and its account,
// the secrets as byte slices the caller zeroes. A name that is not 1 to 64
// characters without a control character, or a method that is not one, is
// 400 AGENT_INSTALL_INVALID, the secrets zeroed. The account is the
// caller's to check, once it is whole.
func readLogin(c echo.Context) (string, Account, error) {
	var b loginBody
	if err := readJSON(c, &b); err != nil {
		return "", Account{}, err
	}
	a := Account{
		User: b.User, SudoSame: b.SudoSame,
		Password: secret(b.Password), Key: secret(b.Key), Passphrase: secret(b.Passphrase), SudoPassword: secret(b.SudoPassword),
	}
	b.Password, b.Key, b.Passphrase, b.SudoPassword = "", "", "", ""
	name := strings.TrimSpace(b.Name)
	if n := utf8.RuneCountInString(name); n == 0 || n > maxLoginName || strings.ContainsFunc(name, unicode.IsControl) {
		a.wipe()
		return "", Account{}, invalid(fmt.Sprintf("A saved login's name is 1 to %d characters, none of them a control character", maxLoginName),
			"field", "name", "max", strconv.Itoa(maxLoginName))
	}
	method, ok := methods[b.Method]
	if !ok {
		a.wipe()
		return "", Account{}, invalid("The login method is password, key or tailscale", "field", "method")
	}
	a.Method = method
	return name, a, nil
}

// toStore is the login to store: name and a, whose secrets the store keeps
// as strings.
func toStore(id, name string, a Account, by string) cluster.InstallLogin {
	return cluster.InstallLogin{
		ID: id, Name: name, User: a.User, Method: a.Method,
		Password: string(a.Password), Key: string(a.Key), Passphrase: string(a.Passphrase),
		SudoPassword: string(a.SudoPassword), SudoSame: a.SudoSame, UpdatedBy: by,
	}
}

// keepStored fills a, a change's account, from old, the login as stored: a
// secret left empty keeps the stored one where a's method uses it. The
// passphrase is kept only with the stored key: a new key comes with its own
// passphrase, or none. The sudo password is kept only without sudo_same,
// which drops it. A stored secret a's method does not use is not taken, and
// goes with the change.
func keepStored(a *Account, old cluster.InstallLogin) {
	if a.Method == "password" && len(a.Password) == 0 {
		a.Password = secret(old.Password)
	}
	if a.Method == "key" && len(a.Key) == 0 {
		a.Key = secret(old.Key)
		if len(a.Passphrase) == 0 {
			a.Passphrase = secret(old.Passphrase)
		}
	}
	if !a.SudoSame && len(a.SudoPassword) == 0 {
		a.SudoPassword = secret(old.SudoPassword)
	}
}

// loginErr is the API's refusal for the store's: 404
// AGENT_INSTALL_LOGIN_NOT_FOUND, 409 AGENT_INSTALL_LOGIN_EXISTS (field
// name) and 409 AGENT_INSTALL_LOGIN_LIMIT (max). Anything else is err.
func loginErr(err error) error {
	switch {
	case errors.Is(err, cluster.ErrInstallLoginNotFound):
		return refusal(http.StatusNotFound, response.ErrAgentInstallLoginNotFound, "No saved login has this ID")
	case errors.Is(err, cluster.ErrInstallLoginNameTaken):
		return refusal(http.StatusConflict, response.ErrAgentInstallLoginExists, "Another saved login has this name", "field", "name")
	case errors.Is(err, cluster.ErrInstallLoginLimit):
		return refusal(http.StatusConflict, response.ErrAgentInstallLoginLimit, cluster.ErrInstallLoginLimit.Error(),
			"max", strconv.Itoa(cluster.MaxInstallLogins))
	}
	return err
}

// ListLogins answers GET /cluster/agent-installs/logins (leader):
// {logins}, by name without regard to case.
func (h *Handler) ListLogins(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	list, err := h.engine.env.InstallLogins()
	if err != nil {
		return answerErr(c, err)
	}
	views := make([]LoginView, 0, len(list))
	for _, l := range list {
		views = append(views, loginView(l))
	}
	return response.OK(c, map[string][]LoginView{"logins": views})
}

// CreateLogin answers POST /cluster/agent-installs/logins (leader): 201
// {login}, saved under a new ID. Its account is checked as an install's is.
func (h *Handler) CreateLogin(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	name, a, err := readLogin(c)
	if err != nil {
		return answerErr(c, err)
	}
	defer a.wipe()
	if field, msg := accountProblem(a); field != "" {
		return answerErr(c, invalid(msg, "field", field))
	}
	l, err := h.engine.env.PutInstallLogin(toStore("", name, a, operator(c)))
	if err != nil {
		return answerErr(c, loginErr(err))
	}
	return c.JSON(http.StatusCreated, response.Response{Success: true, Data: map[string]LoginView{"login": loginView(l)}})
}

// UpdateLogin answers PUT /cluster/agent-installs/logins/:id (leader):
// {login}, changed. A secret left empty keeps the stored one where the
// login's method uses it (keepStored), and the account that comes of it is
// checked as an install's is. Changes are made one at a time (loginMu), so
// each keeps what the one before it wrote.
func (h *Handler) UpdateLogin(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	name, a, err := readLogin(c)
	if err != nil {
		return answerErr(c, err)
	}
	defer a.wipe()
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	old, err := h.engine.env.InstallLogin(c.Param("id"))
	if err != nil {
		return answerErr(c, loginErr(err))
	}
	keepStored(&a, old)
	if field, msg := accountProblem(a); field != "" {
		return answerErr(c, invalid(msg, "field", field))
	}
	l, err := h.engine.env.PutInstallLogin(toStore(old.ID, name, a, operator(c)))
	if err != nil {
		return answerErr(c, loginErr(err))
	}
	return response.OK(c, map[string]LoginView{"login": loginView(l)})
}

// DeleteLogin answers DELETE /cluster/agent-installs/logins/:id (leader):
// {id}, as a task's delete answers. An install under way with the login
// keeps its own copy of the account.
func (h *Handler) DeleteLogin(c echo.Context) error {
	if handled, err := h.forward(c); handled {
		return err
	}
	id := c.Param("id")
	if err := h.engine.env.DeleteInstallLogin(id); err != nil {
		return answerErr(c, loginErr(err))
	}
	return response.OK(c, map[string]string{"id": id})
}

// useLogin makes saved login id the account of r, on the leader, before
// the start is judged: fresh byte copies of the stored secrets, which the
// install zeroes as its own while the store keeps its strings for the next.
// An ID no login has is 404 AGENT_INSTALL_LOGIN_NOT_FOUND.
func (e *Engine) useLogin(r *Request, id string) error {
	l, err := e.env.InstallLogin(id)
	if err != nil {
		return loginErr(err)
	}
	r.Account = Account{
		User: l.User, Method: l.Method, SudoSame: l.SudoSame,
		Password: secret(l.Password), Key: secret(l.Key), Passphrase: secret(l.Passphrase), SudoPassword: secret(l.SudoPassword),
	}
	r.Login, r.saved = l.Name, true
	return nil
}

// sendsSecret reports whether a gives the server a secret: a password
// login's password, or a sudo password (with SudoSame, the login password,
// of a password login). A key login sends none, as public-key
// authentication never sends the key, and Tailscale SSH has none to send.
func (a *Account) sendsSecret() bool {
	return a.Method == "password" || len(a.SudoPassword) > 0
}

// savedLoginAllowed refuses the row, before anything reaches its server,
// its host key included, when the install's account is a saved login that
// sends the server a secret and the row's address is neither private nor a
// Tailscale address the leader's tailnet lists on a peer: a stolen panel
// session could otherwise aim a saved password at a server of its own. A
// tailnet that cannot be read refuses, and the connect log says why. An
// account typed in the form is not limited: the operator typed it for these
// servers.
func (r *row) savedLoginAllowed(ctx context.Context, log *phaseLog) error {
	s := r.s
	if !s.savedSecret || r.ip.IsPrivate() {
		return nil
	}
	if cluster.IsTailscaleIP(r.ip) {
		t, err := s.env.Tailnet(ctx)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.printf("the saved login is not sent to %s: the tailnet could not be read (%s)", r.ip, err)
		case t.BackendState != "Running":
			log.printf("the saved login is not sent to %s: Tailscale on this leader is %s", r.ip, t.BackendState)
		default:
			for _, p := range t.Peer {
				if p != nil && p.holds(r.ip) {
					return nil
				}
			}
		}
	}
	return fail(response.ErrAgentInstallLoginOffNetwork,
		"a saved login's password goes only to servers on a private network or in the leader's tailnet; a password typed in the pane is not limited",
		"address", r.ip.String())
}
