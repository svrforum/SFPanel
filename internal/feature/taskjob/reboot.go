package taskjob

// A reboot step reboots the node and is done once it has booted again. The
// reboot is scheduled a few seconds out in a transient systemd timer, so the
// answer to the leader leaves first; the job waits in "rebooting", which
// holds the node like a running job, until a panel that starts in another
// boot — the kernel's boot ID changed — settles it as succeeded (Recover).
//
// A Proxmox VE host is not rebooted: the panel does not know the guests on
// it. That, and a node without systemd, is refused before anything is
// recorded or scheduled (Refuse), with a code the engine reads.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	// DefaultRebootRequiredPath is the file Debian's packages leave when an
	// upgrade needs a reboot.
	DefaultRebootRequiredPath = "/var/run/reboot-required"
	// rebootUnitPrefix names a reboot's transient unit:
	// sfpanel-task-reboot-<id>, a .timer and the .service it starts.
	rebootUnitPrefix = "sfpanel-task-reboot-"
)

// RebootRunner runs reboot steps.
type RebootRunner struct {
	Cmd      exec.Commander
	Platform func() platform.Info
	// RequiredPath is the file whose presence says a reboot is required.
	RequiredPath string
}

// NewRebootRunner returns the runner of reboot steps.
func NewRebootRunner(cmd exec.Commander, platformInfo func() platform.Info) *RebootRunner {
	return &RebootRunner{Cmd: cmd, Platform: platformInfo, RequiredPath: DefaultRebootRequiredPath}
}

// Check refuses a reboot of a Proxmox VE host, as a run would.
func (r *RebootRunner) Check(tasks.JobSpec) tasks.CheckVerdict {
	if ref := r.refusePVE(); ref != nil {
		return tasks.CheckVerdict{Refused: true, Code: ref.Code, Reason: ref.Message}
	}
	return tasks.CheckVerdict{}
}

func (r *RebootRunner) refusePVE() *Refusal {
	if r.Platform().PVE {
		return &Refusal{Status: http.StatusConflict, Code: response.ErrTaskRebootPVEHost,
			Message: "This is a Proxmox VE host; the panel does not reboot one, since it does not know the guests running on it"}
	}
	return nil
}

// Refuse turns a reboot away before it is recorded: on a Proxmox VE host,
// even one that is required; and, when one would be scheduled, on a node
// without systemd to schedule it with.
func (r *RebootRunner) Refuse(spec tasks.JobSpec) *Refusal {
	if ref := r.refusePVE(); ref != nil {
		return ref
	}
	if spec.Reboot.OnlyIfRequired && !r.required() {
		return nil
	}
	if !systemdActive() || !r.Cmd.Exists("systemd-run") {
		return &Refusal{Status: http.StatusConflict, Code: response.ErrTaskRebootUnsupported,
			Message: "This node has no systemd to schedule a reboot with"}
	}
	return nil
}

// required reports whether the system says it needs a reboot.
func (r *RebootRunner) required() bool {
	_, err := os.Stat(r.RequiredPath)
	return err == nil
}

// rebootWait is how long the step waits for the node to come back.
func rebootWait(step *tasks.RebootStep) time.Duration {
	if step.Wait > 0 {
		return step.Wait
	}
	return tasks.DefaultRebootWait
}

// Start ends at once, a success that changed nothing, when the step is for
// a required reboot and none is; otherwise it schedules
//
//	systemd-run --on-active=5 --unit=sfpanel-task-reboot-<id> systemctl reboot
//
// and waits, the job "rebooting", for the reboot to end this process.
func (r *RebootRunner) Start(ctx context.Context, job *Job) error {
	finish := func(res Result) error {
		if err := job.Finish(res); err != nil {
			logger.Error("recording a job's end", "id", job.Spec.ID, "error", err)
		}
		return nil
	}
	// Refused before the job was recorded; asked again in case the host
	// changed in between.
	if ref := r.Refuse(job.Spec); ref != nil {
		return finish(Result{Status: tasks.JobFailed, Summary: ref.Code + ": " + ref.Message})
	}
	if job.Spec.Reboot.OnlyIfRequired && !r.required() {
		appendLog(job, "no reboot is required")
		return finish(Result{Status: tasks.JobSucceeded, Summary: "not required"})
	}
	if ctx.Err() != nil {
		return finish(Result{Status: tasks.JobCancelled, Summary: "cancelled before the reboot was scheduled"})
	}
	unit := rebootUnitPrefix + job.Spec.ID
	// Recorded first: the reboot may end this process at any moment after
	// systemd-run, and the panel that boots next must find the job waiting
	// on it, not running.
	if err := job.setRebooting(unit + ".timer"); err != nil {
		return err
	}
	out, err := r.Cmd.RunWithTimeout(30*time.Second, "systemd-run", "--on-active=5", "--unit="+unit, "systemctl", "reboot")
	if err != nil {
		appendLog(job, strings.TrimSpace(out))
		return finish(Result{Status: tasks.JobFailed,
			Summary: "systemd-run: " + response.SanitizeOutput(strings.TrimSpace(firstNonEmpty(out, err.Error())))})
	}
	appendLog(job, "reboot scheduled in 5 seconds ("+unit+")")
	logger.Info("task job scheduled a reboot", "id", job.Spec.ID, "unit", unit)
	r.await(ctx, job)
	return nil
}

// await waits for the reboot to end this process. If the step's wait runs
// out first, the reboot did not happen — something held it off — and the
// job fails, freeing the node. A forced cancel calls the reboot off, unless
// the timer has already fired and the system is going down: then the job
// stays rebooting for the next boot to settle by its boot ID, rather than
// ending cancelled while the host reboots under it.
func (r *RebootRunner) await(ctx context.Context, job *Job) {
	wait := rebootWait(job.Spec.Reboot)
	timer := time.NewTimer(max(0, job.StartedAt.Add(wait).Sub(job.Now())))
	defer timer.Stop()
	didNotReboot := Result{Status: tasks.JobFailed, Summary: "the node did not reboot within " + shortDuration(wait)}
	var res Result
	select {
	case <-ctx.Done():
		if out, err := r.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "stop", job.Unit); err != nil {
			logger.Warn("calling off a cancelled job's reboot", "id", job.Spec.ID, "unit", job.Unit,
				"error", err, "output", response.SanitizeOutput(out))
		}
		if r.goingDown() {
			logger.Info("a forced cancel came after the reboot began; the job stays rebooting for the next boot to settle",
				"id", job.Spec.ID, "unit", job.Unit)
			appendLog(job, "cancel asked after the reboot had begun: it cannot be called off")
			// The reboot ends this process; the wait still frees the node
			// should it somehow not.
			<-timer.C
			res = didNotReboot
			break
		}
		res = Result{Status: tasks.JobCancelled, Summary: "cancelled: the reboot was called off"}
	case <-timer.C:
		res = didNotReboot
	}
	appendLog(job, res.Summary)
	if err := job.Finish(res); err != nil {
		logger.Error("recording a job's end", "id", job.Spec.ID, "error", err)
	}
}

// goingDown reports whether systemd has begun shutting the system down —
// the reboot's timer fired — so stopping the timer no longer calls it off.
// is-system-running exits non-zero for every state but "running"; the state
// is in its output either way.
func (r *RebootRunner) goingDown() bool {
	out, _ := r.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "is-system-running")
	return strings.TrimSpace(out) == "stopping"
}

// settleReboot is how a job left rebooting stands at boot: succeeded once
// the boot ID differs from the one the job started in. In the same boot the
// reboot is still to come — the panel restarted before the timer fired — or
// did not happen, and the runner waits for it again until the step's wait
// is up. It reports whether the job was settled.
func (h *Handler) settleReboot(r record) bool {
	now := h.bootID()
	if r.bootID == "" || now == "" || now == r.bootID {
		return false
	}
	res := Result{Status: tasks.JobSucceeded, Changed: true, Summary: "rebooted"}
	if err := h.finish(r.spec.ID, res); err != nil {
		logger.Error("settling a reboot", "id", r.spec.ID, "error", err)
	}
	return true
}

// recoverRebooting settles each job left waiting on its reboot, or waits on
// it again.
func (h *Handler) recoverRebooting() {
	rows, err := h.DB.Query(`SELECT `+recordColumns+` FROM task_jobs WHERE status = ?`, tasks.JobRebooting)
	if err != nil {
		logger.Error("reading the jobs left rebooting", "error", err)
		return
	}
	var waiting []record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			logger.Error("reading a job left rebooting", "error", err)
			continue
		}
		waiting = append(waiting, r)
	}
	rows.Close()
	for _, r := range waiting {
		if h.settleReboot(r) {
			logger.Info("task job's reboot done", "id", r.spec.ID)
			continue
		}
		runner, ok := h.runners[tasks.StepReboot].(*RebootRunner)
		if !ok {
			continue
		}
		logger.Info("task job still waiting on its reboot", "id", r.spec.ID, "boot_id", r.bootID)
		job := &Job{Spec: r.spec, LogPath: h.logPath(r.spec.ID), StartedAt: r.startedAt, Unit: r.unit, h: h}
		h.launch(job, func(ctx context.Context, job *Job) error {
			runner.await(ctx, job)
			return nil
		})
	}
}

// setRebooting marks the running job as waiting on its reboot, in this
// boot, with the unit that schedules it.
func (j *Job) setRebooting(unit string) error {
	res, err := j.h.DB.Exec(`UPDATE task_jobs SET status = ?, unit = ?, boot_id = ? WHERE id = ? AND status = ?`,
		tasks.JobRebooting, unit, j.h.bootID(), j.Spec.ID, tasks.JobRunning)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return errors.New("the job is no longer running")
	}
	j.Unit = unit
	return nil
}
