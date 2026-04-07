package exec

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// DefaultTimeout is the default timeout for shell commands.
const DefaultTimeout = 5 * time.Minute

// Commander abstracts system command execution for testability.
type Commander interface {
	Run(name string, args ...string) (string, error)
	RunWithTimeout(timeout time.Duration, name string, args ...string) (string, error)
	RunWithEnv(env []string, name string, args ...string) (string, error)
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", timeout)
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

func (c *SystemCommander) Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// AptEnv returns the standard environment variables for non-interactive apt operations.
func AptEnv() []string {
	return []string{"DEBIAN_FRONTEND=noninteractive"}
}
