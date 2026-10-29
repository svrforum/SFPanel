package featurecluster

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// call answers method path on h's route fn and returns status and body.
func call(t *testing.T, fn echo.HandlerFunc, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := fn(echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	return rec.Code, rec.Body.String()
}

func savedConfig(t *testing.T, path string) config.Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// waitForCond waits up to 5 s for cond, looking every 20 ms.
func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// rotationHandler is the leader mgr's handler as the router builds it, its
// config at a file in a temp dir, the cluster and the running panel on key old.
func rotationHandler(t *testing.T, mgr *cluster.Manager, ended *atomic.Int32) (*Handler, string, string) {
	t.Helper()
	old := strings.Repeat("a", 64)
	auth.SetJWTSecret(old)
	t.Cleanup(func() { auth.SetJWTSecret("") })
	if err := mgr.SetConfig("jwt_secret", old); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	h := &Handler{Manager: mgr, Config: &config.Config{Auth: config.AuthConfig{JWTSecret: old, TokenExpiry: "1h"}}, ConfigPath: path,
		EndSessions: func(swap func()) error { ended.Add(1); swap(); return nil }}
	h.ActivateBootManager()
	return h, path, old
}

// The rotation ends every session on this manager and saves the new key
// with its generation, which a restart and the CLI read; a token signed with
// the retired key is refused from then on.
func TestRotateJWTEndsEverySessionAndSavesTheKey(t *testing.T) {
	mgr := tokenLeader(t)
	var ended atomic.Int32
	h, path, old := rotationHandler(t, mgr, &ended)
	before, _ := auth.GenerateToken("alice", old, time.Hour)
	status, body := call(t, h.RotateJWT, http.MethodPost, "/api/v1/cluster/rotate/jwt", "")
	if status != http.StatusOK || !strings.Contains(body, `"generation":1`) {
		t.Fatalf("rotate: %d %s", status, body)
	}
	waitForCond(t, "the new key to run", func() bool { return auth.JWTSecret() != old })
	if _, err := auth.ParseToken(before, auth.JWTSecret()); err == nil {
		t.Fatal("a token signed with the retired key is still accepted")
	}
	saved := savedConfig(t, path)
	if saved.Auth.JWTSecret != auth.JWTSecret() || saved.Auth.JWTGeneration != 1 || h.Config.Auth.JWTSecret != auth.JWTSecret() {
		t.Fatalf("saved generation %d; the saved, in-memory and running keys differ", saved.Auth.JWTGeneration)
	}
	if ended.Load() != 1 {
		t.Fatalf("sessions ended %d times", ended.Load())
	}
	if strings.Contains(body, auth.JWTSecret()) {
		t.Fatal("the answer carries the key")
	}
}

// A manager that has not been heard from may run a release that cannot
// follow, and would go on accepting the retired key: refused, nothing changed.
func TestRotateJWTWaitsForEveryManager(t *testing.T) {
	mgr := tokenLeader(t)
	var ended atomic.Int32
	h, _, old := rotationHandler(t, mgr, &ended)
	data, _ := json.Marshal(cluster.Node{ID: "m2", Name: "m2", Kind: cluster.KindManager, Role: cluster.RoleVoter})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: data}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	status, body := call(t, h.RotateJWT, http.MethodPost, "/api/v1/cluster/rotate/jwt", "")
	if status != http.StatusConflict || !strings.Contains(body, response.ErrClusterRotationRefused) || !strings.Contains(body, "m2 (not heard from)") {
		t.Fatalf("rotate: %d %s", status, body)
	}
	if auth.JWTSecret() != old || ended.Load() != 0 {
		t.Fatal("a refused rotation changed the key or ended sessions")
	}
}

// A config.yaml that cannot be written does not keep the retired key
// running: the node changes its key and ends its sessions all the same, and
// the error makes the manager call again, which then only saves — ending the
// sessions again would end the ones signed in since.
func TestAFailedSaveStillRunsTheNewKeyAndEndsTheSessionsOnce(t *testing.T) {
	old := strings.Repeat("a", 64)
	auth.SetJWTSecret(old)
	t.Cleanup(func() { auth.SetJWTSecret("") })
	dir := filepath.Join(t.TempDir(), "etc")
	path := filepath.Join(dir, "config.yaml") // not there yet: the save fails
	var ended atomic.Int32
	h := &Handler{Config: &config.Config{Auth: config.AuthConfig{JWTSecret: old, TokenExpiry: "1h"}}, ConfigPath: path,
		EndSessions: func(swap func()) error { ended.Add(1); swap(); return nil }}
	k := cluster.JWTKey{Generation: 1, Secret: strings.Repeat("c", 64)}
	if err := h.applyJWTKey(k); err == nil {
		t.Fatal("a failed save was not reported: the manager would not save it again")
	}
	if auth.JWTSecret() != k.Secret || ended.Load() != 1 {
		t.Fatalf("after a failed save: the new key runs %v, sessions ended %d times", auth.JWTSecret() == k.Secret, ended.Load())
	}
	if h.Config.Auth.JWTGeneration != 0 {
		t.Fatal("the in-memory config says a generation is saved that config.yaml does not hold")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := h.applyJWTKey(k); err != nil {
		t.Fatal(err)
	}
	if saved := savedConfig(t, path); saved.Auth.JWTSecret != k.Secret || saved.Auth.JWTGeneration != 1 || ended.Load() != 1 {
		t.Fatalf("the save tried again: saved generation %d, sessions ended %d times", saved.Auth.JWTGeneration, ended.Load())
	}
}

// A node that ran a rotated key in a cluster it left starts a new cluster at
// generation 0: kept, the old number would stand above every rotation of the
// new cluster, and the node would never take one.
func TestInitStartsTheJWTKeyAtGenerationZero(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Auth: config.AuthConfig{JWTSecret: strings.Repeat("b", 64), JWTGeneration: 3}}
	cfg.Server.Port = 1
	cfg.Cluster = config.ClusterConfig{GRPCPort: raftPort - 1, DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs")}
	h := &Handler{Config: cfg, ConfigPath: path}
	status, body := call(t, h.InitCluster, http.MethodPost, "/api/v1/cluster/init", `{"name":"c","advertise_address":"127.0.0.1"}`)
	if status != http.StatusOK {
		t.Fatalf("init: %d %s", status, body)
	}
	if saved := savedConfig(t, path); saved.Auth.JWTGeneration != 0 || cfg.Auth.JWTGeneration != 0 {
		t.Fatalf("saved %d, in memory %d", saved.Auth.JWTGeneration, cfg.Auth.JWTGeneration)
	}
}

// A manager the panel makes without a restart — a live init or join —
// follows a rotation as one it booted with does: setManager wires it.
func TestALiveInitFollowsARotation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	t.Cleanup(func() { auth.SetJWTSecret("") })
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Auth: config.AuthConfig{JWTSecret: strings.Repeat("b", 64)}}
	cfg.Server.Port = 1
	cfg.Cluster = config.ClusterConfig{GRPCPort: raftPort - 1, DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs")}
	var ended atomic.Int32
	h := &Handler{Config: cfg, ConfigPath: path,
		LiveActivate: func(_ *config.Config, _ string, existing *cluster.Manager) (*cluster.Manager, error) {
			return existing, nil
		},
		EndSessions: func(swap func()) error { ended.Add(1); swap(); return nil }}
	status, body := call(t, h.InitCluster, http.MethodPost, "/api/v1/cluster/init", `{"name":"c","advertise_address":"127.0.0.1"}`)
	if status != http.StatusOK || h.GetManager() == nil {
		t.Fatalf("init: %d %s", status, body)
	}
	t.Cleanup(h.GetManager().Shutdown)
	if status, body = call(t, h.RotateJWT, http.MethodPost, "/api/v1/cluster/rotate/jwt", ""); status != http.StatusOK {
		t.Fatalf("rotate: %d %s", status, body)
	}
	waitForCond(t, "the new key to be applied", func() bool { return ended.Load() == 1 && savedConfig(t, path).Auth.JWTGeneration == 1 })
}
