package tasks

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// designExample is the task from the design's section 2, word for word.
const designExample = `name: 웹서버 보안 업데이트          # 필수, 클러스터 안에서 유일
description: …
inputs:                               # 실행할 때 물어볼 값
  service: { label: 재시작할 서비스, default: nginx, required: true }
targets: { groups: [web], kinds: [agent], nodes: [] }   # 기본 대상, 실행할 때 바꿀 수 있음
batch: 2                              # 에이전트 묶음 크기 (매니저는 늘 1)
stop_on_failure: true
steps:
  - packages: { update: true }                       # apt-get update
  - packages: { upgrade: all }                       # 또는 [이름…]
  - packages: { install: [htop, curl] }              # 한 거래로
  - packages: { remove: [foo] }
  - service:  { name: "{{ service }}", action: restart }   # start|stop|restart|enable|disable
  - command:  { run: "nginx -t", timeout: 10m }      # 또는 script: | 여러 줄
  - file:     { path: /etc/motd, content: "…", mode: "0644", owner: root }
  - stack:    { name: web, compose: "…", up: true }  # 만들거나 바꾸고 올림
  - reboot:   { only_if_required: true, wait: 10m }
`

func TestParseAcceptsTheDesignExample(t *testing.T) {
	task, problems := Parse([]byte(designExample))
	if len(problems) != 0 {
		t.Fatalf("the design's example has problems: %+v", problems)
	}
	if task.Name != "웹서버 보안 업데이트" {
		t.Errorf("name = %q", task.Name)
	}
	if len(task.Steps) != 9 {
		t.Fatalf("steps = %d, want 9", len(task.Steps))
	}
	if len(task.Inputs) != 1 {
		t.Fatalf("inputs = %+v, want one", task.Inputs)
	}
	in := task.Inputs[0]
	if in.Name != "service" || in.Default != "nginx" || !in.Required || in.Label != "재시작할 서비스" || in.Raw {
		t.Errorf("input = %+v", in)
	}
	if task.Batch != 2 || !task.StopOnFailure {
		t.Errorf("batch = %d, stop_on_failure = %v", task.Batch, task.StopOnFailure)
	}
	if fmt.Sprint(task.Targets.Groups) != "[web]" || fmt.Sprint(task.Targets.Kinds) != "[agent]" || len(task.Targets.Nodes) != 0 {
		t.Errorf("targets = %+v", task.Targets)
	}

	wantKinds := []string{StepPackages, StepPackages, StepPackages, StepPackages, StepService, StepCommand, StepFile, StepStack, StepReboot}
	for i, s := range task.Steps {
		if s.Kind != wantKinds[i] {
			t.Errorf("step %d kind = %q, want %q", i, s.Kind, wantKinds[i])
		}
		set := 0
		for _, p := range []bool{s.Packages != nil, s.Service != nil, s.Command != nil, s.File != nil, s.Stack != nil, s.Reboot != nil} {
			if p {
				set++
			}
		}
		if set != 1 {
			t.Errorf("step %d has %d kinds set, want exactly one", i, set)
		}
	}
	if !task.Steps[0].Packages.Update {
		t.Errorf("step 0 = %+v", task.Steps[0].Packages)
	}
	if !task.Steps[1].Packages.UpgradeAll {
		t.Errorf("step 1 = %+v", task.Steps[1].Packages)
	}
	if fmt.Sprint(task.Steps[2].Packages.Install) != "[htop curl]" {
		t.Errorf("step 2 = %+v", task.Steps[2].Packages)
	}
	if fmt.Sprint(task.Steps[3].Packages.Remove) != "[foo]" {
		t.Errorf("step 3 = %+v", task.Steps[3].Packages)
	}
	if sv := task.Steps[4].Service; sv.Name != "{{ service }}" || sv.Action != "restart" {
		t.Errorf("step 4 = %+v", sv)
	}
	if c := task.Steps[5].Command; c.Run != "nginx -t" || c.Timeout != 10*time.Minute {
		t.Errorf("step 5 = %+v", c)
	}
	if f := task.Steps[6].File; f.Path != "/etc/motd" || f.Content != "…" || f.Mode != "0644" || f.Owner != "root" {
		t.Errorf("step 6 = %+v", f)
	}
	if s := task.Steps[7].Stack; s.Name != "web" || s.Compose != "…" || !s.Up {
		t.Errorf("step 7 = %+v", s)
	}
	if r := task.Steps[8].Reboot; !r.OnlyIfRequired || r.Wait != 10*time.Minute {
		t.Errorf("step 8 = %+v", r)
	}
}

func TestParseFillsInTheDefaults(t *testing.T) {
	task, problems := Parse([]byte(`name: t
steps:
  - command: { run: "true" }
  - reboot: {}
`))
	if len(problems) != 0 {
		t.Fatalf("problems: %+v", problems)
	}
	if task.Batch != 1 || !task.StopOnFailure {
		t.Errorf("batch = %d, stop_on_failure = %v; want 1 and true", task.Batch, task.StopOnFailure)
	}
	if task.Steps[0].Command.Timeout != 10*time.Minute || task.Steps[0].ContinueOnError {
		t.Errorf("command = %+v, continue_on_error = %v", task.Steps[0].Command, task.Steps[0].ContinueOnError)
	}
	if r := task.Steps[1].Reboot; !r.OnlyIfRequired || r.Wait != 10*time.Minute {
		t.Errorf("reboot = %+v; want only_if_required and a ten-minute wait", r)
	}
}

func TestParseReportsEveryProblemWithItsLine(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		line int
		want string
	}{
		{
			name: "an unknown top-level key",
			doc: `name: t
description: d
colour: blue
steps:
  - command: { run: "true" }
`,
			line: 3, want: `unknown key "colour"`,
		},
		{
			name: "an unknown step kind",
			doc: `name: t
steps:
  - command: { run: "true" }
  - shell: { run: "true" }
`,
			line: 4, want: `unknown step kind "shell"`,
		},
		{
			name: "a step with two kinds",
			doc: `name: t
steps:
  - service: { name: nginx, action: restart }
    command: { run: "true" }
`,
			line: 4, want: "exactly one kind",
		},
		{
			name: "a service action that does not exist",
			doc: `name: t
steps:
  - service:
      name: nginx
      action: reload
`,
			line: 5, want: `service action "reload"`,
		},
		{
			name: "a timeout that is not a duration",
			doc: `name: t
steps:
  - command:
      run: "true"
      timeout: forever
`,
			line: 5, want: `timeout "forever"`,
		},
		{
			name: "a file mode that is not octal",
			doc: `name: t
steps:
  - file:
      path: /etc/motd
      content: hi
      mode: "999"
`,
			line: 6, want: `mode "999"`,
		},
		{
			name: "a package name with a space",
			doc: `name: t
steps:
  - packages:
      install: [htop, "bad name"]
`,
			line: 4, want: `package name "bad name"`,
		},
		{
			name: "a reference to an input that is not defined",
			doc: `name: t
inputs:
  msg: { label: message }
steps:
  - command: { run: "echo {{ msg }}" }
  - command: { run: "echo {{ nope }}" }
`,
			line: 6, want: `"nope" is not an input`,
		},
		{
			name: "the name is missing",
			doc: `steps:
  - command: { run: "true" }
`,
			line: 1, want: `"name" is required`,
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

func TestParseCollectsEveryProblemNotJustTheFirst(t *testing.T) {
	_, problems := Parse([]byte(`description: no name
batch: 0
steps:
  - service: { name: nginx, action: reload }
  - command: { run: "true", timeout: forever }
  - teleport: {}
`))
	want := []struct {
		line int
		text string
	}{
		{1, `"name" is required`},
		{2, "batch"},
		{4, `"reload"`},
		{5, `"forever"`},
		{6, `"teleport"`},
	}
	if len(problems) != len(want) {
		t.Fatalf("problems = %+v, want %d", problems, len(want))
	}
	for i, w := range want {
		if problems[i].Line != w.line || !strings.Contains(problems[i].Message, w.text) {
			t.Errorf("problem %d = %+v, want line %d containing %q", i, problems[i], w.line, w.text)
		}
	}
}

func TestParseReportsAReferenceInsideAScriptAtItsOwnLine(t *testing.T) {
	_, problems := Parse([]byte(`name: t
steps:
  - command:
      script: |
        echo one
        echo {{ missing }}
`))
	if len(problems) != 1 || problems[0].Line != 6 || !strings.Contains(problems[0].Message, `"missing"`) {
		t.Errorf("problems = %+v, want one on line 6 about \"missing\"", problems)
	}
}

func TestParseLeavesDockerFormatStringsAlone(t *testing.T) {
	// `{{.Name}}` and `{{json .}}` are Go templates for docker, not
	// references to task inputs; refusing them would refuse ordinary commands.
	task, problems := Parse([]byte(`name: t
steps:
  - command: { run: "docker ps --format '{{.Names}} {{json .Ports}}'" }
`))
	if len(problems) != 0 {
		t.Fatalf("problems: %+v", problems)
	}
	if task.Steps[0].Command.Run != "docker ps --format '{{.Names}} {{json .Ports}}'" {
		t.Errorf("run = %q", task.Steps[0].Command.Run)
	}
}

func TestParseRefusesASyntaxErrorWithItsLine(t *testing.T) {
	_, problems := Parse([]byte("name: t\nsteps:\n  - command: { run: \"true\"\n"))
	if len(problems) != 1 || problems[0].Line == 0 || !strings.Contains(problems[0].Message, "YAML") {
		t.Errorf("problems = %+v, want one YAML problem with a line", problems)
	}
}

func TestParseEnforcesTheLimits(t *testing.T) {
	t.Run("a document over 64 KiB", func(t *testing.T) {
		doc := "name: t\ndescription: " + strings.Repeat("a", MaxDocumentBytes) + "\nsteps:\n  - command: { run: \"true\" }\n"
		task, problems := Parse([]byte(doc))
		if task != nil || len(problems) != 1 || !strings.Contains(problems[0].Message, "64 KiB") {
			t.Errorf("problems = %+v, want one naming the 64 KiB limit", problems)
		}
	})

	steps := func(n int) string {
		var b strings.Builder
		b.WriteString("name: t\nsteps:\n")
		for range n {
			b.WriteString("  - command: { run: \"true\" }\n")
		}
		return b.String()
	}
	t.Run("51 steps", func(t *testing.T) {
		task, problems := Parse([]byte(steps(51)))
		if task != nil || len(problems) != 1 || problems[0].Line != 2 || !strings.Contains(problems[0].Message, "at most 50 steps") {
			t.Errorf("problems = %+v, want one on line 2 naming the 50-step limit", problems)
		}
	})
	t.Run("50 steps are allowed", func(t *testing.T) {
		if _, problems := Parse([]byte(steps(50))); len(problems) != 0 {
			t.Errorf("problems = %+v", problems)
		}
	})
}

func TestParseKeepsInputsInDocumentOrder(t *testing.T) {
	task, problems := Parse([]byte(`name: t
inputs:
  zeta: {}
  alpha: { raw: true }
  mid: { default: x }
steps:
  - command: { run: "echo {{ zeta }} {{ alpha }} {{ mid }}" }
`))
	if len(problems) != 0 {
		t.Fatalf("problems: %+v", problems)
	}
	var names []string
	for _, in := range task.Inputs {
		names = append(names, in.Name)
	}
	if fmt.Sprint(names) != "[zeta alpha mid]" {
		t.Errorf("inputs = %v, want document order", names)
	}
}

func TestParseAPackagesStepTakesOneActionBesidesUpdate(t *testing.T) {
	cases := []struct {
		name, step, want string
	}{
		{"update with install is fine", `packages: { update: true, install: [htop] }`, ""},
		{"install and remove together", `packages: { install: [htop], remove: [foo] }`, "one of upgrade, install or remove"},
		{"no action at all", `packages: { update: false }`, "needs update, upgrade, install or remove"},
		{"an empty install list", `packages: { install: [] }`, "at least one package"},
		{"upgrade something that is not a list or all", `packages: { upgrade: everything }`, `"all" or a list`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte("name: t\nsteps:\n  - " + tc.step + "\n"))
			if tc.want == "" {
				if len(problems) != 0 {
					t.Errorf("problems = %+v", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0].Message, tc.want) {
				t.Errorf("problems = %+v, want one containing %q", problems, tc.want)
			}
		})
	}
}

func TestParseRefusesASecondDocument(t *testing.T) {
	_, problems := Parse([]byte("name: t\nsteps:\n  - reboot: {}\n---\nname: u\n"))
	if len(problems) != 1 || problems[0].Line != 4 || !strings.Contains(problems[0].Message, "one YAML document") {
		t.Errorf("problems = %+v, want one on line 4 (the ---) about a second document", problems)
	}
}
