package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
// environment (accounts.go), so that is the command the Commander sees. Exit 0
// on that key also means serverRunning is true, i.e. a spawn takes the client
// form — use noServerMock for the first-session case.
func tmuxMock(listWindows string) *exec.MockCommander {
	return &exec.MockCommander{Outputs: map[string]exec.MockResult{
		"exists:tmux": {}, "exists:systemd-run": {}, "infocmp": {},
		"env": {Output: listWindows}, "systemd-run": {},
	}}
}

// noServerMock is a host where the account has no tmux server yet: every
// client command fails, which is exactly what list-sessions and list-windows
// do with nothing on the socket.
func noServerMock() *exec.MockCommander {
	m := tmuxMock("")
	m.Outputs["env"] = exec.MockResult{Err: errTest}
	return m
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

// A tmux below the floor produced a bare COMMAND_FAILED from the usage error
// tmuxOptions triggers. Assert the reason: TMUX_MISSING/503, both versions in
// the message so the operator knows what to upgrade from, and no spawn at all.
func TestCreateSession_RefusesTooOldTmux(t *testing.T) {
	for _, fn := range []struct {
		name string
		call func(*Handler) *httptest.ResponseRecorder
	}{
		{"create", func(h *Handler) *httptest.ResponseRecorder {
			return call(t, h.CreateSession, http.MethodPost, `{"tool":"shell","cwd":"/"}`, "", "")
		}},
		{"restart", func(h *Handler) *httptest.ResponseRecorder {
			_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "root", CWD: "/"})
			_ = setSessionEnded(h.DB, "aaaaaaaaaaaa", true)
			return call(t, h.RestartSession, http.MethodPost, "", "aaaaaaaaaaaa", "")
		}},
	} {
		m := noServerMock()
		m.Outputs["tmux"] = exec.MockResult{Output: "tmux 3.0a\n"}
		h := newTestHandler(t, m)
		h.DB = openTestDB(t)
		rec := fn.call(h)
		code, msg := failCode(t, rec)
		if code != response.ErrTmuxMissing || rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %s/%d, want TMUX_MISSING/503", fn.name, code, rec.Code)
		}
		if !strings.Contains(msg, "3.2") || !strings.Contains(msg, "3.0a") {
			t.Errorf("%s: message %q must name both the required and the found version", fn.name, msg)
		}
		for _, c := range m.Calls {
			if c.Name == "systemd-run" || c.Name == "setsid" || slices.Contains(c.Args, "new-session") {
				t.Errorf("%s: a refused create must not spawn: %+v", fn.name, c)
			}
		}
	}
}

// The first session for an account starts its tmux server as a transient
// service owned by PID 1 — the whole point of the design, because a scope
// would inherit the panel's PrivateTmp namespace and lose its /tmp on every
// restart. Assert the reason: --uid/--gid and Type=forking, and no runuser or
// env prefix, which systemd makes unnecessary.
func TestCreateSession_StartsTheServerAsAServiceAndStoresRow(t *testing.T) {
	m := noServerMock()
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
	if !validSessionID(s.ID) || s.Tool != "claude" || s.RunAs != "alice" || s.Title != "Claude · "+filepath.Base(dir) || s.State != StateWorking || s.Persistence != "service" {
		t.Errorf("created = %+v", s)
	}
	// The create response is built in Go, not read back, so its created_at
	// must already be the format the very next list will return.
	if _, err := time.Parse(time.RFC3339, s.CreatedAt); err != nil {
		t.Errorf("created_at %q is not RFC 3339: %v", s.CreatedAt, err)
	}
	alice, _ := h.resolveAccount("alice")
	want := "--unit=sfpanel-ai-1000 --collect --uid=alice --gid=1000 -p Type=forking --setenv=LANG=C.UTF-8 --setenv=COLORTERM=truecolor -- tmux -f /dev/null -S " + h.socketPath(alice)
	var spawned bool
	for _, c := range m.Calls {
		if c.Name != "systemd-run" {
			continue
		}
		joined := strings.Join(c.Args, " ")
		if !strings.HasPrefix(joined, want) {
			t.Errorf("spawn argv = %q\nwant prefix %q", joined, want)
		}
		for _, bad := range []string{"--scope", "runuser", "env", "-e"} {
			if slices.Contains(c.Args, bad) {
				t.Errorf("the service form must not carry %s: %q", bad, c.Args)
			}
		}
		spawned = true
	}
	if !spawned {
		t.Errorf("no transient-service spawn for alice in %+v", m.Calls)
	}
	if _, ok, _ := getSessionRow(h.DB, s.ID); !ok {
		t.Error("row not stored")
	}
}

// A second session for the same account must not ask for the unit again — the
// unit name is per account and systemd would refuse it. When list-sessions
// answers, the spawn is an ordinary client on the running server.
func TestCreateSession_SecondSessionUsesTheRunningServer(t *testing.T) {
	m := tmuxMock("")
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	rec := call(t, h.CreateSession, http.MethodPost, `{"tool":"claude","cwd":"`+t.TempDir()+`","run_as":"alice"}`, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	alice, _ := h.resolveAccount("alice")
	want := envPrefix(h, alice) + " runuser -u alice -- tmux -f /dev/null -S " + h.socketPath(alice)
	var spawned bool
	for _, c := range m.Calls {
		if c.Name == "systemd-run" {
			t.Errorf("the server is already up; no unit may be requested: %+v", c)
		}
		if c.Name == "env" && slices.Contains(c.Args, "new-session") {
			if joined := strings.Join(c.Args, " "); !strings.HasPrefix(joined, want) {
				t.Errorf("spawn argv = %q\nwant prefix %q", joined, want)
			}
			spawned = true
		}
	}
	if !spawned {
		t.Errorf("no client-form spawn for alice in %+v", m.Calls)
	}
}

// A panel that is not root cannot ask PID 1 to run a unit as anyone, so it
// never emits systemd-run and must say so: persistence "process" is what puts
// the warning marker on the tab.
func TestCreateSession_NonRootPanelIsProcessMode(t *testing.T) {
	m := noServerMock()
	h := newTestHandler(t, m)
	h.isRoot = func() bool { return false }
	h.DB = openTestDB(t)
	rec := call(t, h.CreateSession, http.MethodPost, `{"tool":"shell","cwd":"`+t.TempDir()+`"}`, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data Session `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Data.Persistence != "process" {
		t.Errorf("persistence = %q, want process", env.Data.Persistence)
	}
	var spawned bool
	for _, c := range m.Calls {
		if c.Name == "systemd-run" {
			t.Errorf("a non-root panel must never invoke systemd-run: %+v", c)
		}
		if c.Name == "setsid" {
			spawned = true
		}
	}
	if !spawned {
		t.Errorf("no setsid fallback spawn in %+v", m.Calls)
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
	// The freshly stamped ended_at goes out before the row is re-read, so it
	// has to be formatted the way the row will come back: RFC 3339.
	if _, err := time.Parse(time.RFC3339, got["bbbbbbbbbbbb"].EndedAt); err != nil {
		t.Errorf("ended_at %q is not RFC 3339: %v", got["bbbbbbbbbbbb"].EndedAt, err)
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

	m := noServerMock()
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
		if c.Name == "systemd-run" || c.Name == "setsid" || slices.Contains(c.Args, "new-session") {
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

// gateCommander holds the first new-session for one account inside the
// Commander until the test lets it out, and announces every later one. Every
// other call answers from the embedded mock. This is how the spawn lock is
// asserted by ordering rather than by wall-clock timing: MockCommander records
// argv but cannot block, and a "it took less than 45 s" assertion would prove
// nothing about which lock was held.
type gateCommander struct {
	*exec.MockCommander
	socket  string // whose new-session is gated: the account's socket path
	mu      sync.Mutex
	held    bool
	entered chan struct{} // closed once the gated call is inside
	release chan struct{} // close to let it out
	arrived chan struct{} // one send per later new-session on that socket
}

func newGateCommander(m *exec.MockCommander, socket string) *gateCommander {
	return &gateCommander{MockCommander: m, socket: socket,
		entered: make(chan struct{}), release: make(chan struct{}), arrived: make(chan struct{}, 4)}
}

func (g *gateCommander) RunWithTimeout(d time.Duration, name string, args ...string) (string, error) {
	out, err := g.MockCommander.RunWithTimeout(d, name, args...)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, g.socket) || !slices.Contains(args, "new-session") {
		return out, err
	}
	g.mu.Lock()
	first := !g.held
	g.held = true
	g.mu.Unlock()
	if !first {
		g.arrived <- struct{}{}
		return out, err
	}
	close(g.entered)
	<-g.release
	return out, err
}

// The spawn lock is per account. It is held across the check-then-act pair and
// up to three 15 s command runs, so a process-wide one turned a single
// account's hung systemd-run into a 45 s stall on every other account's
// creates. Both halves are asserted: while alice's spawn is held inside the
// Commander, dave's must finish, and a second one for alice must not.
func TestSpawn_LocksPerAccountNotProcessWide(t *testing.T) {
	// Exit 0 on the socket means the server is up, so each spawn is a single
	// client-form call and the gate is the only thing that can block it.
	m := tmuxMock("")
	h := newTestHandler(t, m)
	alice, _ := h.resolveAccount("alice")
	dave, _ := h.resolveAccount("dave")
	g := newGateCommander(m, h.socketPath(alice))
	h.Cmd = g

	first := make(chan error, 1)
	go func() { first <- h.spawn("0123456789ab", "/", ToolShell, alice) }()
	<-g.entered // alice's new-session is inside the Commander; her lock is held

	other := make(chan error, 1)
	go func() { other <- h.spawn("0123456789ac", "/", ToolShell, dave) }()
	select {
	case err := <-other:
		if err != nil {
			t.Fatalf("dave's spawn: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dave's spawn never finished while alice's was held: the lock is shared between accounts")
	}

	// Only the first new-session on alice's socket is gated, so a second one
	// that got past her lock would sail straight through the Commander — that
	// is what makes this fail when the per-account lock is taken out.
	second := make(chan error, 1)
	go func() { second <- h.spawn("0123456789ad", "/", ToolShell, alice) }()
	select {
	case <-g.arrived:
		t.Fatal("a second spawn for alice reached tmux while the first still held her lock")
	case err := <-second:
		t.Fatalf("a second spawn for alice finished while the first still held her lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	close(g.release)
	if err := <-first; err != nil {
		t.Fatalf("alice's first spawn: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("alice's second spawn: %v", err)
	}
	<-g.arrived // it ran, and only after the first was let go
}
