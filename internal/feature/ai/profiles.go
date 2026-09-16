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
	if err := h.ensureProfileRoot(acct, req.Tool); err != nil {
		slog.Error("ai could not prepare a profile root", "component", "ai", "account", acct.Name, "tool", req.Tool, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDirError, "could not prepare the profile directory")
	}
	// os.Mkdir, not a stat followed by a create: the refusal and the creation
	// have to be one step, and EEXIST covers an entry of that name which is
	// not a directory at all. The panel never adopts a directory whose
	// contents it did not make.
	if err := os.Mkdir(dir, 0o700); err != nil {
		if os.IsExist(err) {
			return response.Fail(c, http.StatusConflict, response.ErrAIProfileExists, "a profile of that name already exists")
		}
		slog.Error("ai could not create a profile", "component", "ai", "account", acct.Name, "tool", req.Tool, "name", req.Name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDirError, "could not create the profile directory")
	}
	if err := h.ownProfileDir(acct, dir); err != nil {
		// An empty directory nobody can use is worse than none: leaving it
		// would answer the retry with AI_PROFILE_EXISTS. os.Remove, not
		// RemoveAll — it was created one statement ago and is empty.
		_ = os.Remove(dir)
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
	// os.Lstat, not os.Stat: the leaf has to be a real directory. A symlink
	// is refused rather than followed, because with Stat a link pointing
	// anywhere would decide what the panel believes it is deleting.
	info, err := os.Lstat(dir)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "no profile directory of that name")
	}
	if !info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "the profile path is not a directory; a symlink is never followed")
	}
	// The string handed to an unbounded recursive delete is the string that
	// has to have been checked, so it is checked here, at the call site.
	if root := profileRoot(acct, tool); dir == root || filepath.Dir(dir) != root {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, "the profile path is outside the panel's profile directory")
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Error("ai could not remove a profile", "component", "ai", "account", acct.Name, "tool", tool, "name", name, "err", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDeleteError, "could not remove the profile directory")
	}
	slog.Info("ai profile deleted", "component", "ai", "account", acct.Name, "tool", tool, "name", name)
	return response.OK(c, map[string]string{"deleted": name, "tool": tool, "account": acct.Name})
}

// ensureProfileRoot creates <home>/.sfpanel-ai/<tool> and hands both levels
// to the account. ensureSocketDir chowns only its leaf because the levels
// above it there are 0711 root and anyone can traverse them; these two are
// 0700 inside the account's own home, so an account that does not own them
// cannot reach — or log in to — its own profile.
func (h *Handler) ensureProfileRoot(acct Account, tool string) error {
	// Never create the home directory itself: under a root panel MkdirAll
	// would make it root-owned, which is a mess for the account that needs it
	// and is not this handler's business to fix.
	info, err := os.Stat(acct.Home)
	if err != nil {
		return fmt.Errorf("ai: account home %q: %w", acct.Home, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ai: account home %q is not a directory", acct.Home)
	}
	root := profileRoot(acct, tool)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(root), root} {
		if err := h.ownProfileDir(acct, d); err != nil {
			return err
		}
	}
	return nil
}

// ownProfileDir pins 0700 (MkdirAll and Mkdir both apply the umask) and, when
// the panel is root, hands the directory to the account that will write
// credentials into it — the same sequence ensureSocketDir uses.
func (h *Handler) ownProfileDir(acct Account, dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if !h.isRoot() {
		return nil
	}
	return h.chown(dir, acct.UID, acct.GID)
}
