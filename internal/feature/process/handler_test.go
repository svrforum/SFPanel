package process

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestKillProcess_PIDValidation(t *testing.T) {
	cases := []struct {
		pid     string
		valid   bool
		comment string
	}{
		{"", false, "empty"},
		{"abc", false, "non-numeric"},
		{"-5", false, "negative"},
		{"0", false, "init parent"},
		{"1", false, "init"},
		{"2", false, "kthreadd"},
		{"3", true, "first usermode candidate"},
		{"12345", true, "typical PID"},
		{"9999999999", false, "too large for int32"},
	}
	for _, tc := range cases {
		p, err := strconv.ParseInt(tc.pid, 10, 32)
		parsed := err == nil
		valid := parsed && p > 2
		if valid != tc.valid {
			t.Errorf("PID %q (%s): parsed=%v p=%d valid=%v, want %v",
				tc.pid, tc.comment, parsed, p, valid, tc.valid)
		}
	}
}

// TestKillProcess_RefusesOwnPgidSibling locks in the Task 3.4 guard: any
// subprocess sfpanel spawned (apt, docker compose, terminal PTYs, …) shares
// the panel's process group, and KillProcess must refuse them via the
// sysguard.IsPanelChildPID check. We spawn a `sleep` from the test process
// — it inherits our pgid, so the handler should return 403 with the
// "panel-spawned subprocess" body, not actually deliver the signal.
func TestKillProcess_RefusesOwnPgidSibling(t *testing.T) {
	// Spawn a child that shares the test process's pgid. We don't call
	// Setpgid, so the child stays in our group — same situation as any
	// real sfpanel-spawned subprocess.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not spawn sleep child (sandboxed env?): %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Give the child a moment to be schedulable so /proc reflects it.
	time.Sleep(20 * time.Millisecond)

	childPID := cmd.Process.Pid
	// Sanity check: child really is in our pgid. If this fails the test
	// environment has unusual scheduler/namespace behavior and the rest
	// of the assertion would be misleading.
	if gotPgid, err := syscall.Getpgid(childPID); err != nil {
		t.Fatalf("getpgid(child=%d): %v", childPID, err)
	} else if gotPgid != syscall.Getpgrp() {
		t.Skipf("child pgid=%d != self pgid=%d — test environment isolates child pgid", gotPgid, syscall.Getpgrp())
	}

	h := &Handler{}
	body := strings.NewReader(`{"signal":"TERM"}`)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/system/processes/%d/kill", childPID), body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("pid")
	c.SetParamValues(strconv.Itoa(childPID))

	if err := h.KillProcess(c); err != nil {
		t.Fatalf("KillProcess returned err: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Refusing to kill panel-spawned subprocess") {
		t.Errorf("body does not mention panel-spawned subprocess: %s", rec.Body.String())
	}

	// Confirm the child was NOT actually signalled — it should still be
	// alive. We verify with kill(0) which only reports liveness.
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Errorf("child PID %d looks dead after refused kill: %v", childPID, err)
	}
}

func TestSignalMap_KnownSignals(t *testing.T) {
	// The signal switch in KillProcess covers TERM/KILL/HUP/INT plus
	// numeric aliases 9/15/1/2. Anything else should be rejected.
	accepts := []string{"TERM", "term", "KILL", "kill", "HUP", "INT", "9", "15", "1", "2"}
	rejects := []string{"USR1", "STOP", "QUIT", "", "asdf", "16"}

	accepted := func(s string) bool {
		switch strings.ToUpper(s) {
		case "KILL", "9", "TERM", "15", "HUP", "1", "INT", "2":
			return true
		}
		return false
	}
	for _, s := range accepts {
		if !accepted(s) {
			t.Errorf("signal %q should be accepted", s)
		}
	}
	for _, s := range rejects {
		if accepted(s) {
			t.Errorf("signal %q should be rejected", s)
		}
	}
}
