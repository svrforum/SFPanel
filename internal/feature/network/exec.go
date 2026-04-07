package network

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// commandTimeout is the default timeout for shell commands.
const commandTimeout = 5 * time.Minute

// runCommand executes a shell command with a 5-minute timeout and returns
// combined stdout+stderr output.
func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", commandTimeout)
	}
	return string(out), err
}

// runCommandEnv executes a shell command with custom environment variables,
// a 5-minute timeout, and returns combined stdout+stderr output.
func runCommandEnv(env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("command timed out after %s", commandTimeout)
	}
	return string(out), err
}

// aptEnv returns the standard environment variables for non-interactive apt operations.
func aptEnv() []string {
	return []string{"DEBIAN_FRONTEND=noninteractive"}
}
