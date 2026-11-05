package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// The remove's quorum guard refuses with "pass ?force=true to confirm", and
// the CLI had no way to: '--force' was taken for the node's ID when it came
// first and dropped when it came after. A remove through the running service
// passes the guard only with --force, on either side of the ID.
func TestARemoveThroughTheServiceForcesOnlyWithTheFlag(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"removed":"n2"}}`))
	}))
	t.Cleanup(panel.Close)
	u, err := url.Parse(panel.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Server.Port, _ = strconv.Atoi(u.Port())
	cfg.Database.Path = filepath.Join(dir, "db")
	cfg.Auth.JWTSecret = strings.Repeat("c", 64)
	cfg.Cluster = config.ClusterConfig{Enabled: true, NodeID: "n1", NodeName: "n1"}
	path := filepath.Join(dir, "config.yaml")
	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	clusterRemove([]string{"n2", "--config", path})
	clusterRemove([]string{"--force", "n2", "--config", path})
	clusterRemove([]string{"n2", "--force", "--config", path})
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"DELETE /api/v1/cluster/nodes/n2",
		"DELETE /api/v1/cluster/nodes/n2?force=true",
		"DELETE /api/v1/cluster/nodes/n2?force=true",
	}
	if strings.Join(asked, " | ") != strings.Join(want, " | ") {
		t.Fatalf("asked %q, want %q", asked, want)
	}
}
