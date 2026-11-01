package taskjob

// A service step runs one systemctl action on a unit, behind the Services
// page's own guard (services.UnitGuard): a unit the page refuses, the step
// refuses for the same reason, and one the page asks the operator to
// confirm goes through only when the step was confirmed in the preview.

import (
	"context"
	"strings"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/feature/services"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// ServiceRunner runs service steps.
type ServiceRunner struct {
	Cmd exec.Commander
	// Platform says what kind of host this is: the units a host cannot lose
	// differ on Proxmox VE.
	Platform func() platform.Info
}

// NewServiceRunner returns the runner of service steps.
func NewServiceRunner(cmd exec.Commander, platformInfo func() platform.Info) *ServiceRunner {
	return &ServiceRunner{Cmd: cmd, Platform: platformInfo}
}

// Check is UnitGuard's verdict as the preview shows it: a unit that needs
// confirming is named so, whether or not the step is confirmed yet; any
// other refusal refuses the step.
func (r *ServiceRunner) Check(spec tasks.JobSpec) tasks.CheckVerdict {
	ref := services.UnitGuard(r.Platform(), spec.Service.Name, spec.Service.Action, false)
	switch {
	case ref == nil:
		return tasks.CheckVerdict{}
	case ref.Code == response.ErrServiceConfirmRequired:
		return tasks.CheckVerdict{NeedsConfirm: true, Code: ref.Code, Reason: ref.Message}
	default:
		return tasks.CheckVerdict{Refused: true, Code: ref.Code, Reason: ref.Message}
	}
}

// Start runs `systemctl <action> <unit>`, as the Services page does. The
// step changed something when the unit's is-active or is-enabled answer
// differs afterwards, and a restart always has: the unit did restart.
func (r *ServiceRunner) Start(_ context.Context, job *Job) error {
	step := job.Spec.Service
	var res Result
	if ref := services.UnitGuard(r.Platform(), step.Name, step.Action, job.Spec.Confirmed); ref != nil {
		res = Result{Status: tasks.JobFailed, Summary: ref.Code + ": " + ref.Message}
	} else {
		res = r.run(job, step)
	}
	if err := job.Finish(res); err != nil {
		logger().Error("recording a job's end", "id", job.Spec.ID, "error", err)
	}
	return nil
}

func (r *ServiceRunner) run(job *Job, step *tasks.ServiceStep) Result {
	before := r.state(step.Name)
	appendLog(job, "$ systemctl "+step.Action+" "+step.Name)
	out, err := r.Cmd.Run("systemctl", step.Action, step.Name)
	appendLog(job, strings.TrimSpace(out))
	after := r.state(step.Name)
	appendLog(job, step.Name+": "+after)
	changed := after != before || step.Action == "restart"
	if err != nil {
		return Result{Status: tasks.JobFailed, Changed: changed,
			Summary: "systemctl " + step.Action + " failed: " + response.SanitizeOutput(strings.TrimSpace(firstNonEmpty(out, err.Error())))}
	}
	summary := step.Name + ": " + before + " → " + after
	switch {
	case step.Action == "restart" && after == before:
		summary = step.Name + ": restarted (" + after + ")"
	case !changed:
		summary = step.Name + ": already " + after
	}
	return Result{Status: tasks.JobSucceeded, Changed: changed, Summary: summary}
}

// state is what systemctl says of the unit: whether it runs and whether it
// starts at boot, as "active, enabled". Both commands exit non-zero for the
// answers that are not "active" and "enabled", so only the words count.
func (r *ServiceRunner) state(unit string) string {
	active, _ := r.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "is-active", unit)
	enabled, _ := r.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "is-enabled", unit)
	return firstLine(active) + ", " + firstLine(enabled)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" {
		return "unknown"
	}
	return s
}
