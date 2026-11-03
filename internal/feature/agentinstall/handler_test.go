package agentinstall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	featurecluster "github.com/svrforum/SFPanel/internal/feature/cluster"
)

// apiFixture is the install API of one manager over a fake cluster and fake
// servers. Requests come from the operator; every answer is kept.
type apiFixture struct {
	t   *testing.T
	env *fakeEnv
	eng *Engine
	h   *Handler
	e   *echo.Echo

	mu        sync.Mutex
	at        place
	busy      featurecluster.Busy
	forwarded []string // "METHOD path body" sent on to the leader
	answers   []answer
	// inClaim is how many runs were recorded when the start gate's claim
	// returned.
	inClaim int
}

type answer struct {
	what   string
	status int
	body   string
}

func newAPIFixture(t *testing.T, servers ...*fakeServer) *apiFixture {
	t.Helper()
	env := newFakeEnv()
	if len(servers) > 0 {
		env = servers[0].env
	}
	f := &apiFixture{t: t, env: env, at: place{inCluster: true, leader: true, caughtUp: true}}
	f.eng = newTestEngine(env, newFakeDialer(servers...))
	f.h = &Handler{engine: f.eng}
	f.h.where = func() place {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.at
	}
	f.h.startGate = func(claim func(featurecluster.Busy) error) error {
		f.mu.Lock()
		b := f.busy
		f.mu.Unlock()
		err := claim(b)
		env.mu.Lock()
		n := len(env.runs)
		env.mu.Unlock()
		f.mu.Lock()
		f.inClaim = n
		f.mu.Unlock()
		return err
	}
	f.h.toLeader = func(c echo.Context) (bool, error) {
		body, _ := io.ReadAll(c.Request().Body)
		f.mu.Lock()
		f.forwarded = append(f.forwarded, c.Request().Method+" "+c.Request().URL.Path+" "+string(body))
		f.mu.Unlock()
		return true, response.Accepted(c, map[string]string{"run_id": "from-the-leader"})
	}
	f.e = echo.New()
	f.e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("username", "operator")
			return next(c)
		}
	})
	g := f.e.Group("/api/v1/cluster")
	g.GET("/agent-installs/tailnet", f.h.Tailnet)
	g.GET("/agent-installs/logins", f.h.ListLogins)
	g.POST("/agent-installs/logins", f.h.CreateLogin)
	g.PUT("/agent-installs/logins/:id", f.h.UpdateLogin)
	g.DELETE("/agent-installs/logins/:id", f.h.DeleteLogin)
	g.POST("/agent-installs", f.h.Start)
	g.POST("/agent-installs/:id/servers/:key/host-key", f.h.ApproveHostKey)
	g.POST("/agent-installs/:id/servers/:key/standalone", f.h.ConfirmStandalone)
	g.POST("/agent-installs/:id/proceed", f.h.Proceed)
	g.POST("/agent-installs/:id/servers/:key/recheck", f.h.Recheck)
	g.POST("/agent-installs/:id/servers/:key/revert", f.h.Revert)
	return f
}

// do sends a request and keeps its answer.
func (f *apiFixture) do(method, path, body string) (int, envelope) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, r)
	f.mu.Lock()
	f.answers = append(f.answers, answer{what: method + " " + path, status: rec.Code, body: rec.Body.String()})
	f.mu.Unlock()
	var env envelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

// envelope is an answer as response.OK and response.FailWith write it.
type envelope struct {
	Success bool `json:"success"`
	Data    struct {
		RunID string `json:"run_id"`
	} `json:"data"`
	Error struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func (f *apiFixture) start(body string) (int, envelope) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/v1/cluster/agent-installs", body)
}

func (f *apiFixture) rowAction(runID, key, action, body string) (int, envelope) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/v1/cluster/agent-installs/"+runID+"/servers/"+key+"/"+action, body)
}

// installBody is a start request for servers with account, a JSON object.
func installBody(account string, servers ...*fakeServer) string {
	var rows []string
	for _, s := range servers {
		rows = append(rows, fmt.Sprintf(`{"host":%q}`, s.ip.String()))
	}
	return `{"servers":[` + strings.Join(rows, ",") + `],"account":` + account + `}`
}

func (f *apiFixture) approve(runID, key string) {
	f.t.Helper()
	rec := f.env.waitRow(f.t, runID, key, func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
	if st, ans := f.rowAction(runID, key, "host-key", fmt.Sprintf(`{"fingerprint":%q,"accept":true}`, rec.HostKey)); st != http.StatusOK {
		f.t.Fatalf("approving %s's host key = %d %+v", key, st, ans)
	}
}

func (f *apiFixture) runs() int {
	f.env.mu.Lock()
	defer f.env.mu.Unlock()
	return len(f.env.runs)
}

func TestTheInstallRequestIsValidated(t *testing.T) {
	const pw = `{"user":"ops","method":"password","password":"pw"}`
	host := func(i int) string { return fmt.Sprintf(`{"host":"192.0.2.%d"}`, 10+i) }
	many := make([]string, 51)
	for i := range many {
		many[i] = host(i)
	}
	body := func(servers, account string) string {
		return `{"servers":[` + servers + `],"account":` + account + `}`
	}
	cases := []struct {
		name, body, field, row string
	}{
		{"no servers", body("", pw), "servers", ""},
		{"51 servers", body(strings.Join(many, ","), pw), "servers", ""},
		{"port 0", body(host(1)+`,{"host":"192.0.2.12","port":0}`, pw), "port", "s2"},
		{"port 65536", body(`{"host":"192.0.2.12","port":65536}`, pw), "port", "s1"},
		{"a bad name", body(host(1)+`,{"host":"192.0.2.12","name":"web 1"}`, pw), "name", "s2"},
		{"a bad group", body(`{"host":"192.0.2.12","group":"웹서버"}`, pw), "group", "s1"},
		{"a host twice", body(host(1)+","+host(2)+`,{"host":" 192.0.2.11 "}`, pw), "host", "s3"},
		{"a name twice", body(`{"host":"web.example"},{"host":"WEB.example."}`, pw), "host", "s2"},
		{"no user", body(host(1), `{"method":"password","password":"pw"}`), "user", ""},
		{"an unknown method", body(host(1), `{"user":"ops","method":"none"}`), "method", ""},
		{"a password login without one", body(host(1), `{"user":"ops","method":"password"}`), "password", ""},
		{"a password login with a key", body(host(1), `{"user":"ops","method":"password","password":"pw","key":"k"}`), "key", ""},
		{"a password login with a passphrase", body(host(1), `{"user":"ops","method":"password","password":"pw","passphrase":"pp"}`), "passphrase", ""},
		{"a key login without a key", body(host(1), `{"user":"ops","method":"key","passphrase":"pp"}`), "key", ""},
		{"a key login with a password", body(host(1), `{"user":"ops","method":"key","key":"k","password":"pw"}`), "password", ""},
		{"Tailscale with a password", body(host(1), `{"user":"ops","method":"tailscale","password":"pw"}`), "password", ""},
		{"Tailscale with a key", body(host(1), `{"user":"ops","method":"tailscale","key":"k"}`), "key", ""},
		{"Tailscale with a passphrase", body(host(1), `{"user":"ops","method":"tailscale","passphrase":"pp"}`), "passphrase", ""},
		{"sudo the same as no password", body(host(1), `{"user":"ops","method":"key","key":"k","sudo_same":true}`), "sudo_same", ""},
		{"sudo the same and its own", body(host(1), `{"user":"ops","method":"password","password":"pw","sudo_same":true,"sudo_password":"s"}`), "sudo_password", ""},
		{"a bad mode", `{"servers":[` + host(1) + `],"account":` + pw + `,"mode":"later"}`, "mode", ""},
		{"too many at once", `{"servers":[` + host(1) + `],"account":` + pw + `,"concurrency":6}`, "concurrency", ""},
		{"a field no install has", `{"servers":[` + host(1) + `],"account":` + pw + `,"sudo_pasword":"x"}`, "body", ""},
		{"no JSON", `servers`, "body", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			// An update under way would refuse a start that is one.
			f.busy = featurecluster.Busy{UpdateRunning: true}
			st, ans := f.start(tc.body)
			if st != http.StatusBadRequest || ans.Error.Code != response.ErrAgentInstallInvalid ||
				ans.Error.Details["field"] != tc.field || ans.Error.Details["row"] != tc.row {
				t.Fatalf("answer %d %+v, want 400 %s {field: %s, row: %q}", st, ans.Error, response.ErrAgentInstallInvalid, tc.field, tc.row)
			}
			if f.runs() != 0 || f.eng.Active() {
				t.Fatal("a refused request started an install")
			}
		})
	}

	t.Run("a host twice names the first", func(t *testing.T) {
		f := newAPIFixture(t)
		_, ans := f.start(body(host(1)+","+host(2)+","+host(1), pw))
		if ans.Error.Details["same_as"] != "s1" {
			t.Fatalf("details %v, want same_as s1", ans.Error.Details)
		}
	})

	t.Run("a request that is one starts", func(t *testing.T) {
		env := newFakeEnv()
		s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
		f := newAPIFixture(t, s1, s2)
		st, ans := f.start(`{"servers":[{"host":"192.0.2.11","port":2222,"name":"web-1","group":"web"},{"host":"192.0.2.12"}],` +
			`"account":{"user":"ops","method":"tailscale","sudo_password":"sudo-pw"},"review":true,"mode":"online","concurrency":5}`)
		if st != http.StatusAccepted || ans.Data.RunID == "" {
			t.Fatalf("answer %d %+v, want 202 with a run", st, ans)
		}
		head := f.env.head(ans.Data.RunID)
		if head.Batch != 5 || head.Inputs["mode"] != ModeOnline || head.Inputs["review"] != "true" || head.StartedBy != "operator" {
			t.Fatalf("head %+v, want the request's concurrency, mode, review and operator", head)
		}
		// A typed account names no saved login, as the pane's start does
		// with the account it has just saved.
		if login, ok := head.Inputs["login"]; ok {
			t.Fatalf("the run's inputs name the login %q, want none: the account was typed", login)
		}
		r1 := f.env.row(ans.Data.RunID, "s1")
		if r1.Host != "192.0.2.11" || r1.Name != "web-1" || r1.Group != "web" {
			t.Fatalf("s1 = %+v", r1)
		}
		f.eng.mu.Lock()
		s := f.eng.cur
		f.eng.mu.Unlock()
		if s.auth.Method != "none" || s.rows[0].srv.Port != 2222 || s.rows[1].srv.Port != 22 || string(s.sudoPw) != "sudo-pw" {
			t.Fatalf("auth %s, ports %d %d; want Tailscale SSH, 2222 and 22, and the sudo password", s.auth.Method, s.rows[0].srv.Port, s.rows[1].srv.Port)
		}
		if err := f.eng.Stop(ans.Data.RunID, "operator"); err != nil {
			t.Fatal(err)
		}
		f.env.waitEnded(t, ans.Data.RunID)
	})

	t.Run("an answer that is none", func(t *testing.T) {
		env := newFakeEnv()
		s1 := passingServer(t, env, 1)
		f := newAPIFixture(t, s1)
		_, ans := f.start(installBody(`{"user":"ops","method":"password","password":"sudo-pw","sudo_same":true}`, s1))
		id := ans.Data.RunID
		rec := env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
		for _, tc := range []struct{ action, body, field string }{
			{"host-key", `{"fingerprint":"` + rec.HostKey + `"}`, "accept"},
			{"host-key", `{"accept":true}`, "fingerprint"},
			{"host-key", `{"accept":true,"fingerprint":"x","extra":1}`, "body"},
			{"standalone", `{}`, "accept"},
		} {
			st, ans := f.rowAction(id, "s1", tc.action, tc.body)
			if st != http.StatusBadRequest || ans.Error.Code != response.ErrAgentInstallInvalid || ans.Error.Details["field"] != tc.field {
				t.Fatalf("%s %s = %d %+v, want 400 %s {field: %s}", tc.action, tc.body, st, ans.Error, response.ErrAgentInstallInvalid, tc.field)
			}
		}
		if r := env.row(id, "s1"); r.Awaiting != awaitHostKey {
			t.Fatalf("s1 awaits %q after answers that were none, want %s", r.Awaiting, awaitHostKey)
		}
		if err := f.eng.Stop(id, "operator"); err != nil {
			t.Fatal(err)
		}
		env.waitEnded(t, id)
	})
}

// A start is judged under the start gate, as a task run's is: it waits for
// a cluster update, a CA rotation, a conversion and a promotion, and is
// recorded inside the gate's claim, so the next starter sees it.
func TestAnInstallStartsUnderTheStartGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		busy featurecluster.Busy
		code string
	}{
		{"an update", featurecluster.Busy{UpdateRunning: true}, response.ErrUpdateInProgress},
		{"a CA rotation", featurecluster.Busy{CARotation: true}, response.ErrClusterCARotating},
		{"a conversion", featurecluster.Busy{Converting: true}, response.ErrTaskClusterChanging},
		{"a promotion", featurecluster.Busy{Promoting: true}, response.ErrTaskClusterChanging},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newFakeEnv()
			s1 := passingServer(t, env, 1)
			f := newAPIFixture(t, s1)
			f.busy = tc.busy
			if st, ans := f.start(installBody(`{"user":"ops","method":"password","password":"sudo-pw","sudo_same":true}`, s1)); st != http.StatusConflict || ans.Error.Code != tc.code {
				t.Fatalf("start during %s = %d %+v, want 409 %s", tc.name, st, ans.Error, tc.code)
			}
			if f.runs() != 0 || f.eng.Active() {
				t.Fatal("a refused start started an install")
			}
		})
	}
	t.Run("recorded inside the claim", func(t *testing.T) {
		env := newFakeEnv()
		s1 := passingServer(t, env, 1)
		f := newAPIFixture(t, s1)
		st, ans := f.start(installBody(`{"user":"ops","method":"password","password":"sudo-pw","sudo_same":true}`, s1))
		if st != http.StatusAccepted {
			t.Fatalf("start = %d %+v", st, ans)
		}
		if f.inClaim != 1 {
			t.Fatalf("%d runs recorded when the claim returned, want the install's", f.inClaim)
		}
		if err := f.eng.Stop(ans.Data.RunID, "operator"); err != nil {
			t.Fatal(err)
		}
		env.waitEnded(t, ans.Data.RunID)
	})
}

// The secrets of a request reach no answer, no record, no audit row and no
// log line: not the start's, not a row action's, not a refusal's, not the
// run's as GET and the stream send it, not a phase's output.
func TestNoSecretInAnyAnswer(t *testing.T) {
	const (
		loginPw    = "login-pw-7f3c91"
		sudoPw     = "sudo-pw-9a1e44"
		keyBody    = "key-body-5d2b07"
		passphrase = "phrase-c4e8a2"
	)
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" + keyBody + "\n-----END OPENSSH PRIVATE KEY-----\n"
	secrets := []string{loginPw, sudoPw, keyBody, passphrase}

	logs := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// sent is everything a client or the cluster could see of an install:
	// the answers, the run record as GET and the stream send it, each
	// phase's output, the audit rows and the log.
	check := func(t *testing.T, f *apiFixture, runID string) {
		t.Helper()
		var sent []string
		for _, a := range f.answers {
			sent = append(sent, a.what+" → "+a.body)
		}
		f.env.mu.Lock()
		snapshot, _ := json.Marshal(map[string]any{"type": "snapshot", "run": f.env.runs[runID], "nodes": f.env.rows[runID]})
		sent = append(sent, "GET and the stream → "+string(snapshot), "every record written → "+strings.Join(f.env.writes, "\n"),
			"audit → "+strings.Join(f.env.audits, "\n"))
		f.env.mu.Unlock()
		for _, k := range []string{"s1", "s2"} {
			for step := range phases {
				if v, err := f.eng.StepOutput(runID, k, step, 0); err == nil {
					b, _ := json.Marshal(v)
					sent = append(sent, fmt.Sprintf("output %s/%d → %s", k, step, b))
				}
			}
		}
		sent = append(sent, "log → "+logs.String())
		for _, s := range sent {
			for _, secret := range secrets {
				if strings.Contains(s, secret) {
					t.Fatalf("a secret reached what is sent: %s", s)
				}
			}
		}
	}

	t.Run("a password login with its own sudo password, reviewed, failing and reverted", func(t *testing.T) {
		env := newFakeEnv()
		env.heartbeat = func(string) (bool, bool, string) { return false, false, "" }
		s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
		s1.sudoPw, s2.sudoPw = sudoPw, sudoPw
		f := newAPIFixture(t, s1, s2)
		account := fmt.Sprintf(`{"user":"ops","method":"password","password":%q,"sudo_password":%q}`, loginPw, sudoPw)

		// A refusal of a request holding the secrets.
		if st, _ := f.start(`{"servers":[{"host":"192.0.2.11","port":0}],"account":` + account + `}`); st != http.StatusBadRequest {
			t.Fatalf("a bad port = %d, want 400", st)
		}
		st, ans := f.start(`{"servers":[{"host":"192.0.2.11"},{"host":"192.0.2.12"}],"account":` + account + `,"review":true}`)
		if st != http.StatusAccepted {
			t.Fatalf("start = %d %+v", st, ans)
		}
		id := ans.Data.RunID
		// A start while it runs.
		if st, ans := f.start(`{"servers":[{"host":"192.0.2.13"}],"account":` + account + `}`); st != http.StatusConflict || ans.Error.Code != response.ErrAgentInstallActive {
			t.Fatalf("a second start = %d %+v, want 409 %s", st, ans.Error, response.ErrAgentInstallActive)
		}
		f.approve(id, "s1")
		f.approve(id, "s2")
		for _, k := range []string{"s1", "s2"} {
			env.waitRow(t, id, k, func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitReview }, "to wait for the review")
		}
		if st, ans := f.do(http.MethodPost, "/api/v1/cluster/agent-installs/"+id+"/proceed", ""); st != http.StatusOK {
			t.Fatalf("proceed = %d %+v", st, ans)
		}
		if st, _ := f.do(http.MethodPost, "/api/v1/cluster/agent-installs/"+id+"/proceed", ""); st != http.StatusConflict {
			t.Fatalf("a second proceed = %d, want 409", st)
		}
		env.waitEnded(t, id)
		if st, ans := f.rowAction(id, "s1", "recheck", ""); st != http.StatusOK {
			t.Fatalf("recheck = %d %+v", st, ans)
		}
		waitFor(t, "the recheck to end", func() bool { return !f.eng.Active() })
		if st, ans := f.rowAction(id, "s2", "revert", ""); st != http.StatusOK {
			t.Fatalf("revert = %d %+v", st, ans)
		}
		env.waitRow(t, id, "s2", func(r cluster.RunNodeRecord) bool { return r.ReasonDetails["reverted"] == "purged" }, "to be purged")
		check(t, f, id)
		// The secrets did reach the server, on sudo's stdin: the test looks
		// where they would show.
		if !strings.Contains(string(s1.execList()[1].stdin), sudoPw) {
			t.Fatalf("sudo on s1 was not given the sudo password: %q", s1.execList()[1].stdin)
		}
	})

	t.Run("a key login with a passphrase and its own sudo password, succeeding", func(t *testing.T) {
		env := newFakeEnv()
		s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
		s1.sudoPw, s2.sudoPw = sudoPw, sudoPw
		f := newAPIFixture(t, s1, s2)
		account := fmt.Sprintf(`{"user":"ops","method":"key","key":%q,"passphrase":%q,"sudo_password":%q}`, key, passphrase, sudoPw)
		st, ans := f.start(installBody(account, s1, s2))
		if st != http.StatusAccepted {
			t.Fatalf("start = %d %+v", st, ans)
		}
		id := ans.Data.RunID
		f.eng.mu.Lock()
		got := string(f.eng.cur.auth.Key)
		f.eng.mu.Unlock()
		if got != key {
			t.Fatalf("the engine holds the key %q, want the one sent", got)
		}
		f.approve(id, "s1")
		f.approve(id, "s2")
		if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
			t.Fatalf("run %s, want succeeded", h.Status)
		}
		if st, _ := f.rowAction(id, "s1", "revert", ""); st != http.StatusConflict {
			t.Fatalf("revert of a success = %d, want 409", st)
		}
		check(t, f, id)
	})
}

// An SSH login a server refuses is that row's reason; no answer of the API
// is a 401, which the browser takes for its own session ending.
func TestAnSSHFailureIsNeverA401(t *testing.T) {
	env := newFakeEnv()
	s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
	s1.authFail = true
	f := newAPIFixture(t, s1, s2)
	st, ans := f.start(installBody(`{"user":"ops","method":"password","password":"wrong","sudo_same":true}`, s1, s2))
	if st != http.StatusAccepted {
		t.Fatalf("start = %d %+v, want 202", st, ans)
	}
	id := ans.Data.RunID
	f.approve(id, "s1")
	rec := env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.Status == cluster.RunNodeFailed }, "to fail")
	if rec.Reason != response.ErrAgentInstallAuthFailed {
		t.Fatalf("s1 failed for %s, want %s", rec.Reason, response.ErrAgentInstallAuthFailed)
	}
	f.rowAction(id, "s1", "host-key", `{"fingerprint":"`+rec.HostKey+`","accept":true}`)
	f.rowAction(id, "s1", "standalone", `{"accept":true}`)
	f.rowAction(id, "s1", "recheck", "")
	f.rowAction(id, "s1", "revert", "")
	f.do(http.MethodPost, "/api/v1/cluster/agent-installs/"+id+"/proceed", "")
	f.rowAction(id, "s9", "revert", "")
	f.rowAction("no-such-run", "s1", "recheck", "")
	f.start(installBody(`{"user":"ops","method":"password","password":"wrong","sudo_same":true}`, s1))
	env.waitRow(t, id, "s2", func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
	f.rowAction(id, "s2", "host-key", `{"fingerprint":"SHA256:another","accept":true}`)
	f.rowAction(id, "s2", "host-key", `{"accept":false}`)
	env.waitEnded(t, id)
	if len(f.answers) != 12 {
		t.Fatalf("%d answers, want 12", len(f.answers))
	}
	for _, a := range f.answers {
		if a.status == http.StatusUnauthorized {
			t.Fatalf("%s = 401 %s: the browser would sign the operator out", a.what, a.body)
		}
	}
}

// A follower sends an install and its answers on to the leader, which holds
// the session; a leader answers 503 until it has caught up; outside a
// cluster there is nothing to install into.
func TestAFollowerForwardsTheInstall(t *testing.T) {
	env := newFakeEnv()
	s1 := passingServer(t, env, 1)
	f := newAPIFixture(t, s1)
	body := installBody(`{"user":"ops","method":"password","password":"sudo-pw","sudo_same":true}`, s1)

	f.at = place{inCluster: true}
	st, ans := f.start(body)
	if st != http.StatusAccepted || ans.Data.RunID != "from-the-leader" {
		t.Fatalf("a follower's start = %d %+v, want the leader's 202", st, ans)
	}
	f.rowAction("r1", "s1", "host-key", `{"fingerprint":"SHA256:x","accept":true}`)
	f.rowAction("r1", "s1", "standalone", `{"accept":true}`)
	f.do(http.MethodPost, "/api/v1/cluster/agent-installs/r1/proceed", "")
	f.rowAction("r1", "s1", "recheck", "")
	f.rowAction("r1", "s1", "revert", "")
	want := []string{
		"POST /api/v1/cluster/agent-installs " + body,
		`POST /api/v1/cluster/agent-installs/r1/servers/s1/host-key {"fingerprint":"SHA256:x","accept":true}`,
		`POST /api/v1/cluster/agent-installs/r1/servers/s1/standalone {"accept":true}`,
		"POST /api/v1/cluster/agent-installs/r1/proceed ",
		"POST /api/v1/cluster/agent-installs/r1/servers/s1/recheck ",
		"POST /api/v1/cluster/agent-installs/r1/servers/s1/revert ",
	}
	if strings.Join(f.forwarded, "\n") != strings.Join(want, "\n") {
		t.Fatalf("forwarded\n%s\nwant\n%s", strings.Join(f.forwarded, "\n"), strings.Join(want, "\n"))
	}
	if f.runs() != 0 || f.eng.Active() {
		t.Fatal("the follower started an install of its own")
	}

	f.forwarded = nil
	f.at = place{inCluster: true, leader: true}
	for _, send := range []func() (int, envelope){
		func() (int, envelope) { return f.start(body) },
		func() (int, envelope) { return f.rowAction("r1", "s1", "recheck", "") },
		func() (int, envelope) { return f.do(http.MethodPost, "/api/v1/cluster/agent-installs/r1/proceed", "") },
	} {
		if st, ans := send(); st != http.StatusServiceUnavailable || ans.Error.Code != response.ErrInternalError {
			t.Fatalf("a leader catching up answered %d %+v, want 503", st, ans.Error)
		}
	}
	if len(f.forwarded) != 0 || f.runs() != 0 || f.eng.Active() {
		t.Fatalf("a leader catching up forwarded %v or started an install", f.forwarded)
	}

	f.at = place{}
	if st, ans := f.start(body); st != http.StatusBadRequest || ans.Error.Code != response.ErrInvalidRequest {
		t.Fatalf("outside a cluster = %d %+v, want 400 %s", st, ans.Error, response.ErrInvalidRequest)
	}

	// A forward that lands on a node no longer the leader (ProxyToLeader
	// lets a proxied request through) is the engine's to refuse: 503.
	f.at = place{inCluster: true}
	f.h.toLeader = func(echo.Context) (bool, error) { return false, nil }
	env.notLeader = true
	if st, ans := f.start(body); st != http.StatusServiceUnavailable || ans.Error.Code != response.ErrInternalError {
		t.Fatalf("a start on a node that lost the leadership = %d %+v, want 503", st, ans.Error)
	}
}

// lockedBuffer is a log sink the test reads while the engine writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
