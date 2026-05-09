package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

// TestSaveConfigWritesRestrictivePerms guards against accidentally widening
// config.yaml perms — the file holds the JWT secret and must stay 0600.
func TestSaveConfigWritesRestrictivePerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := &config.Config{}
	cfg.Server.Port = 19443
	cfg.Auth.JWTSecret = "super-secret-test-token"

	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config.yaml perm = %o, want 0600 (JWT secret must not be world-readable)", got)
	}
}
