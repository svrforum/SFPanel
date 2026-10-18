package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// capture-pane emits LF lines with the ANSI colours -e kept; xterm needs
// CR LF or every line staircases.
func TestReplayHistory(t *testing.T) {
	got := string(replayHistory("\x1b[32m$ ls\x1b[0m\nfile\n"))
	if got != "\x1b[32m$ ls\x1b[0m\r\nfile\r\n" {
		t.Errorf("got %q", got)
	}
	if replayHistory("") != nil || replayHistory("\n") != nil {
		t.Error("empty history must replay nothing")
	}
}

// The attach client is the one invocation without an `env` prefix: it needs a
// TERM, and it gets its environment through cmd.Env (attachEnv) instead.
func TestAttachArgv(t *testing.T) {
	h := newTestHandler(t, nil)
	alice := Account{Name: "alice", UID: 1000, GID: 1000, Home: "/home/alice", Shell: "/bin/bash"}
	name, argv := h.attachArgv(alice, "aaaaaaaaaaaa")
	if name != "runuser" || strings.Join(argv, " ") != "-u alice -- tmux -f /dev/null -S "+h.socketPath(alice)+" set-option -t aaaaaaaaaaaa mouse on ; attach-session -t aaaaaaaaaaaa" {
		t.Errorf("alice: %s %q", name, argv)
	}
	name, argv = h.attachArgv(h.panel, "aaaaaaaaaaaa")
	if name != "tmux" || strings.Join(argv, " ") != "-f /dev/null -S "+h.socketPath(h.panel)+" set-option -t aaaaaaaaaaaa mouse on ; attach-session -t aaaaaaaaaaaa" {
		t.Errorf("root: %s %q", name, argv)
	}
}

// A session that vanished between the list and the click gets one text
// frame and a close — never a PTY, never a hang.
func TestAttachWS_GoneSessionSendsOneFrameAndCloses(t *testing.T) {
	// NewMockCommander, not a zero value: SetOutput writes to Outputs, which
	// the zero value leaves nil.
	m := exec.NewMockCommander()
	// Every tmux invocation but the attach client runs behind `env`.
	m.SetOutput("env", "can't find session: aaaaaaaaaaaa", errTest)
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "root", CWD: "/"})

	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS(func() string { return "test-secret" }, nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()

	tok, err := auth.GenerateToken("admin", "test-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// ?token= is accepted from loopback only — which httptest is.
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=aaaaaaaaaaaa&token=" + tok
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	mt, msg, err := ws.ReadMessage()
	if err != nil || mt != websocket.TextMessage || !strings.Contains(string(msg), "session ended") {
		t.Fatalf("first frame = %d %q %v", mt, msg, err)
	}
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Error("the server must close after the notice")
	}
	for _, c := range m.Calls {
		if strings.Contains(strings.Join(c.Args, " "), "attach-session") {
			t.Error("no attach client may be started for a gone session")
		}
	}
}

func TestAttachWS_RefusesBadIDBeforeUpgrade(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{})
	h.DB = openTestDB(t)
	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS(func() string { return "test-secret" }, nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()
	tok, _ := auth.GenerateToken("admin", "test-secret", time.Minute)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=../work&token=" + tok
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("a malformed id must be a 404 before the upgrade; got err=%v resp=%v", err, resp)
	}
	// The refusal is the module's own code, not a bare English sentence: the
	// page keys its i18n string off it, as it does for /ai/sessions/:id.
	if code, _ := dialFailCode(t, resp); code != response.ErrAISessionNotFound {
		t.Errorf("code = %s, want %s", code, response.ErrAISessionNotFound)
	}
}

// The row's account left the allowlist (bob has a nologin shell), so there is
// no account to attach as. Refused before the upgrade, with the same code the
// REST routes use for it.
func TestAttachWS_RefusesAGoneAccountBeforeUpgrade(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{})
	h.DB = openTestDB(t)
	if err := insertSession(h.DB, sessionRow{ID: "bbbbbbbbbbbb", Tool: ToolClaude, Title: "b", RunAs: "bob", CWD: "/"}); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS(func() string { return "test-secret" }, nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()
	tok, _ := auth.GenerateToken("admin", "test-secret", time.Minute)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=bbbbbbbbbbbb&token=" + tok
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil || resp == nil || resp.StatusCode != 400 {
		t.Fatalf("a vanished account must be a 400 before the upgrade; got err=%v resp=%v", err, resp)
	}
	if code, _ := dialFailCode(t, resp); code != response.ErrInvalidAccount {
		t.Errorf("code = %s, want %s", code, response.ErrInvalidAccount)
	}
}

// A read error is not a not-found. Treating both as "no row" attached the
// session as the panel account — the wrong account, silently — instead of
// saying the table could not be read.
func TestAttachWS_ADatabaseErrorIsNotAMissingRow(t *testing.T) {
	h := newTestHandler(t, &exec.MockCommander{})
	h.DB = openTestDB(t)
	h.DB.Close() // every query now errors
	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS(func() string { return "test-secret" }, nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()
	tok, _ := auth.GenerateToken("admin", "test-secret", time.Minute)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=aaaaaaaaaaaa&token=" + tok
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("an unreadable table must be a 500 before the upgrade; got err=%v resp=%v", err, resp)
	}
	if code, _ := dialFailCode(t, resp); code != response.ErrInternalError {
		t.Errorf("code = %s, want %s", code, response.ErrInternalError)
	}
}

// #134: a live session with no row was attached as the panel account, whatever
// socket it was on. The sockets are per uid, so a row-less session of alice's —
// her row lost to a restored backup, which still held an older one of hers —
// was looked for on root's socket: the attach answered [session ended] while
// the list, which found it on alice's, kept it live, and the page reconnected
// to it until it gave up. The attach and the text view take the account the
// list names and reach the session through that account's socket, as it;
// root's own row-less session still goes to root's socket, and one on no
// socket at all still ends with the notice.
func TestAttachWS_ARowlessSessionIsAttachedOnTheSocketItIsListedOn(t *testing.T) {
	h := newTestHandler(t, nil)
	h.DB = openTestDB(t)
	alice, _ := h.resolveAccount("alice")
	if err := insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolShell, Title: "a", RunAs: "alice", CWD: "/home/alice"}); err != nil {
		t.Fatal(err)
	}
	_ = setSessionEnded(h.DB, "aaaaaaaaaaaa", true)
	const aliceID, rootID = "969018a4f943", "cccccccccccc"
	m := socketsCommander(map[string]string{
		h.socketPath(h.panel): rootID + "\t1\tbash\t0\t1789348400\t0",
		h.socketPath(alice):   aliceID + "\t1\tbash\t0\t1789348400\t0",
	}, "$ ls\nnotes.txt\n")
	h.Cmd = m
	h.now = func() time.Time { return time.Unix(1789348400, 0) }

	snap, err := h.sessionsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, s := range snap {
		listed[s.ID] = s.RunAs
	}
	if listed[aliceID] != "alice" || listed[rootID] != "root" {
		t.Fatalf("list = %v, want %s as alice and %s as root", listed, aliceID, rootID)
	}

	rec := call(t, h.SessionText, http.MethodGet, "", aliceID, "")
	if rec.Code != http.StatusOK {
		code, msg := failCode(t, rec)
		t.Errorf("text of a session listed as alice's: got %s %q, want 200", code, msg)
	}
	if got := tmuxCall(h, m.Calls, "capture-pane -p -J -t "+aliceID+" -S -"+strconv.Itoa(historyLines)); got != "alice "+h.socketPath(alice) {
		t.Errorf("the text view read %q, want alice's own socket %s", got, h.socketPath(alice))
	}

	// The attach client is started with pty.Start, not through the Commander.
	// With nothing on PATH it cannot start — after the history replay, which
	// only the socket that has the session can answer, and before anything at
	// all is exec'd: no runuser, no tmux.
	t.Setenv("PATH", t.TempDir())
	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS(func() string { return "test-secret" }, nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()
	tok, err := auth.GenerateToken("admin", "test-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		acct Account
	}{{aliceID, alice}, {rootID, h.panel}} {
		m.Calls = nil
		url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=" + tc.id + "&token=" + tok
		ws, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		mt, msg, err := ws.ReadMessage()
		ws.Close()
		if err != nil || mt != websocket.BinaryMessage || string(msg) != string(replayHistory("$ ls\nnotes.txt\n")) {
			t.Errorf("%s: first frame = %d %q %v, want the replayed history", tc.id, mt, msg, err)
		}
		want := tc.acct.Name + " " + h.socketPath(tc.acct)
		if got := tmuxCall(h, m.Calls, "has-session -t "+tc.id); got != want {
			t.Errorf("%s: attach looked for it as %q, want %q", tc.id, got, want)
		}
	}

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=dddddddddddd&token=" + tok
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if mt, msg, err := ws.ReadMessage(); err != nil || mt != websocket.TextMessage || !strings.Contains(string(msg), "session ended") {
		t.Errorf("a session on no socket: first frame = %d %q %v, want the [session ended] notice", mt, msg, err)
	}
}

// dialFailCode reads the failure envelope out of a refused WS handshake.
// gorilla keeps up to 1 KiB of the response body on ErrBadHandshake.
func dialFailCode(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var env response.Response
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("bad envelope %q: %v", body, err)
	}
	if env.Success || env.Error == nil {
		t.Fatalf("expected a failure envelope, got %q", body)
	}
	return env.Error.Code, env.Error.Message
}

// The client is exec'd through runuser and then runs as the account, which can
// read its own /proc/self/environ. SFPANEL_JWT_SECRET is a supported config
// override, so inheriting the panel's environment would hand a less privileged
// account the JWT signing key — the ability to mint admin tokens, which is the
// boundary runuser is there to draw.
func TestAttachEnv_ANonPanelAccountInheritsNothing(t *testing.T) {
	h := newTestHandler(t, nil)
	base := []string{"PATH=/usr/sbin:/usr/bin", "HOME=/root", "SFPANEL_JWT_SECRET=signing-key", "NOTIFY_SOCKET=/run/systemd/notify"}

	env := attachEnv(base, Account{Name: "alice", Home: "/home/alice"}, h.panel)
	for _, key := range []string{"SFPANEL_JWT_SECRET", "NOTIFY_SOCKET"} {
		if got := lastEnv(env, key); got != "" {
			t.Errorf("alice's client carries %s=%q; env = %q", key, got, env)
		}
	}
	if lastEnv(env, "TERM") != "xterm-256color" || lastEnv(env, "HOME") != "/home/alice" ||
		lastEnv(env, "PATH") == "" || lastEnv(env, "LANG") != "C.UTF-8" {
		t.Errorf("alice's client needs TERM, PATH, LANG and its own HOME; env = %q", env)
	}

	// The panel's own account is not a boundary — the client has exactly the
	// privileges the panel already has — so it keeps the inherited environment.
	env = attachEnv(base, h.panel, h.panel)
	if lastEnv(env, "SFPANEL_JWT_SECRET") != "signing-key" || lastEnv(env, "TERM") != "xterm-256color" {
		t.Errorf("the panel account's client = %q", env)
	}
}
