package agentinstall

import (
	"context"
	"errors"
	"strings"
	"testing"
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
