package cron

import "testing"

func TestParseCronLine_StandardJob(t *testing.T) {
	job := parseCronLine("0 3 * * * /usr/bin/backup.sh", 0)
	if job.Type != "job" {
		t.Errorf("Type: got %q, want job", job.Type)
	}
	if job.Schedule != "0 3 * * *" {
		t.Errorf("Schedule: got %q, want '0 3 * * *'", job.Schedule)
	}
	if job.Command != "/usr/bin/backup.sh" {
		t.Errorf("Command: got %q, want /usr/bin/backup.sh", job.Command)
	}
	if !job.Enabled {
		t.Error("Enabled: expected true for un-commented job")
	}
}

func TestParseCronLine_PredefinedSchedule(t *testing.T) {
	job := parseCronLine("@daily /usr/bin/cleanup.sh", 1)
	if job.Type != "job" {
		t.Errorf("Type: got %q", job.Type)
	}
	if job.Schedule != "@daily" {
		t.Errorf("Schedule: got %q, want @daily", job.Schedule)
	}
	if job.Command != "/usr/bin/cleanup.sh" {
		t.Errorf("Command: got %q", job.Command)
	}
}

func TestParseCronLine_DisabledJob(t *testing.T) {
	job := parseCronLine("# 0 3 * * * /usr/bin/backup.sh", 2)
	if job.Type != "job" {
		t.Errorf("Type: got %q, want job (disabled)", job.Type)
	}
	if job.Enabled {
		t.Error("Enabled: expected false for commented-out job")
	}
	if job.Schedule != "0 3 * * *" {
		t.Errorf("Schedule: got %q", job.Schedule)
	}
}

func TestParseCronLine_PlainComment(t *testing.T) {
	job := parseCronLine("# this is just a comment", 3)
	if job.Type != "comment" {
		t.Errorf("Type: got %q, want comment", job.Type)
	}
	if job.Enabled {
		t.Error("Enabled: comment should not be Enabled")
	}
}

func TestParseCronLine_EnvAssignment(t *testing.T) {
	job := parseCronLine("PATH=/usr/local/bin:/usr/bin", 4)
	if job.Type != "env" {
		t.Errorf("Type: got %q, want env", job.Type)
	}
	if job.Command != "PATH=/usr/local/bin:/usr/bin" {
		t.Errorf("Command: got %q", job.Command)
	}
}

func TestParseCronLine_Empty(t *testing.T) {
	job := parseCronLine("   ", 5)
	if job.Type != "comment" {
		t.Errorf("Type: got %q, want comment", job.Type)
	}
}

func TestExtractScheduleAndCommand(t *testing.T) {
	cases := []struct {
		line, schedule, command string
	}{
		{"0 3 * * * /usr/bin/backup", "0 3 * * *", "/usr/bin/backup"},
		{"*/5 * * * * /opt/job", "*/5 * * * *", "/opt/job"},
		{"@reboot /etc/init.sh", "@reboot", "/etc/init.sh"},
		{"@daily echo hi", "@daily", "echo hi"},
		// less than 6 fields → schedule empty, line preserved as command
		// for the caller to inspect (current behavior).
		{"echo hi", "", "echo hi"},
		// pipes / args inside command preserved
		{"0 3 * * * /usr/bin/backup --rsync /src /dst", "0 3 * * *", "/usr/bin/backup --rsync /src /dst"},
	}
	for _, c := range cases {
		schedule, command := extractScheduleAndCommand(c.line)
		if schedule != c.schedule || command != c.command {
			t.Errorf("extractScheduleAndCommand(%q) = (%q,%q); want (%q,%q)",
				c.line, schedule, command, c.schedule, c.command)
		}
	}
}

func TestIsCronField(t *testing.T) {
	// Regex is `^[0-9*,/\-?LW#]+$` — strictly numeric/operators. Named day
	// fields ("sun"/"mon-fri") are NOT accepted by this validator. This is
	// the deliberate behavior: the system crontab uses 5-numeric format,
	// and accepting named fields would force a richer validator with no
	// real benefit here.
	cases := []struct {
		field string
		ok    bool
	}{
		{"*", true},
		{"0", true}, {"59", true}, {"23", true},
		{"*/5", true}, {"5,10,15", true}, {"1-5", true},
		{"L", true}, {"W", true}, {"#", true}, // crontab special chars
		{"", false},
		{"sun", false}, {"mon-fri", false}, {"abc", false},
		// Trailing comma is currently accepted because the regex matches
		// the character class, not the structure. Documented here as the
		// observed behavior — not a security issue (writeCrontab calls
		// isValidSchedule which exercises the full 5-field check).
		{"1,", true},
	}
	for _, c := range cases {
		got := isCronField(c.field)
		if got != c.ok {
			t.Errorf("isCronField(%q) = %v; want %v", c.field, got, c.ok)
		}
	}
}
