package exec

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout is the default timeout for shell commands.
const DefaultTimeout = 5 * time.Minute

// Commander abstracts system command execution for testability.
//
// The Run/RunWithTimeout/RunWithEnv/RunWithInput methods all derive their
// own context.Background — convenient but unable to honor a request
// being cancelled. RunCtx is the same shape but threads a caller-supplied
// ctx so a client disconnect kills the subprocess instead of letting it
// run to its 5-minute default timeout. Adopt RunCtx in new handlers and
// any existing handler whose subprocess can take long enough that a
// client disconnect would otherwise leak the work.
type Commander interface {
	Run(name string, args ...string) (string, error)
	RunWithTimeout(timeout time.Duration, name string, args ...string) (string, error)
	RunWithEnv(env []string, name string, args ...string) (string, error)
	RunWithInput(input string, name string, args ...string) (string, error)
	RunCtx(ctx context.Context, name string, args ...string) (string, error)
	Exists(name string) bool
}

// SystemCommander executes real system commands via os/exec.
type SystemCommander struct{}

// NewCommander returns a Commander that executes real system commands.
func NewCommander() Commander {
	return &SystemCommander{}
}

func (c *SystemCommander) Run(name string, args ...string) (string, error) {
	return c.RunWithTimeout(DefaultTimeout, name, args...)
}

func (c *SystemCommander) RunWithTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	if timeout <= 0 {
		// Zero or negative timeout would deadline immediately — easy footgun
		// when a config field carrying a timeout defaults to a zero-value
		// time.Duration. Fall back to DefaultTimeout instead.
		timeout = DefaultTimeout
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	if ctx.Err() == context.DeadlineExceeded {
		slog.Warn("command timeout", "cmd", name, "duration_ms", duration.Milliseconds())
		return string(out), fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		slog.Debug("command failed", "cmd", name, "duration_ms", duration.Milliseconds(), "error", err)
	}
	return string(out), err
}

func (c *SystemCommander) RunWithEnv(env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", DefaultTimeout)
	}
	return string(out), err
}

func (c *SystemCommander) RunWithInput(input string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", DefaultTimeout)
	}
	return string(out), err
}

// RunCtx runs the command bound to the caller-supplied context. If the
// context carries no deadline, DefaultTimeout is applied — the same
// guarantee Run/RunWithEnv/RunWithInput provide today. Returns whatever
// the subprocess wrote to combined stdout+stderr regardless of whether
// it errored, so callers can surface the operator-relevant tail.
func (c *SystemCommander) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	duration := time.Since(start)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		slog.Warn("command timeout", "cmd", name, "duration_ms", duration.Milliseconds())
		return string(out), fmt.Errorf("command timed out")
	case ctx.Err() == context.Canceled:
		slog.Debug("command cancelled", "cmd", name, "duration_ms", duration.Milliseconds())
		return string(out), ctx.Err()
	case err != nil:
		slog.Debug("command failed", "cmd", name, "duration_ms", duration.Milliseconds(), "error", err)
	}
	return string(out), err
}

func (c *SystemCommander) Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// AptEnv returns the standard environment variables for non-interactive apt operations.
func AptEnv() []string {
	return []string{"DEBIAN_FRONTEND=noninteractive"}
}
