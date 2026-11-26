package tasks

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/serveragents"
)

func TestAnAgentStepNamesZabbixOrWazuh(t *testing.T) {
	for _, name := range []string{serveragents.Zabbix, serveragents.Wazuh} {
		task := mustParse(t, "name: t\nsteps:\n  - agent: { name: "+name+" }\n")
		want := Step{Kind: StepAgent, Agent: &AgentStep{Name: name}}
		if len(task.Steps) != 1 || !reflect.DeepEqual(task.Steps[0], want) {
			t.Errorf("agent %s parses to %+v, want %+v", name, task.Steps, want)
		}
	}

	// The document names the agent and nothing else: the server's name and
	// the connection values are the leader's to fill in, so a document that
	// tries to carry them is refused rather than trusted.
	cases := []struct {
		name string
		doc  string
		line int
		want string
	}{
		{
			name: "an agent the panel does not deploy",
			doc: `name: t
steps:
  - agent:
      name: nagios
`,
			line: 4, want: `agent "nagios" is not one of zabbix, wazuh`,
		},
		{
			name: "a reference for the agent",
			doc: `name: t
inputs:
  which: { default: zabbix }
steps:
  - agent: { name: "{{ which }}" }
`,
			line: 5, want: `agent "{{ which }}" is not one of zabbix, wazuh`,
		},
		{
			name: "connection values in the document",
			doc: `name: t
steps:
  - agent:
      name: zabbix
      settings: { zabbix: { server: 192.0.2.10 } }
`,
			line: 5, want: `unknown key "settings"`,
		},
		{
			name: "a server name in the document",
			doc: `name: t
steps:
  - agent:
      name: wazuh
      node_name: node-b
`,
			line: 5, want: `unknown key "node_name"`,
		},
		{
			name: "no agent named",
			doc: `name: t
steps:
  - agent: {}
`,
			line: 3, want: `"name" is required`,
		},
		{
			name: "an agent key with nothing after it",
			doc: `name: t
steps:
  - agent:
`,
			line: 3, want: `"name" is required`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, problems := Parse([]byte(tc.doc))
			if task != nil {
				t.Errorf("a document with problems still produced a task: %+v", task)
			}
			if len(problems) != 1 {
				t.Fatalf("problems = %+v, want exactly one", problems)
			}
			if problems[0].Line != tc.line || !strings.Contains(problems[0].Message, tc.want) {
				t.Errorf("problem = %+v, want line %d containing %q", problems[0], tc.line, tc.want)
			}
		})
	}
}

func TestHasAgentStepsFindsAnAgentStepAnywhere(t *testing.T) {
	with := mustParse(t, `name: t
steps:
  - command: { run: "true" }
  - agent: { name: wazuh }
`)
	without := mustParse(t, `name: t
steps:
  - command: { run: "true" }
`)
	if !HasAgentSteps(with) {
		t.Error("a task with an agent step as its second step: HasAgentSteps = false")
	}
	if HasAgentSteps(without) || HasAgentSteps(nil) {
		t.Error("a task without an agent step, or none: HasAgentSteps = true")
	}
}

func TestRenderNamesTheNodeItRendersFor(t *testing.T) {
	task := mustParse(t, "name: t\nsteps:\n  - agent: { name: zabbix }\n")

	a, err := Render(task, nil, NodeVars{Name: "node-a", Address: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Kind != StepAgent || a[0].Agent == nil || a[0].Agent.Name != serveragents.Zabbix || a[0].Agent.NodeName != "node-a" || a[0].Agent.Settings != nil {
		t.Fatalf("rendered for node-a: %+v, want the zabbix agent named node-a with no values yet", a[0].Agent)
	}

	// Each server gets its own name, never another's: the spec is a copy,
	// not the task's step, so rendering for the next server leaves the
	// first one's alone.
	b, err := Render(task, nil, NodeVars{Name: "agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	if b[0].Agent.NodeName != "agent-1" || a[0].Agent.NodeName != "node-a" || task.Steps[0].Agent.NodeName != "" {
		t.Errorf("after rendering for agent-1: agent-1 %q, node-a %q, the task %q", b[0].Agent.NodeName, a[0].Agent.NodeName, task.Steps[0].Agent.NodeName)
	}

	// Values the model happens to hold are never what a server gets: they
	// are filled in from the cluster's settings after rendering.
	task.Steps[0].Agent.NodeName = "node-b"
	task.Steps[0].Agent.Settings = &serveragents.Settings{Zabbix: serveragents.ZabbixSettings{Server: "198.51.100.9", ServerActive: "198.51.100.9"}}
	c, err := Render(task, nil, NodeVars{Name: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	if c[0].Agent.NodeName != "node-a" || c[0].Agent.Settings != nil {
		t.Errorf("rendered from a model carrying node-b and values: %+v, want node-a and no values", c[0].Agent)
	}
}

func TestFillAgentSettingsRefusesAnAgentWithoutValues(t *testing.T) {
	task := mustParse(t, "name: t\nsteps:\n  - agent: { name: zabbix }\n")
	for name, s := range map[string]*serveragents.Settings{
		"only wazuh set": {Wazuh: serveragents.WazuhSettings{Manager: "198.51.100.5"}},
		"nothing saved":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			specs, err := Render(task, nil, NodeVars{Name: "node-a"})
			if err != nil {
				t.Fatal(err)
			}
			err = FillAgentSettings(specs, s)
			var missing *AgentSettingsMissingError
			if !errors.As(err, &missing) || missing.Agent != serveragents.Zabbix || !errors.Is(err, ErrAgentSettingsMissing) {
				t.Fatalf("FillAgentSettings = %v, want the zabbix agent's values missing", err)
			}
			if specs[0].Agent.Settings != nil {
				t.Errorf("a refused fill still set %+v", specs[0].Agent.Settings)
			}
		})
	}

	// The refusal comes before anything is filled, so a step whose agent
	// has values is not left filled beside one that has none.
	both := mustParse(t, "name: t\nsteps:\n  - agent: { name: wazuh }\n  - agent: { name: zabbix }\n")
	specs, err := Render(both, nil, NodeVars{Name: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	err = FillAgentSettings(specs, &serveragents.Settings{Wazuh: serveragents.WazuhSettings{Manager: "198.51.100.5"}})
	var missing *AgentSettingsMissingError
	if !errors.As(err, &missing) || missing.Agent != serveragents.Zabbix {
		t.Fatalf("FillAgentSettings = %v, want the zabbix agent's values missing", err)
	}
	if specs[0].Agent.Settings != nil {
		t.Errorf("the wazuh step was filled although the fill was refused: %+v", specs[0].Agent.Settings)
	}
}

func TestFillAgentSettingsGivesEachStepItsOwnCopy(t *testing.T) {
	task := mustParse(t, `name: t
steps:
  - agent: { name: zabbix }
  - command: { run: "true" }
  - agent: { name: wazuh }
`)
	specs, err := Render(task, nil, NodeVars{Name: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	s := &serveragents.Settings{
		Zabbix: serveragents.ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10", HostMetadata: "linux"},
		Wazuh:  serveragents.WazuhSettings{Manager: "198.51.100.5", Version: serveragents.DefaultWazuhVersion},
	}
	want := *s
	if err := FillAgentSettings(specs, s); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		if a := specs[i].Agent; a == nil || a.Settings == nil || *a.Settings != want {
			t.Fatalf("step %d agent = %+v, want settings %+v", i, a, want)
		}
	}
	if specs[1].Agent != nil {
		t.Errorf("the command step was given an agent: %+v", specs[1].Agent)
	}

	// Neither the caller's value nor the other step's shares memory with a
	// step's: a change to one reaches nothing else.
	s.Zabbix.Server = "198.51.100.77"
	specs[0].Agent.Settings.Wazuh.Manager = "192.0.2.77"
	if got := specs[0].Agent.Settings.Zabbix.Server; got != "192.0.2.10" {
		t.Errorf("changing the caller's settings changed step 0's server to %q", got)
	}
	if got := specs[2].Agent.Settings.Wazuh.Manager; got != "198.51.100.5" {
		t.Errorf("changing step 0's settings changed step 2's manager to %q", got)
	}
}
