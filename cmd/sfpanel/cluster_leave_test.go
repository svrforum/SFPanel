package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// An offline leave saved the config as standalone first, then removed the
// cluster's files and only warned when that failed: ca.key and the cluster's
// JWT key stayed beside a config that no longer said they belonged to a
// cluster, and 'agent join' then took the server. The files go first now,
// and a failure leaves the config saying what the server still is.
func TestAnOfflineLeaveKeepsTheConfigWhileTheClusterFilesStay(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes files from a read-only directory")
	}
	dir := t.TempDir()
	certDir, dataDir := filepath.Join(dir, "certs"), filepath.Join(dir, "data")
	for _, d := range []string{certDir, dataDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(certDir, "ca.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	cfg := &config.Config{}
	cfg.Server.Port = port
	cfg.Database.Path = filepath.Join(dir, "db")
	cfg.Auth.JWTSecret = strings.Repeat("c", 64)
	cfg.Cluster = config.ClusterConfig{Enabled: true, NodeID: "n1", NodeName: "n1", AdvertiseAddress: "127.0.0.1", GRPCPort: port, CertDir: certDir, DataDir: dataDir}
	path := filepath.Join(dir, "config.yaml")
	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(certDir, 0o500); err != nil { // its entries cannot be removed
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(certDir, 0o700) })

	err := leaveOffline(cfg, path)
	if err == nil || !strings.Contains(err.Error(), certDir) {
		t.Fatalf("leave with a cert dir that cannot be removed: %v", err)
	}
	after, _ := os.ReadFile(path)
	var saved config.Config
	_ = yaml.Unmarshal(after, &saved)
	if !saved.Cluster.Enabled {
		t.Fatal("the config was saved as standalone while the cluster's files stayed")
	}
}
