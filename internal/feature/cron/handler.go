package cron

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// crontabMu serializes the read-modify-write cycles in CreateJob / UpdateJob /
// DeleteJob. Without it, two concurrent API requests would each `crontab -l`,
// compute independent edits, and then `crontab -` with a stale view, losing
// one of the edits.
var crontabMu sync.Mutex

// CronJob represents a single entry in the system crontab.
type CronJob struct {
	ID       int    `json:"id"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
	Raw      string `json:"raw"`
	Type     string `json:"type"` // "job", "env", "comment"
}

// Handler exposes REST handlers for system crontab management.
type Handler struct {
	Cmd exec.Commander
}

// predefinedSchedules contains the special cron schedule keywords.
var predefinedSchedules = map[string]bool{
	"@reboot":   true,
	"@yearly":   true,
	"@annually": true,
	"@monthly":  true,
	"@weekly":   true,
	"@daily":    true,
	"@midnight": true,
	"@hourly":   true,
}

// envLinePattern matches environment variable assignments in crontab (e.g. SHELL=/bin/bash).
var envLinePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// ListJobs returns all entries from the root crontab.
func (h *Handler) ListJobs(c echo.Context) error {
	// Remote cluster nodes reached via ?node= may not have crontab
	// installed at all (containerised hosts, distroless images). Short-
	// circuit to an empty list instead of relying on the downstream
	// "not found" string match against the exec.LookPath error.
	if !h.Cmd.Exists("crontab") {
		return response.OK(c, []CronJob{})
	}
	content, err := readCrontab(h.Cmd)
	if err != nil {
		// crontab -l returns exit code 1 when no crontab is installed,
		// or crontab binary may not exist on the system
		if strings.Contains(err.Error(), "no crontab for") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") {
			return response.OK(c, []CronJob{})
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+response.SanitizeOutput(err.Error()))
	}

	lines := strings.Split(content, "\n")
	jobs := make([]CronJob, 0, len(lines))
	for i, line := range lines {
		// Skip trailing empty line produced by the final newline
		if i == len(lines)-1 && line == "" {
			continue
		}
		jobs = append(jobs, parseCronLine(line, i))
	}

	return response.OK(c, jobs)
}

// CreateJob appends a new cron job to the crontab.
// Accepts JSON body: {"schedule": "...", "command": "..."}.
func (h *Handler) CreateJob(c echo.Context) error {
	// Mutating crontab operations must fail explicitly when crontab(1) is
	// not installed — silently returning "ok" would mislead the operator,
	// and 503 with a clear message lets the UI render an actionable hint.
	if !h.Cmd.Exists("crontab") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrCronError,
			"crontab is not installed on this node")
	}
	crontabMu.Lock()
	defer crontabMu.Unlock()
	var req struct {
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Schedule == "" || req.Command == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Schedule and command are required")
	}
	if strings.ContainsAny(req.Schedule, "\n\r") || strings.ContainsAny(req.Command, "\n\r") {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Schedule and command must not contain newlines")
	}
	if !isValidSchedule(req.Schedule) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSchedule, "Invalid cron schedule format")
	}

	content, err := readCrontab(h.Cmd)
	if err != nil {
		// If there is no existing crontab, start with an empty one
		if strings.Contains(err.Error(), "no crontab for") {
			content = ""
		} else {
			return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+response.SanitizeOutput(err.Error()))
		}
	}

	newLine := req.Schedule + " " + req.Command

	// Ensure the existing content ends with a newline before appending
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += newLine + "\n"

	if err := writeCrontab(h.Cmd, content); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to write crontab: "+response.SanitizeOutput(err.Error()))
	}

	// Determine the index of the newly added line
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	idx := len(lines) - 1

	return response.OK(c, parseCronLine(newLine, idx))
}

// UpdateJob modifies an existing crontab entry by line index.
// Accepts JSON body: {"schedule": "...", "command": "...", "enabled": true/false}.
func (h *Handler) UpdateJob(c echo.Context) error {
	if !h.Cmd.Exists("crontab") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrCronError,
			"crontab is not installed on this node")
	}
	crontabMu.Lock()
	defer crontabMu.Unlock()
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid job ID")
	}

	var req struct {
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
		Enabled  *bool  `json:"enabled"`
		// ExpectedRaw, when set, is the raw crontab line the client believes sits
		// at this index. The server verifies it before mutating so a concurrent
		// add/remove (which shifts indices) can't silently rewrite the wrong job.
		ExpectedRaw string `json:"expected_raw"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Schedule == "" || req.Command == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Schedule and command are required")
	}
	if strings.ContainsAny(req.Schedule, "\n\r") || strings.ContainsAny(req.Command, "\n\r") {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Schedule and command must not contain newlines")
	}
	if !isValidSchedule(req.Schedule) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSchedule, "Invalid cron schedule format")
	}

	content, err := readCrontab(h.Cmd)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+response.SanitizeOutput(err.Error()))
	}

	lines := strings.Split(content, "\n")
	// Remove trailing empty line produced by final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if id < 0 || id >= len(lines) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Job not found")
	}
	if req.ExpectedRaw != "" && lines[id] != req.ExpectedRaw {
		return response.Fail(c, http.StatusConflict, response.ErrCronConflict,
			"Crontab changed since it was loaded; refresh and retry")
	}

	newLine := req.Schedule + " " + req.Command
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if !enabled {
		newLine = "# " + newLine
	}

	lines[id] = newLine

	if err := writeCrontab(h.Cmd, strings.Join(lines, "\n")+"\n"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to write crontab: "+response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, parseCronLine(newLine, id))
}

// DeleteJob removes a crontab entry by line index.
func (h *Handler) DeleteJob(c echo.Context) error {
	if !h.Cmd.Exists("crontab") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrCronError,
			"crontab is not installed on this node")
	}
	crontabMu.Lock()
	defer crontabMu.Unlock()
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid job ID")
	}

	content, err := readCrontab(h.Cmd)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+response.SanitizeOutput(err.Error()))
	}

	lines := strings.Split(content, "\n")
	// Remove trailing empty line produced by final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if id < 0 || id >= len(lines) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Job not found")
	}
	// Optional optimistic-concurrency guard: if the client passes the raw line
	// it intends to delete, verify the index still points at it (a concurrent
	// add/remove shifts indices and would otherwise delete the wrong job).
	if expected := c.QueryParam("expected_raw"); expected != "" && lines[id] != expected {
		return response.Fail(c, http.StatusConflict, response.ErrCronConflict,
			"Crontab changed since it was loaded; refresh and retry")
	}

	lines = append(lines[:id], lines[id+1:]...)

	if err := writeCrontab(h.Cmd, strings.Join(lines, "\n")+"\n"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to write crontab: "+response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]string{"message": "job deleted"})
}

// RunJob executes a crontab entry's command immediately (out of schedule) and
// returns the captured output. The command runs via `sh -c` under the panel's
// privilege (root) — the same context cron would use on schedule — so this adds
// no new privilege, only on-demand timing for testing a job.
func (h *Handler) RunJob(c echo.Context) error {
	if !h.Cmd.Exists("crontab") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrCronError,
			"crontab is not installed on this node")
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid job ID")
	}

	content, err := readCrontab(h.Cmd)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError,
			"Failed to read crontab: "+response.SanitizeOutput(err.Error()))
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if id < 0 || id >= len(lines) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Job not found")
	}

	job := parseCronLine(lines[id], id)
	if job.Type != "job" || strings.TrimSpace(job.Command) == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrCronError, "Selected line is not a runnable job")
	}

	out, runErr := h.Cmd.RunWithTimeout(5*time.Minute, "sh", "-c", job.Command)
	resp := map[string]interface{}{
		"output":  response.SanitizeOutput(out),
		"success": runErr == nil,
	}
	if runErr != nil {
		resp["error"] = response.SanitizeOutput(runErr.Error())
	}
	return response.OK(c, resp)
}

type cronLogsResponse struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

// GetLogs returns recent cron daemon execution log lines (when jobs ran),
// sourced from the journal (systemd) with a /var/log/syslog fallback. This is
// the system cron log: it records executions, not each job's stdout (scheduled
// output goes to mail or the command's own redirect). Per-node action — a
// remote node without journald/syslog returns an empty log, not an error.
func (h *Handler) GetLogs(c echo.Context) error {
	const maxLines = 300

	// Prefer journalctl: Ubuntu/Debian log the cron unit to the journal, and
	// `-u cron` scopes output to cron without unrelated syslog noise.
	if h.Cmd.Exists("journalctl") {
		out, err := h.Cmd.RunWithTimeout(30*time.Second, "journalctl",
			"-u", "cron", "-n", strconv.Itoa(maxLines), "--no-pager", "-o", "short-iso")
		if err == nil {
			trimmed := strings.TrimSpace(out)
			if trimmed != "" && !strings.Contains(trimmed, "No entries") {
				return response.OK(c, cronLogsResponse{
					Source:  "journalctl -u cron",
					Content: response.SanitizeLog(out),
				})
			}
		}
	}

	// Fallback: extract CRON lines from syslog (the conventional rsyslog target).
	out, err := h.Cmd.RunWithTimeout(30*time.Second, "sh", "-c",
		"grep -a CRON /var/log/syslog 2>/dev/null | tail -n "+strconv.Itoa(maxLines))
	if err == nil && strings.TrimSpace(out) != "" {
		return response.OK(c, cronLogsResponse{
			Source:  "/var/log/syslog",
			Content: response.SanitizeLog(out),
		})
	}

	return response.OK(c, cronLogsResponse{Source: "", Content: ""})
}

// readCrontab executes `crontab -l` and returns its output.
func readCrontab(cmd exec.Commander) (string, error) {
	out, err := cmd.Run("crontab", "-l")
	if err != nil {
		return "", fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(out))
	}
	return out, nil
}

// writeCrontab writes the given content to the crontab via `crontab -` (stdin).
func writeCrontab(cmd exec.Commander, content string) error {
	out, err := cmd.RunWithInput(content, "crontab", "-")
	if err != nil {
		return fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(out))
	}
	return nil
}

// parseCronLine parses a single crontab line and returns a CronJob struct.
func parseCronLine(line string, index int) CronJob {
	job := CronJob{
		ID:      index,
		Raw:     line,
		Enabled: true,
	}

	trimmed := strings.TrimSpace(line)

	// Empty lines
	if trimmed == "" {
		job.Type = "comment"
		job.Enabled = false
		return job
	}

	// Environment variable assignments (e.g. SHELL=/bin/bash, PATH=..., MAILTO=...)
	if envLinePattern.MatchString(trimmed) {
		job.Type = "env"
		job.Command = trimmed
		return job
	}

	// Comment lines — check if this is a disabled cron entry
	if strings.HasPrefix(trimmed, "#") {
		inner := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))

		// Check if the uncommented content looks like a cron schedule line
		if looksLikeCronEntry(inner) {
			// Parse the inner content as a cron job
			schedule, command := extractScheduleAndCommand(inner)
			job.Type = "job"
			job.Schedule = schedule
			job.Command = command
			job.Enabled = false
			return job
		}

		// Plain comment
		job.Type = "comment"
		job.Command = trimmed
		job.Enabled = false
		return job
	}

	// Active cron job
	schedule, command := extractScheduleAndCommand(trimmed)
	if schedule != "" {
		job.Type = "job"
		job.Schedule = schedule
		job.Command = command
		return job
	}

	// Fallback: unrecognised line treated as comment
	job.Type = "comment"
	job.Command = trimmed
	job.Enabled = false
	return job
}

// looksLikeCronEntry checks whether a string (with leading # already removed)
// appears to be a cron schedule entry.
func looksLikeCronEntry(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	// Check for predefined schedule keywords
	if strings.HasPrefix(s, "@") {
		word := strings.Fields(s)[0]
		if predefinedSchedules[strings.ToLower(word)] {
			return true
		}
	}

	// Check for standard 5-field schedule
	fields := strings.Fields(s)
	if len(fields) >= 6 {
		// The first 5 fields should look like cron time fields
		allValid := true
		for _, f := range fields[:5] {
			if !isCronField(f) {
				allValid = false
				break
			}
		}
		if allValid {
			return true
		}
	}

	return false
}

// extractScheduleAndCommand splits a crontab line into its schedule and command parts.
func extractScheduleAndCommand(line string) (schedule, command string) {
	line = strings.TrimSpace(line)

	// Predefined schedule keywords
	if strings.HasPrefix(line, "@") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && predefinedSchedules[strings.ToLower(fields[0])] {
			return fields[0], strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		}
		return "", line
	}

	// Standard 5-field schedule
	fields := strings.Fields(line)
	if len(fields) >= 6 {
		allValid := true
		for _, f := range fields[:5] {
			if !isCronField(f) {
				allValid = false
				break
			}
		}
		if allValid {
			sched := strings.Join(fields[:5], " ")
			// The command is everything after the 5 schedule fields
			cmd := strings.TrimSpace(line)
			// Remove the 5 schedule fields from the front
			for i := 0; i < 5; i++ {
				cmd = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cmd), fields[i]))
			}
			return sched, cmd
		}
	}

	return "", line
}

// isCronField checks whether a string looks like a valid cron time field
// (numbers, wildcards, ranges, steps, and lists).
func isCronField(s string) bool {
	if s == "" {
		return false
	}
	matched, _ := regexp.MatchString(`^[0-9*,/\-?LW#]+$`, s)
	return matched
}

// isValidSchedule validates a cron schedule string.
// Accepts either a predefined keyword (@reboot, @daily, etc.) or a standard 5-field schedule.
func isValidSchedule(schedule string) bool {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return false
	}

	// Predefined schedules
	if strings.HasPrefix(schedule, "@") {
		return predefinedSchedules[strings.ToLower(schedule)]
	}

	// Standard 5-field format: minute hour day-of-month month day-of-week
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return false
	}

	for _, f := range fields {
		if !isCronField(f) {
			return false
		}
	}

	return true
}
