package agentinstall

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// labelFailEnv fails every label update.
type labelFailEnv struct{ *fakeEnv }

func (labelFailEnv) UpdateNodeLabels(string, map[string]string) error {
	return errors.New("raft apply timed out")
}

// testEngineOver is newTestEngine's engine over env, a wrapper of base.
func testEngineOver(env Env, base *fakeEnv, d Dialer) *Engine {
	ref := newTestEngine(base, d)
	e := New(env)
	e.dialer, e.resolve, e.t = ref.dialer, ref.resolve, ref.t
	base.engine = e
	return e
}

// A group label that cannot be set after the three checks passed leaves a
// working agent: the row succeeds as its node and says the label is missing,
// the run succeeds, and no credentials are kept for it.
func TestALabelThatFailsLeavesTheRowSucceeded(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	e := testEngineOver(labelFailEnv{env}, env, newFakeDialer(s))
	req := request(s)
	req.Servers[0].Group = "web"
	id, err := e.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	h := env.waitEnded(t, id)
	r := env.row(id, "s1")
	if r.Status != cluster.RunNodeSucceeded || r.NodeID != "a-web-1" || r.Reason != response.ErrAgentInstallLabelFailed || r.ReasonDetails["group"] != "web" {
		t.Fatalf("s1 = %s %q %s %v, want succeeded as a-web-1 with %s {group: web}", r.Status, r.NodeID, r.Reason, r.ReasonDetails, response.ErrAgentInstallLabelFailed)
	}
	if h.Status != cluster.RunSucceeded || !h.CredentialsUntil.IsZero() {
		t.Fatalf("run = %s, credentials until %v; want succeeded with none kept", h.Status, h.CredentialsUntil)
	}
}

// What the target writes reaches a row's reason details, which Raft copies
// to every manager: each value is cut at 256 bytes, on a rune boundary.
func TestReasonDetailsAreCut(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	// Nearly 1 MiB: the probe's lines are read up to 1 MiB each.
	s.facts.OSID = "x" + strings.Repeat("A", 1<<20-64)
	s.facts.OSLike = strings.Repeat("데비안", 1<<16)
	e := newTestEngine(env, newFakeDialer(s))
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	env.waitEnded(t, id)
	r := env.row(id, "s1")
	if r.Reason != response.ErrAgentInstallOS {
		t.Fatalf("s1 = %s %d details, want %s", r.Reason, len(r.ReasonDetails), response.ErrAgentInstallOS)
	}
	id1, like := r.ReasonDetails["os_id"], r.ReasonDetails["os_like"]
	if len(id1) == 0 || len(id1) > maxDetail || !strings.HasPrefix(id1, "xAAA") {
		t.Fatalf("os_id is %d bytes, want 1 to %d of the server's", len(id1), maxDetail)
	}
	if len(like) == 0 || len(like) > maxDetail || !utf8.ValidString(like) || !strings.HasPrefix(like, "데비안") {
		t.Fatalf("os_like is %d bytes (valid UTF-8 %v), want 1 to %d cut on a rune", len(like), utf8.ValidString(like), maxDetail)
	}
}

// endFailEnv fails the install's ending head writes, failures of them, with
// raft.Apply's enqueue timeout: an error other than a lost leadership.
type endFailEnv struct {
	*fakeEnv
	mu       sync.Mutex
	failures int
	failed   int
}

func (f *endFailEnv) PutRun(r cluster.RunRecord) error {
	f.mu.Lock()
	if !r.Active() && f.failed < f.failures {
		f.failed++
		f.mu.Unlock()
		return errors.New("timed out enqueuing operation")
	}
	f.mu.Unlock()
	return f.fakeEnv.PutRun(r)
}

// An install's ending head write that fails is written again, so the run
// does not stay running in the record while the engine, idle, owns it: the
// supervisor would skip it, and every other run would be refused.
func TestTheEndingHeadWriteIsWrittenAgain(t *testing.T) {
	t.Run("by the retry", func(t *testing.T) {
		base := newFakeEnv()
		env := &endFailEnv{fakeEnv: base, failures: 1}
		s := passingServer(t, base, 1)
		e := testEngineOver(env, base, newFakeDialer(s))
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, base, id, "s1")
		h := base.waitEnded(t, id)
		if h.Status != cluster.RunSucceeded || h.EndedAt.IsZero() || e.Active() {
			t.Fatalf("run = %s ended %v, engine active %v; want succeeded and idle", h.Status, h.EndedAt, e.Active())
		}
		env.mu.Lock()
		defer env.mu.Unlock()
		if env.failed != 1 {
			t.Fatalf("%d ending writes failed, want the one", env.failed)
		}
	})
	t.Run("by a Stop on the ended install", func(t *testing.T) {
		base := newFakeEnv()
		env := &endFailEnv{fakeEnv: base, failures: 1}
		s := passingServer(t, base, 1)
		e := testEngineOver(env, base, newFakeDialer(s))
		// No retry comes in the test's time: only the Stop writes the end.
		e.t.retry, e.t.retryMax = time.Hour, time.Hour
		id, err := e.Start(context.Background(), request(s))
		if err != nil {
			t.Fatal(err)
		}
		approveAll(t, e, base, id, "s1")
		waitFor(t, "the ending write to fail", func() bool {
			env.mu.Lock()
			defer env.mu.Unlock()
			return env.failed == 1
		})
		if h := base.head(id); !h.Active() || e.Active() {
			t.Fatalf("before the Stop: run %s, engine active %v; want the record left running by an idle engine", h.Status, e.Active())
		}
		if err := e.Stop(id, "admin"); err != nil {
			t.Fatal(err)
		}
		if h := base.head(id); h.Status != cluster.RunSucceeded || h.EndedAt.IsZero() {
			t.Fatalf("after the Stop: run %s ended %v, want succeeded", h.Status, h.EndedAt)
		}
	})
}

// A Stop that ends a read the server never answers leaves the row's
// connection spent (sshexec.Client.Run closes it). The clean-up dials the
// server again, against the approved key, and stops and purges the install
// over the new connection.
func TestAStopDuringAStalledReadCleansOverANewConnection(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	s.installHang, s.stallReads = true, true
	d := newFakeDialer(s)
	e := newTestEngine(env, d)
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	waitFor(t, "a read of the install's progress to stall", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.stalled > 0
	})
	if err := e.Stop(id, "admin"); err != nil {
		t.Fatal(err)
	}
	env.waitEnded(t, id)
	if r := env.row(id, "s1"); r.Status != cluster.RunNodeStopped || r.NodeID != "" {
		t.Fatalf("s1 = %s %q, want stopped with no node", r.Status, r.NodeID)
	}
	if dials, open, _ := d.counts(); dials != 2 || open != 0 {
		t.Fatalf("%d logins, %d open; want a second login for the clean-up, closed", dials, open)
	}
	for _, c := range []string{"systemctl stop 'sfpanel-agent-install-", "uninstall --purge", "rm -rf "} {
		if !s.rootRan(c) {
			t.Errorf("the clean-up ran no %q: commands %q", c, s.commands())
		}
	}
}

// install.log is the target's to write: each poll reads at most 1 MiB of
// it, from where the last read ended, so a log of any size is read in
// pieces with its offset right.
func TestTheInstallLogIsReadInPieces(t *testing.T) {
	env := newFakeEnv()
	s := passingServer(t, env, 1)
	s.installLog = bytes.Repeat([]byte("installing\n"), (5<<20)/2/11)
	e := newTestEngine(env, newFakeDialer(s))
	// Sanitizing a MiB of log is slow under the race detector.
	e.t.installLimit = time.Minute
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	approveAll(t, e, env, id, "s1")
	for deadline := time.Now().Add(time.Minute); env.head(id).Active(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("waited a minute for the run to end")
		}
	}
	if h := env.head(id); h.Status != cluster.RunSucceeded {
		t.Fatalf("run = %s", h.Status)
	}
	for _, c := range s.commands() {
		if strings.HasPrefix(c, "tail -c +") && !strings.HasSuffix(c, " | head -c 1048576") {
			t.Fatalf("the log was read by %q, want at most 1 MiB a read", c)
		}
	}
	s.mu.Lock()
	tails := slices.Clone(s.tails)
	s.mu.Unlock()
	// Its exit status is there at the first poll; the log is read to its
	// end all the same, one piece a poll.
	want := []int{1, 1 + 1<<20, 1 + 2<<20}
	if !slices.Equal(tails, want) {
		t.Fatalf("the log (%d bytes) was read from %v, want from %v", len(s.installLog), tails, want)
	}
}
