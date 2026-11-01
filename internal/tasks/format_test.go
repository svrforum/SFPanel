package tasks

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// formatFixtures are documents that between them use every step kind and
// every field Format writes.
var formatFixtures = map[string]string{
	"the design's example": designExample,
	"every field": `name: 모든 필드 ✓
description: "설명: 유니코드와 {{ 괄호 }}"
inputs:
  pkg: { label: "설치할 패키지", default: htop, required: true }
  cmd: { label: 명령, raw: true }
  bare:
  empty: {}
targets: { groups: [web, db], kinds: [agent, manager], nodes: [n1] }
batch: 3
stop_on_failure: false
steps:
  - packages: { update: true }
  - packages: { upgrade: all }
  - packages: { update: true, upgrade: [openssl, "libssl3"] }
  - name: 설치
    packages: { install: ["{{ pkg }}", curl] }
  - packages: { remove: [foo] }
  - name: 재시작
    continue_on_error: true
    service: { name: "{{ pkg }}.service", action: restart }
  - command: { run: "echo {{ node.name }} {{ node.address }}", timeout: 90s }
  - command: { run: "{{ cmd }}", timeout: 1h30m }
  - command: { run: "true" }
  - file: { path: "/etc/{{ pkg }}.conf", content: "", mode: "0644", owner: "root:root" }
  - file: { path: /etc/motd, content: "안녕 {{ node.name }}\n두 번째 줄\n" }
  - stack: { name: web, compose: "services:\n  web:\n    image: nginx\n", up: true }
  - stack: { name: "{{ pkg }}", compose: "x" }
  - reboot: { only_if_required: false, wait: 20m }
  - reboot:
  - reboot: { only_if_required: true, wait: 10m }
`,
	"scripts": `name: scripts
steps:
  - command:
      script: "  echo indented first line\necho second"
  - command:
      script: "echo a\n\techo tab\n"
  - command:
      script: "echo b\necho c\n\n"
  - command:
      script: |
        set -e
        echo {{ node.name }}
  - command:
      script: "echo trailing space  \necho next\n"
  - command:
      script: "echo crlf\r\necho next\r\n"
`,
	"values that need quoting": `name: "- not a list"
description: "#not a comment"
inputs:
  a: { default: "*alias" }
  b: { default: "&anchor" }
  c: { default: "!tag" }
  d: { default: "null" }
  e: { default: "~" }
  f: { default: "123" }
  g: { default: "yes" }
  h: { default: "a: b" }
  i: { default: " leading" }
  j: { default: "trailing " }
  k: { default: "'single'" }
  l: { default: "\"double\"" }
  m: { default: "{flow}" }
  n: { default: "[list]" }
  o: { default: "|" }
  p: { default: ">" }
  q: { default: "%directive" }
  r: { default: "@at" }
  s: { default: "` + "`" + `tick" }
  t: { default: "bell\a" }
  u: { label: "?", default: "--- " }
steps:
  - command: { run: "echo {{ a }}" }
`,
}

func TestFormatRoundTrips(t *testing.T) {
	docs := map[string]string{}
	for name, doc := range formatFixtures {
		docs[name] = doc
	}
	for _, b := range Builtins() {
		docs[b.ID] = string(b.YAML)
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			t1, problems := Parse([]byte(doc))
			if problems != nil {
				t.Fatalf("the fixture itself has problems: %+v", problems)
			}
			out, err := Format(t1)
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			t2, problems := Parse(out)
			if problems != nil {
				t.Fatalf("Format wrote a document with problems %+v:\n%s", problems, out)
			}
			if !reflect.DeepEqual(t1, t2) {
				t.Errorf("the round trip changed the task:\n before %+v\n after  %+v\ndocument:\n%s", t1, t2, out)
			}
		})
	}
}

// TestFormatKeepsEveryStringExactly puts each awkward string in every string
// field Format writes and reads the document back. A block scalar cannot
// start with a line break (the emitter drops it) or a tab (it writes no
// indentation indicator, and the document no longer parses).
//
// Fields Parse checks take what they accept: a task's name and a command's
// run and script cannot be blank ("\n", "\n\n", "\t\n"), so those keep the
// base value; a file path is absolute unless it holds a reference, so the
// string is followed by one; a file mode (octal digits) and a package name
// (no space or line break) take none of these strings and keep the base. A
// reference must name an input, so the one here is the base's own.
func TestFormatKeepsEveryStringExactly(t *testing.T) {
	const base = `name: base
description: d
inputs:
  in: { label: l, default: v }
steps:
  - name: n
    service: { name: s, action: restart }
  - command: { run: r }
  - command: { script: s }
  - file: { path: /p, content: c, mode: "0644", owner: o }
  - stack: { name: s, compose: c }
  - packages: { install: [htop] }
`
	for _, s := range []string{
		"\n", "\n\n", "\na", "\n\na", "\n  a", " \nb", "\ta\nb", "\t\n", "\r\na", "\u2028\na",
		"\nAuthorized use only\n", "\tbuild:\n\tgo build\n",
		"a\nb\n", "  indented\nx", "x  \ny", "a\r\nb", "# not a comment\n", "{{ in }}\n", "yes\nno",
	} {
		t.Run(strconv.Quote(s), func(t *testing.T) {
			model, problems := Parse([]byte(base))
			if problems != nil {
				t.Fatalf("the base has problems: %+v", problems)
			}
			blank := strings.TrimSpace(s) == ""
			if !blank {
				model.Name = s
				model.Steps[1].Command.Run = s
				model.Steps[2].Command.Script = s
			}
			model.Description = s
			model.Inputs[0].Label, model.Inputs[0].Default = s, s
			model.Steps[0].Name = s
			model.Steps[0].Service.Name = s
			model.Steps[3].File.Path = s + "{{ node.name }}"
			model.Steps[3].File.Content = s
			model.Steps[3].File.Owner = s
			model.Steps[4].Stack.Name = s
			model.Steps[4].Stack.Compose = s

			out, err := Format(model)
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			back, problems := Parse(out)
			if problems != nil {
				t.Fatalf("Format wrote a document with problems %+v:\n%s", problems, out)
			}
			if !reflect.DeepEqual(model, back) {
				t.Errorf("the round trip changed the task:\n before %+v\n after  %+v\ndocument:\n%s", model, back, out)
			}
		})
	}
}

func TestFormatWritesTheDocumentsOwnSpelling(t *testing.T) {
	format := func(t *testing.T, doc string) string {
		t.Helper()
		task, problems := Parse([]byte(doc))
		if problems != nil {
			t.Fatalf("fixture: %+v", problems)
		}
		out, err := Format(task)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	defaults := format(t, `name: defaults
batch: 1
stop_on_failure: true
steps:
  - packages: { upgrade: all }
  - command: { run: "echo", timeout: 10m }
  - reboot: { only_if_required: true, wait: 10m }
`)
	if !strings.Contains(defaults, "upgrade: all\n") {
		t.Errorf("upgrade_all is not written as upgrade: all:\n%s", defaults)
	}
	for _, key := range []string{"upgrade_all", "timeout:", "wait:", "batch:", "stop_on_failure:", "only_if_required:", "continue_on_error:", "description:", "inputs:", "targets:"} {
		if strings.Contains(defaults, key) {
			t.Errorf("%q is written although it is what Parse fills in:\n%s", key, defaults)
		}
	}

	set := format(t, `name: set
inputs:
  zeta: { label: last letter }
  alpha: {}
  mid: { default: "{{ not a ref }}" }
batch: 3
stop_on_failure: false
steps:
  - command: { run: "echo", timeout: 1h30m }
  - command:
      script: |
        echo one
        echo two
  - reboot: { only_if_required: false, wait: 20m }
`)
	for _, want := range []string{"batch: 3\n", "stop_on_failure: false\n", "timeout: 1h30m\n", "wait: 20m\n", "only_if_required: false\n", "script: |\n"} {
		if !strings.Contains(set, want) {
			t.Errorf("the document lacks %q:\n%s", want, set)
		}
	}
	if strings.Contains(set, "0s") {
		t.Errorf("a duration is written in the long form:\n%s", set)
	}
	z, a, m := strings.Index(set, "zeta:"), strings.Index(set, "alpha:"), strings.Index(set, "mid:")
	if z < 0 || a < 0 || m < 0 || z > a || a > m {
		t.Errorf("inputs are not a mapping in document order (zeta %d, alpha %d, mid %d):\n%s", z, a, m, set)
	}
	if strings.Contains(set, "- name: zeta") {
		t.Errorf("inputs are written as a list:\n%s", set)
	}

	// The task's own keys, then a step's, in the order a person writes them.
	order := format(t, `steps:
  - command: { run: "echo" }
    continue_on_error: true
    name: first
stop_on_failure: false
batch: 2
targets: { kinds: [agent] }
inputs:
  x: {}
description: d
name: order
`)
	last := -1
	for _, key := range []string{"name: order", "description:", "inputs:", "targets:", "batch:", "stop_on_failure:", "steps:", "- name: first", "continue_on_error:", "command:"} {
		i := strings.Index(order, key)
		if i <= last {
			t.Errorf("%q is at %d, not after the key before it (%d):\n%s", key, i, last, order)
		}
		last = i
	}
}

func TestFormatShortensDurations(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute:            "10m",
		90 * time.Minute:            "1h30m",
		90 * time.Second:            "1m30s",
		10 * time.Second:            "10s",
		time.Hour:                   "1h",
		2*time.Hour + 5*time.Second: "2h0m5s",
		1500 * time.Millisecond:     "1.5s",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
		if back, err := time.ParseDuration(want); err != nil || back != d {
			t.Errorf("%q reads back as %v (%v), want %v", want, back, err, d)
		}
	}
}

func TestFormatIsTotal(t *testing.T) {
	// Models the editor can hold that are not tasks: each still makes a
	// document, which Parse then answers with its problems.
	for name, task := range map[string]*Task{
		"a command step with no command": {Name: "x", Steps: []Step{{Kind: StepCommand}}},
		"every kind with no body": {Name: "x", Steps: []Step{
			{Kind: StepPackages}, {Kind: StepService}, {Kind: StepCommand}, {Kind: StepFile}, {Kind: StepStack},
		}},
		"no kind":               {Name: "x", Steps: []Step{{Name: "nothing"}}},
		"an unknown kind":       {Name: "x", Steps: []Step{{Kind: "teleport"}}},
		"no steps":              {Name: "x"},
		"nothing at all":        {},
		"a negative timeout":    {Name: "x", Steps: []Step{{Kind: StepCommand, Command: &CommandStep{Run: "echo", Timeout: -time.Second}}}},
		"a negative batch":      {Name: "x", Batch: -1, Steps: []Step{{Kind: StepCommand, Command: &CommandStep{Run: "echo"}}}},
		"both run and script":   {Name: "x", Steps: []Step{{Kind: StepCommand, Command: &CommandStep{Run: "a", Script: "b"}}}},
		"an empty upgrade list": {Name: "x", Steps: []Step{{Kind: StepPackages, Packages: &PackagesStep{Upgrade: []string{}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := Format(task)
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if len(out) == 0 {
				t.Fatal("Format wrote nothing")
			}
			if got, problems := Parse(out); got != nil || len(problems) == 0 {
				t.Errorf("Parse accepted %q as %+v, want its problems", out, got)
			}
		})
	}

	// A step with only its kind named reads as the defaults, as `reboot:`
	// does in a document.
	out, err := Format(&Task{Name: "x", Steps: []Step{{Kind: StepReboot}}})
	if err != nil {
		t.Fatal(err)
	}
	got, problems := Parse(out)
	if problems != nil || got.Steps[0].Reboot == nil || !got.Steps[0].Reboot.OnlyIfRequired || got.Steps[0].Reboot.Wait != DefaultRebootWait {
		t.Errorf("a reboot step with no body = %s, %+v, want the defaults", out, problems)
	}

	// Zero is what a model the editor built leaves a value it never set:
	// it reads as the default, not as a timeout of nothing.
	out, err = Format(&Task{Name: "x", Steps: []Step{
		{Kind: StepCommand, Command: &CommandStep{Run: "echo"}},
		{Kind: StepReboot, Reboot: &RebootStep{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, problems = Parse(out)
	if problems != nil || got.Batch != 1 || got.Steps[0].Command.Timeout != DefaultCommandTimeout || got.Steps[1].Reboot.Wait != DefaultRebootWait {
		t.Errorf("zero values = %s, %+v, want the defaults", out, problems)
	}

	if _, err := Format(nil); err == nil {
		t.Error("Format(nil) = no error, want one")
	}
}
