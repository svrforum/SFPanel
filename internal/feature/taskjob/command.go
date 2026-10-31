package taskjob

// A command step runs a script as root, in a transient unit of its own the
// way the packages module runs apt (internal/feature/packages,
// transaction.go): PID 1 runs it, so a panel restart neither stops it nor
// loses how it ended — the script's last line says, and the unit's Result
// says when RuntimeMaxSec cut it off.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	osExec "os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/lifecycle"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	// commandUnitPrefix names a command job's unit: sfpanel-task-<id>.service.
	commandUnitPrefix = "sfpanel-task-"

	// commandScript runs its arguments — /bin/sh and the job's script file —
	// and records the exit status in the log. The step's script never
	// becomes part of a command line.
	commandScript = `"$@"; rc=$?; echo "[sfpanel-task] exit=$rc"; exit $rc`

	// unknownLimit is how many answers in a row from systemctl that are
	// neither "running" nor "gone" are waited out before the log decides.
	unknownLimit = 30
	// stopTimeout bounds systemctl stop, which waits for the unit to go.
	stopTimeout = 2 * time.Minute
)

// systemdActive says whether systemd runs this host; tests replace it.
var systemdActive = lifecycle.IsSystemdActive

// CommandRunner runs command steps.
type CommandRunner struct {
	Cmd exec.Commander
	// poll is how often a running unit is asked whether it is still there.
	poll time.Duration
}

// NewCommandRunner returns the runner of command steps.
func NewCommandRunner(cmd exec.Commander) *CommandRunner {
	return &CommandRunner{Cmd: cmd, poll: time.Second}
}

// Check has nothing to refuse: a command step is the operator's own command.
func (r *CommandRunner) Check(tasks.JobSpec) tasks.CheckVerdict { return tasks.CheckVerdict{} }

// commandTimeout is the step's time limit.
func commandTimeout(step *tasks.CommandStep) time.Duration {
	if step.Timeout > 0 {
		return step.Timeout
	}
	return tasks.DefaultCommandTimeout
}

// Start writes the step's script beside the job's log and runs it in a
// transient unit:
//
//	systemd-run --unit=sfpanel-task-<id>.service --collect --quiet
//	  -p RuntimeMaxSec=<seconds> -p StandardOutput=append:<log>
//	  -p StandardError=inherit
//	  -- /bin/sh -c <commandScript> sh /bin/sh <id>.sh
//
// then watches the unit to its end. Without systemd the panel runs the
// script itself.
func (r *CommandRunner) Start(ctx context.Context, job *Job) error {
	step := job.Spec.Command
	timeout := commandTimeout(step)
	body := step.Script
	if body == "" {
		body = step.Run
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	script := scriptPath(job.LogPath)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return fmt.Errorf("write the job's script: %w", err)
	}
	// WriteFile keeps the mode of a file that was there.
	if err := os.Chmod(script, 0o700); err != nil {
		removeScript(job)
		return fmt.Errorf("write the job's script: %w", err)
	}

	if !systemdActive() || !r.Cmd.Exists("systemd-run") {
		res := runDirect(ctx, job, script, timeout)
		removeScript(job)
		if err := job.Finish(res); err != nil {
			logger.Error("recording a job's end", "id", job.Spec.ID, "error", err)
		}
		return nil
	}
	unit := commandUnitPrefix + job.Spec.ID + ".service"
	// Recorded first: a panel that stops right after systemd-run asks
	// systemd about the unit at boot.
	if err := job.SetUnit(unit); err != nil {
		removeScript(job)
		return fmt.Errorf("record the job's unit: %w", err)
	}
	out, err := r.Cmd.RunWithTimeout(30*time.Second, "systemd-run", commandUnitArgv(job, unit, script, timeout)...)
	if err != nil {
		removeScript(job)
		appendLog(job, strings.TrimSpace(out))
		return fmt.Errorf("systemd-run: %s", strings.TrimSpace(firstNonEmpty(out, err.Error())))
	}
	r.watch(ctx, job, timeout)
	return nil
}

// commandUnitArgv is the systemd-run command line of a command job.
func commandUnitArgv(job *Job, unit, script string, timeout time.Duration) []string {
	return []string{
		"--unit=" + unit, "--collect", "--quiet",
		"-p", "RuntimeMaxSec=" + strconv.FormatInt(int64(math.Ceil(timeout.Seconds())), 10),
		"-p", "StandardOutput=append:" + job.LogPath,
		"-p", "StandardError=inherit",
		"--", "/bin/sh", "-c", commandScript, "sh", "/bin/sh", script,
	}
}

// Resume takes back a command job at boot: its unit is watched again while
// systemd runs it, and once it is gone the job ends as it says.
func (r *CommandRunner) Resume(ctx context.Context, job *Job) error {
	r.watch(ctx, job, commandTimeout(job.Spec.Command))
	return nil
}

// watch waits for the job's unit to go away, then records how the job ended.
// A forced cancel stops the unit, and the job ends cancelled once it is
// gone. --collect unloads a unit as soon as it ends, so the log is what says
// how its command ended.
func (r *CommandRunner) watch(ctx context.Context, job *Job, timeout time.Duration) {
	cancel := ctx.Done()
	cancelled := false
	unknown := 0
	for {
		out, _ := r.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "is-active", job.Unit)
		state := strings.TrimSpace(out)
		if state == "inactive" || state == "failed" {
			break
		}
		if unitRunning(state) {
			unknown = 0
		} else if unknown++; unknown >= unknownLimit {
			break // systemctl is not answering; let the log say
		}
		select {
		case <-cancel:
			cancel, cancelled = nil, true
			if out, err := r.Cmd.RunWithTimeout(stopTimeout, "systemctl", "stop", job.Unit); err != nil {
				logger.Warn("stopping a cancelled job's unit", "id", job.Spec.ID, "unit", job.Unit,
					"error", err, "output", response.SanitizeOutput(out))
			}
		case <-time.After(r.poll):
		}
	}
	res := r.settle(job, timeout)
	if cancelled {
		res = Result{Status: tasks.JobCancelled, Changed: true, Summary: "cancelled: its unit was stopped"}
	}
	removeScript(job)
	if err := job.Finish(res); err != nil {
		logger.Error("recording a job's end", "id", job.Spec.ID, "error", err)
	}
}

// settle is how a command job whose unit is gone ended: systemd's word for
// a unit RuntimeMaxSec stopped, else the exit marker on the log's last line.
// With neither the command was stopped or killed before it returned — a
// timeout still, when the step's time was up, since --collect may have
// unloaded the unit before systemd was asked; otherwise interrupted, which
// is not a success. A command counts as a change however it ended.
func (r *CommandRunner) settle(job *Job, timeout time.Duration) Result {
	out, _ := r.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "show", "-p", "Result", "--value", job.Unit)
	if strings.TrimSpace(out) == "timeout" {
		return timedOut(timeout)
	}
	if code, ok := exitFromLog(job.LogPath); ok {
		return exitResult(code)
	}
	if job.Now().Sub(job.StartedAt) >= timeout {
		return timedOut(timeout)
	}
	return Result{Status: tasks.JobInterrupted, Changed: true,
		Summary: "the command stopped without saying how it ended: its unit was stopped or killed"}
}

func exitResult(code int) Result {
	res := Result{Status: tasks.JobSucceeded, Changed: true, ExitCode: &code, Summary: "exit " + strconv.Itoa(code)}
	if code != 0 {
		res.Status = tasks.JobFailed
	}
	return res
}

func timedOut(timeout time.Duration) Result {
	return Result{Status: tasks.JobFailed, Changed: true, Summary: "timed out after " + shortDuration(timeout)}
}

// shortDuration is d without the zero units time.Duration spells out:
// 10m rather than 10m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// runDirect runs the script as the panel's own child, on a host with no
// systemd to hand it to: a panel restart ends it there. os/exec rather than
// the Commander because the output has to reach the job's log as it is
// written, and a cancel or the timeout has to kill the script's whole
// process group.
var runDirect = func(ctx context.Context, job *Job, script string, timeout time.Duration) Result {
	f, err := os.OpenFile(job.LogPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return Result{Status: tasks.JobFailed, Summary: "could not open the job's log"}
	}
	defer f.Close()
	cmd := osExec.Command("/bin/sh", "-c", commandScript, "sh", "/bin/sh", script)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return Result{Status: tasks.JobFailed, Summary: "the command could not be started: " + response.SanitizeOutput(err.Error())}
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	kill := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		kill()
		return Result{Status: tasks.JobCancelled, Changed: true, Summary: "cancelled: the command was killed"}
	case <-timer.C:
		kill()
		return timedOut(timeout)
	}
	if code, ok := exitFromLog(job.LogPath); ok {
		return exitResult(code)
	}
	return Result{Status: tasks.JobInterrupted, Changed: true, Summary: "the command stopped without saying how it ended"}
}

// removeScript removes the job's script once nothing runs it: it holds what
// the step said, which may be a secret, and the spec keeps it anyway.
func removeScript(job *Job) {
	if err := os.Remove(scriptPath(job.LogPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("removing a job's script", "id", job.Spec.ID, "error", err)
	}
}

// appendLog adds a line the panel writes itself to the job's output.
func appendLog(job *Job, text string) {
	if text == "" {
		return
	}
	f, err := os.OpenFile(job.LogPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		logger.Warn("writing a job's log", "id", job.Spec.ID, "error", err)
		return
	}
	defer f.Close()
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	_, _ = f.WriteString(text)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
