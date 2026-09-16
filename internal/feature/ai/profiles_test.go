package ai

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
)

func TestValidProfileName(t *testing.T) {
	ok := []string{"work", "client-a", "p.2", "A1", "x", strings.Repeat("a", 32)}
	bad := []string{"", ".", "..", "a/b", "../etc", ".hidden", "-lead", "a b", "a\x00b", strings.Repeat("a", 33), "work/../../root"}
	for _, n := range ok {
		if !validProfileName(n) {
			t.Errorf("validProfileName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if validProfileName(n) {
			t.Errorf("validProfileName(%q) = true, want false — it would become a directory name", n)
		}
	}
}

// profileDir must validate the name, join it, and then check the JOINED path,
// which is the lesson the file manager records: the destination only exists
// once the halves are joined.
func TestProfileDir(t *testing.T) {
	acct := Account{Name: "alice", Home: "/home/alice"}
	got, err := profileDir(acct, ToolCodex, "work")
	if err != nil || got != "/home/alice/.sfpanel-ai/codex/work" {
		t.Fatalf("got (%q, %v), want /home/alice/.sfpanel-ai/codex/work", got, err)
	}
	if _, err := profileDir(acct, ToolCodex, "../../etc"); err == nil {
		t.Error("a traversing name must be refused, not joined")
	}
	if _, err := profileDir(acct, ToolShell, "work"); err == nil {
		t.Error("a tool without profile support must be refused")
	}
	if _, err := profileDir(Account{Name: "x", Home: "relative/home"}, ToolCodex, "work"); err == nil {
		t.Error("a non-absolute account home must be refused")
	}
	if root := profileRoot(acct, ToolClaude); root != filepath.Join("/home/alice", profileRootName, "claude") {
		t.Errorf("profileRoot = %q", root)
	}
}

func TestToolSupportsProfiles(t *testing.T) {
	for _, tl := range []string{ToolClaude, ToolCodex} {
		if !toolSupportsProfiles(tl) {
			t.Errorf("%s must support profiles", tl)
		}
	}
	// Gemini has no verified config-directory override, and a shell session
	// runs no tool — neither may offer a picker that cannot work.
	for _, tl := range []string{ToolGemini, ToolShell, "vim"} {
		if toolSupportsProfiles(tl) {
			t.Errorf("%s must not claim profile support", tl)
		}
	}
}

func TestProfiles_ListsTheDefaultFirstAndReportsLogin(t *testing.T) {
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	home := t.TempDir()
	h.panel.Home = home
	// two profiles: one logged in, one not
	for _, p := range []string{"work", "fresh"} {
		if err := os.MkdirAll(filepath.Join(home, profileRootName, "codex", p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, profileRootName, "codex", "work", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the default profile is logged in too
	_ = os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolCodex, Title: "t", RunAs: "root", CWD: "/", Profile: "work"})

	rec := call(t, h.Profiles, http.MethodGet, "", "", "?tool=codex")
	var env struct {
		Data struct {
			Tool     string    `json:"tool"`
			Account  string    `json:"account"`
			Profiles []Profile `json:"profiles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	ps := env.Data.Profiles
	if len(ps) != 3 || !ps[0].Default || ps[0].Name != "" {
		t.Fatalf("profiles = %+v, want the default first", ps)
	}
	byName := map[string]Profile{}
	for _, p := range ps {
		byName[p.Name] = p
	}
	if !byName[""].LoggedIn || !byName["work"].LoggedIn || byName["fresh"].LoggedIn {
		t.Errorf("login states wrong: %+v", ps)
	}
	if byName["work"].LastUsedAt == "" || byName["fresh"].LastUsedAt != "" {
		t.Errorf("last-used wrong: %+v", ps)
	}
	if byName["work"].Path != filepath.Join(home, profileRootName, "codex", "work") {
		t.Errorf("path = %q", byName["work"].Path)
	}
}

// The listing walks the same anchored levels the delete path walks. The
// account owns its home, so ~/.sfpanel-ai/<tool> can be a symlink, and a
// listing read through one — os.ReadDir over a joined string — describes a
// directory somewhere else entirely and hands its contents to the picker as
// this account's profiles. The default profile has to be the whole answer.
func TestProfiles_DoesNotListThroughASymlinkedLevel(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "decoy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, profileRootName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, profileRootName, "codex")); err != nil {
		t.Skip("symlinks unavailable")
	}
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	h.panel.Home = home

	ps := listProfiles(t, h, "codex")
	if len(ps) != 1 || !ps[0].Default {
		t.Fatalf("profiles = %+v, want only the default: a symlinked level is never followed, so the %q behind the link must not be listed",
			ps, filepath.Join(outside, "decoy"))
	}
	// And a tool whose level is simply not there answers the same way — the
	// account has no profile for it yet, which is not an error.
	if ps := listProfiles(t, h, "claude"); len(ps) != 1 || !ps[0].Default {
		t.Errorf("profiles = %+v, want only the default for a tool with no directory yet", ps)
	}
}

// listProfiles is GET /ai/profiles for the handler's own account.
func listProfiles(t *testing.T, h *Handler, tool string) []Profile {
	t.Helper()
	rec := call(t, h.Profiles, http.MethodGet, "", "", "?tool="+tool)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %s: %d %s", tool, rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Profiles []Profile `json:"profiles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data.Profiles
}

func TestProfiles_RefusesUnsupportedToolAndUnknownAccount(t *testing.T) {
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	for _, q := range []string{"?tool=gemini", "?tool=shell", "?tool=vim", ""} {
		rec := call(t, h.Profiles, http.MethodGet, "", "", q)
		if code, _ := failCode(t, rec); code != response.ErrInvalidTool {
			t.Errorf("tool %q: got %s, want INVALID_TOOL", q, code)
		}
	}
	rec := call(t, h.Profiles, http.MethodGet, "", "", "?tool=codex&user=bob")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("bob: got %s, want INVALID_ACCOUNT", code)
	}
}

func TestCreateProfile(t *testing.T) {
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	home := t.TempDir()
	h.panel.Home = home

	rec := call(t, h.CreateProfile, http.MethodPost, `{"tool":"codex","name":"work"}`, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	dir := filepath.Join(home, profileRootName, "codex", "work")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("mode = %o, want 700 — a credential directory is not group- or world-readable", perm)
	}
	// a second create must not adopt a directory whose contents it did not make
	rec = call(t, h.CreateProfile, http.MethodPost, `{"tool":"codex","name":"work"}`, "", "")
	if code, _ := failCode(t, rec); code != response.ErrAIProfileExists {
		t.Errorf("duplicate: got %s, want AI_PROFILE_EXISTS", code)
	}
	for _, body := range []string{`{"tool":"codex","name":".."}`, `{"tool":"codex","name":"a/b"}`, `{"tool":"codex","name":""}`, `{"tool":"codex","name":".hidden"}`} {
		rec = call(t, h.CreateProfile, http.MethodPost, body, "", "")
		if code, _ := failCode(t, rec); code != response.ErrInvalidBody {
			t.Errorf("%s: got %s, want INVALID_BODY", body, code)
		}
	}
	rec = call(t, h.CreateProfile, http.MethodPost, `{"tool":"gemini","name":"x"}`, "", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidTool {
		t.Errorf("gemini: got %s, want INVALID_TOOL", code)
	}
}

func TestDeleteProfile_Guards(t *testing.T) {
	home := t.TempDir()
	mk := func(t *testing.T, listWindows string) *Handler {
		h := newTestHandler(t, tmuxMock(listWindows))
		h.DB = openTestDB(t)
		h.panel.Home = home
		return h
	}
	dir := filepath.Join(home, profileRootName, "codex", "work")
	_ = os.MkdirAll(dir, 0o700)

	// the default profile is the tool's own directory and is not ours to delete
	h := mk(t, "")
	rec := callParams(t, h.DeleteProfile, http.MethodDelete, "", map[string]string{"tool": "codex", "name": ""}, "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidBody {
		t.Errorf("default: got %s, want INVALID_BODY", code)
	}

	// a live session on that profile blocks it
	h = mk(t, "aaaaaaaaaaaa\t1\tcodex\t0\t1789348400\t0")
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolCodex, Title: "t", RunAs: "root", CWD: "/", Profile: "work"})
	rec = callParams(t, h.DeleteProfile, http.MethodDelete, "", map[string]string{"tool": "codex", "name": "work"}, "")
	if code, msg := failCode(t, rec); code != response.ErrAIProfileInUse || !strings.Contains(msg, "1") {
		t.Errorf("in use: got %s %q, want AI_PROFILE_IN_USE naming the count", code, msg)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("the directory must survive a refused delete")
	}

	// a symlink planted at the leaf is never followed
	link := filepath.Join(home, profileRootName, "codex", "sneaky")
	target := t.TempDir()
	_ = os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	h = mk(t, "")
	rec = callParams(t, h.DeleteProfile, http.MethodDelete, "", map[string]string{"tool": "codex", "name": "sneaky"}, "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidPath {
		t.Errorf("symlink: got %s, want INVALID_PATH", code)
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Error("the symlink target must be untouched")
	}

	// and the happy path removes only that tree
	h = mk(t, "")
	rec = callParams(t, h.DeleteProfile, http.MethodDelete, "", map[string]string{"tool": "codex", "name": "work"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the directory must be gone")
	}
	if _, err := os.Stat(link); err != nil {
		t.Error("the sibling must survive")
	}
}

// A symlink at a level ABOVE the leaf is refused, not walked. os.Lstat does
// not follow the final component but follows every component above it, so a
// guard on the leaf alone leaves os.RemoveAll — and a chmod, and a chown —
// pointed at whatever a level above it names. Under a root panel every
// regular login account is selectable and its home is its own to rearrange,
// which is the boundary accountEnv already treats as hostile.
func TestProfileLevels_RefuseASymlinkedLevel(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	// the tree behind the link, laid out exactly as the panel's own
	if err := os.MkdirAll(filepath.Join(outside, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "work", "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, profileRootName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, profileRootName, "codex")); err != nil {
		t.Skip("symlinks unavailable")
	}
	mk := func() *Handler {
		h := newTestHandler(t, tmuxMock(""))
		h.DB = openTestDB(t)
		h.panel.Home = home
		return h
	}

	// delete: the link is not walked, so the tree behind it lives
	h := mk()
	rec := callParams(t, h.DeleteProfile, http.MethodDelete, "", map[string]string{"tool": "codex", "name": "work"}, "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidPath {
		t.Errorf("delete through a symlinked level: got %s, want INVALID_PATH", code)
	}
	if _, err := os.Stat(filepath.Join(outside, "work", "keep")); err != nil {
		t.Errorf("the symlink target must be untouched: %v", err)
	}

	// create: the same refusal, nothing made behind the link, and no
	// ownership handed out past the level where the walk stopped
	h = mk()
	var chowned []string
	h.lchownAt = func(r *os.Root, name string, uid, gid int) error {
		chowned = append(chowned, filepath.Join(r.Name(), name))
		return nil
	}
	rec = call(t, h.CreateProfile, http.MethodPost, `{"tool":"codex","name":"fresh"}`, "", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidPath {
		t.Errorf("create through a symlinked level: got %s, want INVALID_PATH", code)
	}
	if _, err := os.Lstat(filepath.Join(outside, "fresh")); !os.IsNotExist(err) {
		t.Errorf("a directory was created behind the symlink: %v", err)
	}
	if want := []string{filepath.Join(home, profileRootName)}; len(chowned) != 1 || chowned[0] != want[0] {
		t.Errorf("chowned %v, want the walk to stop at the link: %v", chowned, want)
	}
}

// The two levels the panel owns inside the home are handed to the account —
// they are 0700, so root-owned levels would leave the account unable to
// traverse to, or log in to, its own profile. Each call is root-relative:
// the directory named is resolved under the descriptor the walk verified,
// never by joining a string a moment later.
func TestCreateProfile_HandsThePanelLevelsToTheAccount(t *testing.T) {
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	home := t.TempDir()
	h.panel.Home = home
	var chowned []string
	h.lchownAt = func(r *os.Root, name string, uid, gid int) error {
		if uid != h.panel.UID || gid != h.panel.GID {
			t.Errorf("chown %s to %d:%d, want the account %d:%d", name, uid, gid, h.panel.UID, h.panel.GID)
		}
		chowned = append(chowned, filepath.Join(r.Name(), name))
		return nil
	}
	rec := call(t, h.CreateProfile, http.MethodPost, `{"tool":"codex","name":"work"}`, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	want := []string{
		filepath.Join(home, profileRootName),
		filepath.Join(home, profileRootName, "codex"),
		filepath.Join(home, profileRootName, "codex", "work"),
	}
	if len(chowned) != len(want) {
		t.Fatalf("chowned %v, want %v", chowned, want)
	}
	for i, w := range want {
		if chowned[i] != w {
			t.Errorf("chowned[%d] = %q, want %q", i, chowned[i], w)
		}
	}
}

// profileLastUsed reads MAX(created_at), and an aggregate carries no declared
// column type: the driver hands a direct created_at read back as RFC 3339 but
// MAX(created_at) back as SQLite's own "2006-01-02 15:04:05". One field
// carrying two formats is the bug sessionsSnapshot already documents for
// ended_at, so the map normalises before the picker ever sees it.
func TestProfileLastUsed(t *testing.T) {
	db := openTestDB(t)
	for _, r := range []sessionRow{
		{ID: "aaaaaaaaaaaa", Tool: ToolCodex, Title: "t", RunAs: "root", CWD: "/", Profile: "work"},
		{ID: "bbbbbbbbbbbb", Tool: ToolCodex, Title: "t", RunAs: "root", CWD: "/", Profile: ""},
		{ID: "cccccccccccc", Tool: ToolClaude, Title: "t", RunAs: "root", CWD: "/", Profile: "elsewhere"},
		{ID: "dddddddddddd", Tool: ToolCodex, Title: "t", RunAs: "alice", CWD: "/", Profile: "someone-else"},
	} {
		if err := insertSession(db, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := profileLastUsed(db, "root", ToolCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want only root's codex profiles", got)
	}
	for name, ts := range got {
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Errorf("profile %q: last used %q is not RFC 3339 like created_at is: %v", name, ts, err)
		}
	}
}
