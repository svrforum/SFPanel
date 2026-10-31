package taskjob

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/feature/services"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// fakeUnits is systemctl over a few units: each action changes what
// is-active and is-enabled answer, the way systemd would.
type fakeUnits struct {
	mu      sync.Mutex
	calls   []exec.MockCall
	active  map[string]bool
	enabled map[string]bool
	fail    map[string]string // action → the output it fails with
}

func newFakeUnits() *fakeUnits {
	return &fakeUnits{active: map[string]bool{}, enabled: map[string]bool{}, fail: map[string]string{}}
}

func (f *fakeUnits) answer(name string, args []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, exec.MockCall{Name: name, Args: append([]string(nil), args...)})
	if name != "systemctl" || len(args) != 2 {
		return "", nil
	}
	action, unit := args[0], args[1]
	if out, ok := f.fail[action]; ok {
		return out, errors.New("exit status 1")
	}
	switch action {
	case "is-active":
		if f.active[unit] {
			return "active\n", nil
		}
		return "inactive\n", errors.New("exit status 3")
	case "is-enabled":
		if f.enabled[unit] {
			return "enabled\n", nil
		}
		return "disabled\n", errors.New("exit status 1")
	case "start", "restart":
		f.active[unit] = true
	case "stop":
		f.active[unit] = false
	case "enable":
		f.enabled[unit] = true
	case "disable":
		f.enabled[unit] = false
	}
	return "", nil
}

func (f *fakeUnits) Run(name string, args ...string) (string, error) { return f.answer(name, args) }
func (f *fakeUnits) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeUnits) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeUnits) RunWithInput(_ string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeUnits) RunCtx(_ context.Context, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeUnits) RunWithEnvCtx(_ context.Context, _ []string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}
func (f *fakeUnits) Exists(string) bool { return true }

// ran reports whether systemctl <action> was run on any unit.
func (f *fakeUnits) ran(action string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == action {
			return true
		}
	}
	return false
}

func serviceSpec(step int, unit, action string, confirmed bool) tasks.JobSpec {
	return kindSpec(step, tasks.StepService, func(s *tasks.JobSpec) {
		s.Service = &tasks.ServiceStep{Name: unit, Action: action}
		s.Confirmed = confirmed
	})
}

// A service step refuses what the Services page refuses, for the same
// reason in the same words — the code and UnitGuard's message are the job's
// summary — and runs nothing it refused. A unit that asks for confirmation
// goes through once the step was confirmed.
func TestAServiceStepIsRefusedForTheEndpointsReasons(t *testing.T) {
	pve := platform.Info{PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2}
	for _, tc := range []struct {
		label     string
		unit      string
		action    string
		confirmed bool
		code      string // "" when the step runs
	}{
		{"a name without .service", "nginx", "restart", true, response.ErrInvalidName},
		{"the panel's own unit, stopped", "sfpanel.service", "stop", true, response.ErrPermissionDenied},
		{"the Proxmox cluster filesystem, stopped", "pve-cluster.service", "stop", true, response.ErrServiceProtected},
		{"pveproxy stopped, not confirmed", "pveproxy.service", "stop", false, response.ErrServiceConfirmRequired},
		{"pveproxy stopped, confirmed", "pveproxy.service", "stop", true, ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			platform.SetForTest(t, pve)
			units := newFakeUnits()
			units.active[tc.unit] = true
			k := newKindNode(t, units)
			r := k.run(t, serviceSpec(0, tc.unit, tc.action, tc.confirmed))
			if tc.code == "" {
				if r.status != tasks.JobSucceeded || !units.ran(tc.action) {
					t.Fatalf("job ended %s %q, ran=%v; want succeeded with systemctl %s run", r.status, r.summary, units.ran(tc.action), tc.action)
				}
				return
			}
			want := services.UnitGuard(pve, tc.unit, tc.action, tc.confirmed)
			if want == nil || want.Code != tc.code {
				t.Fatalf("UnitGuard = %+v, want %s (the table is wrong)", want, tc.code)
			}
			if r.status != tasks.JobFailed || r.summary != tc.code+": "+want.Message {
				t.Errorf("job ended %s %q, want failed %q", r.status, r.summary, tc.code+": "+want.Message)
			}
			if r.changed || units.ran(tc.action) {
				t.Errorf("a refused step ran systemctl %s (changed=%v)", tc.action, r.changed)
			}
		})
	}
}

// A service step has changed something when the unit's state or its
// enablement differs afterwards: stopping a running unit has, stopping a
// stopped one has not, and enabling an enabled unit has not.
func TestAServiceStepSaysWhetherItChangedTheUnit(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	units := newFakeUnits()
	units.active["nginx.service"] = true
	units.enabled["nginx.service"] = true
	k := newKindNode(t, units)
	for i, tc := range []struct {
		action  string
		changed bool
	}{
		{"stop", true},
		{"stop", false},
		{"start", true},
		{"enable", false},
		{"disable", true},
		{"disable", false},
	} {
		r := k.run(t, serviceSpec(i, "nginx.service", tc.action, false))
		if r.status != tasks.JobSucceeded || r.changed != tc.changed {
			t.Errorf("step %d, %s: %s changed=%v %q, want succeeded changed=%v", i, tc.action, r.status, r.changed, r.summary, tc.changed)
		}
		if !strings.Contains(k.logOf(t, r.spec.ID), "systemctl "+tc.action+" nginx.service") {
			t.Errorf("step %d: the log does not say what ran: %q", i, k.logOf(t, r.spec.ID))
		}
	}
}

// systemctl failing fails the step, and says what systemctl said.
func TestAServiceStepThatSystemctlRefusesFails(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	units := newFakeUnits()
	units.fail["start"] = "Job for nginx.service failed because the control process exited with error code."
	k := newKindNode(t, units)
	r := k.run(t, serviceSpec(0, "nginx.service", "start", false))
	if r.status != tasks.JobFailed || !strings.Contains(r.summary, "control process exited") {
		t.Errorf("job ended %s %q, want failed with systemctl's words", r.status, r.summary)
	}
}
