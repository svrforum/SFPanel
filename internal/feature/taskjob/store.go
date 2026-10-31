package taskjob

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/tasks"
)

const (
	// keepJobs and keepFor bound what a node keeps: its newest 200 jobs, none
	// older than 30 days. A job running or waiting on its reboot is kept
	// however old.
	keepJobs = 200
	keepFor  = 30 * 24 * time.Hour

	// outputPiece is the most output one GET returns. The leader reads a
	// node's jobs over the cluster's gRPC proxy, whose messages stop at 4 MB.
	outputPiece = 256 << 10
	// cutContext is how much is read on each side of a cut to see that no
	// escape sequence or secret straddles it.
	cutContext = 4 << 10
	// cutTries is how many places a piece looks at for a safe cut before it
	// takes one anyway, so a log made of nothing but secrets' names still
	// moves.
	cutTries = 64

	// exitMarker starts the last line a job run in a unit writes: how its
	// command ended.
	exitMarker = "[sfpanel-task] exit="

	// systemctlTimeout bounds each question to systemd at boot.
	systemctlTimeout = 10 * time.Second

	// timeLayout is UTC with nanoseconds always written out, so started_at
	// sorts as text.
	timeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

var errNotFound = errors.New("no such task job")

// record is one row of task_jobs.
type record struct {
	spec      tasks.JobSpec
	status    string
	changed   bool
	exitCode  *int
	summary   string
	unit      string // the systemd unit the job runs in, if it does
	bootID    string // the boot it started in
	username  string
	startedAt time.Time
	endedAt   time.Time // zero while the job runs
}

// Job is a started job, as a Runner gets it.
type Job struct {
	Spec tasks.JobSpec
	// LogPath is the job's output file, created empty (0600) before the
	// runner starts. The runner writes to it, or points a unit at it.
	LogPath   string
	StartedAt time.Time
	h         *Handler
}

// Result is how a job ended.
type Result struct {
	Status   string // succeeded, failed, interrupted or cancelled
	Changed  bool
	ExitCode *int
	Summary  string
}

// Finish records how the job ended, and frees the node for the next one. A
// job that is already over keeps the end it has.
func (j *Job) Finish(r Result) error { return j.h.finish(j.Spec.ID, r) }

// active reports whether a job in status holds the node.
func active(status string) bool {
	return status == tasks.JobRunning || status == tasks.JobRebooting
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func (h *Handler) logPath(id string) string { return filepath.Join(h.LogDir, id+".log") }

// createLog creates the job's empty output file, and the directory for it.
func (h *Handler) createLog(id string) error {
	if err := os.MkdirAll(h.LogDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.logPath(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// O_TRUNC keeps the mode of a file left over from a database that was
	// restored to before it.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (h *Handler) insert(r record) error {
	spec, err := json.Marshal(r.spec)
	if err != nil {
		return err
	}
	var ended any
	if !r.endedAt.IsZero() {
		ended = formatTime(r.endedAt)
	}
	_, err = h.DB.Exec(`INSERT INTO task_jobs
		(id, run_id, task, step, kind, spec, status, changed, exit_code, summary, unit, boot_id, username, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.spec.ID, r.spec.RunID, r.spec.Task, r.spec.Step, r.spec.Kind, string(spec), r.status, r.changed,
		r.exitCode, r.summary, r.unit, r.bootID, r.username, formatTime(r.startedAt), ended)
	return err
}

const recordColumns = `spec, status, changed, exit_code, summary, unit, boot_id, username, started_at, ended_at`

func scanRecord(row interface{ Scan(...any) error }) (record, error) {
	var (
		r       record
		spec    string
		exit    sql.NullInt64
		started string
		ended   sql.NullString
	)
	if err := row.Scan(&spec, &r.status, &r.changed, &exit, &r.summary, &r.unit, &r.bootID, &r.username, &started, &ended); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record{}, errNotFound
		}
		return record{}, err
	}
	if err := json.Unmarshal([]byte(spec), &r.spec); err != nil {
		return record{}, fmt.Errorf("job spec: %w", err)
	}
	if exit.Valid {
		code := int(exit.Int64)
		r.exitCode = &code
	}
	var err error
	if r.startedAt, err = time.Parse(timeLayout, started); err != nil {
		return record{}, fmt.Errorf("job start: %w", err)
	}
	if ended.Valid {
		if r.endedAt, err = time.Parse(timeLayout, ended.String); err != nil {
			return record{}, fmt.Errorf("job end: %w", err)
		}
	}
	return r, nil
}

func (h *Handler) get(id string) (record, error) {
	return scanRecord(h.DB.QueryRow(`SELECT `+recordColumns+` FROM task_jobs WHERE id = ?`, id))
}

// active is the job holding the node, nil when it is free.
func (h *Handler) active() (*record, error) {
	r, err := scanRecord(h.DB.QueryRow(`SELECT `+recordColumns+` FROM task_jobs
		WHERE status IN (?, ?) ORDER BY started_at LIMIT 1`, tasks.JobRunning, tasks.JobRebooting))
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (h *Handler) finish(id string, r Result) error {
	switch r.Status {
	case tasks.JobSucceeded, tasks.JobFailed, tasks.JobInterrupted, tasks.JobCancelled:
	default:
		return fmt.Errorf("a job does not end %q", r.Status)
	}
	h.cancelMu.Lock()
	cancel := h.cancels[id]
	delete(h.cancels, id)
	h.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	_, err := h.DB.Exec(`UPDATE task_jobs SET status = ?, changed = ?, exit_code = ?, summary = ?, ended_at = ?
		WHERE id = ? AND status IN (?, ?)`,
		r.Status, r.Changed, r.ExitCode, r.Summary, formatTime(h.now()), id, tasks.JobRunning, tasks.JobRebooting)
	if err == nil {
		logger.Info("task job ended", "id", id, "status", r.Status, "changed", r.Changed)
	}
	return err
}

// sweep drops the jobs past what a node keeps, record and log file. A log
// that cannot be removed keeps its record, for the next sweep to retry.
func (h *Handler) sweep() error {
	rows, err := h.DB.Query(`SELECT id FROM task_jobs WHERE status NOT IN (?, ?) AND (started_at < ? OR id NOT IN
		(SELECT id FROM task_jobs ORDER BY started_at DESC, rowid DESC LIMIT ?))`,
		tasks.JobRunning, tasks.JobRebooting, formatTime(h.now().Add(-keepFor)), keepJobs)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := os.Remove(h.logPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("removing an old job's log", "id", id, "error", err)
			continue
		}
		if _, err := h.DB.Exec(`DELETE FROM task_jobs WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// view is a job as the API answers it, with its output from skip on when
// withOutput is set.
func (h *Handler) view(r record, skip int64, withOutput bool) tasks.JobView {
	v := tasks.JobView{
		ID: r.spec.ID, RunID: r.spec.RunID, Kind: r.spec.Kind, Status: r.status,
		Changed: r.changed, ExitCode: r.exitCode, Summary: r.summary,
		NextSkip: skip, StartedAt: r.startedAt, EndedAt: r.endedAt,
	}
	if !withOutput {
		return v
	}
	final := !active(r.status)
	out, next, atEnd, err := readOutput(h.logPath(r.spec.ID), skip, final)
	if err != nil {
		logger.Warn("reading a job's output", "id", r.spec.ID, "error", err)
	}
	v.Output, v.NextSkip, v.Done = out, next, final && atEnd && err == nil
	return v
}

// readOutput reads a job's output from skip on: at most outputPiece bytes,
// sanitized, ending at a cut that splits no character and no escape sequence
// or secret, so the pieces put together and trimmed are the whole output
// through response.SanitizeLog. It returns the piece, the offset to read
// from next, and whether that offset is the end of the file. final says the
// job is over and its output complete; until then a last line that may go
// on is left for the next read. A missing file is empty output.
func readOutput(path string, skip int64, final bool) (string, int64, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", skip, true, nil
	}
	if err != nil {
		return "", skip, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", skip, false, err
	}
	size := st.Size()
	skip = min(skip, size)
	buf := make([]byte, min(size-skip, outputPiece+cutContext))
	if _, err := io.ReadFull(io.NewSectionReader(f, skip, int64(len(buf))), buf); err != nil {
		return "", skip, false, err
	}
	end := cutPiece(buf, skip+int64(len(buf)) == size, final)
	next := skip + int64(end)
	return response.SanitizeLogPiece(string(buf[:end])), next, next == size, nil
}

// cutPiece is where the piece read into buf ends. atEOF says buf runs to the
// end of the file; final, that the file is complete.
func cutPiece(buf []byte, atEOF, final bool) int {
	if atEOF && final && len(buf) <= outputPiece {
		return len(buf)
	}
	full := len(buf) >= outputPiece
	limit := min(len(buf), outputPiece)
	// Between lines: the last line break in the piece whose cut is safe.
	last := -1
	for i, tries := limit, 0; tries < cutTries; tries++ {
		j := bytes.LastIndexByte(buf[:i], '\n')
		if j < 0 {
			break
		}
		if last < 0 {
			last = j + 1
		}
		if safeCut(buf, j+1, atEOF, final) {
			return j + 1
		}
		i = j
	}
	if !full {
		// More is on its way and there is room for it: wait for it.
		return 0
	}
	if last >= 0 {
		return last
	}
	// One line fills the piece: cut inside it, between characters. c stays
	// short of len(buf) so the byte after the cut is there to look at.
	c := min(limit, len(buf)-1)
	for tries := 0; c > 0 && tries < cutTries; c-- {
		if utf8.RuneStart(buf[c]) {
			if safeCut(buf, c, atEOF, final) {
				return c
			}
			tries++
		}
	}
	c = min(limit, len(buf)-1)
	for c > 0 && !utf8.RuneStart(buf[c]) {
		c--
	}
	return c
}

// safeCut reports whether cutting buf at c sanitizes the two sides as the
// text around the cut would be sanitized whole. While more may be written, a
// cut with nothing but whitespace after it so far is not safe: the value of a
// secret whose name ends the line may still be on its way.
func safeCut(buf []byte, c int, atEOF, final bool) bool {
	before := string(buf[max(0, c-cutContext):c])
	after := string(buf[c:min(len(buf), c+cutContext)])
	if atEOF && !final && c+len(after) == len(buf) && strings.TrimSpace(after) == "" {
		return false
	}
	return response.SanitizeLogPiece(before+after) == response.SanitizeLogPiece(before)+response.SanitizeLogPiece(after)
}

// exitFromLog is the status on the log's last line, if that is the marker.
func exitFromLog(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	const tail = 4096
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		if _, err := f.Seek(-tail, io.SeekEnd); err != nil {
			return 0, false
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return 0, false
	}
	text := strings.TrimRight(string(data), "\r\n\t ")
	last := text[strings.LastIndexByte(text, '\n')+1:]
	rest, ok := strings.CutPrefix(strings.TrimSpace(last), exitMarker)
	if !ok {
		return 0, false
	}
	code, err := strconv.Atoi(rest)
	return code, err == nil
}

// unitRunning reports whether systemctl is-active answered for a unit that
// is still there.
func unitRunning(state string) bool {
	switch state {
	case "active", "activating", "deactivating", "reloading", "refreshing":
		return true
	}
	return false
}

// Recover settles, at boot, the jobs the last process left running. A job in
// a unit systemd still runs stays running. One whose unit is gone ended as
// the exit marker on its log's last line says, and without a marker it was
// interrupted: the command did not get to say how it ended, which is not a
// success. A job that ran inside the panel — no unit — was interrupted when
// the panel stopped. A job waiting on its reboot is left for the boot-ID
// check. Then the old jobs are swept.
func (h *Handler) Recover() {
	rows, err := h.DB.Query(`SELECT `+recordColumns+` FROM task_jobs WHERE status = ?`, tasks.JobRunning)
	if err != nil {
		logger.Error("reading the jobs left running", "error", err)
		return
	}
	var running []record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			logger.Error("reading a job left running", "error", err)
			continue
		}
		running = append(running, r)
	}
	rows.Close()

	for _, r := range running {
		id := r.spec.ID
		res := Result{Status: tasks.JobInterrupted, Summary: "the panel stopped while the job ran"}
		if r.unit != "" {
			out, _ := h.Cmd.RunWithTimeout(systemctlTimeout, "systemctl", "is-active", r.unit)
			if unitRunning(strings.TrimSpace(out)) {
				logger.Info("task job still running after a restart", "id", id, "unit", r.unit)
				continue
			}
			if code, ok := exitFromLog(h.logPath(id)); ok {
				res = Result{Status: tasks.JobSucceeded, ExitCode: &code, Summary: "exit 0"}
				if code != 0 {
					res.Status, res.Summary = tasks.JobFailed, "exit "+strconv.Itoa(code)
				}
			} else {
				res.Summary = "the job's unit ended without saying how"
			}
		}
		if err := h.finish(id, res); err != nil {
			logger.Error("settling a job left running", "id", id, "error", err)
		}
	}
	if err := h.sweep(); err != nil {
		logger.Warn("sweeping old jobs", "error", err)
	}
}
