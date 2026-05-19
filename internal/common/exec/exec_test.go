package exec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMockCommander_Run(t *testing.T) {
	m := NewMockCommander()
	m.SetOutput("echo", "hello\n", nil)
	out, err := m.Run("echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello\n" {
		t.Fatalf("expected 'hello\\n', got %q", out)
	}
	if len(m.Calls) != 1 || m.Calls[0].Name != "echo" {
		t.Fatalf("expected 1 call to echo, got %v", m.Calls)
	}
}

func TestMockCommander_Exists(t *testing.T) {
	m := NewMockCommander()
	m.SetOutput("exists:ufw", "", nil)
	if !m.Exists("ufw") {
		t.Fatal("expected ufw to exist")
	}
	if m.Exists("nonexistent") {
		t.Fatal("expected nonexistent to not exist")
	}
}

func TestSystemCommander_Exists(t *testing.T) {
	cmd := NewCommander()
	if !cmd.Exists("ls") {
		t.Fatal("expected ls to exist")
	}
	if cmd.Exists("nonexistent_command_xyz") {
		t.Fatal("expected fake command to not exist")
	}
}

func TestSystemCommander_Run(t *testing.T) {
	cmd := NewCommander()
	out, err := cmd.Run("echo", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "test\n" {
		t.Fatalf("expected 'test\\n', got %q", out)
	}
}

func TestSystemCommander_Timeout(t *testing.T) {
	cmd := NewCommander()
	_, err := cmd.RunWithTimeout(1*time.Millisecond, "sleep", "10")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSystemCommander_RunWithTimeout_ZeroOrNegativeUsesDefault(t *testing.T) {
	// timeout=0 must NOT instantly time out — it must run the command and succeed.
	// Same for negative timeouts (defensive).
	cmd := NewCommander()

	for _, tc := range []time.Duration{0, -1 * time.Second} {
		out, err := cmd.RunWithTimeout(tc, "echo", "hi")
		if err != nil {
			t.Fatalf("timeout=%v: unexpected error: %v", tc, err)
		}
		if out != "hi\n" {
			t.Fatalf("timeout=%v: expected 'hi\\n', got %q", tc, out)
		}
	}
}

func TestRunCtx_HonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := NewCommander()

	done := make(chan error, 1)
	go func() {
		_, err := cmd.RunCtx(ctx, "sleep", "10")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunCtx should have returned an error after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunCtx did not return within 2s after cancel — context not propagated")
	}
}

func TestRunCtx_DefaultsTimeoutWhenNoDeadline(t *testing.T) {
	// No deadline on ctx → DefaultTimeout (5 min) applies; we just verify
	// a short successful command runs through without error.
	cmd := NewCommander()
	out, err := cmd.RunCtx(context.Background(), "echo", "hi")
	if err != nil {
		t.Fatalf("RunCtx echo: %v", err)
	}
	if !strings.HasPrefix(out, "hi") {
		t.Errorf("unexpected output: %q", out)
	}
}
