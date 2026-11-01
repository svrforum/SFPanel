package agentinstall

import (
	"context"
	"errors"
	"testing"

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
