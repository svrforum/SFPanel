package ai

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// profileEnv is the variable each tool reads for its configuration and
// credential directory. A profile is one such directory, so selecting a
// profile is setting this variable — verified on the reference host: with
// CODEX_HOME or CLAUDE_CONFIG_DIR pointed at an empty directory, both CLIs
// report "not logged in" while the account's default directory keeps its
// login. A tool absent from this map does not support profiles: Gemini has
// no override the project has verified, and a shell session runs no tool.
var profileEnv = map[string]string{
	ToolClaude: "CLAUDE_CONFIG_DIR",
	ToolCodex:  "CODEX_HOME",
}

// profileLoginFile is what the tool writes into that directory once it has
// been logged in. Existence is a hint for the UI, never a gate — an expired
// token leaves the file behind, exactly as loginFiles already documents.
var profileLoginFile = map[string]string{
	ToolClaude: ".credentials.json",
	ToolCodex:  "auth.json",
}

// profileRootName is the directory the panel owns inside an account's home.
// Under the account's home rather than /tmp because Codex refuses to create
// its helper binaries in a temporary directory.
const profileRootName = ".sfpanel-ai"

func toolSupportsProfiles(tool string) bool {
	_, ok := profileEnv[tool]
	return ok
}

// profileNameRe is deliberately narrow: the name becomes a directory name, so
// it may not start with a dot (no hidden directories, no "." or ".."), may
// not contain a separator, and is capped so a path cannot be pushed past a
// filesystem limit.
var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

func validProfileName(name string) bool {
	return profileNameRe.MatchString(name) && !strings.Contains(name, "..")
}

// profileRoot is where an account's profiles for one tool live.
func profileRoot(acct Account, tool string) string {
	return filepath.Join(acct.Home, profileRootName, tool)
}

var errProfileName = errors.New("ai: invalid profile name")

// profileDir resolves one profile's directory. It validates the name, joins
// it, and then re-checks the joined path against the expected prefix: the
// name alone is not the thing that reaches the filesystem, and this module's
// own history (and the file manager's) says to check the result.
func profileDir(acct Account, tool, name string) (string, error) {
	if !toolSupportsProfiles(tool) {
		return "", fmt.Errorf("ai: %s has no profile support", tool)
	}
	if !filepath.IsAbs(acct.Home) {
		return "", fmt.Errorf("ai: account home %q is not absolute", acct.Home)
	}
	if !validProfileName(name) {
		return "", errProfileName
	}
	root := profileRoot(acct, tool)
	dir := filepath.Clean(filepath.Join(root, name))
	if filepath.Dir(dir) != root {
		return "", errProfileName
	}
	return dir, nil
}

// Profile is one selectable configuration directory. The default profile is
// the tool's own directory — the panel neither creates nor deletes it — and
// every other one is a directory under profileRoot.
type Profile struct {
	Name       string `json:"name"`
	Default    bool   `json:"default"`
	Path       string `json:"path"`
	LoggedIn   bool   `json:"logged_in"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// profileDefaultDir is where a session with no profile keeps its
// configuration: the tool's own directory. Derived from loginFiles so the
// directory reported and the file it is checked for cannot drift apart.
func profileDefaultDir(acct Account, tool string) string {
	rel, ok := loginFiles[tool]
	if !ok {
		return ""
	}
	return filepath.Join(acct.Home, filepath.Dir(rel))
}

// profileLoggedIn is the presence of the tool's credential file inside one
// profile directory (spec §2). Existence only: the file is never opened,
// read, parsed or copied, here or anywhere else in this module.
func profileLoggedIn(dir, tool string) bool {
	name, ok := profileLoginFile[tool]
	if !ok || dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// profileList is the default profile first, then every directory under
// profileRoot in name order. Anything that is not a directory is skipped —
// a symlink included, because os.ReadDir reports the link itself, which is
// how a planted link never reaches the picker — and so is any name the
// module would refuse to create.
func (h *Handler) profileList(acct Account, tool string) ([]Profile, error) {
	lastUsed, err := profileLastUsed(h.DB, acct.Name, tool)
	if err != nil {
		return nil, err
	}
	def := profileDefaultDir(acct, tool)
	out := []Profile{{Default: true, Path: def, LoggedIn: profileLoggedIn(def, tool), LastUsedAt: lastUsed[""]}}
	entries, err := os.ReadDir(profileRoot(acct, tool))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil // this account has no profile for this tool yet
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !validProfileName(e.Name()) {
			continue
		}
		dir, err := profileDir(acct, tool, e.Name())
		if err != nil {
			continue
		}
		out = append(out, Profile{Name: e.Name(), Path: dir,
			LoggedIn: profileLoggedIn(dir, tool), LastUsedAt: lastUsed[e.Name()]})
	}
	return out, nil
}

// liveSessionsOnProfile counts the sessions still running on one (account,
// tool, profile) triple. A delete has to refuse over it: removing the
// directory under a running CLI takes its credentials away mid-session.
func liveSessionsOnProfile(snapshot []Session, runAs, tool, profile string) int {
	n := 0
	for _, s := range snapshot {
		if s.State != StateEnded && s.RunAs == runAs && s.Tool == tool && s.Profile == profile {
			n++
		}
	}
	return n
}

// profileNameHint is the refusal message: it names the shape a name may take
// rather than leaving the operator to guess which character was the problem.
const profileNameHint = "name must be 1-32 characters of letters, digits, dot, dash or underscore, starting with a letter or digit"

// profileAccountTool resolves what every profile route needs, in one fixed
// order: the account first (an unlisted one is ErrInvalidAccount), then the
// tool (one without profile support is ErrInvalidTool, never an empty list —
// the UI must not render a picker that cannot work). ok=false means the
// response is already written.
func (h *Handler) profileAccountTool(c echo.Context, user, tool string) (Account, bool) {
	acct, ok := h.resolveAccount(user)
	if !ok {
		_ = response.Fail(c, http.StatusBadRequest, response.ErrInvalidAccount, "user is not a login account on this node")
		return Account{}, false
	}
	if !toolSupportsProfiles(tool) {
		_ = response.Fail(c, http.StatusBadRequest, response.ErrInvalidTool, "tool must be claude or codex; the others have no profile support")
		return Account{}, false
	}
	return acct, true
}

// profileLeaf is profileDir with the refusal written for the client.
// ok=false means the response is already written.
func (h *Handler) profileLeaf(c echo.Context, acct Account, tool, name string) (string, bool) {
	dir, err := profileDir(acct, tool, name)
	if err != nil {
		if errors.Is(err, errProfileName) {
			_ = response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, profileNameHint)
			return "", false
		}
		// profileAccountTool has already checked the tool and the account's
		// home comes from passwd, so what is left is a passwd whose home
		// field is relative.
		slog.Error("ai could not resolve a profile directory", "component", "ai", "account", acct.Name, "tool", tool, "err", err)
		_ = response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not resolve the profile directory")
		return "", false
	}
	return dir, true
}

// Profiles — GET /ai/profiles?user=&tool=
func (h *Handler) Profiles(c echo.Context) error {
	tool := c.QueryParam("tool")
	acct, ok := h.profileAccountTool(c, c.QueryParam("user"), tool)
	if !ok {
		return nil
	}
	list, err := h.profileList(acct, tool)
	if err != nil {
		slog.Error("ai could not list profiles", "component", "ai", "account", acct.Name, "tool", tool, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list the profiles")
	}
	return response.OK(c, map[string]any{"tool": tool, "account": acct.Name, "profiles": list})
}

type createProfileReq struct {
	User string `json:"user"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// CreateProfile — POST /ai/profiles {user, tool, name}. It creates one empty
// directory and nothing else: the tool writes its own credentials there on
// its first login, and the panel never copies another profile's in.
func (h *Handler) CreateProfile(c echo.Context) error {
	var req createProfileReq
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	// The dialog sends the account in the body (spec §3); the query parameter
	// is read too, so all three routes accept the same spelling.
	user := req.User
	if user == "" {
		user = c.QueryParam("user")
	}
	acct, ok := h.profileAccountTool(c, user, req.Tool)
	if !ok {
		return nil
	}
	dir, ok := h.profileLeaf(c, acct, req.Tool, req.Name)
	if !ok {
		return nil
	}
	toolDir, err := h.openProfileTool(acct, req.Tool, true)
	if err != nil {
		return h.failProfileWalk(c, acct, req.Tool, err)
	}
	defer toolDir.Close()
	// Mkdir, not a stat followed by a create: the refusal and the creation
	// have to be one step, and EEXIST covers an entry of that name which is
	// not a directory at all — a planted symlink, dangling or not, included.
	// The panel never adopts a directory whose contents it did not make.
	// Root-relative, so the name lands under the descriptor openProfileTool
	// verified and no string join decides where it goes.
	if err := toolDir.Mkdir(req.Name, 0o700); err != nil {
		if os.IsExist(err) {
			return response.Fail(c, http.StatusConflict, response.ErrAIProfileExists, "a profile of that name already exists")
		}
		slog.Error("ai could not create a profile", "component", "ai", "account", acct.Name, "tool", req.Tool, "name", req.Name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDirError, "could not create the profile directory")
	}
	if err := h.ownProfileDir(toolDir, acct, req.Name); err != nil {
		// An empty directory nobody can use is worse than none: leaving it
		// would answer the retry with AI_PROFILE_EXISTS. Remove, not
		// RemoveAll — it was created one statement ago and is empty.
		_ = toolDir.Remove(req.Name)
		slog.Error("ai could not hand a profile to its account", "component", "ai", "account", acct.Name, "tool", req.Tool, "name", req.Name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDirError, "could not set the profile directory owner")
	}
	slog.Info("ai profile created", "component", "ai", "account", acct.Name, "tool", req.Tool, "name", req.Name)
	return response.OK(c, Profile{Name: req.Name, Path: dir})
}

// DeleteProfile — DELETE /ai/profiles/:tool/:name?user=
func (h *Handler) DeleteProfile(c echo.Context) error {
	tool := c.Param("tool")
	acct, ok := h.profileAccountTool(c, c.QueryParam("user"), tool)
	if !ok {
		return nil
	}
	name := c.Param("name")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody,
			"the default profile is the tool's own directory and is not the panel's to delete")
	}
	dir, ok := h.profileLeaf(c, acct, tool, name)
	if !ok {
		return nil
	}
	snap, err := h.sessionsSnapshot()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "could not list sessions")
	}
	if n := liveSessionsOnProfile(snap, acct.Name, tool, name); n > 0 {
		return response.Fail(c, http.StatusConflict, response.ErrAIProfileInUse,
			fmt.Sprintf("%d live session(s) still use this profile", n))
	}
	// The walk refuses a symlink at either level above the leaf; without it
	// os.Lstat below would report the leaf honestly while every component
	// above it had already been followed, and the delete would land wherever
	// the link pointed.
	toolDir, err := h.openProfileTool(acct, tool, false)
	if err != nil {
		return h.failProfileWalk(c, acct, tool, err)
	}
	defer toolDir.Close()
	// Lstat, not Stat: the leaf has to be a real directory too. A symlink is
	// refused rather than followed, because with Stat a link pointing
	// anywhere would decide what the panel believes it is deleting.
	info, err := toolDir.Lstat(name)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "no profile directory of that name")
	}
	if !info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "the profile path is not a directory; a symlink is never followed")
	}
	// The argument handed to an unbounded recursive delete is re-checked at
	// the call site, not two calls earlier: the name must still be one
	// element the module would itself create, and it resolves under the
	// verified descriptor rather than through a joined string.
	if !validProfileName(name) || dir != filepath.Join(profileRoot(acct, tool), name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "the profile path is outside the panel's profile directory")
	}
	if err := toolDir.RemoveAll(name); err != nil {
		slog.Error("ai could not remove a profile", "component", "ai", "account", acct.Name, "tool", tool, "name", name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDeleteError, "could not remove the profile directory")
	}
	slog.Info("ai profile deleted", "component", "ai", "account", acct.Name, "tool", tool, "name", name)
	return response.OK(c, map[string]string{"deleted": name, "tool": tool, "account": acct.Name})
}

// errProfilePath is a level of the profile path that is not the real
// directory the panel expects — a symlink, or an entry of another type — and
// errProfileMissing is a level that is not there at all on a read path.
var (
	errProfilePath    = errors.New("ai: a level of the profile path is not a directory")
	errProfileMissing = errors.New("ai: no such profile directory")
)

// openProfileTool opens <home>/.sfpanel-ai/<tool> as an os.Root, refusing to
// walk anything that is not a real directory on the way down. create=true
// makes the two levels (the read paths take create=false, where a missing
// level is errProfileMissing rather than a directory the caller gets to make).
//
// Two defences, both load-bearing, and neither is the leaf check callers do
// afterwards:
//
//   - The Root anchors every operation below on a descriptor for the
//     account's home, so a level swapped underneath us cannot redirect a
//     Mkdir, a Chmod, a chown or a RemoveAll out of that home. os.MkdirAll,
//     os.Chmod and os.Chown each follow a symlink component without comment,
//     and under a root panel every regular login account is selectable
//     (allowedAccounts) while its home is its own to rearrange — the same
//     hostile boundary accountEnv already draws.
//   - The Lstat at each level refuses a link that was on disk before the
//     operator acted, which the Root alone would follow as long as it pointed
//     back inside the home. This is the chain-of-lstat defence
//     internal/feature/files/archive.go applies, for the same reason.
//
// The home directory itself is never created: os.OpenRoot makes nothing,
// which is the point — a MkdirAll here would leave somebody's home root-owned.
func (h *Handler) openProfileTool(acct Account, tool string, create bool) (*os.Root, error) {
	if !toolSupportsProfiles(tool) {
		return nil, fmt.Errorf("ai: %s has no profile support", tool)
	}
	if !filepath.IsAbs(acct.Home) {
		return nil, fmt.Errorf("ai: account home %q is not absolute", acct.Home)
	}
	cur, err := os.OpenRoot(acct.Home)
	if err != nil {
		return nil, fmt.Errorf("ai: account home %q: %w", acct.Home, err)
	}
	for _, level := range []string{profileRootName, tool} {
		next, err := h.descendProfileLevel(cur, acct, level, create)
		_ = cur.Close() // the descriptor we came from; next holds its own
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// descendProfileLevel is one step of that walk: refuse a non-directory,
// create the level when asked, pin its mode and owner, and open it.
func (h *Handler) descendProfileLevel(parent *os.Root, acct Account, name string, create bool) (*os.Root, error) {
	full := filepath.Join(parent.Name(), name)
	info, err := parent.Lstat(name)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%w: %s is a symlink", errProfilePath, full)
	case err == nil && !info.IsDir():
		return nil, fmt.Errorf("%w: %s", errProfilePath, full)
	case os.IsNotExist(err):
		if !create {
			return nil, fmt.Errorf("%w: %s", errProfileMissing, full)
		}
		if err := parent.Mkdir(name, 0o700); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}
	// ensureSocketDir hands only its leaf to the account because the levels
	// above it there are 0711 root and anyone can traverse them. These two
	// are 0700 inside the account's own home, so an account that does not own
	// them cannot reach — or log in to — its own profile.
	if create {
		if err := h.ownProfileDir(parent, acct, name); err != nil {
			return nil, err
		}
	}
	return parent.OpenRoot(name)
}

// ownProfileDir pins 0700 (Mkdir applies the umask) and, when the panel is
// root, hands the directory to the account that will write credentials into
// it. Both calls are root-relative, and the ownership one is Lchown rather
// than os.Chown: it must land on the entry the check above saw and never on
// whatever a link put in its place afterwards.
func (h *Handler) ownProfileDir(parent *os.Root, acct Account, name string) error {
	if err := parent.Chmod(name, 0o700); err != nil {
		return err
	}
	if !h.isRoot() {
		return nil
	}
	return h.lchownAt(parent, name, acct.UID, acct.GID)
}

// failProfileWalk answers a profile path the module refuses to walk. A level
// that is a symlink or missing is the client's problem (INVALID_PATH, 400);
// anything else is the node's, and gets logged.
func (h *Handler) failProfileWalk(c echo.Context, acct Account, tool string, err error) error {
	switch {
	case errors.Is(err, errProfilePath):
		slog.Warn("ai refused to walk a profile path", "component", "ai", "account", acct.Name, "tool", tool, "err", err)
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath,
			"a level of the profile path is not a directory; a symlink is never followed")
	case errors.Is(err, errProfileMissing):
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "no profile directory of that name")
	}
	slog.Error("ai could not prepare a profile root", "component", "ai", "account", acct.Name, "tool", tool, "err", err)
	return response.Fail(c, http.StatusInternalServerError, response.ErrDirError, "could not prepare the profile directory")
}
