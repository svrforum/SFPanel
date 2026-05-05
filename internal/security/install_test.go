package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCmd is a tiny Commander stub that returns whatever the matcher
// function says. We need this because MockCommander returns the same
// output for a given command name regardless of args, but our tests
// need to differentiate `cosign version` from other invocations and to
// simulate stat-vs-exec behavior cleanly.
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

// TestInstaller_AlreadyInstalledShortCircuits — when a binary already
// exists at the canonical path AND `cosign version` reports >= 2.x, return
// that path without any network or verify work.
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
	i := &Installer{
		Cmd:  cmd,
		Path: binPath,
		Get: func(ctx context.Context, url string) ([]byte, error) {
			t.Fatal("network should not be hit")
			return nil, nil
		},
	}
	got, err := i.EnsureCosign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != binPath {
		t.Fatalf("path: got %q want %q", got, binPath)
	}
}

// TestInstaller_FallbackWhenNetworkFails — primary download fails,
// /etc/sfpanel/cosign is used as fallback.
func TestInstaller_FallbackWhenNetworkFails(t *testing.T) {
	dir := t.TempDir()
	primaryPath := filepath.Join(dir, "primary-cosign")
	fallbackDir := t.TempDir()
	fallbackPath := filepath.Join(fallbackDir, "cosign")
	if err := os.WriteFile(fallbackPath, []byte("#!/bin/sh\necho 'cosign version v2.4.1'"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		if strings.HasSuffix(name, "/cosign") || strings.HasSuffix(name, "primary-cosign") {
			return "cosign version v2.4.1\n", nil
		}
		return "", errors.New("unexpected: " + name)
	}}
	i := &Installer{
		Cmd:          cmd,
		Path:         primaryPath,
		FallbackPath: fallbackPath,
		Get: func(ctx context.Context, url string) ([]byte, error) {
			return nil, errors.New("simulated network failure")
		},
	}
	got, err := i.EnsureCosign(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != fallbackPath {
		t.Fatalf("path: got %q want %q (fallback)", got, fallbackPath)
	}
}

// TestInstaller_BothFailReturnsSentinel — total failure should wrap
// ErrCosignInstallFailed so callers can errors.Is-detect it.
func TestInstaller_BothFailReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	primaryPath := filepath.Join(dir, "missing-cosign")
	fallbackPath := filepath.Join(dir, "also-missing")
	cmd := &fakeCmd{handle: func(name string, args []string) (string, error) {
		return "", errors.New("not installed")
	}}
	i := &Installer{
		Cmd:          cmd,
		Path:         primaryPath,
		FallbackPath: fallbackPath,
		Get: func(ctx context.Context, url string) ([]byte, error) {
			return nil, errors.New("network down")
		},
	}
	_, err := i.EnsureCosign(context.Background())
	if !errors.Is(err, ErrCosignInstallFailed) {
		t.Fatalf("got %v want ErrCosignInstallFailed", err)
	}
}
