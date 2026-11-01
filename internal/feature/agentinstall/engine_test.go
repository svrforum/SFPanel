package agentinstall

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

func TestAHostKeyWaitsForTheOperator(t *testing.T) {
	env := newFakeEnv()
	s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
	d := newFakeDialer(s1, s2)
	e := newTestEngine(env, d)
	id, err := e.Start(context.Background(), request(s1, s2))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		key string
		srv *fakeServer
	}{{"s1", s1}, {"s2", s2}} {
		rec := env.waitRow(t, id, s.key, func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
		if want := "ED25519 " + s.srv.hostKey.Fingerprint; rec.HostKey != want {
			t.Fatalf("%s host key = %q, want %q", s.key, rec.HostKey, want)
		}
	}
	if dials, _, _ := d.counts(); dials != 0 || len(s1.commands()) != 0 {
		t.Fatalf("before an approval: %d logins, commands %q; want none", dials, s1.commands())
	}

	other := newHostKey(t)
	err = e.ApproveHostKey(id, "s1", "ED25519 "+other.Fingerprint, true)
	if st, code := refusalCode(err); st != http.StatusConflict || code != response.ErrAgentInstallHostKeyChanged {
		t.Fatalf("approving another fingerprint: %v, want 409 %s", err, response.ErrAgentInstallHostKeyChanged)
	}
	if rec := env.row(id, "s1"); rec.Awaiting != awaitHostKey {
		t.Fatalf("after a wrong fingerprint s1 awaits %q, want it to wait still", rec.Awaiting)
	}
	if err := e.ApproveHostKey(id, "s1", env.row(id, "s1").HostKey, true); err != nil {
		t.Fatal(err)
	}
	if err := e.ApproveHostKey(id, "s2", "", false); err != nil {
		t.Fatal(err)
	}
	h := env.waitEnded(t, id)
	if r := env.row(id, "s1"); r.Status != cluster.RunNodeSucceeded {
		t.Fatalf("s1 = %+v, want succeeded", r)
	}
	if r := env.row(id, "s2"); r.Status != cluster.RunNodeStopped || r.Steps[phaseConnect].Status != cluster.RunStepCancelled {
		t.Fatalf("s2 = %+v, want stopped at its connect", r)
	}
	if len(s2.commands()) != 0 {
		t.Fatalf("the rejected server ran %q", s2.commands())
	}
	if h.Status != cluster.RunSucceeded {
		t.Fatalf("run = %s, want succeeded", h.Status)
	}
	err = e.ApproveHostKey(id, "s1", env.row(id, "s1").HostKey, true)
	if _, code := refusalCode(err); code != response.ErrAgentInstallNotWaiting {
		t.Fatalf("approving again: %v, want %s", err, response.ErrAgentInstallNotWaiting)
	}

	t.Run("a key that changes after the approval is refused", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		s.keyChanged = true
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitEnded(t, id)
		if r := env.row(id, "s1"); r.Reason != response.ErrAgentInstallHostKeyChanged || len(s.commands()) != 0 {
			t.Fatalf("s1 = %+v, commands %q; want %s and nothing run", r, s.commands(), response.ErrAgentInstallHostKeyChanged)
		}
	})
}

func TestPreflightRefusalStopsOnlyThatServer(t *testing.T) {
	env := newFakeEnv()
	s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
	s2.facts.Arch = "armv7l"
	e := newTestEngine(env, newFakeDialer(s1, s2))
	id, err := e.Start(context.Background(), request(s1, s2))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1", "s2")
	h := env.waitEnded(t, id)
	r2 := env.row(id, "s2")
	if r2.Status != cluster.RunNodeFailed || r2.Reason != response.ErrAgentInstallArch ||
		!reflect.DeepEqual(r2.ReasonDetails, map[string]string{"arch": "armv7l"}) {
		t.Fatalf("s2 = %+v, want failed %s with arch armv7l", r2, response.ErrAgentInstallArch)
	}
	if r2.Steps[phasePreflight].Status != cluster.RunStepFailed || r2.Steps[phaseInstall].Status != cluster.RunStepSkipped {
		t.Fatalf("s2 steps = %+v", r2.Steps)
	}
	if s2.ran("systemd-run") {
		t.Fatal("the refused server was installed")
	}
	for _, m := range env.mintedFor() {
		if m.ip == s2.ip.String() {
			t.Fatal("a token was minted for the refused server")
		}
	}
	if r1 := env.row(id, "s1"); r1.Status != cluster.RunNodeSucceeded || !s1.ran("systemd-run") {
		t.Fatalf("s1 = %+v, want installed", r1)
	}
	if h.Status != cluster.RunFailed {
		t.Fatalf("run = %s, want failed", h.Status)
	}
}

func TestReviewModeWaitsForProceed(t *testing.T) {
	env := newFakeEnv()
	s1, s2, s3 := passingServer(t, env, 1), passingServer(t, env, 2), passingServer(t, env, 3)
	s3.facts.Systemd = false
	e := newTestEngine(env, newFakeDialer(s1, s2, s3))
	req := request(s1, s2, s3)
	req.Review = true
	id, err := e.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1", "s2", "s3")
	for _, k := range []string{"s1", "s2"} {
		env.waitRow(t, id, k, func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitReview }, "to wait for the review")
	}
	env.waitRow(t, id, "s3", func(r cluster.RunNodeRecord) bool { return r.Reason == response.ErrAgentInstallNoSystemd }, "to fail its pre-flight")
	time.Sleep(20 * time.Millisecond)
	for _, s := range []*fakeServer{s1, s2, s3} {
		if s.ran("systemd-run") {
			t.Fatal("a server was installed before Proceed")
		}
	}
	if n := len(env.mintedFor()); n != 0 {
		t.Fatalf("%d tokens minted before Proceed", n)
	}
	if err := e.Proceed(id); err != nil {
		t.Fatal(err)
	}
	env.waitEnded(t, id)
	for _, k := range []string{"s1", "s2"} {
		if r := env.row(id, k); r.Status != cluster.RunNodeSucceeded {
			t.Fatalf("%s = %+v, want succeeded", k, r)
		}
	}
	if s3.ran("systemd-run") {
		t.Fatal("the server that failed its pre-flight was installed")
	}
	if _, code := refusalCode(e.Proceed(id)); code != response.ErrAgentInstallNotWaiting {
		t.Fatalf("a second Proceed: want %s", response.ErrAgentInstallNotWaiting)
	}
}

func TestTheTokenIsBoundUsedOnceAndRevoked(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*fakeServer)
		stop   bool
		status string
	}{
		{name: "a success", status: cluster.RunNodeSucceeded},
		{name: "a failure", setup: func(s *fakeServer) { s.installExit = 1 }, status: cluster.RunNodeFailed},
		{name: "a stop", setup: func(s *fakeServer) { s.installHang = true }, stop: true, status: cluster.RunNodeStopped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv()
			s := passingServer(t, env, 1)
			if c.setup != nil {
				c.setup(s)
			}
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
			h := env.waitEnded(t, id)
			if r := env.row(id, "s1"); r.Status != c.status {
				t.Fatalf("s1 = %+v, want %s", r, c.status)
			}
			minted := env.mintedFor()
			if len(minted) != 1 || minted[0].ip != s.ip.String() {
				t.Fatalf("tokens minted = %+v, want one bound to %s", minted, s.ip)
			}
			tok := minted[0].token
			u, ok := s.uploaded("/tmp/sfpanel-agent.abcDEF0123/token")
			if !ok || string(u.data) != tok || u.mode != 0o600 {
				t.Fatalf("token file = %+v (%v), want the token at mode 0600", u, ok)
			}
			for _, x := range s.execList() {
				if strings.Contains(x.cmd, tok) || bytes.Contains(x.stdin, []byte(tok)) {
					t.Fatalf("the token reached a command: %q", x.cmd)
				}
			}
			if !s.rootRan("'--token-file' '/tmp/sfpanel-agent.abcDEF0123/token'") {
				t.Fatalf("install.sh was not given the token file: %q", s.commands())
			}
			if got := env.revokedTokens(); len(got) != 1 || got[0] != tok {
				t.Fatalf("revoked = %q, want the token", got)
			}
			if !s.rootRan("rm -rf '/tmp/sfpanel-agent.abcDEF0123'") {
				t.Fatal("the row's directory was not removed")
			}
			if strings.Contains(env.allWrites(), tok) {
				t.Fatal("the token reached the run record")
			}
			if c.stop {
				if h.Status != cluster.RunStopped || h.StoppedBy != "operator" {
					t.Fatalf("run = %+v, want stopped by operator", h)
				}
				if !s.rootRan("systemctl stop 'sfpanel-agent-install-" + id + "-s1.service'") {
					t.Fatalf("the install unit was not stopped: %q", s.commands())
				}
			}
			if c.status != cluster.RunNodeSucceeded && !s.ran(" uninstall --purge") {
				t.Fatalf("a server with no SFPanel before was not purged: %q", s.commands())
			}
		})
	}
}

func TestOnlineNeedsAllThreeChecks(t *testing.T) {
	cases := []struct {
		name    string
		env     func(*fakeEnv)
		details map[string]string
	}{
		{name: "a relay that fails", env: func(f *fakeEnv) { f.proxy = func(string) (int, error) { return 0, errors.New("dial node: refused") } },
			details: map[string]string{"cause": "managers_to_agent", "ports": "3629,3628", "failed": "relay"}},
		{name: "a panel port that fails", env: func(f *fakeEnv) { f.panel = func(string) (int, error) { return http.StatusForbidden, nil } },
			details: map[string]string{"cause": "managers_to_agent", "ports": "3629,3628", "failed": "panel"}},
		{name: "no heartbeat", env: func(f *fakeEnv) { f.heartbeat = func(string) (bool, bool, string) { return false, false, "" } },
			details: map[string]string{"cause": "agent_to_managers", "port": "3629"}},
		{name: "a heartbeat at another version", env: func(f *fakeEnv) { f.heartbeat = func(string) (bool, bool, string) { return true, true, "0.80.0" } },
			details: map[string]string{"cause": "version", "version": "0.80.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv()
			c.env(env)
			s := passingServer(t, env, 1)
			e := newTestEngine(env, newFakeDialer(s))
			start := time.Now()
			id, err := e.Start(context.Background(), request(s))
			if err != nil {
				t.Fatal(err)
			}
			approveAll(t, e, env, id, "s1")
			env.waitEnded(t, id)
			r := env.row(id, "s1")
			if r.Status != cluster.RunNodeFailed || r.Reason != response.ErrAgentInstallOffline || !reflect.DeepEqual(r.ReasonDetails, c.details) {
				t.Fatalf("s1 = %s %s %v, want failed %s %v", r.Status, r.Reason, r.ReasonDetails, response.ErrAgentInstallOffline, c.details)
			}
			if r.NodeID != "a-web-1" {
				t.Fatalf("node = %q, want the registered agent", r.NodeID)
			}
			if time.Since(start) < e.t.verifyLimit {
				t.Fatal("the checks gave up before their time")
			}
			if env.proxyCalls < 3 {
				t.Fatalf("checked %d times, want it to keep trying", env.proxyCalls)
			}
		})
	}

	t.Run("all three pass and the group joins the labels", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		s.labels = map[string]string{"zone": "a"}
		e := newTestEngine(env, newFakeDialer(s))
		req := request(s)
		req.Servers[0].Group = "web"
		id, err := e.Start(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitEnded(t, id)
		if r := env.row(id, "s1"); r.Status != cluster.RunNodeSucceeded || r.NodeID != "a-web-1" || r.Group != "web" {
			t.Fatalf("s1 = %+v, want succeeded as a-web-1", r)
		}
		if got, want := env.labels["a-web-1"], map[string]string{"zone": "a", "group": "web"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	})
}

func TestCredentialsLeaveMemory(t *testing.T) {
	start := func(t *testing.T, env *fakeEnv, s *fakeServer) (*Engine, string, Account) {
		t.Helper()
		e := newTestEngine(env, newFakeDialer(s))
		req := request(s)
		req.Account.SudoSame = false
		req.Account.SudoPassword = []byte("sudo-pw")
		acct := req.Account
		id, err := e.Start(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		return e, id, acct
	}
	gone := func(a Account) bool { return isZero(a.Password) && isZero(a.SudoPassword) }

	t.Run("a success drops them at once", func(t *testing.T) {
		env := newFakeEnv()
		_, id, acct := start(t, env, passingServer(t, env, 1))
		h := env.waitEnded(t, id)
		if h.Status != cluster.RunSucceeded || !h.CredentialsUntil.IsZero() || !gone(acct) {
			t.Fatalf("run %s until %v, password %q sudo %q; want succeeded with the bytes zeroed", h.Status, h.CredentialsUntil, acct.Password, acct.SudoPassword)
		}
	})
	t.Run("a failure keeps them ten minutes", func(t *testing.T) {
		env := newFakeEnv()
		env.heartbeat = func(string) (bool, bool, string) { return false, false, "" }
		_, id, acct := start(t, env, passingServer(t, env, 1))
		h := env.waitEnded(t, id)
		if h.Status != cluster.RunFailed || !h.CredentialsUntil.Equal(h.EndedAt.Add(10*time.Minute)) {
			t.Fatalf("run %s ended %v until %v, want failed and kept 10 minutes", h.Status, h.EndedAt, h.CredentialsUntil)
		}
		time.Sleep(20 * time.Millisecond)
		if gone(acct) {
			t.Fatal("the credentials went before their time")
		}
		env.advance(10 * time.Minute)
		waitFor(t, "the credentials to go", func() bool { return gone(acct) && env.head(id).CredentialsUntil.IsZero() })
	})
	t.Run("a failure before the registration drops them at once", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		s.facts.Systemd = false
		_, id, acct := start(t, env, s)
		h := env.waitEnded(t, id)
		if h.Status != cluster.RunFailed || !h.CredentialsUntil.IsZero() || !gone(acct) {
			t.Fatalf("run %s until %v; want failed with nothing kept", h.Status, h.CredentialsUntil)
		}
	})
	t.Run("a stop drops them at once", func(t *testing.T) {
		env := newFakeEnv()
		env.heartbeat = func(string) (bool, bool, string) { return false, false, "" }
		e, id, acct := start(t, env, passingServer(t, env, 1))
		env.waitEnded(t, id)
		if err := e.Stop(id, "operator"); err != nil {
			t.Fatal(err)
		}
		if !gone(acct) || !env.head(id).CredentialsUntil.IsZero() {
			t.Fatal("a stop left the credentials")
		}
	})
	t.Run("a stop under way drops them once the rows end", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		s.installHang = true
		e, id, acct := start(t, env, s)
		waitFor(t, "install.sh to start", func() bool { return s.ran("systemd-run") })
		if err := e.Stop(id, "operator"); err != nil {
			t.Fatal(err)
		}
		if h := env.waitEnded(t, id); h.Status != cluster.RunStopped || !gone(acct) {
			t.Fatalf("run %s; want stopped with the credentials gone", h.Status)
		}
		if !s.ran(sudoWithPassword + "'bash '\\''/tmp/sfpanel-agent.abcDEF0123/install.sh'\\'' uninstall --purge'") {
			t.Fatalf("the stop did not clean the server as root first: %q", s.commands())
		}
	})
}

func TestRevertPurgesOnlyWhatThisRunInstalled(t *testing.T) {
	failVerify := func(env *fakeEnv) { env.heartbeat = func(string) (bool, bool, string) { return false, false, "" } }
	run := func(t *testing.T, env *fakeEnv, s *fakeServer) (*Engine, *fakeDialer, string) {
		t.Helper()
		d := newFakeDialer(s)
		e := newTestEngine(env, d)
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		if s.facts.SFPanelVersion != "" {
			env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitStandalone }, "to ask about its panel")
			if err := e.ConfirmStandalone(id, "s1", true); err != nil {
				t.Fatal(err)
			}
		}
		env.waitEnded(t, id)
		return e, d, id
	}

	t.Run("a server without SFPanel before is purged", func(t *testing.T) {
		env := newFakeEnv()
		failVerify(env)
		s := passingServer(t, env, 1)
		e, _, id := run(t, env, s)
		if s.ran("uninstall --purge") {
			t.Fatal("purged before the revert")
		}
		if err := e.Revert(id, "s1", "operator"); err != nil {
			t.Fatal(err)
		}
		if got := env.removedNodes(); !reflect.DeepEqual(got, []string{"a-web-1"}) {
			t.Fatalf("removed %v, want a-web-1", got)
		}
		r := env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.ReasonDetails["reverted"] == "purged" }, "to be purged")
		if r.Reason != response.ErrAgentInstallOffline {
			t.Fatalf("reason = %s, want the failure's own", r.Reason)
		}
		waitFor(t, "the credentials to go once nothing can use them", func() bool { return env.head(id).CredentialsUntil.IsZero() })
		if !s.ran(sudoWithPassword+"'bash '\\''/tmp/sfpanel-agent.abcDEF0123/install.sh'\\'' uninstall --purge'") ||
			!s.ran(sudoWithPassword+"'rmdir /opt/stacks 2>/dev/null || true'") {
			t.Fatalf("commands %q, want the purge and /opt/stacks removed as root", s.commands())
		}
		if _, code := refusalCode(e.Revert(id, "s1", "operator")); code != response.ErrAgentInstallNotWaiting {
			t.Fatalf("a second revert: want %s", response.ErrAgentInstallNotWaiting)
		}
	})
	t.Run("an /opt/stacks that was there stays", func(t *testing.T) {
		env := newFakeEnv()
		failVerify(env)
		s := passingServer(t, env, 1)
		s.facts.OptStacks = true
		e, _, id := run(t, env, s)
		if err := e.Revert(id, "s1", "operator"); err != nil {
			t.Fatal(err)
		}
		env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.ReasonDetails["reverted"] == "purged" }, "to be purged")
		if !s.ran("uninstall --purge") || s.ran("rmdir /opt/stacks") {
			t.Fatalf("commands %q, want the purge and /opt/stacks kept", s.commands())
		}
	})
	t.Run("a standalone panel before only loses its node", func(t *testing.T) {
		env := newFakeEnv()
		failVerify(env)
		s := passingServer(t, env, 1)
		s.facts.SFPanelVersion = "0.80.0"
		s.facts.PortsBusy = []BusyPort{{Port: 3628, Owner: "sfpanel"}}
		e, d, id := run(t, env, s)
		dials, _, _ := d.counts()
		if err := e.Revert(id, "s1", "operator"); err != nil {
			t.Fatal(err)
		}
		r := env.row(id, "s1")
		if !reflect.DeepEqual(env.removedNodes(), []string{"a-web-1"}) || r.ReasonDetails["reverted"] != "removed" || r.ReasonDetails["standalone"] != "1" {
			t.Fatalf("removed %v, row %+v; want the node removed and the row saying so", env.removedNodes(), r)
		}
		time.Sleep(20 * time.Millisecond)
		if after, _, _ := d.counts(); after != dials || s.ran("uninstall --purge") {
			t.Fatal("the server that ran a standalone panel was touched")
		}
	})
	t.Run("once the credentials are gone only the node goes", func(t *testing.T) {
		env := newFakeEnv()
		failVerify(env)
		s := passingServer(t, env, 1)
		e, d, id := run(t, env, s)
		env.advance(10 * time.Minute)
		waitFor(t, "the credentials to go", func() bool { return env.head(id).CredentialsUntil.IsZero() })
		dials, _, _ := d.counts()
		if err := e.Revert(id, "s1", "operator"); err != nil {
			t.Fatalf("a revert without credentials: %v, want it done", err)
		}
		r := env.row(id, "s1")
		if r.Reason != response.ErrAgentInstallCredentialsGone || !reflect.DeepEqual(env.removedNodes(), []string{"a-web-1"}) {
			t.Fatalf("row %+v removed %v; want %s and the node removed", r, env.removedNodes(), response.ErrAgentInstallCredentialsGone)
		}
		time.Sleep(20 * time.Millisecond)
		if after, _, _ := d.counts(); after != dials || s.ran("uninstall --purge") {
			t.Fatal("the server was reached without credentials")
		}
	})
	t.Run("a row that succeeded is not reverted", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		e, _, id := run(t, env, s)
		if _, code := refusalCode(e.Revert(id, "s1", "operator")); code != response.ErrAgentInstallNotWaiting || len(env.removedNodes()) != 0 {
			t.Fatalf("revert of a success: %s, removed %v", code, env.removedNodes())
		}
	})
}

func TestRecheckRunsTheChecksAgain(t *testing.T) {
	env := newFakeEnv()
	var online bool
	env.heartbeat = func(string) (bool, bool, string) {
		env.mu.Lock()
		defer env.mu.Unlock()
		return online, online, "0.81.0"
	}
	s := passingServer(t, env, 1)
	e := newTestEngine(env, newFakeDialer(s))
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	if h := env.waitEnded(t, id); h.Status != cluster.RunFailed {
		t.Fatalf("run = %s, want failed", h.Status)
	}
	env.mu.Lock()
	online = true
	env.mu.Unlock()
	if err := e.Recheck(id, "s1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the recheck to pass", func() bool {
		return env.row(id, "s1").Status == cluster.RunNodeSucceeded && env.head(id).Status == cluster.RunSucceeded
	})
	if !env.head(id).CredentialsUntil.IsZero() {
		t.Fatal("the credentials outlived the last failure")
	}
}

func TestStandaloneNeedsConfirmation(t *testing.T) {
	env := newFakeEnv()
	s1, s2 := passingServer(t, env, 1), passingServer(t, env, 2)
	for _, s := range []*fakeServer{s1, s2} {
		s.facts.SFPanelVersion = "0.80.0"
		s.facts.PortsBusy = []BusyPort{{Port: 3628, Owner: "sfpanel"}}
	}
	d := newFakeDialer(s1, s2)
	e := newTestEngine(env, d)
	id, err := e.Start(context.Background(), request(s1, s2))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1", "s2")
	for _, k := range []string{"s1", "s2"} {
		env.waitRow(t, id, k, func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitStandalone }, "to ask about its panel")
	}
	if _, open, _ := d.counts(); open != 0 || s1.ran("systemd-run") || s2.ran("systemd-run") {
		t.Fatalf("while waiting: %d connections open, installed %v %v; want none", open, s1.ran("systemd-run"), s2.ran("systemd-run"))
	}
	if err := e.ConfirmStandalone(id, "s1", true); err != nil {
		t.Fatal(err)
	}
	if err := e.ConfirmStandalone(id, "s2", false); err != nil {
		t.Fatal(err)
	}
	env.waitEnded(t, id)
	if r := env.row(id, "s1"); r.Status != cluster.RunNodeSucceeded {
		t.Fatalf("s1 = %+v, want succeeded", r)
	}
	if r := env.row(id, "s2"); r.Status != cluster.RunNodeSkipped || s2.ran("systemd-run") {
		t.Fatalf("s2 = %+v, want skipped and not installed", r)
	}
	if _, code := refusalCode(e.ConfirmStandalone(id, "s1", true)); code != response.ErrAgentInstallNotWaiting {
		t.Fatalf("confirming again: want %s", response.ErrAgentInstallNotWaiting)
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	env := newFakeEnv()
	var servers []*fakeServer
	for i := 1; i <= 5; i++ {
		s := passingServer(t, env, i)
		s.probeDelay = 30 * time.Millisecond
		servers = append(servers, s)
	}
	d := newFakeDialer(servers...)
	e := newTestEngine(env, d)
	req := request(servers...)
	req.Concurrency = 2
	id, err := e.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1", "s2", "s3", "s4", "s5")
	h := env.waitEnded(t, id)
	if h.Status != cluster.RunSucceeded || h.Batch != 2 {
		t.Fatalf("run = %+v, want succeeded with batch 2", h)
	}
	if _, _, peak := d.counts(); peak != 2 {
		t.Fatalf("at most %d servers at once, want 2", peak)
	}
}

func TestUnreleasedVersionNeedsTheArchiveDir(t *testing.T) {
	t.Run("without the directory", func(t *testing.T) {
		env := newFakeEnv()
		env.version, env.managers = "0.81.0-dev", map[string]string{"m2": "0.81.0-dev"}
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		req := request(s)
		pw := req.Account.Password
		_, err := e.Start(context.Background(), req)
		if st, code := refusalCode(err); st != http.StatusConflict || code != response.ErrAgentInstallUnreleased ||
			!strings.Contains(err.Error(), "names no directory") {
			t.Fatalf("start = %v, want 409 %s for the variable unset", err, response.ErrAgentInstallUnreleased)
		}
		if len(env.runs) != 0 || !isZero(pw) {
			t.Fatal("a refused start recorded a run or kept the password")
		}
	})
	t.Run("with a directory that holds no checksums.txt", func(t *testing.T) {
		env := newFakeEnv()
		env.version, env.managers = "0.81.0-dev", map[string]string{"m2": "0.81.0-dev"}
		env.archives = t.TempDir()
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		_, err := e.Start(context.Background(), request(s))
		if st, code := refusalCode(err); st != http.StatusConflict || code != response.ErrAgentInstallUnreleased {
			t.Fatalf("start = %v, want 409 %s", err, response.ErrAgentInstallUnreleased)
		}
	})
	t.Run("with the directory it installs offline from it", func(t *testing.T) {
		env := newFakeEnv()
		env.version, env.managers = "0.81.0-dev", map[string]string{"m2": "0.81.0-dev"}
		dir := t.TempDir()
		name := "sfpanel_0.81.0-dev_linux_amd64.tar.gz"
		for f, body := range map[string]string{"checksums.txt": "123  " + name + "\n", name: "dev archive"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		env.archives = dir
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
			t.Fatalf("run = %s, want succeeded", h.Status)
		}
		u, ok := s.uploaded("/tmp/sfpanel-agent.abcDEF0123/" + name)
		if !ok || string(u.data) != "dev archive" {
			t.Fatalf("archive uploaded = %q (%v), want the directory's", u.data, ok)
		}
		if sums, _ := s.uploaded("/tmp/sfpanel-agent.abcDEF0123/checksums.txt"); !strings.Contains(string(sums.data), name) {
			t.Fatalf("checksums uploaded = %q", sums.data)
		}
		if !s.rootRan("'--version' '0.81.0-dev'") || !s.rootRan("'--archive' '/tmp/sfpanel-agent.abcDEF0123/"+name+"'") {
			t.Fatalf("install.sh was not run offline at the version: %q", s.commands())
		}
		if env.sumsAsked != 0 || len(env.archiveAsked) != 0 {
			t.Fatal("the release was asked for an unreleased version")
		}
	})
	t.Run("a release never reads the variable", func(t *testing.T) {
		env := newFakeEnv()
		env.archives = t.TempDir()
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitEnded(t, id)
		if env.archivesAsked != 0 {
			t.Fatalf("SFPANEL_AGENT_ARCHIVES read %d times for a release", env.archivesAsked)
		}
		if s.ran("--archive") || env.sumsAsked != 1 {
			t.Fatalf("a release online: commands %q, checksums asked %d", s.commands(), env.sumsAsked)
		}
	})
}

func TestMixedVersionsAreRefused(t *testing.T) {
	env := newFakeEnv()
	env.managers = map[string]string{"m2": "0.80.0", "m3": "v0.81.0", "m4": ""}
	s := passingServer(t, env, 1)
	e := newTestEngine(env, newFakeDialer(s))
	_, err := e.Start(context.Background(), request(s))
	var r *Refusal
	if !errors.As(err, &r) || r.Status != http.StatusConflict || r.Code != response.ErrAgentInstallMixedVersions || r.Details["managers"] != "m2 (0.80.0)" {
		t.Fatalf("start = %#v, want 409 %s naming m2 (0.80.0)", err, response.ErrAgentInstallMixedVersions)
	}
	if len(env.runs) != 0 {
		t.Fatal("a refused start recorded a run")
	}
}

func TestStartRefusesWhileARunIsActive(t *testing.T) {
	env := newFakeEnv()
	env.activeRun = "20261102-090000-abcdef"
	s := passingServer(t, env, 1)
	e := newTestEngine(env, newFakeDialer(s))
	_, err := e.Start(context.Background(), request(s))
	if st, code := refusalCode(err); st != http.StatusConflict || code != response.ErrTaskRunActive {
		t.Fatalf("start during a task run = %v, want 409 %s", err, response.ErrTaskRunActive)
	}

	env.activeRun = ""
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	if !e.Active() || !e.Owns(id) {
		t.Fatal("the install is not under way")
	}
	_, err = e.Start(context.Background(), request(s))
	if st, code := refusalCode(err); st != http.StatusConflict || code != response.ErrAgentInstallActive {
		t.Fatalf("a second install = %v, want 409 %s", err, response.ErrAgentInstallActive)
	}
	if err := e.Stop(id, "operator"); err != nil {
		t.Fatal(err)
	}
	env.waitEnded(t, id)
	waitFor(t, "the install to end", func() bool { return !e.Active() })
	if env.unownedPuts != 0 {
		t.Fatalf("%d run writes while the session did not own the run", env.unownedPuts)
	}
	if want := []string{"admin POST /api/v1/cluster/task-runs/" + id + "#start 202", "admin TASK /api/v1/cluster/task-runs/" + id + "#end:stopped 200"}; !reflect.DeepEqual(env.audits, want) {
		t.Fatalf("audits = %q, want %q", env.audits, want)
	}
}

func TestSudoGetsThePasswordOnStdinOnly(t *testing.T) {
	t.Run("a password", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitEnded(t, id)
		x := s.execList()
		if x[0].cmd != "sh -s" || string(x[0].stdin) != LoginCheckScript() {
			t.Fatalf("first command %q, want the login check on stdin", x[0].cmd)
		}
		probe := ProbeScript([]string{"192.0.2.1:3629"}, "0.81.0", true)
		if x[1].cmd != "sudo -k -S -p '' bash -c 'bash -s'" || string(x[1].stdin) != "sudo-pw\n"+probe {
			t.Fatalf("probe = %q with stdin %d bytes, want bash -s under sudo -S with the password line, then the script", x[1].cmd, len(x[1].stdin))
		}
		for _, c := range s.commands() {
			if strings.Contains(c, "sudo-pw") {
				t.Fatalf("the sudo password reached a command line: %q", c)
			}
		}
	})
	t.Run("passwordless sudo is given no password", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		s.facts.Sudo = SudoNoPassword
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
			t.Fatalf("run = %s", h.Status)
		}
		for _, x := range s.execList() {
			if strings.HasPrefix(x.cmd, "sudo") && (!strings.HasPrefix(x.cmd, sudoNoPassword) || bytes.Contains(x.stdin, []byte("sudo-pw"))) {
				t.Fatalf("command %q stdin %q: want sudo -n and no password", x.cmd, x.stdin)
			}
		}
	})
	t.Run("a refused password is no root", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		s.sudoPw = "another"
		e := newTestEngine(env, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, env, id, "s1")
		env.waitEnded(t, id)
		if r := env.row(id, "s1"); r.Reason != response.ErrAgentInstallNotRoot || r.ReasonDetails["sudo"] != "refused" {
			t.Fatalf("s1 = %s %v, want %s with sudo refused", r.Reason, r.ReasonDetails, response.ErrAgentInstallNotRoot)
		}
	})
}

func TestAPhaseLogIsTheRowsAndPhasesOwn(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	e := newTestEngine(env, newFakeDialer(s))
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	env.waitEnded(t, id)
	v, err := e.StepOutput(id, "s1", phaseInstall, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "install/s1/2" || v.Status != cluster.RunStepSucceeded || !v.Done || !strings.Contains(v.Output, "[INFO] installing\n") || strings.Contains(v.Output, "\x1b") {
		t.Fatalf("view = %+v, want install.sh's log without its colours", v)
	}
	if rest, _ := e.StepOutput(id, "s1", phaseInstall, v.NextSkip); rest.Output != "" || rest.NextSkip != v.NextSkip {
		t.Fatalf("from the end: %+v", rest)
	}
	for p, step := range env.row(id, "s1").Steps {
		if step.JobID != jobID("s1", p) {
			t.Fatalf("step %d job = %q", p, step.JobID)
		}
	}
	if _, err := e.StepOutput("20261102-090000-abcdef", "s1", 0, 0); err == nil {
		t.Fatal("output of a run the session does not hold")
	}

	var l phaseLog
	l.write(bytes.Repeat([]byte("0123456789abcde\n"), logCap/8))
	if out, n := l.from(0); n != logCap || !strings.HasSuffix(out, logCutNote) {
		t.Fatalf("a long log is %d bytes, want it cut at %d with a note", n, logCap)
	}
}

func TestTheWaitsAreTheDesigns(t *testing.T) {
	got := New(newFakeEnv()).t
	want := timings{poll: 2 * time.Second, installLimit: 30 * time.Minute, verifyEvery: 3 * time.Second,
		verifyLimit: 3 * time.Minute, checkWait: 10 * time.Minute, answerWait: 30 * time.Minute,
		keep: 10 * time.Minute, sweep: 5 * time.Second, cleanup: time.Minute, call: 10 * time.Second, tmpBase: "/tmp"}
	if got != want {
		t.Fatalf("timings = %+v, want %+v", got, want)
	}
	if _, ok := New(newFakeEnv()).dialer.(sshDialer); !ok {
		t.Fatal("the engine does not reach servers over SSH")
	}
}
