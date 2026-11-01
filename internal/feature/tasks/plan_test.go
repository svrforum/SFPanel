package featuretasks

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/tasks"
)

func parse(t *testing.T, doc string) *tasks.Task {
	t.Helper()
	task, problems := tasks.Parse([]byte(doc))
	if task == nil {
		t.Fatalf("the test's task does not parse: %+v", problems)
	}
	return task
}

func grouped(n cluster.Node, group string) cluster.Node {
	n.Labels = map[string]string{"group": group}
	return n
}

func TestPlanOrdersAgentsInBatchesThenManagersThenTheLeader(t *testing.T) {
	// IDs sort the other way from names: the order must follow the names.
	e := newFakeEnv(t, "m-a",
		manager("m-a", "leader"), manager("m-b", "mgr-z"), manager("m-c", "mgr-y"),
		agent("a-1", "web-e"), agent("a-2", "web-d"), agent("a-3", "web-c"), agent("a-4", "web-b"), agent("a-5", "web-a"))
	e.checks["a-5"] = []tasks.CheckVerdict{{Step: 0, NeedsConfirm: true}}
	e.checkErr["m-b"] = errors.New("deadline exceeded")

	plan, err := planRun(e, parse(t, steps(true, cmdTrue)), tasks.Targets{Kinds: []string{"agent", "manager"}}, nil, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a-5", "a-4"}, {"a-3", "a-2"}, {"a-1"}, {"m-c"}, {"m-b"}, {"m-a"}}
	if got := plan.WaveIDs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("waves = %v, want %v", got, want)
	}
	if len(plan.Left) != 0 {
		t.Errorf("left out = %+v, want none", plan.Left)
	}
	if len(plan.Waves) != len(want) {
		t.FailNow()
	}
	last := plan.Waves[len(plan.Waves)-1][0]
	if !last.Leader || last.Kind != "manager" {
		t.Errorf("the last wave = %+v, want the leader", last)
	}
	if v := plan.Waves[0][0].Verdicts; len(v) != 1 || !v[0].NeedsConfirm {
		t.Errorf("web-a's verdicts = %+v, want its step needing confirmation", v)
	}
	if got := plan.Waves[4][0].CheckError; got != "deadline exceeded" {
		t.Errorf("mgr-z's check error = %q", got)
	}
}

func TestPlanTargetsTheUnionOfGroupsKindsAndNodes(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"),
		grouped(agent("a1", "web-1"), "web"), grouped(agent("a2", "db-1"), "db"), agent("a3", "other"), manager("m2", "mgr-2"))
	plan, err := planRun(e, parse(t, steps(true, cmdTrue)), tasks.Targets{Groups: []string{"web"}, Nodes: []string{"a3", "m2"}}, nil, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a3", "a1"}, {"m2"}} // "other" before "web-1"
	if got := plan.WaveIDs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("waves = %v, want %v", got, want)
	}
}

func TestPlanSaysWhyANodeIsLeftOut(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		setup        func(e *fakeEnv)
	}{
		{"an offline agent", LeftOffline, func(e *fakeEnv) { e.health["a1"] = cluster.StatusOffline }},
		{"a suspect agent", LeftOffline, func(e *fakeEnv) { e.health["a1"] = cluster.StatusSuspect }},
		{"an agent on an old release", LeftOutdated, func(e *fakeEnv) { e.outdated["a1"] = true }},
		{"an agent this leader does not reach", LeftUnreachable, func(e *fakeEnv) { e.unreachable["a1"] = true }},
		{"an agent another run holds", LeftBusy, func(e *fakeEnv) {
			_ = e.store.PutRun(cluster.RunRecord{ID: "20261101-110000-000001", Status: cluster.RunRunning, Nodes: []string{"a1"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"), agent("a2", "web-2"))
			tc.setup(e)
			plan, err := planRun(e, parse(t, steps(true, cmdTrue)), tasks.Targets{Kinds: []string{"agent"}}, nil, 1, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Left) != 1 || plan.Left[0].ID != "a1" || plan.Left[0].Reason != tc.reason {
				t.Fatalf("left out = %+v, want a1 for %q", plan.Left, tc.reason)
			}
			if tc.reason == LeftBusy && plan.Left[0].RunID != "20261101-110000-000001" {
				t.Errorf("the busy node names run %q", plan.Left[0].RunID)
			}
			if got := plan.NodeIDs(); !slices.Equal(got, []string{"a2"}) {
				t.Errorf("planned = %v, want [a2]", got)
			}
		})
	}
	t.Run("a run that has ended holds nothing", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
		_ = e.store.PutRun(cluster.RunRecord{ID: "20261101-110000-000001", Status: cluster.RunFailed, Nodes: []string{"a1"}})
		plan, err := planRun(e, parse(t, steps(true, cmdTrue)), tasks.Targets{Kinds: []string{"agent"}}, nil, 1, "")
		if err != nil || len(plan.Left) != 0 {
			t.Errorf("plan = %+v, %v; want a1 planned", plan, err)
		}
	})
	t.Run("a node ID nobody has", func(t *testing.T) {
		e := newFakeEnv(t, "m1", manager("m1", "leader"))
		plan, err := planRun(e, parse(t, steps(true, cmdTrue)), tasks.Targets{Nodes: []string{"gone"}}, nil, 1, "")
		if err != nil || len(plan.Left) != 1 || plan.Left[0].Reason != LeftUnknown {
			t.Errorf("plan = %+v, %v; want gone left out as unknown", plan, err)
		}
	})
}

func TestPlanCapsTargetsAt200(t *testing.T) {
	nodes := []cluster.Node{manager("m1", "leader")}
	for i := range 200 {
		nodes = append(nodes, agent(fmt.Sprintf("a%03d", i), fmt.Sprintf("web-%03d", i)))
	}
	e := newFakeEnv(t, "m1", nodes...)
	task := parse(t, steps(true, cmdTrue))
	if _, err := planRun(e, task, tasks.Targets{Kinds: []string{"agent"}}, nil, 10, ""); err != nil {
		t.Fatalf("200 targets: %v", err)
	}
	if _, err := planRun(e, task, tasks.Targets{Kinds: []string{"agent", "manager"}}, nil, 10, ""); !errors.Is(err, ErrTooManyTargets) {
		t.Fatalf("201 targets: %v, want ErrTooManyTargets", err)
	}
}

func TestPlanRefusesAMissingInput(t *testing.T) {
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	doc := "name: t\ninputs:\n  svc: { required: true }\nsteps:\n  - service: { name: \"{{ svc }}\", action: restart }\n"
	_, err := planRun(e, parse(t, doc), tasks.Targets{Kinds: []string{"agent"}}, nil, 1, "")
	if !errors.Is(err, tasks.ErrInputMissing) {
		t.Fatalf("err = %v, want ErrInputMissing", err)
	}
}
