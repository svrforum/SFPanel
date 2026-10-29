package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

func TestRotateRequest(t *testing.T) {
	got, err := rotateRequest([]string{"jwt"})
	if err != nil || got.method != http.MethodPost || got.path != "/api/v1/cluster/rotate/jwt" {
		t.Fatalf("jwt: %+v %v", got, err)
	}
	for _, bad := range [][]string{nil, {"everything"}, {"jwt", "extra"}} {
		if _, err := rotateRequest(bad); err == nil {
			t.Fatalf("%v was taken", bad)
		}
	}
}

// The variable puts a manager back on its own key at every start, whatever
// the cluster rotated to.
func TestJWTOverrideWarning(t *testing.T) {
	manager := &config.Config{Cluster: config.ClusterConfig{Enabled: true}}
	if w := jwtOverrideWarning(manager, true); !strings.Contains(w, "SFPANEL_JWT_SECRET") {
		t.Fatalf("manager: %q", w)
	}
	if w := jwtOverrideWarning(manager, false); w != "" {
		t.Fatalf("not set: %q", w)
	}
	agent := &config.Config{Cluster: config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent}}
	if w := jwtOverrideWarning(agent, true); w != "" {
		t.Fatalf("agent: %q", w)
	}
	if w := jwtOverrideWarning(&config.Config{}, true); w != "" {
		t.Fatalf("standalone: %q", w)
	}
}

// The CLI founds a cluster at generation 0 as the panel does
// (featurecluster's TestInitStartsTheJWTKeyAtGenerationZero): a number kept
// from a cluster this node left would stand above every rotation of the new
// one, and the founder would never take its own.
func TestTheCLIInitStartsTheJWTKeyAtGenerationZero(t *testing.T) {
	// Nothing from the environment: a port there could be a running panel,
	// which the CLI would hand the init to.
	for _, v := range []string{"SFPANEL_PORT", "SFPANEL_JWT_SECRET", "SFPANEL_DB_PATH"} {
		t.Setenv(v, "")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	panelPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // nothing answers there: the CLI inits the cluster itself
	dir := t.TempDir()
	cfg := &config.Config{Auth: config.AuthConfig{JWTSecret: strings.Repeat("b", 64), JWTGeneration: 3}}
	cfg.Server.Port = panelPort
	cfg.Database.Path = filepath.Join(dir, "sfpanel.db")
	cfg.Cluster = config.ClusterConfig{GRPCPort: freeRaftPorts(t), DataDir: filepath.Join(dir, "data"), CertDir: filepath.Join(dir, "certs")}
	path := filepath.Join(dir, "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	clusterInit([]string{"--config", path, "--name", "c", "--advertise", "127.0.0.1"})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved config.Config
	if err := yaml.Unmarshal(after, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Cluster.Enabled || saved.Auth.JWTGeneration != 0 {
		t.Fatalf("saved enabled %v, generation %d", saved.Cluster.Enabled, saved.Auth.JWTGeneration)
	}
}
