package tasks

import (
	"strings"
	"testing"
)

func TestEveryBuiltinParses(t *testing.T) {
	want := []string{
		"builtin/package-update",
		"builtin/package-install",
		"builtin/service-restart",
		"builtin/command",
		"builtin/reboot",
	}
	got := Builtins()
	if len(got) != len(want) {
		t.Fatalf("builtins = %d, want %d", len(got), len(want))
	}
	names := map[string]string{}
	for i, b := range got {
		if b.ID != want[i] || !strings.HasPrefix(b.ID, "builtin/") {
			t.Errorf("builtin %d id = %q, want %q", i, b.ID, want[i])
		}
		task, problems := Parse(b.YAML)
		if len(problems) != 0 {
			t.Errorf("%s does not parse: %+v", b.ID, problems)
			continue
		}
		if other, dup := names[task.Name]; dup {
			t.Errorf("%s and %s share the name %q", other, b.ID, task.Name)
		}
		names[task.Name] = b.ID
	}
}

func TestTheBuiltinsDoWhatTheirNamesSay(t *testing.T) {
	byID := map[string]*Task{}
	for _, b := range Builtins() {
		task, problems := Parse(b.YAML)
		if len(problems) != 0 {
			t.Fatalf("%s: %+v", b.ID, problems)
		}
		byID[b.ID] = task
	}

	if s := byID["builtin/package-update"].Steps; len(s) != 1 || s[0].Packages == nil || !s[0].Packages.Update || !s[0].Packages.UpgradeAll {
		t.Errorf("package-update steps = %+v, want one update+upgrade-all step", s)
	}

	specs, err := Render(byID["builtin/package-install"], map[string]string{"packages": "htop curl"}, NodeVars{})
	if err != nil || specs[0].Packages == nil || strings.Join(specs[0].Packages.Install, " ") != "htop curl" {
		t.Errorf("package-install renders to %+v, %v", specs, err)
	}

	specs, err = Render(byID["builtin/service-restart"], map[string]string{"service": "nginx"}, NodeVars{})
	if err != nil || specs[0].Service == nil || specs[0].Service.Name != "nginx" || specs[0].Service.Action != "restart" {
		t.Errorf("service-restart renders to %+v, %v", specs, err)
	}

	// The command built-in takes the whole command as its input, so that
	// input must reach the shell unquoted or the built-in could only ever
	// run a single word.
	specs, err = Render(byID["builtin/command"], map[string]string{"command": "uptime; df -h /"}, NodeVars{})
	if err != nil || specs[0].Command == nil || specs[0].Command.Run != "uptime; df -h /" {
		t.Errorf("command renders to %+v, %v", specs, err)
	}

	if s := byID["builtin/reboot"].Steps; len(s) != 1 || s[0].Reboot == nil || !s[0].Reboot.OnlyIfRequired {
		t.Errorf("reboot steps = %+v, want one reboot only when required", s)
	}

	for id, task := range byID {
		for _, in := range task.Inputs {
			if _, err := Render(task, nil, NodeVars{}); in.Required && in.Default == "" && err == nil {
				t.Errorf("%s renders without its required input %q", id, in.Name)
			}
		}
	}
}
