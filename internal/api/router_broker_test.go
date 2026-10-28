package api

import (
	"context"
	"embed"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/docker"
	featureAlert "github.com/svrforum/SFPanel/internal/feature/alert"
)

// A manager carries a stack on an agent with no docker of its own — the
// source's and the target's do the work — so a manager without docker still
// runs the migration, while its own docker routes answer that it has none.
func TestAManagerWithoutDockerCarriesAnAgentsStack(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { auth.SetJWTSecret(""); auth.SetProxySecrets("", nil) })
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	mgr := cluster.NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1,
	})
	if err := mgr.Init("broker"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	auth.SetProxySecrets("managers-secret", []string{"managers-secret"})
	// a1's cluster port is closed: the pre-flight's query of the source fails,
	// which is how this test tells the migration ran.
	v, _ := json.Marshal(cluster.Node{ID: "a1", Name: "a1", Kind: cluster.KindAgent, APIAddress: "127.0.0.1:1", GRPCAddress: "127.0.0.1:1"})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	database, err := sfdb.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Server.StacksPath = filepath.Join(dir, "stacks")
	cfg.Database.Path = filepath.Join(dir, "t.db")
	cfg.Auth.JWTSecret = testJWTKey
	e, cleanup := NewRouter(database, sfdb.NewAsyncWriter(ctx, database, "audit", 16), featureAlert.NewManager(database), cfg, embed.FS{}, "test", mgr, filepath.Join(dir, "config.yaml"), nil, docker.NewLazy("unix:///nonexistent.sock"), nil, nil)
	t.Cleanup(cleanup)

	tok, err := auth.GenerateToken("admin", testJWTKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"targetNodeId":"a2"}`))
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		return serve(e, "127.0.0.1:5000", r)
	}
	if rec := call(http.MethodPost, "/api/v1/docker/compose/demo/migrate/preflight?node=a1"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "could not query the source node") {
		t.Fatalf("pre-flight of a1's stack: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodGet, "/api/v1/docker/compose"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"DOCKER_UNAVAILABLE"`) {
		t.Fatalf("this manager's own stacks: %d %s", rec.Code, rec.Body)
	}
}
