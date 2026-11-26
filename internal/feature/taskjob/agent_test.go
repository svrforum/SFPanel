package taskjob

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	featureserveragents "github.com/svrforum/SFPanel/internal/feature/serveragents"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// fakeDeployer is the server-agents module: Skip answers with skip, and
// Deploy with deploy, or a success that changed nothing.
type fakeDeployer struct {
	mu     sync.Mutex
	skip   *featureserveragents.Skip
	deploy func(ctx context.Context, step tasks.AgentStep, io featureserveragents.JobIO) featureserveragents.Outcome
	calls  int
	steps  []tasks.AgentStep
}

func (f *fakeDeployer) Skip(string, *serveragents.Settings) *featureserveragents.Skip {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.skip
}

func (f *fakeDeployer) Deploy(ctx context.Context, step tasks.AgentStep, io featureserveragents.JobIO) featureserveragents.Outcome {
	f.mu.Lock()
	f.calls++
	f.steps = append(f.steps, step)
	deploy := f.deploy
	f.mu.Unlock()
	if deploy == nil {
		return featureserveragents.Outcome{Status: tasks.JobSucceeded}
	}
	return deploy(ctx, step, io)
}

func (f *fakeDeployer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newAgentNode is a node whose agent steps run through the real runner, on
// the deployer and package transactions the test plays.
func newAgentNode(t *testing.T, d AgentDeployer, pkgs PackageTransactions) *testNode {
	t.Helper()
	n := newTestNode(t)
	r := NewAgentRunner(d, pkgs)
	r.poll = time.Millisecond
	n.h.Register(tasks.StepAgent, r)
	return n
}

// agentSpec is an agent step of run, at step 0, for node-a.
func agentSpec(run, name string, settings *serveragents.Settings) tasks.JobSpec {
	s := spec(run, 0)
	s.Kind, s.Command = tasks.StepAgent, nil
	s.Agent = &tasks.AgentStep{Name: name, NodeName: "node-a", Settings: settings}
	return s
}

// zabbixValues are connection values with Zabbix set and Wazuh not.
func zabbixValues() *serveragents.Settings {
	return &serveragents.Settings{Zabbix: serveragents.ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10:10051"}}
}

// recvErr waits for what a deploy's apt call returned.
func recvErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the deploy's apt call did not return")
		return nil
	}
}

// Connection values are checked again on the node before anything is
// recorded, as the field that is wrong: a Server value with a line break in
// it would add a line of its own to zabbix_agent2.conf — here a
// UserParameter, a command the agent runs for its server. The deploy is
// never reached, and the preview refuses it for the same reason.
func TestAnAgentJobRefusesValuesThatCouldBreakAConfigFile(t *testing.T) {
	d := &fakeDeployer{}
	n := newAgentNode(t, d, newFakePackages(t))
	values := &serveragents.Settings{Zabbix: serveragents.ZabbixSettings{Server: "192.0.2.10\nUserParameter=x,id", ServerActive: "192.0.2.10"}}
	s := agentSpec(runA, serveragents.Zabbix, values)

	status, env := n.start(t, s)
	if status != http.StatusBadRequest || env.code() != response.ErrInvalidRequest || !strings.Contains(env.Error.Message, "zabbix.server") {
		t.Fatalf("start: %d %+v, want 400 %s naming zabbix.server", status, env.Error, response.ErrInvalidRequest)
	}
	if got := d.callCount(); got != 0 {
		t.Fatalf("the deploy ran %d times for refused values", got)
	}
	if status, env := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+s.ID, nil, true); status != http.StatusNotFound || env.code() != response.ErrTaskJobNotFound {
		t.Fatalf("the refused job: %d %q, want 404 %s: nothing recorded", status, env.code(), response.ErrTaskJobNotFound)
	}

	_, env = n.do(t, http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{s}, true)
	if v := verdicts(t, env); len(v) != 1 || !v[0].Refused || v[0].Code != response.ErrInvalidRequest || !strings.Contains(v[0].Reason, "zabbix.server") {
		t.Fatalf("verdict %+v, want refused %s naming zabbix.server", v, response.ErrInvalidRequest)
	}
}

// A step needs values for its own agent: none at all, values with only the
// other agent's, or an agent the panel does not deploy are each refused,
// saying which, before anything is recorded.
func TestAnAgentJobWithoutValuesIsRefused(t *testing.T) {
	d := &fakeDeployer{}
	n := newAgentNode(t, d, newFakePackages(t))
	wazuhOnly := &serveragents.Settings{Wazuh: serveragents.WazuhSettings{Manager: "198.51.100.5", Version: "4.14.10"}}
	for _, tc := range []struct {
		label  string
		spec   tasks.JobSpec
		reason string
	}{
		{"no values", agentSpec(runA, serveragents.Zabbix, nil), "no connection values"},
		{"the other agent's values", agentSpec(runA, serveragents.Zabbix, wazuhOnly), "no zabbix connection values"},
		{"an agent the panel does not deploy", agentSpec(runA, "nagios", zabbixValues()), `"nagios" is not an agent`},
	} {
		status, env := n.start(t, tc.spec)
		if status != http.StatusBadRequest || env.code() != response.ErrInvalidRequest || !strings.Contains(env.Error.Message, tc.reason) {
			t.Errorf("%s: %d %+v, want 400 %s saying %q", tc.label, status, env.Error, response.ErrInvalidRequest, tc.reason)
		}
		_, env = n.do(t, http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{tc.spec}, true)
		if v := verdicts(t, env); len(v) != 1 || !v[0].Refused || v[0].Code != response.ErrInvalidRequest || !strings.Contains(v[0].Reason, tc.reason) {
			t.Errorf("%s: verdict %+v, want refused %s saying %q", tc.label, v, response.ErrInvalidRequest, tc.reason)
		}
	}
	if got := d.callCount(); got != 0 {
		t.Fatalf("the deploy ran %d times", got)
	}
	if status, _ := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+runA+"-0", nil, true); status != http.StatusNotFound {
		t.Fatalf("a refused job was recorded: GET %d", status)
	}
}

// A node that leaves the agent alone says so before a job is recorded, with
// the reason as details the engine records the step skipped with; the
// preview gives the same verdict.
func TestASkippedAgentIsRefusedWithItsReason(t *testing.T) {
	why := map[string]string{"agent": serveragents.Zabbix, "why": featureserveragents.WhyLegacy}
	d := &fakeDeployer{skip: &featureserveragents.Skip{
		Why:     featureserveragents.WhyLegacy,
		Message: "skipped: the old Zabbix agent (zabbix-agent) is here",
		Details: maps.Clone(why),
	}}
	n := newAgentNode(t, d, newFakePackages(t))
	s := agentSpec(runA, serveragents.Zabbix, zabbixValues())

	status, env := n.start(t, s)
	if status != http.StatusConflict || env.code() != response.ErrServerAgentSkipped || !maps.Equal(env.Error.Details, why) {
		t.Fatalf("start: %d %+v, want 409 %s with details %v", status, env.Error, response.ErrServerAgentSkipped, why)
	}
	if got := d.callCount(); got != 0 {
		t.Fatalf("the deploy ran %d times on a node that skips the agent", got)
	}
	if status, _ := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+s.ID, nil, true); status != http.StatusNotFound {
		t.Fatalf("a skipped job was recorded: GET %d", status)
	}

	status, env = n.do(t, http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{s}, true)
	if status != http.StatusOK {
		t.Fatalf("check: %d %q", status, env.code())
	}
	v := verdicts(t, env)
	if len(v) != 1 || !v[0].Refused || v[0].Code != response.ErrServerAgentSkipped || !maps.Equal(v[0].Details, why) || !strings.Contains(v[0].Reason, "old Zabbix agent") {
		t.Fatalf("verdict %+v, want refused %s with details %v and the reason", v, response.ErrServerAgentSkipped, why)
	}
}

// The job ends as the deploy says, and what the deploy logs is in the job's
// output under the agent's name. The deploy gets the step as the leader sent
// it.
func TestAnAgentJobEndsAsTheDeploySays(t *testing.T) {
	d := &fakeDeployer{deploy: func(_ context.Context, _ tasks.AgentStep, io featureserveragents.JobIO) featureserveragents.Outcome {
		io.Logf("%s added", "repository")
		return featureserveragents.Outcome{Status: tasks.JobSucceeded, Changed: true, Summary: "installed 7.0.22, configured, connected to 192.0.2.10:10051"}
	}}
	n := newAgentNode(t, d, newFakePackages(t))
	values := zabbixValues()
	s := agentSpec(runA, serveragents.Zabbix, values)
	startOK(t, n, s)
	n.ended(t, s.ID)

	_, env := n.do(t, http.MethodGet, "/api/v1/task-jobs/"+s.ID, nil, true)
	v := view(t, env)
	if v.Status != tasks.JobSucceeded || !v.Changed || v.Summary != "installed 7.0.22, configured, connected to 192.0.2.10:10051" {
		t.Errorf("job %s changed=%v %q, want succeeded, changed and the deploy's summary", v.Status, v.Changed, v.Summary)
	}
	if !strings.Contains(v.Output, "[zabbix] repository added\n") {
		t.Errorf("output %q, want the deploy's line under [zabbix]", v.Output)
	}
	d.mu.Lock()
	steps := slices.Clone(d.steps)
	d.mu.Unlock()
	if len(steps) != 1 || steps[0].Name != serveragents.Zabbix || steps[0].NodeName != "node-a" || steps[0].Settings == nil || steps[0].Settings.Zabbix != values.Zabbix {
		t.Errorf("the deploy got %+v, want the step as sent", steps)
	}

	// A failed deploy is a failed job, with its summary.
	d2 := &fakeDeployer{deploy: func(context.Context, tasks.AgentStep, featureserveragents.JobIO) featureserveragents.Outcome {
		return featureserveragents.Outcome{Status: tasks.JobFailed, Summary: "not connected after 1m0s: unreachable"}
	}}
	n2 := newAgentNode(t, d2, newFakePackages(t))
	startOK(t, n2, s)
	if r := n2.ended(t, s.ID); r.status != tasks.JobFailed || r.changed || r.summary != "not connected after 1m0s: unreachable" {
		t.Errorf("job ended %s changed=%v %q, want failed with the deploy's summary", r.status, r.changed, r.summary)
	}
}

// apt in a deploy is the packages module's transaction, the one the Packages
// page and a packages step use: its output goes to the job's output, and it
// fails with apt's exit status or the module's refusal.
func TestAptInAnAgentJobIsThePackagesTransaction(t *testing.T) {
	aptDeploy := func(errs chan<- error) func(context.Context, tasks.AgentStep, featureserveragents.JobIO) featureserveragents.Outcome {
		return func(ctx context.Context, _ tasks.AgentStep, io featureserveragents.JobIO) featureserveragents.Outcome {
			err := io.Apt(ctx, "install", []string{"zabbix-agent2"}, true)
			errs <- err
			if err != nil {
				return featureserveragents.Outcome{Status: tasks.JobFailed, Summary: err.Error()}
			}
			return featureserveragents.Outcome{Status: tasks.JobSucceeded, Changed: true, Summary: "installed"}
		}
	}

	pk := newFakePackages(t)
	errs := make(chan error, 1)
	n := newAgentNode(t, &fakeDeployer{deploy: aptDeploy(errs)}, pk)
	s := agentSpec(runA, serveragents.Zabbix, zabbixValues())
	startOK(t, n, s)
	c := pk.next(t, 0)
	if c.kind != "install" || !slices.Equal(c.names, []string{"zabbix-agent2"}) || !c.update {
		t.Fatalf("transaction %+v, want install of zabbix-agent2 with the lists updated first", c)
	}
	pk.end(t, c.id, "Setting up zabbix-agent2 (1:7.0.22-1+ubuntu24.04) ...\n", "success", 0)
	if err := recvErr(t, errs); err != nil {
		t.Fatalf("apt that succeeded returned %v", err)
	}
	if r := n.ended(t, s.ID); r.status != tasks.JobSucceeded {
		t.Errorf("job ended %s %q, want succeeded", r.status, r.summary)
	}
	log, _ := os.ReadFile(filepath.Join(n.h.LogDir, s.ID+".log"))
	for _, want := range []string{">>> install zabbix-agent2", "Setting up zabbix-agent2"} {
		if strings.Count(string(log), want) != 1 {
			t.Errorf("job log %q, want the transaction's %q once", log, want)
		}
	}

	// apt that failed: its exit status.
	pk2 := newFakePackages(t)
	errs2 := make(chan error, 1)
	n2 := newAgentNode(t, &fakeDeployer{deploy: aptDeploy(errs2)}, pk2)
	startOK(t, n2, s)
	pk2.end(t, pk2.next(t, 0).id, "E: Unable to locate package zabbix-agent2\n", "failed", 100)
	if err := recvErr(t, errs2); err == nil || err.Error() != "install failed with exit status 100" {
		t.Errorf("apt that failed returned %v, want install failed with exit status 100", err)
	}
	n2.ended(t, s.ID)

	// A transaction the module refuses: its code and message.
	pk3 := newFakePackages(t)
	if _, err := pk3.StartJobTransaction("upgrade", nil, false); err != nil {
		t.Fatal(err)
	}
	errs3 := make(chan error, 1)
	n3 := newAgentNode(t, &fakeDeployer{deploy: aptDeploy(errs3)}, pk3)
	startOK(t, n3, s)
	if err := recvErr(t, errs3); err == nil || !strings.HasPrefix(err.Error(), response.ErrAPTTransactionRunning+": already running") {
		t.Errorf("a refused transaction returned %v, want %s: and the module's message", err, response.ErrAPTTransactionRunning)
	}
	n3.ended(t, s.ID)
	if got := pk3.calls(); len(got) != 1 {
		t.Errorf("transactions %+v, want none started after the refusal", got)
	}
}

// A forced cancel ends the context the deploy runs under. apt in the middle
// of a transaction is not cut off: the transaction is followed to its end —
// dpkg stopped half way can be left half-configured — and only then does apt
// say it was cancelled.
func TestAForcedCancelReachesTheDeploy(t *testing.T) {
	ctxs := make(chan context.Context, 1)
	d := &fakeDeployer{deploy: func(ctx context.Context, _ tasks.AgentStep, _ featureserveragents.JobIO) featureserveragents.Outcome {
		ctxs <- ctx
		select {
		case <-ctx.Done():
			return featureserveragents.Outcome{Status: tasks.JobCancelled, Summary: "cancelled while waiting"}
		case <-time.After(5 * time.Second):
			return featureserveragents.Outcome{Status: tasks.JobSucceeded}
		}
	}}
	n := newAgentNode(t, d, newFakePackages(t))
	s := agentSpec(runA, serveragents.Zabbix, zabbixValues())
	startOK(t, n, s)
	var ctx context.Context
	select {
	case ctx = <-ctxs:
	case <-time.After(5 * time.Second):
		t.Fatal("the deploy was not started")
	}
	cancelURI := "/api/v1/task-jobs/" + s.ID + "/cancel"
	if status, env := n.do(t, http.MethodPost, cancelURI, map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel: %d %q", status, env.code())
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("a forced cancel left the deploy's context running")
	}
	if r := n.ended(t, s.ID); r.status != tasks.JobCancelled {
		t.Errorf("job ended %s %q, want cancelled", r.status, r.summary)
	}

	// In the middle of apt.
	pk := newFakePackages(t)
	errs := make(chan error, 1)
	d2 := &fakeDeployer{deploy: func(ctx context.Context, _ tasks.AgentStep, io featureserveragents.JobIO) featureserveragents.Outcome {
		err := io.Apt(ctx, "install", []string{"zabbix-agent2"}, true)
		errs <- err
		return featureserveragents.Outcome{Status: tasks.JobCancelled, Summary: "cancelled while installing"}
	}}
	n2 := newAgentNode(t, d2, pk)
	startOK(t, n2, s)
	tx := pk.next(t, 0)
	if status, env := n2.do(t, http.MethodPost, cancelURI, map[string]bool{"force": true}, true); status != http.StatusOK {
		t.Fatalf("forced cancel during apt: %d %q", status, env.code())
	}
	time.Sleep(50 * time.Millisecond) // past many of the runner's polls
	select {
	case err := <-errs:
		t.Fatalf("apt returned %v while its transaction still ran", err)
	default:
	}
	pk.end(t, tx.id, "Setting up zabbix-agent2 ...\n", "success", 0)
	if err := recvErr(t, errs); err == nil || !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "ran to its end") {
		t.Errorf("apt after a forced cancel returned %v, want cancelled once the transaction ran to its end", err)
	}
	log, _ := os.ReadFile(filepath.Join(n2.h.LogDir, s.ID+".log"))
	if !strings.Contains(string(log), "Setting up zabbix-agent2") {
		t.Errorf("job log %q, want the transaction's output to its end", log)
	}
	n2.ended(t, s.ID)
}

// An agent step carries its agent's settings and no other kind's, as every
// kind does.
func TestAnAgentSpecWithOtherSettingsIsRefused(t *testing.T) {
	d := &fakeDeployer{}
	n := newAgentNode(t, d, newFakePackages(t))
	withCommand := agentSpec(runA, serveragents.Zabbix, zabbixValues())
	withCommand.Command = &tasks.CommandStep{Run: "true"}
	withoutAgent := agentSpec(runA, serveragents.Zabbix, zabbixValues())
	withoutAgent.Agent = nil
	for _, tc := range []struct {
		label  string
		spec   tasks.JobSpec
		reason string
	}{
		{"command settings too", withCommand, "agent job carries command settings"},
		{"no agent settings", withoutAgent, "The job has no agent settings"},
	} {
		status, env := n.start(t, tc.spec)
		if status != http.StatusBadRequest || env.code() != response.ErrInvalidRequest || !strings.Contains(env.Error.Message, tc.reason) {
			t.Errorf("%s: %d %+v, want 400 %s saying %q", tc.label, status, env.Error, response.ErrInvalidRequest, tc.reason)
		}
	}
	if got := d.callCount(); got != 0 {
		t.Fatalf("the deploy ran %d times", got)
	}
	_, env := n.do(t, http.MethodPost, "/api/v1/task-jobs/check", []tasks.JobSpec{withCommand}, true)
	var v []tasks.CheckVerdict
	if err := json.Unmarshal(env.Data, &v); err != nil || len(v) != 1 || !v[0].Refused || !strings.Contains(v[0].Reason, "agent job carries command settings") {
		t.Fatalf("verdict %s (%v), want refused for the command settings", env.Data, err)
	}
}
