package tasks

import (
	"errors"
	"fmt"
	osexec "os/exec"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, doc string) *Task {
	t.Helper()
	task, problems := Parse([]byte(doc))
	if len(problems) != 0 {
		t.Fatalf("problems: %+v", problems)
	}
	return task
}

func TestRenderQuotesInputsIntoCommands(t *testing.T) {
	task := mustParse(t, `name: t
inputs:
  msg: {}
  cmd: { raw: true }
steps:
  - command: { run: "echo {{ msg }}" }
  - command: { run: "{{ cmd }}" }
  - command:
      script: |
        printf '%s\n' {{msg}}
`)
	specs, err := Render(task, map[string]string{"msg": `x'; rm -rf / #`, "cmd": "ls -l | wc -l"}, NodeVars{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := specs[0].Command.Run, `echo 'x'\''; rm -rf / #'`; got != want {
		t.Errorf("run = %s, want %s", got, want)
	}
	if got := specs[1].Command.Run; got != "ls -l | wc -l" {
		t.Errorf("a raw input was changed: %q", got)
	}
	if got, want := specs[2].Command.Script, "printf '%s\\n' 'x'\\''; rm -rf / #'\n"; got != want {
		t.Errorf("script = %q, want %q", got, want)
	}
}

func TestShellQuoteSurvivesTheShellAsOneArgument(t *testing.T) {
	// The shell itself is the judge here: whatever goes in must come back
	// out of "$1" byte for byte, with nothing run along the way.
	// The strings are fixed test values; the only thing ever run is printf.
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh to check against: %v", err)
	}
	for _, s := range []string{"", "plain", "x'; rm -rf / #", "''", `a\'b`, "$(id) `id` $HOME", "line\nbreak", "한글 '따옴표'"} {
		out, err := osexec.Command(sh, "-c", "set -- "+ShellQuote(s)+`; printf '%s' "$#:$1"`).Output()
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if got, want := string(out), "1:"+s; got != want {
			t.Errorf("ShellQuote(%q) came back as %q, want %q", s, got, want)
		}
	}
}

func TestRenderFillsNodeVarsAndDefaults(t *testing.T) {
	task := mustParse(t, `name: 웹
inputs:
  service: { default: nginx, required: true }
  note: {}
steps:
  - service: { name: "{{ service }}", action: restart }
  - file: { path: /etc/motd, content: "host {{ node.name }} at {{ node.address }}{{ note }}" }
  - command: { run: "ping -c1 {{ node.address }}" }
  - reboot: {}
`)
	specs, err := Render(task, map[string]string{}, NodeVars{Name: "web-1", Address: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 4 {
		t.Fatalf("specs = %d, want 4", len(specs))
	}
	for i, s := range specs {
		if s.Step != i || s.Task != "웹" || s.Kind != task.Steps[i].Kind || s.Confirmed || s.ID != "" || s.RunID != "" {
			t.Errorf("spec %d header = %+v", i, s)
		}
	}
	if got := specs[0].Service.Name; got != "nginx" {
		t.Errorf("service = %q, want the default nginx", got)
	}
	if got := specs[1].File.Content; got != "host web-1 at 192.0.2.10" {
		t.Errorf("file content = %q, want node vars as they are and an unset input as empty", got)
	}
	if got := specs[2].Command.Run; got != "ping -c1 '192.0.2.10'" {
		t.Errorf("command = %q, want the address quoted", got)
	}
	if specs[2].Command.Timeout != 10*time.Minute {
		t.Errorf("timeout = %v", specs[2].Command.Timeout)
	}
	if r := specs[3].Reboot; r == nil || !r.OnlyIfRequired || r.Wait != 10*time.Minute {
		t.Errorf("reboot = %+v", r)
	}

	// Rendering is per node; the parsed task must not carry one node's values
	// into the next.
	if task.Steps[0].Service.Name != "{{ service }}" {
		t.Errorf("Render changed the task: %+v", task.Steps[0].Service)
	}
	again, err := Render(task, map[string]string{"service": "caddy"}, NodeVars{Name: "web-2", Address: "192.0.2.11"})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Service.Name != "caddy" || specs[0].Service.Name != "nginx" || again[1].File.Content != "host web-2 at 192.0.2.11" {
		t.Errorf("second render = %+v / %+v; first = %+v", again[0].Service, again[1].File, specs[0].Service)
	}
}

func TestRenderRefusesAMissingRequiredInput(t *testing.T) {
	task := mustParse(t, `name: t
inputs:
  optional: {}
  packages: { label: 설치할 패키지, required: true }
steps:
  - packages: { install: ["{{ packages }}"] }
`)
	for _, inputs := range []map[string]string{nil, {"optional": "x"}, {"packages": ""}} {
		_, err := Render(task, inputs, NodeVars{})
		if !errors.Is(err, ErrInputMissing) {
			t.Fatalf("inputs %v: err = %v, want ErrInputMissing", inputs, err)
		}
		var missing *InputMissingError
		if !errors.As(err, &missing) || missing.Name != "packages" || !strings.Contains(err.Error(), "packages") {
			t.Errorf("inputs %v: err = %#v, want one naming the input \"packages\"", inputs, err)
		}
	}
}

func TestRenderSplitsAFilledPackageListIntoNames(t *testing.T) {
	task := mustParse(t, `name: t
inputs:
  packages: { required: true }
steps:
  - packages: { update: true, install: [vim, "{{ packages }}"] }
`)
	specs, err := Render(task, map[string]string{"packages": "htop, curl  jq"}, NodeVars{})
	if err != nil {
		t.Fatal(err)
	}
	p := specs[0].Packages
	if !p.Update || fmt.Sprint(p.Install) != "[vim htop curl jq]" {
		t.Errorf("packages = %+v", p)
	}
}

func TestRenderPassesNonCommandValuesThroughForTheNodeToJudge(t *testing.T) {
	// Only commands are quoted. A file path or a service name is handed over
	// as typed; the node's own guards decide whether it is acceptable.
	task := mustParse(t, `name: t
inputs:
  path: {}
  unit: {}
steps:
  - file: { path: "{{ path }}", content: x }
  - service: { name: "{{ unit }}", action: stop }
`)
	specs, err := Render(task, map[string]string{"path": "/etc/sfpanel/config.yaml", "unit": "a b'c"}, NodeVars{})
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].File.Path != "/etc/sfpanel/config.yaml" || specs[1].Service.Name != "a b'c" {
		t.Errorf("specs = %+v / %+v", specs[0].File, specs[1].Service)
	}
}
