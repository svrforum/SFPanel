package featuretasks

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const zabbixDoc = "name: zbx\nsteps:\n  - agent: { name: zabbix }\n"

// agentValues are connection values as the agents page saves them:
// normalized.
func agentValues() *serveragents.Settings {
	return &serveragents.Settings{
		Zabbix: serveragents.ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10:10051", HostMetadata: "linux"},
		Wazuh:  serveragents.WazuhSettings{Manager: "198.51.100.5", Version: serveragents.DefaultWazuhVersion},
	}
}

// wantAgentSpec checks that spec deploys the zabbix agent on the node named
// name, with values.
func wantAgentSpec(t *testing.T, what string, spec tasks.JobSpec, name string, values serveragents.Settings) {
	t.Helper()
	a := spec.Agent
	if a == nil || a.Name != serveragents.Zabbix || a.NodeName != name || a.Settings == nil || !reflect.DeepEqual(*a.Settings, values) {
		t.Errorf("%s = %+v, want the zabbix agent on %s with %+v", what, a, name, values)
	}
}

func TestAPlanLeavesOutANodeThatDoesNotRunAgentSteps(t *testing.T) {
	e := newFakeEnv(t, "m-1", manager("m-1", "node-a"), agent("a-1", "agent-1"), agent("a-2", "agent-2"))
	e.lacks["a-2"] = []string{cluster.CapServerAgentStep}
	e.agentSettings = agentValues()
	all := tasks.Targets{Kinds: []string{"manager", "agent"}}

	plan, err := planRun(e, parse(t, zabbixDoc), all, nil, 1, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Left) != 1 || plan.Left[0].ID != "a-2" || plan.Left[0].Reason != "step_unsupported" {
		t.Errorf("left out = %+v, want a-2 alone, as step_unsupported", plan.Left)
	}
	if got := fmt.Sprint(plan.WaveIDs()); got != "[[a-1] [m-1]]" {
		t.Errorf("waves = %s, want [[a-1] [m-1]]", got)
	}
	if _, asked := e.checked["a-2"]; asked {
		t.Errorf("a-2 was asked for its verdicts on a step it cannot run")
	}

	plan, err = planRun(e, parse(t, steps(true, cmdTrue)), all, nil, 1, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(plan.WaveIDs()); len(plan.Left) != 0 || got != "[[a-1] [a-2] [m-1]]" {
		t.Errorf("a command task: waves = %s, left out = %+v; want a-2 in a wave", got, plan.Left)
	}
}

func TestARunWithAnAgentStepWaitsForEveryManagerToKnowIt(t *testing.T) {
	f := newAPI(t)
	f.env.agentSettings = agentValues()
	f.store("zbx", zabbixDoc)
	f.store("t1", greetDoc)
	f.env.store.notReadyFor = map[string][]string{cluster.CapServerAgentStep: {"m-2 (0.97.0)"}}
	zbx := map[string]any{"task_id": "zbx", "targets": map[string]any{"kinds": []string{"agent"}}}
	preview := func() []string {
		t.Helper()
		a := f.do("POST", "/api/v1/cluster/task-runs/plan", zbx)
		if a.Status != http.StatusOK {
			t.Fatalf("plan = %d %+v", a.Status, a.Error)
		}
		var p struct {
			ManagersNotReady []string `json:"managers_not_ready"`
		}
		a.data(t, &p)
		return p.ManagersNotReady
	}

	a := f.do("POST", "/api/v1/cluster/task-runs", zbx)
	wantRefusal(t, "a run with an agent step", a, http.StatusConflict, response.ErrTaskManagersNotReady, map[string]string{"managers": "m-2 (0.97.0)"})
	if runs, _ := f.env.store.ListRuns(); len(runs) != 0 {
		t.Errorf("runs = %+v, want none", runs)
	}
	if got := preview(); !slices.Equal(got, []string{"m-2 (0.97.0)"}) {
		t.Errorf("managers_not_ready = %v, want [m-2 (0.97.0)]", got)
	}

	// A manager missing both is named once, and the names stay in order.
	f.env.store.notReady = []string{"m-3 (offline)"}
	f.env.store.notReadyFor[cluster.CapServerAgentStep] = []string{"m-3 (offline)", "m-2 (0.97.0)"}
	if got := preview(); !slices.Equal(got, []string{"m-2 (0.97.0)", "m-3 (offline)"}) {
		t.Errorf("managers_not_ready = %v, want [m-2 (0.97.0) m-3 (offline)]", got)
	}

	f.env.store.notReady = nil
	a = f.do("POST", "/api/v1/cluster/task-runs", map[string]any{"task_id": "t1", "inputs": map[string]string{"who": "x"}})
	if a.Status != http.StatusAccepted {
		t.Errorf("a command task = %d %+v, want 202: it needs no agent step", a.Status, a.Error)
	}
}

func TestAnAgentStepCarriesTheClusterValuesAndItsNodesName(t *testing.T) {
	e := newFakeEnv(t, "m-1", manager("m-1", "node-a"), agent("a-1", "agent-1"))
	e.agentSettings = agentValues()

	if _, err := planRun(e, parse(t, zabbixDoc), tasks.Targets{Kinds: []string{"manager", "agent"}}, nil, 1, "operator"); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[string]string{"a-1": "agent-1", "m-1": "node-a"} {
		if specs := e.checked[id]; len(specs) != 1 {
			t.Errorf("%s checked %+v, want one step", id, specs)
		} else {
			wantAgentSpec(t, id+"'s step as checked", specs[0], name, *e.agentSettings)
		}
	}

	mustRun(t, e, newRun(t, e, zabbixDoc, [][]string{{"a-1"}, {"m-1"}}))
	wantStarted(t, e, "a-1/0", "m-1/0")
	wantAgentSpec(t, "a-1's step", e.specs[key("a-1", 0)], "agent-1", *e.agentSettings)
	wantAgentSpec(t, "m-1's step", e.specs[key("m-1", 0)], "node-a", *e.agentSettings)
	wantHead(t, e, cluster.RunSucceeded, "")
}

// A node checks the values exactly as it gets them, so the leader sends them
// normalized whatever the store holds.
func TestAnAgentStepCarriesTheValuesNormalized(t *testing.T) {
	e := newFakeEnv(t, "m-1", manager("m-1", "node-a"), agent("a-1", "agent-1"))
	e.agentSettings = &serveragents.Settings{
		Zabbix: serveragents.ZabbixSettings{Server: " 192.0.2.10 , ,192.0.2.11 ", ServerActive: " 192.0.2.10 "},
		Wazuh:  serveragents.WazuhSettings{Manager: " 198.51.100.5 "},
	}
	mustRun(t, e, newRun(t, e, zabbixDoc, [][]string{{"a-1"}}))

	want := serveragents.Settings{
		Zabbix: serveragents.ZabbixSettings{Server: "192.0.2.10,192.0.2.11", ServerActive: "192.0.2.10"},
		Wazuh:  serveragents.WazuhSettings{Manager: "198.51.100.5", Version: serveragents.DefaultWazuhVersion},
	}
	wantAgentSpec(t, "a-1's step", e.specs[key("a-1", 0)], "agent-1", want)
	if a := e.specs[key("a-1", 0)].Agent; a != nil && a.Settings != nil {
		if ferr := a.Settings.Validate(); ferr != nil {
			t.Errorf("the values sent fail the node's own check: %v", ferr)
		}
	}
}

func TestAnAgentStepWithoutValuesIsRefusedBeforeTheRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values *serveragents.Settings
	}{
		{"nothing saved", nil},
		{"only Wazuh's values", &serveragents.Settings{Wazuh: serveragents.WazuhSettings{Manager: "198.51.100.5", Version: serveragents.DefaultWazuhVersion}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPI(t)
			f.env.agentSettings = tc.values
			f.store("zbx", zabbixDoc)
			body := map[string]any{"task_id": "zbx", "targets": map[string]any{"kinds": []string{"agent"}}}
			for _, path := range []string{"/api/v1/cluster/task-runs/plan", "/api/v1/cluster/task-runs"} {
				a := f.do("POST", path, body)
				wantRefusal(t, path, a, http.StatusBadRequest, response.ErrServerAgentNotConfigured, map[string]string{"agent": "zabbix"})
				if a.Error != nil && a.Error.Message != "Save the zabbix connection values on the agents page first" {
					t.Errorf("%s: message = %q", path, a.Error.Message)
				}
			}
			if runs, _ := f.env.store.ListRuns(); len(runs) != 0 {
				t.Errorf("runs = %+v, want none", runs)
			}
			if len(f.env.checkedBy) != 0 {
				t.Errorf("nodes asked for verdicts: %v, want none", f.env.checkedBy)
			}
		})
	}

	// A rerun, or values cleared once the run started: the node fails at
	// its turn, and nothing is posted to it.
	t.Run("at the node's turn", func(t *testing.T) {
		e := newFakeEnv(t, "m-1", manager("m-1", "node-a"), agent("a-1", "agent-1"))
		mustRun(t, e, newRun(t, e, zabbixDoc, [][]string{{"a-1"}}))
		wantNode(t, e, "a-1", cluster.RunNodeFailed, "the zabbix agent's connection values are not set")
		if posts := e.postList(); len(posts) != 0 {
			t.Errorf("posted %v, want nothing", posts)
		}
		wantHead(t, e, cluster.RunFailed, "")
	})
}

func TestASkippedAgentIsASkippedStep(t *testing.T) {
	const why = "skipped: the old Zabbix agent (agentd) is installed"
	e := newFakeEnv(t, "m-1", manager("m-1", "node-a"), agent("a-1", "agent-1"))
	e.agentSettings = agentValues()
	e.refuse[key("a-1", 0)] = &jobRefusal{Status: http.StatusConflict, Code: response.ErrServerAgentSkipped, Message: why,
		Details: map[string]string{"agent": "zabbix", "why": "legacy"}}
	mustRun(t, e, newRun(t, e, zabbixDoc, [][]string{{"a-1"}, {"m-1"}}))

	rec := wantNode(t, e, "a-1", cluster.RunNodeSucceeded, "")
	wantStep(t, rec, 0, cluster.RunStepSkipped, why)
	if len(rec.Steps) == 1 && rec.Steps[0].Summary != why {
		t.Errorf("a-1's step says %q, want %q", rec.Steps[0].Summary, why)
	}
	wantStarted(t, e, "m-1/0")
	wantHead(t, e, cluster.RunSucceeded, "")
}

func TestValuesSavedDuringARunReachTheNodesNotYetStarted(t *testing.T) {
	e := newFakeEnv(t, "m-1", manager("m-1", "node-a"), agent("a-1", "agent-1"))
	before, after := agentValues(), agentValues()
	after.Zabbix.Server, after.Zabbix.ServerActive = "198.51.100.20", "198.51.100.20:10051"
	e.agentSettings = before
	e.onStart = func(node string, _ tasks.JobSpec) {
		if node == "a-1" {
			e.mu.Lock()
			e.agentSettings = after
			e.mu.Unlock()
		}
	}
	mustRun(t, e, newRun(t, e, zabbixDoc, [][]string{{"a-1"}, {"m-1"}}))

	wantAgentSpec(t, "a-1's step", e.specs[key("a-1", 0)], "agent-1", *before)
	wantAgentSpec(t, "m-1's step", e.specs[key("m-1", 0)], "node-a", *after)
}

// A rerun of a run with an agent step waits for the managers and leaves out
// the nodes that cannot run it, as a start does.
func TestARerunOfAnAgentStepWaitsAndLeavesOutAsAStartDoes(t *testing.T) {
	f := newAPI(t)
	f.env.agentSettings = agentValues()
	rev, _ := f.env.store.PutTaskRevision(zabbixDoc)
	src := cluster.RunRecord{ID: srcRun, TaskID: "zbx", TaskName: "zbx", Rev: rev, Nodes: []string{"a1", "a2"},
		Waves: [][]string{{"a1", "a2"}}, Batch: 2, StopOnFailure: true, Status: cluster.RunFailed, StartedBy: "operator"}
	if err := f.env.store.PutRun(src); err != nil {
		t.Fatal(err)
	}
	rerun := func() answer {
		return f.do("POST", "/api/v1/cluster/task-runs/"+srcRun+"/rerun?only=failed", nil)
	}

	f.env.store.notReadyFor = map[string][]string{cluster.CapServerAgentStep: {"m-2 (0.97.0)"}}
	wantRefusal(t, "the rerun", rerun(), http.StatusConflict, response.ErrTaskManagersNotReady, map[string]string{"managers": "m-2 (0.97.0)"})

	f.env.store.notReadyFor = nil
	f.env.lacks["a2"] = []string{cluster.CapServerAgentStep}
	a := rerun()
	if a.Status != http.StatusAccepted {
		t.Fatalf("the rerun = %d %+v, want 202", a.Status, a.Error)
	}
	var d struct {
		RunID string `json:"run_id"`
	}
	a.data(t, &d)
	if got := f.env.store.head(d.RunID).Nodes; !slices.Equal(got, []string{"a1"}) {
		t.Errorf("the rerun's nodes = %v, want [a1]: a2 does not run agent steps", got)
	}
}
