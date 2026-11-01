package taskjob

// A stack step creates or updates a compose stack and, with up, brings it
// up, behind the compose endpoints' own guards: the stack-name rule the
// compose module applies (docker.ValidateProjectName) and the safety tiers
// (composex.Analyze) — forbidden refused always, risky only until the step
// was confirmed in the preview. Up keeps its own check of the resolved
// configuration (ComposeManager.UpStream).
//
// The step runs inside the panel: a restart while it runs leaves the job
// interrupted (Recover).

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/composex"
	"github.com/svrforum/SFPanel/internal/docker"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// stackUpTimeout bounds `docker compose up` for one step: pulling images
// may take a while. It goes through compose's streaming path, whose own cap
// is the same 30 minutes; the plain ComposeManager.Up stops at 5 whatever
// its caller allows.
const stackUpTimeout = 30 * time.Minute

// StackRunner is what a stack step needs of the compose module;
// *docker.ComposeManager is one.
type StackRunner interface {
	GetProjectYAML(ctx context.Context, name string) (yaml, file string, err error)
	CreateProject(ctx context.Context, name, yaml string) (*docker.ComposeProject, error)
	UpdateProject(ctx context.Context, name, yaml string) error
	UpStream(ctx context.Context, name string, onLine func(string)) error
}

// StackStepRunner runs stack steps on the handler's Compose.
type StackStepRunner struct{}

// stackGuard is the endpoints' verdict on a stack step: a refusal, or a
// risky document that needs the operator's confirmation (needsConfirm, with
// the code and reason it is refused with until then).
func stackGuard(step *tasks.StackStep) (code, reason string, needsConfirm bool) {
	if err := docker.ValidateProjectName(step.Name); err != nil {
		return response.ErrInvalidName, err.Error(), false
	}
	report, err := composex.Analyze(step.Compose)
	if err != nil {
		return response.ErrInvalidYAML, err.Error(), false
	}
	if len(report.Forbidden) > 0 {
		return response.ErrComposeForbidden, report.Error(true).Error(), false
	}
	if err := report.Error(false); err != nil {
		return response.ErrComposeRisky, err.Error(), true
	}
	return "", "", false
}

// Check refuses a name or a document the endpoints refuse, and names a
// risky document as needing confirmation.
func (StackStepRunner) Check(spec tasks.JobSpec) tasks.CheckVerdict {
	code, reason, confirm := stackGuard(spec.Stack)
	if code == "" {
		return tasks.CheckVerdict{}
	}
	return tasks.CheckVerdict{Refused: !confirm, NeedsConfirm: confirm, Code: code, Reason: reason}
}

// Start writes the stack's compose file when it differs — creating the
// stack, or updating it when CreateProject finds it there — then brings it
// up when the step says so. ctx ends on a forced cancel.
func (StackStepRunner) Start(ctx context.Context, job *Job) error {
	res := runStack(ctx, job)
	if err := job.Finish(res); err != nil {
		logger.Error("recording a job's end", "id", job.Spec.ID, "error", err)
	}
	return nil
}

func runStack(ctx context.Context, job *Job) Result {
	step := job.Spec.Stack
	if code, reason, confirm := stackGuard(step); code != "" && (!confirm || !job.Spec.Confirmed) {
		return refusedResult(code, reason)
	}
	compose := job.h.Compose
	if compose == nil {
		return refusedResult(response.ErrDockerUnavailable, "Docker Compose is not available on this node")
	}

	wrote := false
	if have, _, err := compose.GetProjectYAML(ctx, step.Name); err != nil || have != step.Compose {
		verb := "created"
		_, err := compose.CreateProject(ctx, step.Name, step.Compose)
		if errors.Is(err, docker.ErrProjectExists) {
			verb, err = "updated", compose.UpdateProject(ctx, step.Name, step.Compose)
		}
		if err != nil {
			return Result{Status: tasks.JobFailed, Summary: response.ErrComposeError + ": " + response.SanitizeOutput(err.Error())}
		}
		wrote = true
		appendLog(job, "stack "+step.Name+": compose file "+verb)
	} else {
		appendLog(job, "stack "+step.Name+": compose file unchanged")
	}
	summary := "compose file unchanged"
	if wrote {
		summary = "compose file written"
	}
	if !step.Up {
		return Result{Status: tasks.JobSucceeded, Changed: wrote, Summary: summary}
	}

	// Each line goes to the log as compose prints it, and is kept to judge
	// what up changed and to say why it failed.
	var output strings.Builder
	upCtx, cancel := context.WithTimeout(ctx, stackUpTimeout)
	err := compose.UpStream(upCtx, step.Name, func(line string) {
		appendLog(job, line)
		output.WriteString(line + "\n")
	})
	cancel()
	out := output.String()
	changed := wrote || upChanged(out)
	switch {
	case ctx.Err() != nil:
		return Result{Status: tasks.JobCancelled, Changed: true, Summary: "cancelled while the stack came up"}
	case err != nil:
		return Result{Status: tasks.JobFailed, Changed: changed,
			Summary: response.ErrComposeError + ": " + response.SanitizeOutput(strings.TrimSpace(firstNonEmpty(out, err.Error())))}
	}
	if changed {
		summary += ", up"
	} else {
		summary += ", already up"
	}
	return Result{Status: tasks.JobSucceeded, Changed: changed, Summary: summary}
}

// upEvent is a line of `docker compose up` saying it made, replaced, started
// or removed something: " ✔ Container web-app-1  Started   0.4s". A stack
// that was already up reports its containers "Running" and nothing else.
var upEvent = regexp.MustCompile(`(?m)^\W*(Container|Network|Volume)\s+\S+\s+(Created|Recreated|Started|Removed)\b`)

func upChanged(out string) bool { return upEvent.MatchString(out) }
