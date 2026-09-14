package ai

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// Session is what the page renders: a row joined with tmux's view of it.
type Session struct {
	ID             string `json:"id"`
	Tool           string `json:"tool"`
	Title          string `json:"title"`
	RunAs          string `json:"run_as"`
	CWD            string `json:"cwd"`
	State          string `json:"state"`
	Persistence    string `json:"persistence"` // "scope" | "process"
	Attached       bool   `json:"attached"`
	Unknown        bool   `json:"unknown,omitempty"` // live on the socket, no row
	CreatedAt      string `json:"created_at"`
	LastAttachedAt string `json:"last_attached_at,omitempty"`
	EndedAt        string `json:"ended_at,omitempty"`
}

var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

func validSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// newSessionID is 12 hex chars: the tmux session name, the scope unit
// suffix and the primary key. Never client-supplied.
func newSessionID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

var toolNames = map[string]string{ToolClaude: "Claude", ToolCodex: "Codex", ToolGemini: "Gemini", ToolShell: "Shell"}

func defaultTitle(tool, cwd string) string {
	return fmt.Sprintf("%s · %s", toolNames[tool], filepath.Base(cwd))
}

const maxTitleRunes = 64

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxTitleRunes {
		r := []rune(s)
		s = string(r[:maxTitleRunes])
	}
	return s
}

// validateCWD accepts only an absolute path to an existing directory and
// names which of the three checks failed so the UI can say so.
func validateCWD(p string) (string, string) {
	if !filepath.IsAbs(p) {
		return "", "relative"
	}
	clean := filepath.Clean(p)
	info, err := os.Stat(clean)
	if err != nil {
		return "", "missing"
	}
	if !info.IsDir() {
		return "", "not a directory"
	}
	return clean, ""
}

func (h *Handler) persistence() string {
	if h.haveSystemdRun() {
		return "scope"
	}
	return "process"
}

// sessionsSnapshot joins the table with tmux (spec §1 "Liveness and state").
// One list-windows per distinct account; the panel account is always asked
// so sessions whose rows were lost still surface.
func (h *Handler) sessionsSnapshot() ([]Session, error) {
	rows, err := listSessionRows(h.DB)
	if err != nil {
		return nil, err
	}
	now := h.now()
	persistence := h.persistence()
	live := map[string]map[string]liveWindow{}
	lookup := func(name string) map[string]liveWindow {
		if l, ok := live[name]; ok {
			return l
		}
		l := map[string]liveWindow{}
		if acct, ok := h.resolveAccount(name); ok {
			l = h.liveWindows(acct)
		}
		live[name] = l
		return l
	}
	lookup(h.panel.Name)

	seen := map[string]bool{}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		s := Session{ID: r.ID, Tool: r.Tool, Title: r.Title, RunAs: r.RunAs, CWD: r.CWD, Persistence: persistence,
			CreatedAt: r.CreatedAt, LastAttachedAt: r.LastAttachedAt.String, EndedAt: r.EndedAt.String}
		if w, alive := lookup(r.RunAs)[r.ID]; alive {
			s.State = deriveState(w, now)
			s.Attached = w.Attached
			if r.EndedAt.Valid { // came back (restart raced the poll)
				_ = setSessionEnded(h.DB, r.ID, false)
				s.EndedAt = ""
			}
		} else {
			s.State = StateEnded
			if !r.EndedAt.Valid {
				_ = setSessionEnded(h.DB, r.ID, true)
				s.EndedAt = now.UTC().Format("2006-01-02 15:04:05")
			}
		}
		seen[r.ID] = true
		out = append(out, s)
	}
	for acct, l := range live {
		for id, w := range l {
			if seen[id] || !validSessionID(id) {
				continue
			}
			out = append(out, Session{ID: id, Tool: ToolShell, Title: id, RunAs: acct, State: deriveState(w, now),
				Attached: w.Attached, Persistence: persistence, Unknown: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (h *Handler) spawn(id, cwd, tool string, acct Account) error {
	name, argv := h.spawnArgv(id, cwd, tool, acct, h.haveSystemdRun())
	out, err := h.Cmd.RunWithTimeout(tmuxTimeout, name, argv...)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return nil
}

// ListSessions — GET /ai/sessions
func (h *Handler) ListSessions(c echo.Context) error {
	out, err := h.sessionsSnapshot()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list sessions")
	}
	return response.OK(c, out)
}

type createSessionReq struct {
	Tool  string `json:"tool"`
	CWD   string `json:"cwd"`
	RunAs string `json:"run_as"`
	Title string `json:"title"`
}

// CreateSession — POST /ai/sessions. Validation order is fixed: nothing
// touches tmux until every client-supplied field has passed its allowlist.
func (h *Handler) CreateSession(c echo.Context) error {
	var req createSessionReq
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if !validTool(req.Tool) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidTool, "tool must be claude, codex, gemini or shell")
	}
	acct, ok := h.resolveAccount(req.RunAs)
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "run_as is not a login account on this node")
	}
	cwd, reason := validateCWD(req.CWD)
	if reason != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "cwd: "+reason)
	}
	if !h.Cmd.Exists("tmux") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrTmuxMissing, "tmux is not installed on this node")
	}
	snap, err := h.sessionsSnapshot()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list sessions")
	}
	liveCount := 0
	for _, s := range snap {
		if s.State != StateEnded {
			liveCount++
		}
	}
	if liveCount >= maxSessions {
		return response.Fail(c, http.StatusConflict, response.ErrAISessionLimit, fmt.Sprintf("maximum of %d live sessions reached", maxSessions))
	}
	id, err := newSessionID()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not generate a session id")
	}
	title := cleanTitle(req.Title)
	if title == "" {
		title = defaultTitle(req.Tool, cwd)
	}
	if err := h.spawn(id, cwd, req.Tool, acct); err != nil {
		slog.Error("ai session spawn failed", "component", "ai", "id", id, "account", acct.Name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, response.SanitizeOutput(err.Error()))
	}
	if err := insertSession(h.DB, sessionRow{ID: id, Tool: req.Tool, Title: title, RunAs: acct.Name, CWD: cwd}); err != nil {
		_, _ = h.tmux(acct, "kill-session", "-t", id)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not store the session")
	}
	slog.Info("ai session created", "component", "ai", "id", id, "tool", req.Tool, "account", acct.Name, "cwd", cwd)
	return response.OK(c, Session{ID: id, Tool: req.Tool, Title: title, RunAs: acct.Name, CWD: cwd,
		State: StateWorking, Persistence: h.persistence(), CreatedAt: h.now().UTC().Format("2006-01-02 15:04:05")})
}

// lookupSessionRow resolves :id to a row, writing the failure itself.
// ok=false means the response is already written. It deliberately says
// nothing about the row's account: a route that only needs the row (delete)
// must not be blocked by an account that has left the allowlist.
func (h *Handler) lookupSessionRow(c echo.Context) (sessionRow, bool) {
	id := c.Param("id")
	if !validSessionID(id) {
		_ = response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
		return sessionRow{}, false
	}
	row, found, err := getSessionRow(h.DB, id)
	if err != nil {
		_ = response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the session")
		return sessionRow{}, false
	}
	if !found {
		_ = response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
		return sessionRow{}, false
	}
	return row, true
}

// lookupRow is lookupSessionRow plus the row's account, for the routes that
// genuinely have to spawn as it (rerun, restart). ok=false means the response
// is already written.
func (h *Handler) lookupRow(c echo.Context) (sessionRow, Account, bool) {
	row, ok := h.lookupSessionRow(c)
	if !ok {
		return sessionRow{}, Account{}, false
	}
	acct, ok := h.resolveAccount(row.RunAs)
	if !ok {
		_ = response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "the session's account no longer exists")
		return sessionRow{}, Account{}, false
	}
	return row, acct, true
}

// rowState is the live state of one row, StateEnded when tmux has no session.
func (h *Handler) rowState(row sessionRow, acct Account) string {
	if w, ok := h.liveWindows(acct)[row.ID]; ok {
		return deriveState(w, h.now())
	}
	return StateEnded
}

// RenameSession — PATCH /ai/sessions/:id {title}
func (h *Handler) RenameSession(c echo.Context) error {
	var req struct {
		Title string `json:"title"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	title := cleanTitle(req.Title)
	if title == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "title must not be empty")
	}
	if !validSessionID(c.Param("id")) {
		return response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
	}
	ok, err := updateSessionTitle(h.DB, c.Param("id"), title)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not rename the session")
	}
	if !ok {
		return response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
	}
	return response.OK(c, map[string]string{"id": c.Param("id"), "title": title})
}

// RerunSession — POST /ai/sessions/:id/rerun: type the tool again into a
// pane that has dropped back to its shell.
func (h *Handler) RerunSession(c echo.Context) error {
	row, acct, ok := h.lookupRow(c)
	if !ok {
		return nil
	}
	if row.Tool == ToolShell || !validTool(row.Tool) {
		return response.Fail(c, http.StatusConflict, response.ErrAISessionState, "a shell session has nothing to rerun")
	}
	if st := h.rowState(row, acct); st != StateShell {
		return response.Fail(c, http.StatusConflict, response.ErrAISessionState, "session is "+st+"; rerun needs the shell state")
	}
	if out, err := h.tmux(acct, "send-keys", "-t", row.ID, row.Tool, "Enter"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, response.SanitizeOutput(out))
	}
	return response.OK(c, map[string]string{"id": row.ID, "state": StateWorking})
}

// RestartSession — POST /ai/sessions/:id/restart: a new tmux session with
// the row's tool, directory and account.
func (h *Handler) RestartSession(c echo.Context) error {
	row, acct, ok := h.lookupRow(c)
	if !ok {
		return nil
	}
	if st := h.rowState(row, acct); st != StateEnded {
		return response.Fail(c, http.StatusConflict, response.ErrAISessionState, "session is "+st+"; only an ended session can be restarted")
	}
	cwd, reason := validateCWD(row.CWD)
	if reason != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "cwd: "+reason)
	}
	if !h.Cmd.Exists("tmux") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrTmuxMissing, "tmux is not installed on this node")
	}
	if err := h.spawn(row.ID, cwd, row.Tool, acct); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, response.SanitizeOutput(err.Error()))
	}
	_ = setSessionEnded(h.DB, row.ID, false)
	slog.Info("ai session restarted", "component", "ai", "id", row.ID, "tool", row.Tool, "account", acct.Name)
	return response.OK(c, map[string]string{"id": row.ID, "state": StateWorking})
}

// DeleteSession — DELETE /ai/sessions/:id: kill it if alive, forget the row.
// The account is needed only for the kill. When it has left the allowlist —
// removed, its shell changed to nologin, or the panel no longer running as
// root — there is by definition no session of ours left running as it, so the
// kill is skipped and the row still goes. Refusing here would leave the
// operator an undeletable tab.
func (h *Handler) DeleteSession(c echo.Context) error {
	row, ok := h.lookupSessionRow(c)
	if !ok {
		return nil
	}
	if acct, resolved := h.resolveAccount(row.RunAs); resolved && h.rowState(row, acct) != StateEnded {
		if out, err := h.tmux(acct, "kill-session", "-t", row.ID); err != nil {
			slog.Warn("ai kill-session", "component", "ai", "id", row.ID, "err", err, "out", response.SanitizeOutput(out))
		}
	}
	if _, err := deleteSessionRow(h.DB, row.ID); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not delete the session")
	}
	slog.Info("ai session deleted", "component", "ai", "id", row.ID, "account", row.RunAs)
	return response.OK(c, map[string]string{"deleted": row.ID})
}

// Dirs — GET /ai/dirs?user= feeds the working-directory picker.
func (h *Handler) Dirs(c echo.Context) error {
	acct, ok := h.resolveAccount(c.QueryParam("user"))
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "user is not a login account on this node")
	}
	recent, err := recentSessionDirs(h.DB, 8)
	if err != nil {
		recent = []string{}
	}
	stacks := []string{}
	if entries, err := os.ReadDir(h.StacksPath); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				stacks = append(stacks, filepath.Join(h.StacksPath, e.Name()))
			}
		}
	}
	return response.OK(c, map[string]any{"recent": recent, "stacks": stacks, "home": acct.Home})
}
