package ai

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

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

func TestAttachArgv(t *testing.T) {
	h := newTestHandler(t, nil)
	name, argv := h.attachArgv(Account{Name: "alice"}, "aaaaaaaaaaaa")
	if name != "runuser" || strings.Join(argv, " ") != "-u alice -- tmux -f /dev/null -L sfpanel attach-session -t aaaaaaaaaaaa" {
		t.Errorf("alice: %s %q", name, argv)
	}
	name, argv = h.attachArgv(h.panel, "aaaaaaaaaaaa")
	if name != "tmux" || strings.Join(argv, " ") != "-f /dev/null -L sfpanel attach-session -t aaaaaaaaaaaa" {
		t.Errorf("root: %s %q", name, argv)
	}
}

// A session that vanished between the list and the click gets one text
// frame and a close — never a PTY, never a hang.
func TestAttachWS_GoneSessionSendsOneFrameAndCloses(t *testing.T) {
	// NewMockCommander, not a zero value: SetOutput writes to Outputs, which
	// the zero value leaves nil.
	m := exec.NewMockCommander()
	m.SetOutput("tmux", "can't find session: aaaaaaaaaaaa", errTest)
	h := newTestHandler(t, m)
	h.DB = openTestDB(t)
	_ = insertSession(h.DB, sessionRow{ID: "aaaaaaaaaaaa", Tool: ToolClaude, Title: "a", RunAs: "root", CWD: "/"})

	e := echo.New()
	e.GET("/ws/ai/attach", h.AttachWS("test-secret", nil, nil))
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
	e.GET("/ws/ai/attach", h.AttachWS("test-secret", nil, nil))
	srv := httptest.NewServer(e)
	defer srv.Close()
	tok, _ := auth.GenerateToken("admin", "test-secret", time.Minute)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/ai/attach?session_id=../work&token=" + tok
	if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("a malformed id must be a 404 before the upgrade; got err=%v resp=%v", err, resp)
	}
}
