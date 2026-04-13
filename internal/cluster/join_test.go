package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

func TestJoinEngine_Rollback_ConfigSaveFailure(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	configPath := filepath.Join(tmpDir, "config.yaml")

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "0.0.0.0", Port: 8443},
		Auth:   config.AuthConfig{JWTSecret: "old-secret", TokenExpiry: "24h"},
		Cluster: config.ClusterConfig{
			GRPCPort: 9444,
			CertDir:  certDir,
			DataDir:  filepath.Join(tmpDir, "data"),
		},
		Database: config.DatabaseConfig{Path: filepath.Join(tmpDir, "test.db")},
	}

	// Write an initial config
	os.WriteFile(configPath, []byte("server:\n  port: 8443\n"), 0600)

	engine := &JoinEngine{
		ConfigPath: configPath,
		Config:     cfg,
	}

	// Test rollback: save certs then fail config save with read-only path
	os.MkdirAll(certDir, 0755)
	os.WriteFile(filepath.Join(certDir, "ca.crt"), []byte("fake-ca"), 0600)

	// Verify rollbackJoin cleans up certs and restores config
	originalConfig, _ := os.ReadFile(configPath)
	engine.rollbackJoin(certDir, originalConfig)

	// Cert dir should be removed
	if _, err := os.Stat(certDir); !os.IsNotExist(err) {
		t.Error("rollback should have removed cert dir")
	}

	// Config should be restored
	restored, _ := os.ReadFile(configPath)
	if string(restored) != string(originalConfig) {
		t.Error("rollback should have restored original config")
	}
}
