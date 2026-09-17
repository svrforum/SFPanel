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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// Session is what the page renders: a row joined with tmux's view of it.
type Session struct {
	ID    string `json:"id"`
	Tool  string `json:"tool"`
	Title string `json:"title"`
	RunAs string `json:"run_as"`
	CWD   string `json:"cwd"`
	// Profile is the row's configuration directory; "" is the tool's own
	// (see profiles.go). Carried on the session because a delete has to see
	// which profiles are in use, and the tab bar shows a non-default one.
	Profile string `json:"profile,omitempty"`
	// Launch is what the session was started with (launch.go). nil is a tool
	// started bare, which is every session created before this feature: a
	// session with no options carries nothing new in the JSON, so the tab has
	// nothing to mark and the info action nothing to list (spec §6).
	Launch         *LaunchOptions `json:"launch,omitempty"`
	State          string         `json:"state"`
	Persistence    string         `json:"persistence"` // "service" | "process"
	Attached       bool           `json:"attached"`
	Unknown        bool           `json:"unknown,omitempty"` // live on the socket, no row
	CreatedAt      string         `json:"created_at"`
	LastAttachedAt string         `json:"last_attached_at,omitempty"`
	EndedAt        string         `json:"ended_at,omitempty"`
}

// launchPtr is Session.Launch: nil for the zero value, so "no options" is one
// thing in the JSON as well as in the column (LaunchOptions.Encode writes the
// empty string for it).
func launchPtr(o LaunchOptions) *LaunchOptions {
	if o.IsZero() {
		return nil
	}
	return &o
}

var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

func validSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// newSessionID is 12 hex chars: the tmux session name and the primary key
// (the transient unit is named per account, not per session). Never
// client-supplied.
func newSessionID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

var toolNames = map[string]string{ToolClaude: "Claude", ToolCodex: "Codex", ToolGemini: "Gemini", ToolShell: "Shell"}

// defaultTitle names the tool and the directory: "Codex · myapp". The
// profile is deliberately not in it (spec §5): the tab renders the profile as
// its own pill, so a second copy in the title costs width in a strip that
// truncates around 18 characters, and it goes stale the moment the operator
// renames the session — while the pill survives the rename.
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

// persistence is what the tab marker reports: "service" when the next spawn
// can hand the account's tmux server to PID 1 as a transient unit — the only
// arrangement that outlives a panel restart — and "process" when it cannot
// and the server is merely setsid'd off the panel.
func (h *Handler) persistence() string {
	if h.serviceFormAvailable() {
		return "service"
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
		s := Session{ID: r.ID, Tool: r.Tool, Title: r.Title, RunAs: r.RunAs, CWD: r.CWD, Profile: r.Profile, Persistence: persistence,
			CreatedAt: r.CreatedAt, LastAttachedAt: r.LastAttachedAt.String, EndedAt: r.EndedAt.String}
		// A column this poll cannot read is not a reason to fail the poll:
		// the tab would vanish for a session that is running perfectly well.
		// It is logged and the session lists without its options.
		if o, err := decodeLaunch(r.Launch); err != nil {
			slog.Debug("ai stored launch options unreadable", "component", "ai", "id", r.ID, "err", err)
		} else {
			s.Launch = launchPtr(o)
		}
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
				// The same format the row will read back as. The columns are
				// DATETIME, so the driver hands them to us as RFC 3339; a
				// "2006-01-02 15:04:05" string here was a second format in the
				// same field, differing only on rows the poll had just stamped.
				s.EndedAt = now.UTC().Format(time.RFC3339)
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
	// Creation order, and total: an unknown session has no row and therefore no
	// CreatedAt, so comparing that field alone left the unknowns interleaved in
	// whatever order the socket happened to list them — a different tab order
	// on every 5 s poll. Unknowns go last, ids break a same-second tie.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.CreatedAt == "") != (b.CreatedAt == "") {
			return b.CreatedAt == ""
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.ID < b.ID
	})
	return out, nil
}

// liveSessionCount is what the 20-session ceiling counts: every session the
// node still has, rows and row-less alike.
func (h *Handler) liveSessionCount() (int, error) {
	snap, err := h.sessionsSnapshot()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range snap {
		if s.State != StateEnded {
			n++
		}
	}
	return n, nil
}

// spawnLock is the lock one account's spawns take turns on, created on first
// use. Accounts do not share it: the lock is held across the check-then-act
// pair and up to three 15 s command runs, so a process-wide one made a hung
// systemd-run for a single account everyone else's problem.
func (h *Handler) spawnLock(acct Account) *sync.Mutex {
	h.spawnMu.Lock()
	defer h.spawnMu.Unlock()
	if h.spawnLocks == nil {
		h.spawnLocks = map[string]*sync.Mutex{}
	}
	mu, ok := h.spawnLocks[acct.Name]
	if !ok {
		mu = &sync.Mutex{}
		h.spawnLocks[acct.Name] = mu
	}
	return mu
}

// spawn creates one session, starting the account's tmux server first if it
// has none. The lock is what makes "has none" safe to act on: the server form
// claims the fixed unit name sfpanel-ai-<uid>, so two concurrent creates for
// one account must not both decide there is no server and both ask for it.
//
// The launch options are turned into an argv here rather than handed in ready
// made: toolArgv validates before it builds, so there is no way to reach a
// spawn with an argv nothing checked.
func (h *Handler) spawn(id, cwd, tool, profile string, acct Account, launch LaunchOptions) error {
	launchArgv, err := toolArgv(tool, launch)
	if err != nil {
		return err
	}
	if err := h.ensureSocketDir(acct); err != nil {
		return fmt.Errorf("could not prepare the session socket directory: %w", err)
	}
	mu := h.spawnLock(acct)
	mu.Lock()
	defer mu.Unlock()
	form := h.spawnFormFor(acct)
	name, argv := h.spawnArgv(id, cwd, tool, profile, acct, form, launchArgv)
	out, err := h.Cmd.RunWithTimeout(tmuxTimeout, name, argv...)
	if err != nil && form == spawnService && h.serverRunning(acct) {
		// The session may already be there: systemd-run can exit non-zero
		// *after* the new-session in its argv took hold, and then the retry
		// below asks tmux for a second session of the same name. tmux answers
		// "duplicate session", the create fails with COMMAND_FAILED — and a
		// live session with that id is left on the socket with no row behind
		// it, i.e. an unattended agent the page can only show as unknown.
		if _, probeErr := h.tmux(acct, "has-session", "-t", id); probeErr == nil {
			slog.Debug("ai spawn: the unit failed after its session took hold", "component", "ai", "id", id, "account", acct.Name, "err", err)
			return nil
		}
		// The unit was claimed between the check and the call — by another
		// panel process, or by a server this one started and has not seen
		// yet. There is a server now, so talk to it; that is not a failure
		// the operator should be shown.
		slog.Debug("ai spawn retrying on the running server", "component", "ai", "id", id, "account", acct.Name, "err", err)
		name, argv = h.spawnArgv(id, cwd, tool, profile, acct, spawnClient, launchArgv)
		out, err = h.Cmd.RunWithTimeout(tmuxTimeout, name, argv...)
	}
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
	Tool    string `json:"tool"`
	CWD     string `json:"cwd"`
	RunAs   string `json:"run_as"`
	Title   string `json:"title"`
	Profile string `json:"profile"`
	// Launch is a pointer so that an absent object and an empty one are the
	// same thing and neither is an error: every client that predates the
	// feature sends nothing, and the dialog sends {} when the section is
	// opened and nothing in it is chosen.
	Launch *LaunchOptions `json:"launch"`
}

// launchRefusal is POST /ai/sessions' check on the launch options: the
// per-tool validation of launch.go plus the one rule of spec §3 that needs
// the resolved account. A non-empty code is the refusal to write; the message
// is shown beside the control that produced it, so it names the field and the
// rule rather than saying "invalid".
func launchRefusal(tool string, o LaunchOptions, acct Account) (string, string) {
	if err := validateLaunch(tool, o); err != nil {
		// validateLaunch's text is both a Go error and what the dialog shows.
		// The "ai: " prefix is the package convention for the first and noise
		// in front of an operator, so the field name replaces it here.
		return response.ErrInvalidBody, "launch: " + strings.TrimPrefix(err.Error(), "ai: ")
	}
	// UID, not the name: an account named something else with uid 0 is root
	// as far as the CLI's own check is concerned.
	if tool == ToolClaude && o.Dangerous && acct.UID == 0 {
		return response.ErrLaunchRootDanger, fmt.Sprintf(
			"launch: claude refuses --dangerously-skip-permissions when it runs as root, and %s is root; pick a non-root account", acct.Name)
	}
	return "", ""
}

// profileRefusal is the membership check POST /ai/sessions makes on a
// client-supplied profile: it must be one the picker could have offered, i.e.
// a directory profileList already reports, never a name the panel creates on
// the fly (spec §3). The default profile is always valid, and a non-empty one
// needs a tool that has a profile at all. A non-empty first return value is
// the refusal message; an error is the node's failure to look.
func (h *Handler) profileRefusal(acct Account, tool, profile string) (string, error) {
	if profile == "" {
		return "", nil
	}
	if !toolSupportsProfiles(tool) {
		return "profile: a " + tool + " session has no profile", nil
	}
	list, err := h.profileList(acct, tool)
	if err != nil {
		return "", err
	}
	for _, p := range list {
		if !p.Default && p.Name == profile {
			return "", nil
		}
	}
	return "profile: no profile of that name for this account and tool", nil
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
	// Before the cwd, and well before anything runs: the profile decides
	// which credentials the tool will use, and a refused request must not
	// have spawned a session on the wrong one.
	refusal, err := h.profileRefusal(acct, req.Tool, req.Profile)
	if err != nil {
		slog.Error("ai could not list profiles for a create", "component", "ai", "account", acct.Name, "tool", req.Tool, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list the profiles")
	}
	if refusal != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, refusal)
	}
	// After the account because the root rule needs the resolved one, and
	// still before the cwd: no command has run yet, so a refused option set
	// has spawned nothing (spec §3).
	launch := LaunchOptions{}
	if req.Launch != nil {
		launch = *req.Launch
	}
	if code, msg := launchRefusal(req.Tool, launch, acct); code != "" {
		return response.Fail(c, http.StatusBadRequest, code, msg)
	}
	cwd, reason := validateCWD(req.CWD)
	if reason != "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "cwd: "+reason)
	}
	if !h.Cmd.Exists("tmux") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrTmuxMissing, "tmux is not installed on this node")
	}
	if msg := h.tmuxTooOld(); msg != "" {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrTmuxMissing, msg)
	}
	liveCount, err := h.liveSessionCount()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list sessions")
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
	// Encoded before the spawn: a set of options that cannot be stored is a
	// session 다시 시작 could not reproduce, and finding that out after the
	// tool is running would mean killing it again.
	launchJSON, err := launch.Encode()
	if err != nil {
		slog.Error("ai could not encode the launch options", "component", "ai", "tool", req.Tool, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not store the launch options")
	}
	if err := h.spawn(id, cwd, req.Tool, req.Profile, acct, launch); err != nil {
		slog.Error("ai session spawn failed", "component", "ai", "id", id, "account", acct.Name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, response.SanitizeOutput(err.Error()))
	}
	if err := insertSession(h.DB, sessionRow{ID: id, Tool: req.Tool, Title: title, RunAs: acct.Name, CWD: cwd, Profile: req.Profile, Launch: launchJSON}); err != nil {
		_, _ = h.tmux(acct, "kill-session", "-t", id)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not store the session")
	}
	slog.Info("ai session created", "component", "ai", "id", id, "tool", req.Tool, "account", acct.Name, "cwd", cwd, "profile", req.Profile, "launch", launchJSON)
	return response.OK(c, Session{ID: id, Tool: req.Tool, Title: title, RunAs: acct.Name, CWD: cwd, Profile: req.Profile, Launch: launchPtr(launch),
		State: StateWorking, Persistence: h.persistence(), CreatedAt: h.now().UTC().Format(time.RFC3339)})
}

// lookupSessionRow resolves :id to a row, writing the failure itself.
// ok=false means the response is already written. It says nothing about the
// row's account; the account is resolved by whoever needs it (lookupRow for
// the routes that spawn, DeleteSession for the kill it can skip).
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
// pane that has dropped back to its shell — with the options the session was
// created with, because a rerun that started the tool bare would silently
// change what the session is.
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
	launch, err := decodeLaunch(row.Launch)
	if err != nil {
		slog.Error("ai stored launch options unreadable", "component", "ai", "id", row.ID, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the session's launch options")
	}
	// Not ErrInvalidBody: there is no body on a rerun to blame. A stored set
	// this panel cannot build is a row from a newer one (or an edited
	// database), so it answers like the decode failure above and names the
	// rule it could not satisfy.
	launchArgv, err := toolArgv(row.Tool, launch)
	if err != nil {
		slog.Error("ai stored launch options no longer valid", "component", "ai", "id", row.ID, "tool", row.Tool, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "launch: "+strings.TrimPrefix(err.Error(), "ai: "))
	}
	// ONE argument, not one per word: tmux puts nothing between adjacent
	// send-keys arguments, so `send-keys claude --continue` types
	// "claude--continue" (verified on tmux 3.6).
	//
	// This is the one path where the options do reach a shell — the pane's own,
	// which re-reads the line it is typed — and that is what validateLaunch's
	// no-whitespace, no-metacharacter token rule is for. The spawn path never
	// does (sessionCommands passes the words through the wrapper's "$@").
	line := strings.Join(append([]string{row.Tool}, launchArgv...), " ")
	if out, err := h.tmux(acct, "send-keys", "-t", row.ID, line, "Enter"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, response.SanitizeOutput(out))
	}
	return response.OK(c, map[string]string{"id": row.ID, "state": StateWorking})
}

// RestartSession — POST /ai/sessions/:id/restart: a new tmux session with
// the row's tool, directory, account and profile.
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
	if msg := h.tmuxTooOld(); msg != "" {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrTmuxMissing, msg)
	}
	// A restart adds a live session just as a create does, so it has to meet
	// the same ceiling — otherwise twenty ended tabs are twenty free sessions.
	liveCount, err := h.liveSessionCount()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list sessions")
	}
	if liveCount >= maxSessions {
		return response.Fail(c, http.StatusConflict, response.ErrAISessionLimit, fmt.Sprintf("maximum of %d live sessions reached", maxSessions))
	}
	// The row's profile and the row's launch options, not the defaults: the
	// session comes back on the login it was created with and started the way
	// it was started (spec §4).
	launch, err := decodeLaunch(row.Launch)
	if err != nil {
		slog.Error("ai stored launch options unreadable", "component", "ai", "id", row.ID, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the session's launch options")
	}
	if err := h.spawn(row.ID, cwd, row.Tool, row.Profile, acct, launch); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCommandFailed, response.SanitizeOutput(err.Error()))
	}
	_ = setSessionEnded(h.DB, row.ID, false)
	slog.Info("ai session restarted", "component", "ai", "id", row.ID, "tool", row.Tool, "account", acct.Name, "profile", row.Profile, "launch", row.Launch)
	return response.OK(c, map[string]string{"id": row.ID, "state": StateWorking})
}

// DeleteSession — DELETE /ai/sessions/:id: kill it if alive, forget the row.
// The account is needed only for the kill. When it has left the allowlist —
// removed, its shell changed to nologin, or the panel no longer running as
// root — there is by definition no session of ours left running as it, so the
// kill is skipped and the row still goes. Refusing here would leave the
// operator an undeletable tab.
//
// An id with no row is not automatically a 404: it may still name a live
// session (spec §1 — the DB was lost, and such a session "can still be
// attached or killed"). See deleteLiveRowless.
func (h *Handler) DeleteSession(c echo.Context) error {
	id := c.Param("id")
	if !validSessionID(id) {
		return response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
	}
	row, found, err := getSessionRow(h.DB, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not read the session")
	}
	if !found {
		return h.deleteLiveRowless(c, id)
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

// deleteLiveRowless kills a session that is live on one of the sockets but has
// no row. The tab bar offers 종료 for it (it is listed as unknown), so the 404
// the row lookup would give left the operator a tab they could not close and a
// tmux session — possibly an unattended agent — they could not reach. Only an
// id that is neither in the table nor on any socket is a 404.
func (h *Handler) deleteLiveRowless(c echo.Context, id string) error {
	snap, err := h.sessionsSnapshot()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list sessions")
	}
	for _, s := range snap {
		if s.ID != id || s.State == StateEnded {
			continue
		}
		acct, resolved := h.resolveAccount(s.RunAs)
		if !resolved {
			break // listed as live, but no account to run kill-session as
		}
		if out, err := h.tmux(acct, "kill-session", "-t", id); err != nil {
			slog.Warn("ai kill-session", "component", "ai", "id", id, "err", err, "out", response.SanitizeOutput(out))
		}
		slog.Info("ai session deleted", "component", "ai", "id", id, "account", s.RunAs)
		return response.OK(c, map[string]string{"deleted": id})
	}
	return response.Fail(c, http.StatusNotFound, response.ErrAISessionNotFound, "no such session")
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
