package agentinstall

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// installLog is row key's install phase log as the output route serves it.
func installLog(t *testing.T, e *Engine, id, key string) string {
	t.Helper()
	v, err := e.StepOutput(id, key, phaseInstall, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v.Output
}

// wantStoppedRegistered checks that row s1 ended stopped as the node it
// registered as, saying so, with its server neither purged nor its node
// removed.
func wantStoppedRegistered(t *testing.T, env *fakeEnv, s *fakeServer, id string) {
	t.Helper()
	r := env.row(id, "s1")
	if r.Status != cluster.RunNodeStopped || r.NodeID != "a-web-1" || r.Reason != response.ErrAgentInstallStopped ||
		!reflect.DeepEqual(r.ReasonDetails, map[string]string{"registered": "1"}) {
		t.Fatalf("s1 = %s %q %s %v, want stopped as a-web-1 with %s {registered: 1}", r.Status, r.NodeID, r.Reason, r.ReasonDetails, response.ErrAgentInstallStopped)
	}
	if s.rootRan("uninstall --purge") || len(env.removedNodes()) != 0 {
		t.Fatalf("purged %v, removed %v: a registered row was taken apart", s.rootRan("uninstall --purge"), env.removedNodes())
	}
}

// wantRevertedWithoutCredentials reverts row s1 after a stop: the node
// goes, and the row says the server is the operator's to clean.
func wantRevertedWithoutCredentials(t *testing.T, e *Engine, env *fakeEnv, id string) {
	t.Helper()
	if err := e.Revert(id, "s1", "operator"); err != nil {
		t.Fatalf("revert of a stopped registered row: %v", err)
	}
	r := env.row(id, "s1")
	if !reflect.DeepEqual(env.removedNodes(), []string{"a-web-1"}) || r.Reason != response.ErrAgentInstallCredentialsGone || r.ReasonDetails["reverted"] != "removed" {
		t.Fatalf("removed %v, s1 = %s %v; want a-web-1 removed and %s {reverted: removed}", env.removedNodes(), r.Reason, r.ReasonDetails, response.ErrAgentInstallCredentialsGone)
	}
}

// install.sh registers the agent inside its join and has steps after it
// that can fail or hang: a node at the row's IP is the row's agent, which
// goes on to the connection check rather than be purged.
func TestAnAgentRegisteredBeforeInstallShEndsIsTheRows(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeServer, *Engine)
		log   string
	}{
		{name: "install.sh exits 1 after the join", setup: func(s *fakeServer, _ *Engine) { s.installExit = 1 },
			log: "install.sh exited 1 after the agent registered as node a-web-1"},
		{name: "install.sh does not end in time after the join", setup: func(s *fakeServer, e *Engine) {
			s.installHang = true
			e.t.installLimit = 50 * time.Millisecond
		}, log: "install.sh did not end in time after the agent registered as node a-web-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv()
			s := passingServer(t, env, 1)
			s.registerAnyway = true
			e := newTestEngine(env, newFakeDialer(s))
			c.setup(s, e)
			id, err := e.Start(context.Background(), request(s))
			if err != nil {
				t.Fatal(err)
			}
			approveAll(t, e, env, id, "s1")
			env.waitEnded(t, id)
			r := env.row(id, "s1")
			if r.Status != cluster.RunNodeSucceeded || r.NodeID != "a-web-1" || r.Steps[phaseVerify].Status != cluster.RunStepSucceeded {
				t.Fatalf("s1 = %+v, want succeeded as a-web-1 through the connection check", r)
			}
			if s.rootRan("uninstall --purge") || len(env.removedNodes()) != 0 {
				t.Fatal("the registered agent was taken apart")
			}
			if out := installLog(t, e, id, "s1"); !strings.Contains(out, c.log) {
				t.Fatalf("install log =\n%s\nwant %q", out, c.log)
			}
		})
	}
}

// A Stop that lands after the join leaves the agent registered: the row
// keeps the node and says so, the install unit is left to finish, and the
// node goes only by a revert.
func TestAStopAfterTheJoinKeepsTheNode(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	s.installHang = true
	s.registerAnyway = true
	e := newTestEngine(env, newFakeDialer(s))
	req := request(s)
	acct := req.Account
	id, err := e.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	waitFor(t, "the agent to register", func() bool { return len(env.Nodes()) == 1 })
	if err := e.Stop(id, "operator"); err != nil {
		t.Fatal(err)
	}
	if h := env.waitEnded(t, id); h.Status != cluster.RunStopped || !isZero(acct.Password) {
		t.Fatalf("run %s, password %q; want stopped with the credentials gone", h.Status, acct.Password)
	}
	wantStoppedRegistered(t, env, s, id)
	if s.rootRan("systemctl stop") {
		t.Fatal("the install unit was stopped past the join")
	}
	if got := env.revokedTokens(); len(got) != 1 {
		t.Fatalf("revoked %v, want the row's token", got)
	}
	if _, code := refusalCode(e.Recheck(id, "s1")); code != response.ErrAgentInstallNotWaiting {
		t.Fatalf("recheck after a stop: %s, want %s", code, response.ErrAgentInstallNotWaiting)
	}
	wantRevertedWithoutCredentials(t, e, env, id)
}

// A registered row stopped in its connection check, or in a recheck, ends
// as one stopped after the join.
func TestAStopInTheConnectionCheckKeepsTheNode(t *testing.T) {
	t.Run("in the connection check", func(t *testing.T) {
		env := newFakeEnv()
		env.heartbeat = func(string) (bool, bool, string) { return false, false, "" }
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		e.t.verifyLimit = 10 * time.Second
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool {
			return r.NodeID != "" && r.Steps[phaseVerify].Status == cluster.RunStepRunning
		}, "to be in its connection check")
		if err := e.Stop(id, "operator"); err != nil {
			t.Fatal(err)
		}
		env.waitEnded(t, id)
		wantStoppedRegistered(t, env, s, id)
		wantRevertedWithoutCredentials(t, e, env, id)
	})
	t.Run("in a recheck", func(t *testing.T) {
		env := newFakeEnv()
		env.heartbeat = func(string) (bool, bool, string) { return false, false, "" }
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitEnded(t, id)
		e.t.verifyLimit = 10 * time.Second
		if err := e.Recheck(id, "s1"); err != nil {
			t.Fatal(err)
		}
		env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.Status == cluster.RunNodeRunning }, "to recheck")
		if err := e.Stop(id, "operator"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the recheck to end", func() bool {
			return env.row(id, "s1").Status != cluster.RunNodeRunning && !env.head(id).Active()
		})
		wantStoppedRegistered(t, env, s, id)
		wantRevertedWithoutCredentials(t, e, env, id)
	})
}

// A connection that breaks while install.sh runs under systemd-run is
// dialled again, and the row follows the install to its end over the new
// one.
func TestAConnectionThatBreaksDuringTheInstallIsDialledAgain(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	s.dropAfterInstall = true
	d := newFakeDialer(s)
	e := newTestEngine(env, d)
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	env.waitEnded(t, id)
	if r := env.row(id, "s1"); r.Status != cluster.RunNodeSucceeded || r.NodeID != "a-web-1" {
		t.Fatalf("s1 = %+v, want succeeded as a-web-1", r)
	}
	out := installLog(t, e, id, "s1")
	if !strings.Contains(out, "reading the install's progress failed") || !strings.Contains(out, "reconnected to "+s.addr()) ||
		!strings.Contains(out, "[INFO] installing") || strings.Contains(out, "did not end in time") {
		t.Fatalf("install log =\n%s\nwant the failure, the reconnection and install.sh's log read over it", out)
	}
	if dials, _, _ := d.counts(); dials != 2 {
		t.Fatalf("dialled %d times, want the login and one reconnection", dials)
	}
}

// A row that ends with no agent at its IP is cleaned as before; the token
// is revoked before the install unit is stopped, and a join that lands
// during the stop still makes the row the node's.
func TestAnInstallWithNoAgentIsCleanedAfterTheTokenGoes(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(*fakeServer)
		stop       bool
		registered bool
	}{
		{name: "a failure", setup: func(s *fakeServer) { s.installExit, s.noRegister = 1, true }},
		{name: "a stop", setup: func(s *fakeServer) { s.installHang = true }, stop: true},
		{name: "a stop the join lands in", setup: func(s *fakeServer) { s.installHang, s.registerOnStop = true, true }, stop: true, registered: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv()
			s := passingServer(t, env, 1)
			c.setup(s)
			e := newTestEngine(env, newFakeDialer(s))
			id, err := e.Start(context.Background(), request(s))
			if err != nil {
				t.Fatal(err)
			}
			approveAll(t, e, env, id, "s1")
			if c.stop {
				waitFor(t, "install.sh to start", func() bool { return s.ran("systemd-run") })
				if err := e.Stop(id, "operator"); err != nil {
					t.Fatal(err)
				}
			}
			env.waitEnded(t, id)
			r := env.row(id, "s1")
			if c.stop {
				s.mu.Lock()
				revoked := s.revokedAtStop
				s.mu.Unlock()
				if !s.rootRan("systemctl stop") || !revoked {
					t.Fatalf("unit stopped %v, token revoked by then %v; want the token revoked first", s.rootRan("systemctl stop"), revoked)
				}
			}
			if c.registered {
				if r.NodeID != "a-web-1" || r.Reason != response.ErrAgentInstallStopped || s.rootRan("uninstall --purge") {
					t.Fatalf("s1 = %+v, purged %v; want the node the join made kept", r, s.rootRan("uninstall --purge"))
				}
				return
			}
			if r.NodeID != "" || r.Reason == response.ErrAgentInstallStopped || !s.rootRan("uninstall --purge") {
				t.Fatalf("s1 = %+v, purged %v; want no node and the server purged", r, s.rootRan("uninstall --purge"))
			}
		})
	}
}
