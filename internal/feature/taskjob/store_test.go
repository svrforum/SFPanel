package taskjob

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// bigLog is about 1.5 MiB of what a job prints: Korean and English, colour
// codes, secrets to redact, blank lines, and one line longer than a whole
// piece, so a cut has to fall inside a line as well as between lines.
func bigLog() string {
	var b strings.Builder
	for i := 0; b.Len() < 1200<<10; i++ {
		switch i % 5 {
		case 0:
			fmt.Fprintf(&b, "\x1b[32m설치 중\x1b[0m 패키지 %d — 진행률 %d%%\n", i, i%100)
		case 1:
			fmt.Fprintf(&b, "Setting up libfoo%d (1.2.%d) ...\n", i, i)
		case 2:
			fmt.Fprintf(&b, "DB_PASSWORD=hunter%d 그리고 --token secret%d\n", i, i)
		case 3:
			b.WriteString("\n   \n")
		case 4:
			fmt.Fprintf(&b, "\x1b]0;title %d\x07완료 ✓ 🎉\n", i)
		}
	}
	b.WriteString(strings.Repeat("긴줄", 300<<10/6) + "\n")
	b.WriteString("[sfpanel-task] exit=0\n")
	return b.String()
}

// A job's output comes in pieces from where the last one ended. Put back
// together they are the whole output sanitized, and no piece — nor the offset
// it stops at — splits a character, even inside a line longer than a piece.
func TestOutputIsPagedSanitizedAndWholeRunesOnly(t *testing.T) {
	n := newTestNode(t)
	if status, env := n.start(t, spec(runA, 0)); status != 201 {
		t.Fatalf("start: %d %q", status, env.code())
	}
	job := n.runner.next(t)
	raw := bigLog()
	if len(raw) < 1500<<10 {
		t.Fatalf("the fixture is %d bytes, want 1.5 MiB", len(raw))
	}
	if err := os.WriteFile(job.LogPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	code := 0
	if err := job.Finish(Result{Status: tasks.JobSucceeded, ExitCode: &code}); err != nil {
		t.Fatal(err)
	}

	var joined strings.Builder
	var skip int64
	pieces := 0
	for {
		status, env := n.do(t, http.MethodGet, fmt.Sprintf("/api/v1/task-jobs/%s-0?skip=%d", runA, skip), nil, true)
		if status != http.StatusOK {
			t.Fatalf("skip=%d: %d %q", skip, status, env.code())
		}
		v := view(t, env)
		pieces++
		if !utf8.ValidString(v.Output) {
			t.Fatalf("the piece from %d is not whole characters", skip)
		}
		if v.NextSkip < skip || !utf8.ValidString(raw[skip:v.NextSkip]) {
			t.Fatalf("the piece from %d stops at %d, inside a character", skip, v.NextSkip)
		}
		joined.WriteString(v.Output)
		if v.Done {
			if v.NextSkip != int64(len(raw)) {
				t.Fatalf("done at %d of %d bytes", v.NextSkip, len(raw))
			}
			break
		}
		if v.NextSkip == skip {
			t.Fatalf("no progress from %d on a finished job", skip)
		}
		skip = v.NextSkip
		if pieces > 100 {
			t.Fatal("the output never ended")
		}
	}
	if pieces < 6 {
		t.Fatalf("1.5 MiB came in %d pieces; it was not paged", pieces)
	}
	want := response.SanitizeLog(raw)
	if got := strings.TrimSpace(joined.String()); got != want {
		i := 0
		for i < len(got) && i < len(want) && got[i] == want[i] {
			i++
		}
		t.Fatalf("the pieces put together differ from the whole output sanitized, at byte %d of %d: got %q, want %q",
			i, len(want), got[max(0, i-40):min(len(got), i+40)], want[max(0, i-40):min(len(want), i+40)])
	}
	if strings.Contains(joined.String(), "hunter1") || strings.Contains(joined.String(), "\x1b") {
		t.Fatal("a secret or an escape sequence came through")
	}
}

// While a job runs, its last line may not be finished, and a secret's name
// may be at the end of one line with the value still to come on the next:
// both wait for what follows, so each piece is sanitized as the whole
// output would be.
func TestARunningJobHoldsBackALineThatMayGoOn(t *testing.T) {
	n := newTestNode(t)
	if status, env := n.start(t, spec(runA, 0)); status != 201 {
		t.Fatalf("start: %d %q", status, env.code())
	}
	job := n.runner.next(t)
	read := func(skip int64) tasks.JobView {
		t.Helper()
		status, env := n.do(t, http.MethodGet, fmt.Sprintf("/api/v1/task-jobs/%s-0?skip=%d", runA, skip), nil, true)
		if status != http.StatusOK {
			t.Fatalf("skip=%d: %d %q", skip, status, env.code())
		}
		return view(t, env)
	}
	for _, tc := range []struct{ log, shown string }{
		{"first line\nsecond li", "first line\n"},
		{"first line\napi_key:\n", "first line\n"},
		{"first line\n--password\n\n", "first line\n"},
	} {
		if err := os.WriteFile(job.LogPath, []byte(tc.log), 0o600); err != nil {
			t.Fatal(err)
		}
		v := read(0)
		if v.Output != tc.shown || v.NextSkip != int64(len(tc.shown)) || v.Done {
			t.Fatalf("log %q while running: %q to %d (done %v), want %q", tc.log, v.Output, v.NextSkip, v.Done, tc.shown)
		}
	}
	// The value arrives: the flag and its value come together, redacted.
	if err := os.WriteFile(job.LogPath, []byte("first line\n--password\n\nhunter2\nnext\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := read(int64(len("first line\n"))); v.Output != "--password\n\n***\n" {
		t.Fatalf("once the value arrived: %q, want the flag with its value redacted", v.Output)
	}
}

// insertFinished records a finished job that started at, with a log file.
func insertFinished(t *testing.T, h *Handler, id string, at time.Time) {
	t.Helper()
	s := spec(id[:22], 0)
	s.ID = id
	if err := h.insert(record{spec: s, status: tasks.JobSucceeded, startedAt: at, endedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.logPath(id), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jobIDAt(i int) string { return fmt.Sprintf("20261001-000000-%06x-0", i) }

// A node keeps its newest 200 jobs, and none older than 30 days: starting a
// job sweeps the rest, record and log file both.
func TestOldJobsAreSwept(t *testing.T) {
	n := newTestNode(t)
	if err := os.MkdirAll(n.h.LogDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range 200 {
		insertFinished(t, n.h, jobIDAt(i), t0.Add(time.Duration(i)*time.Minute))
	}
	*n.clock = t0.Add(1000 * time.Minute)
	if status, env := n.start(t, spec(runA, 0)); status != 201 {
		t.Fatalf("the 201st job: %d %q", status, env.code())
	}
	job := n.runner.next(t)
	kept := func() int {
		t.Helper()
		var c int
		if err := n.h.DB.QueryRow(`SELECT COUNT(*) FROM task_jobs`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	gone := func(id string) bool {
		t.Helper()
		_, err := n.h.get(id)
		_, statErr := os.Stat(n.h.logPath(id))
		return errors.Is(err, errNotFound) && errors.Is(statErr, os.ErrNotExist)
	}
	if c := kept(); c != 200 {
		t.Fatalf("%d jobs kept after the 201st, want 200", c)
	}
	if !gone(jobIDAt(0)) {
		t.Fatal("the oldest job, or its log, is still there")
	}
	if gone(jobIDAt(1)) {
		t.Fatal("the second oldest job went too")
	}

	// 30 days after the running job started, and after every job before
	// it: those go, one started 30 days ago to the minute stays, and the
	// running job is never swept, however old.
	*n.clock = t0.Add(30*24*time.Hour + 1100*time.Minute)
	insertFinished(t, n.h, jobIDAt(500), t0.Add(1100*time.Minute))
	insertFinished(t, n.h, jobIDAt(501), *n.clock)
	if err := n.h.sweep(); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{1, 100, 199} {
		if !gone(jobIDAt(i)) {
			t.Fatalf("job %d, over 30 days old, is still there", i)
		}
	}
	if gone(jobIDAt(500)) {
		t.Fatal("a job 30 days old to the minute went")
	}
	if c := kept(); c != 3 {
		t.Fatalf("%d jobs kept, want the running one, the one 30 days old and the newest", c)
	}
	if _, err := n.h.get(job.Spec.ID); err != nil {
		t.Fatalf("the running job was swept: %v", err)
	}
}

// After a restart, a job recorded as running is settled from what is left of
// it: a unit systemd still runs stays running; a unit that is gone is judged
// by the exit marker on its log's last line; and a job with no marker, or
// with no unit at all — it ran inside the panel that stopped — is
// interrupted, never taken for a success. A job waiting on its reboot is left
// for the boot-ID check.
func TestBootMarksARunningJobWithoutItsUnitInterrupted(t *testing.T) {
	n := newTestNode(t)
	if err := os.MkdirAll(n.h.LogDir, 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	add := func(step int, status, unit, log string) string {
		t.Helper()
		s := spec(runA, step)
		if err := n.h.insert(record{spec: s, status: status, unit: unit, startedAt: at}); err != nil {
			t.Fatal(err)
		}
		if log != "" {
			if err := os.WriteFile(n.h.logPath(s.ID), []byte(log), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	inProcess := add(0, tasks.JobRunning, "", "half way\n")
	noMarker := add(1, tasks.JobRunning, "sfpanel-task-b.service", "half way\n")
	failed := add(2, tasks.JobRunning, "sfpanel-task-c.service", "boom\n[sfpanel-task] exit=3\n")
	succeeded := add(3, tasks.JobRunning, "sfpanel-task-d.service", "fine\n[sfpanel-task] exit=0\n\n")
	markerNotLast := add(4, tasks.JobRunning, "sfpanel-task-e.service", "[sfpanel-task] exit=0\nthen more\n")
	noLog := add(5, tasks.JobRunning, "sfpanel-task-f.service", "")
	rebooting := add(6, tasks.JobRebooting, "sfpanel-task-reboot-g", "")

	mock := exec.NewMockCommander()
	mock.SetOutput("systemctl", "inactive\n", errors.New("exit status 3"))
	n.h.Cmd = mock
	n.h.Recover()

	for _, tc := range []struct {
		id, status string
		exit       *int
	}{
		{inProcess, tasks.JobInterrupted, nil},
		{noMarker, tasks.JobInterrupted, nil},
		{failed, tasks.JobFailed, intPtr(3)},
		{succeeded, tasks.JobSucceeded, intPtr(0)},
		{markerNotLast, tasks.JobInterrupted, nil},
		{noLog, tasks.JobInterrupted, nil},
		{rebooting, tasks.JobRebooting, nil},
	} {
		r, err := n.h.get(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if r.status != tc.status || (r.exitCode == nil) != (tc.exit == nil) || (r.exitCode != nil && *r.exitCode != *tc.exit) {
			t.Fatalf("%s: %s exit %v, want %s exit %v", tc.id, r.status, deref(r.exitCode), tc.status, deref(tc.exit))
		}
		if tc.status != tasks.JobRebooting && r.endedAt.IsZero() {
			t.Fatalf("%s: settled with no end time", tc.id)
		}
	}
	var asked []string
	for _, c := range mock.Calls {
		asked = append(asked, c.Name+" "+strings.Join(c.Args, " "))
	}
	if want := []string{
		"systemctl is-active sfpanel-task-b.service",
		"systemctl is-active sfpanel-task-c.service",
		"systemctl is-active sfpanel-task-d.service",
		"systemctl is-active sfpanel-task-e.service",
		"systemctl is-active sfpanel-task-f.service",
	}; strings.Join(asked, "\n") != strings.Join(want, "\n") {
		t.Fatalf("asked systemd:\n%s\nwant:\n%s", strings.Join(asked, "\n"), strings.Join(want, "\n"))
	}

	// A unit systemd still runs keeps its job running, and the node held.
	still := add(7, tasks.JobRunning, "sfpanel-task-h.service", "half way\n")
	if _, err := n.h.DB.Exec(`UPDATE task_jobs SET status = ? WHERE id = ?`, tasks.JobSucceeded, rebooting); err != nil {
		t.Fatal(err)
	}
	mock = exec.NewMockCommander()
	mock.SetOutput("systemctl", "active\n", nil)
	n.h.Cmd = mock
	n.h.Recover()
	if r, _ := n.h.get(still); r.status != tasks.JobRunning || !r.endedAt.IsZero() {
		t.Fatalf("a job whose unit still runs: %s, want running", r.status)
	}
	if status, env := n.start(t, spec(runB, 0)); status != http.StatusConflict || env.code() != response.ErrTaskNodeBusy {
		t.Fatalf("a job while the unit runs: %d %q, want 409 %s", status, env.code(), response.ErrTaskNodeBusy)
	}
}

func intPtr(i int) *int { return &i }

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// The log directory is created 0700 and each log 0600: a job's output can
// hold what a command printed about the host.
func TestAJobsLogIsPrivate(t *testing.T) {
	n := newTestNode(t)
	if status, env := n.start(t, spec(runA, 0)); status != 201 {
		t.Fatalf("start: %d %q", status, env.code())
	}
	job := n.runner.next(t)
	if job.LogPath != filepath.Join(n.h.LogDir, runA+"-0.log") {
		t.Fatalf("log at %s", job.LogPath)
	}
	for path, want := range map[string]os.FileMode{n.h.LogDir: 0o700, job.LogPath: 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Fatalf("%s is %v, want %v", path, st.Mode().Perm(), want)
		}
	}
}
