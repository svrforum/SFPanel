package tasks

import (
	"fmt"
	"os"
	osexec "os/exec"
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

// quotingMessage is the start of the problem a reference in the wrong place
// gets; the rest says how to write it instead.
const quotingMessage = "is inside quotes, a command substitution, a heredoc or a comment"

func TestParseRefusesAReferenceInACommandWhereItsQuotingWouldBreak(t *testing.T) {
	// Render puts 'value' in place of the reference. That is one word only
	// where the shell reads a word; inside quotes, a substitution, a heredoc
	// or a comment the value is opened up to the shell instead, so Parse
	// refuses the reference there.
	cases := []struct {
		name string
		step string // the lines after "steps:"
		ref  string
		line int
	}{
		{"single quotes", `  - command: { run: "echo '{{ x }}'" }`, "x", 5},
		{"double quotes", `  - command: { run: 'echo "{{ x }}"' }`, "x", 5},
		{"inside a double-quoted string with other words", `  - command: { run: 'echo "a {{ x }} b"' }`, "x", 5},
		{"a command substitution inside double quotes", `  - command: { run: 'echo "$(echo {{ x }})"' }`, "x", 5},
		{"a command substitution", `  - command: { run: 'echo $(echo {{ x }})' }`, "x", 5},
		{"backticks", "  - command: { run: 'echo `{{ x }}`' }", "x", 5},
		{"arithmetic", `  - command: { run: 'echo $(( {{ x }} + 1 ))' }`, "x", 5},
		{"a parameter expansion's default", `  - command: { run: 'echo ${HOME:-{{ x }}}' }`, "x", 5},
		{"a comment", `  - command: { run: 'echo hi # {{ x }}' }`, "x", 5},
		{"a node variable in single quotes", `  - command: { run: "ping -c1 '{{ node.address }}'" }`, "node.address", 5},
		{"a heredoc body", "  - command:\n      script: |\n        cat <<EOF >/tmp/out\n        hello {{ x }}\n        EOF\n", "x", 8},
		{"a heredoc body with a quoted delimiter", "  - command:\n      script: |\n        cat <<-'EOF'\n        {{ x }}\n        EOF\n", "x", 8},
		{"the second line of a script, in double quotes", "  - command:\n      script: |\n        echo ok\n        echo \"{{ x }}\"\n", "x", 8},
		{"a single-quoted string over two lines", "  - command:\n      script: |\n        echo 'one\n        {{ x }}'\n", "x", 8},
		{"right after a backslash", `  - command: { run: 'echo \{{ x }}' }`, "x", 5},
		{"after an ANSI-C string", `  - command: { run: "echo $'a\\'' {{ x }}" }`, "x", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := "name: t\ninputs:\n  x: {}\nsteps:\n" + tc.step + "\n"
			task, problems := Parse([]byte(doc))
			if task != nil {
				t.Errorf("the reference was accepted: %+v", task.Steps[0].Command)
			}
			if len(problems) != 1 {
				t.Fatalf("problems = %+v, want exactly one", problems)
			}
			p := problems[0]
			if p.Line != tc.line || !strings.Contains(p.Message, "{{ "+tc.ref+" }}") {
				t.Errorf("problem = %+v, want line %d naming {{ %s }}", p, tc.line, tc.ref)
			}
			if !strings.Contains(p.Message, quotingMessage) && !strings.Contains(p.Message, "cannot follow") {
				t.Errorf("problem = %q, want it to say where the reference sits", p.Message)
			}
		})
	}
}

func TestParseSaysWhyAQuotedReferenceIsRefusedAndWhatToWriteInstead(t *testing.T) {
	_, problems := Parse([]byte("name: t\ninputs:\n  svc: {}\nsteps:\n  - command: { run: \"systemctl restart '{{ svc }}'\" }\n"))
	want := `{{ svc }} is inside quotes, a command substitution, a heredoc or a comment: write it unquoted where a word goes — its value is quoted for you. To use it inside a string, assign it first (X={{ svc }}) and write "$X"`
	if len(problems) != 1 || problems[0].Line != 5 || problems[0].Message != want {
		t.Errorf("problems = %+v, want one on line 5:\n%s", problems, want)
	}
}

func TestParseAcceptsAReferenceWhereTheShellReadsAWord(t *testing.T) {
	cases := []struct {
		name string
		step string
	}{
		{"an argument", `  - command: { run: "echo {{ x }}" }`},
		{"an option's value", `  - command: { run: "curl --opt={{ x }}" }`},
		{"an assignment", `  - command: { run: "X={{ x }}; echo \"$X\"" }`},
		{"after a closed double-quoted string", `  - command: { run: 'echo "done:" {{ x }}' }`},
		{"after a closed single-quoted string", `  - command: { run: "printf '%s\\n' {{ x }}" }`},
		{"joined to a closed string", `  - command: { run: "echo 'a'{{ x }}\"b\"" }`},
		{"after a command substitution has closed", `  - command: { run: 'echo "$(date)" $(hostname) {{ x }}' }`},
		{"in a subshell", `  - command: { run: "(cd /tmp && ls {{ x }})" }`},
		{"after a heredoc has ended", "  - command:\n      script: |\n        cat <<EOF\n        body with 'quotes\n        EOF\n        echo {{ x }}\n"},
		{"after a comment line with an apostrophe", "  - command:\n      script: |\n        # don't worry\n        echo {{ x }}\n"},
		{"a # inside a word is not a comment", `  - command: { run: "echo a#b {{ x }}" }`},
		{"an escaped quote is not a quote", `  - command: { run: "echo it\\'s {{ x }}" }`},
		{"several on a line", `  - command: { run: "echo {{ x }} {{ node.name }} {{ node.address }}" }`},
		{"docker's own templates inside quotes", `  - command: { run: "docker ps --format '{{.Names}}' --filter name={{ x }}" }`},
		{"a here-string", `  - command: { run: "cat <<< {{ x }}" }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := "name: t\ninputs:\n  x: {}\nsteps:\n" + tc.step + "\n"
			if _, problems := Parse([]byte(doc)); len(problems) != 0 {
				t.Errorf("problems = %+v", problems)
			}
		})
	}
}

func TestParseLetsARawInputGoAnywhereInACommand(t *testing.T) {
	// A raw input is spliced in as written, by design: where it sits is the
	// author's business.
	_, problems := Parse([]byte(`name: t
inputs:
  cmd: { raw: true }
steps:
  - command: { run: "sh -c '{{ cmd }}'" }
  - command:
      script: |
        echo "{{ cmd }}"
        cat <<EOF
        {{ cmd }}
        EOF
`))
	if len(problems) != 0 {
		t.Errorf("problems = %+v", problems)
	}
}

func TestParseLeavesQuotedReferencesOutsideCommandsAlone(t *testing.T) {
	// Only a command's value is shell-quoted; a file's content goes in as
	// typed, so quotes around a reference there are the content's own.
	_, problems := Parse([]byte(`name: t
inputs:
  x: {}
steps:
  - file: { path: /etc/motd, content: "hello '{{ x }}' and \"{{ x }}\"" }
`))
	if len(problems) != 0 {
		t.Errorf("problems = %+v", problems)
	}
}

func TestAReferenceParseAcceptsStaysOneWordUnderTheShell(t *testing.T) {
	// The shell is the judge: every place Parse accepts, filled with a value
	// built to break out, must print the value back byte for byte and run
	// nothing. Only printf runs; the marker would land in t.TempDir().
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh to check against: %v", err)
	}
	marker := t.TempDir() + "/ran"
	value := `$(touch ` + marker + `) ` + "`touch " + marker + "`" + ` '"; touch ` + marker + ` #`
	scripts := []struct{ body, want string }{
		{`printf '%s\n' {{ x }}`, value},
		{`X={{ x }}; printf '%s\n' "$X"`, value},
		{`printf '%s\n' "a"{{ x }}'b'`, "a" + value + "b"},
		{`(printf '%s\n' {{ x }})`, value},
		{"cat <<EOF >/dev/null\nit's a body\nEOF\nprintf '%s\\n' {{ x }}", value},
		{"# don't\nprintf '%s\\n' {{ x }}", value},
		{`printf '%s\n' "$(printf done)" {{ x }}`, "done\n" + value},
	}
	for _, sc := range scripts {
		doc := "name: t\ninputs:\n  x: {}\nsteps:\n  - command:\n      script: |\n"
		for _, line := range strings.Split(sc.body, "\n") {
			doc += "        " + line + "\n"
		}
		task, problems := Parse([]byte(doc))
		if len(problems) != 0 {
			t.Fatalf("%q: problems = %+v", sc.body, problems)
		}
		specs, err := Render(task, map[string]string{"x": value}, NodeVars{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := osexec.Command(sh, "-c", specs[0].Command.Script).Output()
		if err != nil {
			t.Fatalf("%q: %v", sc.body, err)
		}
		if got := strings.TrimSuffix(string(out), "\n"); got != sc.want {
			t.Errorf("%q printed %q, want %q", sc.body, got, sc.want)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%q ran part of the value", sc.body)
		}
	}
}
