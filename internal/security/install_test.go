package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCmd is a tiny Commander stub. MockCommander only configures one
// fixed response per command name; our tests need per-call behavior
// based on (name, args). Implements all five Commander methods.
type fakeCmd struct {
	handle func(name string, args []string) (string, error)
}

func (f *fakeCmd) Run(name string, args ...string) (string, error) {
	return f.handle(name, args)
}
func (f *fakeCmd) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return f.handle(name, args)
}
func (f *fakeCmd) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return f.handle(name, args)
}
func (f *fakeCmd) RunWithInput(_ string, name string, args ...string) (string, error) {
	return f.handle(name, args)
}
func (f *fakeCmd) Exists(_ string) bool { return true }

// TestInstaller_AlreadyInstalledShortCircuits — a binary at i.Path with
// a >= 2.x version short-circuits before any apt call.
func TestInstaller_AlreadyInstalledShortCircuits(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cosign")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho 'cosign version v2.4.1'"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if name == binPath && len(args) > 0 && args[0] == "version" {
			return "cosign version v2.4.1\n", nil
		}
		t.Fatalf("unexpected exec: %s %v", name, args)
		return "", errors.New("unexpected")
	}}
	i := &Installer{Cmd: cmd, Path: binPath}
	got, err := i.EnsureCosign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != binPath {
		t.Fatalf("path: got %q want %q", got, binPath)
	}
}

// TestInstaller_FallbackWhenPrimaryMissing — primary path empty,
// FallbackPath used (air-gapped operator placement).
func TestInstaller_FallbackUsedWhenPrimaryMissing(t *testing.T) {
	dir := t.TempDir()
	primaryPath := filepath.Join(dir, "primary-missing")
	fallbackDir := t.TempDir()
	fallbackPath := filepath.Join(fallbackDir, "cosign")
	if err := os.WriteFile(fallbackPath, []byte("#!/bin/sh\necho 'cosign version v2.4.1'"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if name == fallbackPath && len(args) > 0 && args[0] == "version" {
			return "cosign version v2.4.1\n", nil
		}
		return "", errors.New("unexpected: " + name)
	}}
	i := &Installer{Cmd: cmd, Path: primaryPath, FallbackPath: fallbackPath}
	got, err := i.EnsureCosign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != fallbackPath {
		t.Fatalf("path: got %q want %q (fallback)", got, fallbackPath)
	}
}

// TestInstaller_AptInstallFailsReturnsSentinel — primary, fallback, and
// system PATH all miss; apt-get install errors → wrapped
// ErrCosignInstallFailed.
func TestInstaller_AptInstallFailsReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	primaryPath := filepath.Join(dir, "missing-cosign")
	fallbackPath := filepath.Join(dir, "also-missing")
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if name == "apt-get" {
			return "E: Unable to locate package cosign", errors.New("exit 100")
		}
		return "", errors.New("not installed")
	}}
	i := &Installer{Cmd: cmd, Path: primaryPath, FallbackPath: fallbackPath}
	_, err := i.EnsureCosign(context.Background())
	if !errors.Is(err, ErrCosignInstallFailed) {
		t.Fatalf("got %v want ErrCosignInstallFailed", err)
	}
}
