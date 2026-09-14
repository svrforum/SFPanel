package ai

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

const passwdFixture = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
alice:x:1000:1000:Alice:/home/alice:/bin/bash
bob:x:1001:1001:Bob:/home/bob:/usr/sbin/nologin
carol:x:1002:1002:Carol:/home/carol:/bin/false
dave:x:1003:1003:Dave:/home/dave:/usr/bin/zsh
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
this line is broken
`

var testNow = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

var errTest = errors.New("test failure")

// newTestHandler is a root panel on a host whose /etc/passwd is the fixture
// above; Cmd is the given mock. DB is nil — tasks that need rows open one.
func newTestHandler(t *testing.T, m *exec.MockCommander) *Handler {
	t.Helper()
	pw := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(pw, []byte(passwdFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Handler{
		Cmd:        m,
		panel:      Account{Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash"},
		passwdPath: pw,
		isRoot:     func() bool { return true },
		socketRoot: filepath.Join(t.TempDir(), "run", "sfpanel", "ai"),
		// A test binary is not root, so it cannot chown a directory to uid 0.
		chown:      func(string, int, int) error { return nil },
		toolMemo:   map[string]toolMemoEntry{},
		latestMemo: map[string]latestMemoEntry{},
		now:        func() time.Time { return testNow },
	}
}

// envPrefix is the `env` argv prefix a call is expected to carry, as one
// string: `env -i …` for an account the panel is not, `env …` for its own.
func envPrefix(h *Handler, acct Account) string {
	return strings.Join(h.envArgv(acct)[1:], " ")
}
