package handlers

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// CronJob represents a single entry in the system crontab.
type CronJob struct {
	ID       int    `json:"id"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
	Raw      string `json:"raw"`
	Type     string `json:"type"` // "job", "env", "comment"
}

// CronHandler exposes REST handlers for system crontab management.
type CronHandler struct{}

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
func (h *CronHandler) ListJobs(c echo.Context) error {
	content, err := readCrontab()
	if err != nil {
		// crontab -l returns exit code 1 when no crontab is installed,
		// or crontab binary may not exist on the system
		if strings.Contains(err.Error(), "no crontab for") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") {
			return response.OK(c, []CronJob{})
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+err.Error())
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
func (h *CronHandler) CreateJob(c echo.Context) error {
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
	if !isValidSchedule(req.Schedule) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSchedule, "Invalid cron schedule format")
	}

	content, err := readCrontab()
	if err != nil {
		// If there is no existing crontab, start with an empty one
		if strings.Contains(err.Error(), "no crontab for") {
			content = ""
		} else {
			return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+err.Error())
		}
	}

	newLine := req.Schedule + " " + req.Command

	// Ensure the existing content ends with a newline before appending
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += newLine + "\n"

	if err := writeCrontab(content); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to write crontab: "+err.Error())
	}

	// Determine the index of the newly added line
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	idx := len(lines) - 1

	return response.OK(c, parseCronLine(newLine, idx))
}

// UpdateJob modifies an existing crontab entry by line index.
// Accepts JSON body: {"schedule": "...", "command": "...", "enabled": true/false}.
func (h *CronHandler) UpdateJob(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid job ID")
	}

	var req struct {
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if req.Schedule == "" || req.Command == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "Schedule and command are required")
	}
	if !isValidSchedule(req.Schedule) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSchedule, "Invalid cron schedule format")
	}

	content, err := readCrontab()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+err.Error())
	}

	lines := strings.Split(content, "\n")
	// Remove trailing empty line produced by final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if id < 0 || id >= len(lines) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Job not found")
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

	if err := writeCrontab(strings.Join(lines, "\n") + "\n"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to write crontab: "+err.Error())
	}

	return response.OK(c, parseCronLine(newLine, id))
}

// DeleteJob removes a crontab entry by line index.
func (h *CronHandler) DeleteJob(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "Invalid job ID")
	}

	content, err := readCrontab()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to read crontab: "+err.Error())
	}

	lines := strings.Split(content, "\n")
	// Remove trailing empty line produced by final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if id < 0 || id >= len(lines) {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Job not found")
	}

	lines = append(lines[:id], lines[id+1:]...)

	if err := writeCrontab(strings.Join(lines, "\n") + "\n"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrCronError, "Failed to write crontab: "+err.Error())
	}

	return response.OK(c, map[string]string{"message": "job deleted"})
}

// readCrontab executes `crontab -l` and returns its output.
func readCrontab() (string, error) {
	cmd := exec.Command("crontab", "-l")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// writeCrontab writes the given content to the crontab via `crontab -` (stdin).
func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(string(out)))
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
