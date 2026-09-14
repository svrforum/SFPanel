package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// call runs a handler with a JSON body and optional :id path param.
func call(t *testing.T, fn echo.HandlerFunc, method, body, id string, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/ai/sessions"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if id != "" {
		c.SetParamNames("id")
		c.SetParamValues(id)
	}
	if err := fn(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

func failCode(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var env response.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("bad envelope %q: %v", rec.Body.String(), err)
	}
	if env.Success || env.Error == nil {
		t.Fatalf("expected a failure envelope, got %s", rec.Body.String())
	}
	return env.Error.Code, env.Error.Message
}

// tmuxMock answers "tmux exists, systemd-run exists, list-windows says X".
// The key is "env": every tmux invocation runs behind the account's explicit
// environment (accounts.go), so that is the command the Commander sees.
func tmuxMock(listWindows string) *exec.MockCommander {
	return &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:tmux": {}, "exists:systemd-run": {}, "infocmp": {},
		"env": {Output: listWindows}, "systemd-run": {},
	}}
}

func TestCreateSession_RefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	_ = os.WriteFile(file, nil, 0o600)
	cases := []struct {
		name, body, code, msg string
	}{
		{"unknown tool", `{"tool":"vim","cwd":"` + dir + `"}`, response.ErrInvalidTool, ""},
		{"nologin account", `{"tool":"claude","cwd":"` + dir + `","run_as":"bob"}`, response.ErrInvalidAccount, ""},
		{"relative cwd", `{"tool":"claude","cwd":"stacks/app"}`, response.ErrInvalidPath, "relative"},
		{"missing cwd", `{"tool":"claude","cwd":"` + dir + `/nope"}`, response.ErrInvalidPath, "missing"},
		{"cwd is a file", `{"tool":"claude","cwd":"` + file + `"}`, response.ErrInvalidPath, "not a directory"},
	}
	for _, tc := range cases {
		h := newTestHandler(t, tmuxMock(""))
		h.DB = openTestDB(t)
		rec := call(t, h.CreateSession, http.MethodPost, tc.body, "", "")
		code, msg := failCode(t, rec)
		if code != tc.code || !strings.Contains(msg, tc.msg) {
			t.Errorf("%s: got %s %q, want %s containing %q", tc.name, code, msg, tc.code, tc.msg)
		}
		if n := len(h.Cmd.(*exec.MockCommander).Calls); n != 0 {
			t.Errorf("%s: refused input must never reach tmux, but %d commands ran", tc.name, n)
		}
	}
}

func TestCreateSession_RefusesWithoutTmux(t *testing.T) {
	m := tmuxMock("")
	delete(m.Outputs, "exists:tmux")
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	rec := call(t, h.CreateSession, http.MethodPost, `{"tool":"shell","cwd":"`+t.TempDir()+`"}`, "", "")
	if code, _ := failCode(t, rec); code != response.ErrTmuxMissing || rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %s/%d, want TMUX_MISSING/503", code, rec.Code)
	}
}

func TestCreateSession_SpawnsUnderScopeAndStoresRow(t *testing.T) {
	m := tmuxMock("")
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	dir := t.TempDir()
	rec := call(t, h.CreateSession, http.MethodPost, `{"tool":"claude","cwd":"`+dir+`","run_as":"alice"}`, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data Session `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	s := env.Data
	if !validSessionID(s.ID) || s.Tool != "claude" || s.RunAs != "alice" || s.Title != "Claude · "+filepath.Base(dir) || s.State != StateWorking || s.Persistence != "scope" {
		t.Errorf("created = %+v", s)
	}
	alice, _ := h.resolveAccount("alice")
	want := "--unit=sfpanel-ai-" + s.ID + " -- env " + envPrefix(h, alice) + " runuser -u alice -- tmux -f /dev/null -S " + h.socketPath(alice)
	var spawned bool
	for _, c := range m.Calls {
		if c.Name == "systemd-run" && strings.Contains(strings.Join(c.Args, " "), want) {
			spawned = true
		}
	}
	if !spawned {
		t.Errorf("no scope spawn for alice in %+v", m.Calls)
	}
	if _, ok, _ := getSessionRow(h.DB, s.ID); !ok {
		t.Error("row not stored")
	}
}

func TestCreateSession_Limit(t *testing.T) {
	var lines []string
	h := newTestHandler(t, nil)
	h.DB = openTestDB(t)
	for i := 0; i < maxSessions; i++ {
		id := strings.Repeat(string(rune('a'+i%6)), 11) + string(rune('0'+i%10))
		_ = insertSession(h.DB, sessionRow{ID: id, Tool: ToolShell, Title: id, RunAs: "root", CWD: "/"})
		lines = append(lines, id+"\t1\tbash\t0\t1789348400\t0")
	}
	h.Cmd = tmuxMock(strings.Join(lines, "\n"))
	rec := call(t, h.CreateSession, http.MethodPost, `{"tool":"shell","cwd":"/"}`, "", "")
	if code, _ := failCode(t, rec); code != response.ErrAISessionLimit {
		t.Errorf("21st live session: got %s, want AI_SESSION_LIMIT", code)
	}
}

func TestListSessions_StatesAndUnknown(t *testing.T) {
	h := newTestHandler(t, nil)
	h.DB = openTestDB(t)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "root", CWD: "/"})
	_ = insertSession(h.DB, sessionRow{ID: "bbbbbbbbbbbb", Tool: ToolCodex, Title: "b", RunAs: "root", CWD: "/"})
	// a: claude idle 6 s → waiting; b: gone → ended; c: live without a row → unknown; work: not a panel id → ignored
	h.Cmd = tmuxMock("aaaaaaaaaaaa\t1\tclaude\t1\t1789348394\t0\ncccccccccccc\t1\tbash\t0\t1789348400\t0\nwork\t1\tbash\t0\t1789348400\t0")
	h.now = func() time.Time { return time.Unix(1789348400, 0) }
	rec := call(t, h.ListSessions, http.MethodGet, "", "", "")
	var env struct {
		Data []Session `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	got := map[string]Session{}
	for _, s := range env.Data {
		got[s.ID] = s
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions %v, want a, b, c", len(got), got)
	}
	if got["aaaaaaaaaaaa"].State != StateWaiting || !got["aaaaaaaaaaaa"].Attached {
		t.Errorf("a = %+v", got["aaaaaaaaaaaa"])
	}
	if got["bbbbbbbbbbbb"].State != StateEnded || got["bbbbbbbbbbbb"].EndedAt == "" {
		t.Errorf("b = %+v", got["bbbbbbbbbbbb"])
	}
	if !got["cccccccccccc"].Unknown || got["cccccccccccc"].Title != "cccccccccccc" {
		t.Errorf("c = %+v", got["cccccccccccc"])
	}
	if r, _, _ := getSessionRow(h.DB, "bbbbbbbbbbbb"); !r.EndedAt.Valid {
		t.Error("an ended session must be stamped in the table")
	}
}

// Creation order has to be total. An unknown session has no row and therefore
// no created_at, so comparing that field alone let the unknowns land wherever
// the socket happened to list them — a different tab order on every 5 s poll.
func TestListSessions_OrderIsTotalWithUnknownsLast(t *testing.T) {
	h := newTestHandler(t, nil)
	h.DB = openTestDB(t)
	for _, id := range []string{"ffffffffffff", "eeeeeeeeeeee"} {
		_ = insertSession(h.DB, sessionRow{ID: id, Tool: ToolShell, Title: id, RunAs: "root", CWD: "/"})
	}
	// e was created first, f second, whatever their ids say.
	if _, err := h.DB.Exec(`UPDATE ai_sessions SET created_at = '2026-09-13 00:00:00' WHERE id = 'eeeeeeeeeeee'`); err != nil {
		t.Fatal(err)
	}
	h.Cmd = tmuxMock(strings.Join([]string{
		"ffffffffffff\t1\tbash\t0\t1789348400\t0",
		"eeeeeeeeeeee\t1\tbash\t0\t1789348400\t0",
		"bbbbbbbbbbbb\t1\tbash\t0\t1789348400\t0",
		"aaaaaaaaaaaa\t1\tbash\t0\t1789348400\t0",
	}, "\n"))
	want := "eeeeeeeeeeee ffffffffffff aaaaaaaaaaaa bbbbbbbbbbbb"
	for i := 0; i < 20; i++ { // the live map is iterated in random order
		snap, err := h.sessionsSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(snap))
		for _, s := range snap {
			ids = append(ids, s.ID)
		}
		if strings.Join(ids, " ") != want {
			t.Fatalf("poll %d: order = %v, want %q (rows in creation order, unknowns last by id)", i, ids, want)
		}
	}
}

func TestRerun_OnlyFromShell(t *testing.T) {
	h := newTestHandler(t, tmuxMock("aaaaaaaaaaaa\t1\tclaude\t0\t1789348400\t0"))
	h.DB = openTestDB(t)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "root", CWD: "/"})
	rec := call(t, h.RerunSession, http.MethodPost, "", "aaaaaaaaaaaa", "")
	if code, _ := failCode(t, rec); code != response.ErrAISessionState {
		t.Errorf("rerun while working: got %s, want AI_SESSION_STATE", code)
	}

	m := tmuxMock("aaaaaaaaaaaa\t1\tbash\t0\t1789348400\t0")
	h.Cmd = m
	rec = call(t, h.RerunSession, http.MethodPost, "", "aaaaaaaaaaaa", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rerun from shell: %d %s", rec.Code, rec.Body.String())
	}
	last := m.Calls[len(m.Calls)-1]
	want := envPrefix(h, h.panel) + " tmux -f /dev/null -S " + h.socketPath(h.panel) + " send-keys -t aaaaaaaaaaaa claude Enter"
	if last.Name != "env" || strings.Join(last.Args, " ") != want {
		t.Errorf("send-keys = %s %q\nwant env %q", last.Name, last.Args, want)
	}
}

func TestRestart_OnlyWhenEnded(t *testing.T) {
	h := newTestHandler(t, tmuxMock("aaaaaaaaaaaa\t1\tbash\t0\t1789348400\t0"))
	h.DB = openTestDB(t)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "root", CWD: "/"})
	rec := call(t, h.RestartSession, http.MethodPost, "", "aaaaaaaaaaaa", "")
	if code, _ := failCode(t, rec); code != response.ErrAISessionState {
		t.Errorf("restart while live: got %s, want AI_SESSION_STATE", code)
	}

	m := tmuxMock("")
	h.Cmd = m
	_ = setSessionEnded(h.DB, "aaaaaaaaaaaa", true)
	rec = call(t, h.RestartSession, http.MethodPost, "", "aaaaaaaaaaaa", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body.String())
	}
	if m.Calls[len(m.Calls)-1].Name != "systemd-run" {
		t.Errorf("restart must spawn again, last call %+v", m.Calls[len(m.Calls)-1])
	}
	if r, _, _ := getSessionRow(h.DB, "aaaaaaaaaaaa"); r.EndedAt.Valid {
		t.Error("ended_at must be cleared by a restart")
	}
}

// A restart adds a live session, so it meets the same ceiling a create does.
// Without the check, twenty ended tabs were twenty free sessions.
func TestRestart_CountsAgainstTheSessionLimit(t *testing.T) {
	h := newTestHandler(t, nil)
	h.DB = openTestDB(t)
	var lines []string
	for i := 0; i < maxSessions; i++ {
		id := strings.Repeat(string(rune('a'+i%6)), 11) + string(rune('0'+i%10))
		_ = insertSession(h.DB, sessionRow{ID: id, Tool: ToolShell, Title: id, RunAs: "root", CWD: "/"})
		lines = append(lines, id+"\t1\tbash\t0\t1789348400\t0")
	}
	_ = insertSession(h.DB, sessionRow{ID: "fedcba987654", Tool: ToolClaude, Title: "ended", RunAs: "root", CWD: "/"})
	_ = setSessionEnded(h.DB, "fedcba987654", true)
	m := tmuxMock(strings.Join(lines, "\n"))
	h.Cmd = m

	rec := call(t, h.RestartSession, http.MethodPost, "", "fedcba987654", "")
	if code, _ := failCode(t, rec); code != response.ErrAISessionLimit {
		t.Errorf("restart at the ceiling: got %s, want AI_SESSION_LIMIT", code)
	}
	for _, c := range m.Calls {
		if c.Name == "systemd-run" {
			t.Errorf("a refused restart must not spawn: %+v", c)
		}
	}
}

// Spec §1: a live session with no row "can still be attached or killed". The
// tab bar offers 종료 for it, so the row lookup's 404 left the operator a tab
// they could not close and a tmux session they could not reach.
func TestDelete_LiveSessionWithoutARow(t *testing.T) {
	m := tmuxMock("cccccccccccc\t1\tclaude\t0\t1789348400\t0")
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)

	rec := call(t, h.DeleteSession, http.MethodDelete, "", "cccccccccccc", "")
	if rec.Code != http.StatusOK {
		code, msg := failCode(t, rec)
		t.Fatalf("row-less live session: got %s %q, want 200", code, msg)
	}
	var killed bool
	for _, c := range m.Calls {
		if strings.HasSuffix(strings.Join(c.Args, " "), "kill-session -t cccccccccccc") {
			killed = true
		}
	}
	if !killed {
		t.Errorf("the tmux session must be killed; calls %+v", m.Calls)
	}

	// An id that is neither in the table nor on any socket is still a 404.
	rec = call(t, h.DeleteSession, http.MethodDelete, "", "dddddddddddd", "")
	if code, _ := failCode(t, rec); code != response.ErrAISessionNotFound || rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: got %s/%d, want AI_SESSION_NOT_FOUND/404", code, rec.Code)
	}
}

func TestRenameAndDelete(t *testing.T) {
	m := tmuxMock("aaaaaaaaaaaa\t1\tbash\t0\t1789348400\t0")
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolShell, Title: "a", RunAs: "root", CWD: "/"})

	rec := call(t, h.RenameSession, http.MethodPatch, `{"title":"  "}`, "aaaaaaaaaaaa", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidBody {
		t.Errorf("blank title: got %s, want INVALID_BODY", code)
	}
	rec = call(t, h.RenameSession, http.MethodPatch, `{"title":"my work"}`, "zzzzzzzzzzzz", "")
	if code, _ := failCode(t, rec); code != response.ErrAISessionNotFound {
		t.Errorf("unknown id: got %s, want AI_SESSION_NOT_FOUND", code)
	}
	rec = call(t, h.RenameSession, http.MethodPatch, `{"title":"my work"}`, "aaaaaaaaaaaa", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %s", rec.Body.String())
	}

	rec = call(t, h.DeleteSession, http.MethodDelete, "", "aaaaaaaaaaaa", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %s", rec.Body.String())
	}
	var killed bool
	for _, c := range m.Calls {
		if strings.HasSuffix(strings.Join(c.Args, " "), "tmux -f /dev/null -S "+h.socketPath(h.panel)+" kill-session -t aaaaaaaaaaaa") {
			killed = true
		}
	}
	if !killed {
		t.Errorf("a live session must be killed on delete; calls %+v", m.Calls)
	}
	if _, ok, _ := getSessionRow(h.DB, "aaaaaaaaaaaa"); ok {
		t.Error("row must be gone")
	}
}

// A row whose run_as has left the allowlist (removed, shell set to nologin,
// or the panel no longer root) must still be deletable: the tmux session it
// names is gone by definition, and the operator needs the tab closable.
// Rerun and restart still refuse, because they spawn as that account.
func TestDelete_AccountOffTheAllowlist(t *testing.T) {
	m := tmuxMock("")
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	// bob is nologin in the passwd fixture, so resolveAccount("bob") fails.
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "bob", CWD: "/"})
	_ = insertSession(h.DB, sessionRow{ID: "bbbbbbbbbbbb", Tool: ToolClaude, Title: "b", RunAs: "bob", CWD: "/"})

	rec := call(t, h.DeleteSession, http.MethodDelete, "", "aaaaaaaaaaaa", "")
	if rec.Code != http.StatusOK {
		code, msg := failCode(t, rec)
		t.Fatalf("delete with an off-allowlist account: got %s %q, want 200", code, msg)
	}
	if _, ok, _ := getSessionRow(h.DB, "aaaaaaaaaaaa"); ok {
		t.Error("row must be gone even when its account cannot be resolved")
	}
	for _, c := range m.Calls {
		if strings.Contains(strings.Join(c.Args, " "), "kill-session") {
			t.Errorf("no account to run as, yet tmux ran: %+v", c)
		}
	}

	rec = call(t, h.RerunSession, http.MethodPost, "", "bbbbbbbbbbbb", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("rerun needs the account: got %s, want INVALID_ACCOUNT", code)
	}
	rec = call(t, h.RestartSession, http.MethodPost, "", "bbbbbbbbbbbb", "")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("restart needs the account: got %s, want INVALID_ACCOUNT", code)
	}
}

func TestDirs(t *testing.T) {
	h := newTestHandler(t, tmuxMock(""))
	h.DB = openTestDB(t)
	h.StacksPath = t.TempDir()
	_ = os.Mkdir(filepath.Join(h.StacksPath, "app1"), 0o755)
	_ = os.WriteFile(filepath.Join(h.StacksPath, "notes.txt"), nil, 0o600)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolShell, Title: "a", RunAs: "root", CWD: "/srv"})
	rec := call(t, h.Dirs, http.MethodGet, "", "", "?user=dave")
	var env struct {
		Data struct {
			Recent []string `json:"recent"`
			Stacks []string `json:"stacks"`
			Home   string   `json:"home"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Home != "/home/dave" || len(env.Data.Stacks) != 1 || env.Data.Stacks[0] != filepath.Join(h.StacksPath, "app1") || len(env.Data.Recent) != 1 || env.Data.Recent[0] != "/srv" {
		t.Errorf("dirs = %+v", env.Data)
	}
	rec = call(t, h.Dirs, http.MethodGet, "", "", "?user=bob")
	if code, _ := failCode(t, rec); code != response.ErrInvalidAccount {
		t.Errorf("bob: got %s, want INVALID_ACCOUNT", code)
	}
}
