package taskjob

// A packages step is the packages module's own transactions
// (internal/feature/packages, jobtx.go), behind the guards the Packages page
// has: update first, as a transaction of its own, then the upgrade, the
// install and the removal the step asks for, one after another. apt runs in
// a unit of its own, so a panel restart stops none of it; the job's log
// says which transaction the step was in, and the job is taken back there.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/feature/packages"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	// aptUnitPrefix names the unit a package transaction runs in:
	// sfpanel-apt-<transaction id>.service.
	aptUnitPrefix = "sfpanel-apt-"
	// pkgUpdate is the lists refresh, which runs in the panel, not a unit.
	pkgUpdate = "update"
	// phaseMarker starts the line written to the job's log as each of the
	// step's transactions starts: its place in the step and its ID. What
	// follows it, up to the next one, is that transaction's output.
	phaseMarker = "[sfpanel-task] phase="
)

var phaseLine = regexp.MustCompile(`^\[sfpanel-task\] phase=([0-9]+) tx=([0-9]{8}-[0-9]{6}-[0-9a-f]{4})$`)

// PackageTransactions is the packages module's API for task jobs
// (*packages.Handler).
type PackageTransactions interface {
	StartJobTransaction(kind string, packages []string, update bool) (string, error)
	JobTransaction(id string) (running bool, result string, exitCode int, logPath string, err error)
	ChangedPackages(logPath string) (int, bool)
}

// PackageRunner runs packages steps.
type PackageRunner struct {
	Packages PackageTransactions
	// Cmd stops a transaction's unit when the job is cancelled with force.
	Cmd exec.Commander
	// poll is how often a running transaction is asked after.
	poll time.Duration
}

// NewPackageRunner returns the runner of packages steps.
func NewPackageRunner(p PackageTransactions, cmd exec.Commander) *PackageRunner {
	return &PackageRunner{Packages: p, Cmd: cmd, poll: time.Second}
}

// pkgPhase is one transaction of a step.
type pkgPhase struct {
	kind  string
	names []string
}

// packagePhases is the step's transactions, in the order they run.
func packagePhases(p *tasks.PackagesStep) []pkgPhase {
	var out []pkgPhase
	if p.Update {
		out = append(out, pkgPhase{kind: pkgUpdate})
	}
	if p.UpgradeAll {
		out = append(out, pkgPhase{kind: "upgrade"})
	} else if len(p.Upgrade) > 0 {
		out = append(out, pkgPhase{kind: "upgrade", names: p.Upgrade})
	}
	if len(p.Install) > 0 {
		out = append(out, pkgPhase{kind: "install", names: p.Install})
	}
	if len(p.Remove) > 0 {
		out = append(out, pkgPhase{kind: "remove", names: p.Remove})
	}
	return out
}

// Check refuses a name apt-get could not take, as the endpoints refuse it.
// What apt would do — a Docker engine on Proxmox VE — is asked when the
// transaction starts.
func (r *PackageRunner) Check(spec tasks.JobSpec) tasks.CheckVerdict {
	p := spec.Packages
	for _, names := range [][]string{p.Upgrade, p.Install, p.Remove} {
		for _, name := range names {
			if !packages.ValidPackageName(name) {
				return tasks.CheckVerdict{Refused: true, Code: response.ErrInvalidPackageName,
					Reason: "invalid package name " + strconv.Quote(name)}
			}
		}
	}
	return tasks.CheckVerdict{}
}

// Start runs the step's transactions from the first.
func (r *PackageRunner) Start(ctx context.Context, job *Job) error {
	r.run(ctx, job, packagePhases(job.Spec.Packages), 0, "", 0, nil)
	return nil
}

// Resume takes back a packages job at boot, at the transaction whose unit it
// was in: the phase markers in its log say which of the step's it was and how
// much of its output was copied. The transaction is followed to its end,
// whether or not it outlived the panel, and the ones after it still run.
func (r *PackageRunner) Resume(ctx context.Context, job *Job) error {
	phases := packagePhases(job.Spec.Packages)
	tx, ok := strings.CutPrefix(job.Unit, aptUnitPrefix)
	if ok {
		tx, ok = strings.CutSuffix(tx, ".service")
	}
	marks, size, err := readPhaseMarks(job.LogPath)
	if !ok || err != nil {
		return fmt.Errorf("the job's transaction could not be found again (unit %q)", job.Unit)
	}
	from, copied := 0, int64(0)
	var done []string
	if n := len(marks); n > 0 && marks[n-1].tx == tx {
		from, copied = marks[n-1].phase, size-marks[n-1].end
		marks = marks[:n-1]
	} else {
		// The panel stopped after the transaction started and before its
		// marker was written.
		if n > 0 {
			from = marks[n-1].phase + 1
		}
		if from < len(phases) {
			appendLog(job, phaseMarker+strconv.Itoa(from)+" tx="+tx)
		}
	}
	for _, m := range marks {
		if m.phase < len(phases) && phases[m.phase].kind != pkgUpdate {
			done = append(done, m.tx)
		}
	}
	if from >= len(phases) || phases[from].kind == pkgUpdate {
		return fmt.Errorf("the job's log does not say which transaction %s was", tx)
	}
	r.run(ctx, job, phases, from, tx, copied, done)
	return nil
}

// run carries the step from phases[from] to its end and records how it
// ended. tx is the transaction phases[from] is already in, if it is, and
// copied how much of its output is in the job's log; done is the
// transactions before it whose changes count.
func (r *PackageRunner) run(ctx context.Context, job *Job, phases []pkgPhase, from int, tx string, copied int64, done []string) {
	finish := func(res Result) {
		if err := job.Finish(res); err != nil {
			logger.Error("recording a job's end", "id", job.Spec.ID, "error", err)
		}
	}
	for i := from; i < len(phases); i++ {
		ph := phases[i]
		if tx == "" {
			if ctx.Err() != nil {
				finish(r.ended(tasks.JobCancelled, "cancelled before the "+ph.kind+" transaction", nil, done))
				return
			}
			id, err := r.Packages.StartJobTransaction(ph.kind, ph.names, false)
			if err != nil {
				summary := response.SanitizeOutput(err.Error())
				var refusal *packages.Refusal
				if errors.As(err, &refusal) {
					summary = refusal.Code + ": " + refusal.Message
				}
				finish(r.ended(tasks.JobFailed, summary, nil, done))
				return
			}
			tx, copied = id, 0
			unit := ""
			if ph.kind != pkgUpdate {
				unit = aptUnitPrefix + id + ".service"
			}
			if err := job.SetUnit(unit); err != nil {
				logger.Warn("recording a job's unit", "id", job.Spec.ID, "unit", unit, "error", err)
			}
			appendLog(job, phaseMarker+strconv.Itoa(i)+" tx="+id)
		}
		result, code, cancelled, err := r.wait(ctx, job, ph, tx, &copied)
		if ph.kind != pkgUpdate {
			done = append(done, tx)
		}
		tx = ""
		switch {
		case cancelled:
			finish(r.ended(tasks.JobCancelled, "cancelled during the "+ph.kind+" transaction", nil, done))
			return
		case err != nil:
			finish(r.ended(tasks.JobInterrupted, "the "+ph.kind+" transaction's record is gone", nil, done))
			return
		case result == "success":
		case result == "failed":
			var exit *int
			if code >= 0 {
				exit = &code
			}
			finish(r.ended(tasks.JobFailed, fmt.Sprintf("%s failed with exit status %d", ph.kind, code), exit, done))
			return
		default:
			finish(r.ended(tasks.JobInterrupted, "the "+ph.kind+" transaction stopped without saying how it ended", nil, done))
			return
		}
	}
	zero := 0
	finish(r.ended(tasks.JobSucceeded, "", &zero, done))
}

// wait follows transaction tx to its end, copying its output to the job's
// log from copied on. A forced cancel stops the transaction's unit; the lists
// refresh has none and runs to its end.
func (r *PackageRunner) wait(ctx context.Context, job *Job, ph pkgPhase, tx string, copied *int64) (result string, code int, cancelled bool, err error) {
	cancel := ctx.Done()
	for {
		running, result, code, logPath, err := r.Packages.JobTransaction(tx)
		if err != nil {
			return "", -1, cancelled, err
		}
		*copied = copyOutput(job, logPath, *copied)
		if !running {
			return result, code, cancelled, nil
		}
		select {
		case <-cancel:
			cancel, cancelled = nil, true
			if ph.kind != pkgUpdate {
				unit := aptUnitPrefix + tx + ".service"
				if out, err := r.Cmd.RunWithTimeout(stopTimeout, "systemctl", "stop", unit); err != nil {
					logger.Warn("stopping a cancelled job's transaction", "id", job.Spec.ID, "unit", unit,
						"error", err, "output", response.SanitizeOutput(out))
				}
			}
		case <-time.After(r.poll):
		}
	}
}

// ended is the job's result: status and summary, with what changed counted
// from apt's summaries of the transactions in done. A step with nothing but
// the lists refresh changed no package. A transaction whose summary apt did
// not print counts as a change, since nothing says it was none.
func (r *PackageRunner) ended(status, summary string, exit *int, done []string) Result {
	res := Result{Status: status, ExitCode: exit, Summary: summary}
	if len(done) == 0 {
		if status == tasks.JobSucceeded {
			res.Summary = "package lists updated"
		}
		return res
	}
	n, counted := 0, true
	for _, tx := range done {
		_, _, _, logPath, err := r.Packages.JobTransaction(tx)
		c, ok := 0, false
		if err == nil {
			c, ok = r.Packages.ChangedPackages(logPath)
		}
		if !ok {
			counted = false
			continue
		}
		n += c
	}
	var what string
	switch {
	case !counted:
		res.Changed, what = true, "packages changed; apt printed no summary, so they were not counted"
	case n == 0:
		what = "no packages changed"
	case n == 1:
		res.Changed, what = true, "1 package changed"
	default:
		res.Changed, what = true, strconv.Itoa(n)+" packages changed"
	}
	if res.Summary == "" {
		res.Summary = what
	} else {
		res.Summary += " (" + what + ")"
	}
	return res
}

// copyOutput appends what the transaction logged at src after offset to the
// job's log, and returns how far it has copied.
func copyOutput(job *Job, src string, offset int64) int64 {
	in, err := os.Open(src)
	if err != nil {
		return offset
	}
	defer in.Close()
	if _, err := in.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	out, err := os.OpenFile(job.LogPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		logger.Warn("writing a job's log", "id", job.Spec.ID, "error", err)
		return offset
	}
	defer out.Close()
	n, err := io.Copy(out, in)
	if err != nil {
		logger.Warn("copying a transaction's output", "id", job.Spec.ID, "error", err)
	}
	return offset + n
}

// phaseMark is a phase marker in a job's log: the step's transaction, and
// the offset its output starts at.
type phaseMark struct {
	phase int
	tx    string
	end   int64
}

// readPhaseMarks lists the phase markers in the job's log, and its size.
func readPhaseMarks(path string) ([]phaseMark, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var (
		marks []phaseMark
		pos   int64
	)
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadString('\n')
		pos += int64(len(line))
		if strings.HasSuffix(line, "\n") {
			if m := phaseLine.FindStringSubmatch(strings.TrimSuffix(line, "\n")); m != nil {
				phase, _ := strconv.Atoi(m[1])
				marks = append(marks, phaseMark{phase: phase, tx: m[2], end: pos})
			}
		}
		if errors.Is(err, io.EOF) {
			return marks, pos, nil
		}
		if err != nil {
			return nil, 0, err
		}
	}
}
