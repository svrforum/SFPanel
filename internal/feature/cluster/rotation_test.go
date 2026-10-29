package featurecluster

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/release"
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

// settledLeader is tokenLeader's cluster with its proxy secret switched long ago.
// Its FSM's config callbacks run in goroutines of their own, and one that
// applied a CA rotation step as the test ends would write into the
// directories being removed: they apply behind a gate the test closes first,
// once those under way have finished.
func settledLeader(t *testing.T) *cluster.Manager {
	t.Helper()
	mgr := tokenLeader(t)
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	var gate sync.RWMutex
	closed := false
	// What the manager's own callback applies, for every key.
	mgr.GetRaft().GetFSM().SetOnConfigChange(func(string) {
		gate.RLock()
		defer gate.RUnlock()
		if !closed {
			mgr.ApplyProxySecrets()
			mgr.ApplyJWTKey()
			mgr.ApplyClusterCA()
		}
	})
	t.Cleanup(func() { gate.Lock(); closed = true; gate.Unlock() })
	if err := mgr.SetConfig("cluster_proxy_secret", "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig("cluster_proxy_secret_rotated_at", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	return mgr
}

// One rotation at a time; while it runs no join token is minted — its
// fingerprint would name a CA the rotation is about to change — and no
// cluster update restarts nodes under it.
func TestRotateCAStartsOnceAndHoldsTokensAndUpdates(t *testing.T) {
	// An update that got past its refusal would ask GitHub for the latest
	// release: this server answers in its place, with none.
	gh := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(gh.Close)
	prev := release.LatestURL
	release.LatestURL = gh.URL
	t.Cleanup(func() { release.LatestURL = prev })
	mgr := settledLeader(t)
	h := &Handler{Manager: mgr}
	if status, body := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", `{"phase_timeout":"soon"}`); status != http.StatusBadRequest || !strings.Contains(body, response.ErrInvalidRequest) {
		t.Fatalf("a phase timeout that is not a duration: %d %s", status, body)
	}
	status, body := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", `{"phase_timeout":"10m"}`)
	if status != http.StatusAccepted || !strings.Contains(body, `"phase":"trust"`) || !strings.Contains(body, `"phase_timeout_seconds":600`) {
		t.Fatalf("start: %d %s", status, body)
	}
	if status, body := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", ""); status != http.StatusConflict || !strings.Contains(body, response.ErrClusterRotationRefused) {
		t.Fatalf("a second one: %d %s", status, body)
	}
	if status, body := call(t, h.CreateToken, http.MethodPost, "/api/v1/cluster/token", `{"kind":"agent"}`); status != http.StatusConflict || !strings.Contains(body, response.ErrClusterCARotating) {
		t.Fatalf("a token during the rotation: %d %s", status, body)
	}
	if status, body := call(t, h.ClusterUpdate, http.MethodPost, "/api/v1/cluster/update", `{"mode":"rolling"}`); status != http.StatusConflict || !strings.Contains(body, response.ErrClusterCARotating) {
		t.Fatalf("an update during the rotation: %d %s", status, body)
	}
	if status, body := call(t, h.AbortCARotation, http.MethodPost, "/api/v1/cluster/rotate/ca/abort", ""); status != http.StatusAccepted || !strings.Contains(body, `"phase":"drop"`) {
		t.Fatalf("abort: %d %s", status, body)
	}
	if status, body := call(t, h.AbortCARotation, http.MethodPost, "/api/v1/cluster/rotate/ca/abort", ""); status != http.StatusConflict || !strings.Contains(body, response.ErrClusterRotationRefused) {
		t.Fatalf("an abort of a rotation going back already: %d %s", status, body)
	}
}

// An update and a CA rotation asked for at the same moment: one starts and
// the other is refused for it, never both. Each looking for the other first
// is not enough: both would pass their check before either had begun.
func TestAnUpdateAndACARotationNeverStartTogether(t *testing.T) {
	// The release lookup of an update that started waits until the test lets
	// it go, so the update holds its slot while the test looks.
	arrived := make(chan struct{}, 1)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(func() { gh.CloseClientConnections(); gh.Close() })
	prev := release.LatestURL
	release.LatestURL = gh.URL
	t.Cleanup(func() { release.LatestURL = prev })
	mgr := settledLeader(t)
	h := &Handler{Manager: mgr}
	waitForLookup := func(what string) {
		t.Helper()
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the update never asked for a release", what)
		}
	}

	// While an update runs, a rotation is refused for it.
	running := make(chan string, 1)
	go func() {
		status, body := call(t, h.ClusterUpdate, http.MethodPost, "/api/v1/cluster/update", `{"mode":"rolling"}`)
		running <- fmt.Sprint(status, " ", body)
	}()
	waitForLookup("an update")
	if status, body := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", ""); status != http.StatusConflict || !strings.Contains(body, response.ErrUpdateInProgress) {
		t.Fatalf("a rotation during an update: %d %s", status, body)
	}
	gh.CloseClientConnections()
	<-running
	waitForCond(t, "the update to end", func() bool { return !h.updateRunning.Load() })

	for round := 0; round < 10; round++ {
		start := make(chan struct{})
		rotated, updated := make(chan string, 1), make(chan string, 1)
		go func() {
			<-start
			status, body := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", "")
			rotated <- fmt.Sprint(status, " ", body)
		}()
		go func() {
			<-start
			status, body := call(t, h.ClusterUpdate, http.MethodPost, "/api/v1/cluster/update", `{"mode":"rolling"}`)
			updated <- fmt.Sprint(status, " ", body)
		}()
		close(start)
		switch rot := <-rotated; {
		case strings.HasPrefix(rot, "202 "):
			if h.updateRunning.Load() {
				t.Fatalf("round %d: an update started alongside the CA rotation", round)
			}
			if upd := <-updated; !strings.Contains(upd, response.ErrClusterCARotating) {
				t.Fatalf("round %d: the update beside a CA rotation answered %s", round, upd)
			}
			if _, err := mgr.AbortCARotation(); err != nil {
				t.Fatal(err)
			}
			waitForCond(t, "the rotation to end", func() bool {
				_ = mgr.MaybeAdvanceCARotation()
				st, _ := mgr.RotationStatus()
				return st != nil && st.CA.Rotation != nil && st.CA.Rotation.Phase == cluster.CAPhaseAborted
			})
		case strings.Contains(rot, response.ErrUpdateInProgress):
			waitForLookup(fmt.Sprintf("round %d", round))
			gh.CloseClientConnections()
			<-updated
		default:
			t.Fatalf("round %d: the rotation answered %s", round, rot)
		}
		waitForCond(t, "the update to end", func() bool { return !h.updateRunning.Load() })
	}
}

// The status names each node's step and what it waits for — fingerprints
// and generations, never a key.
func TestRotationStatusCarriesNoKey(t *testing.T) {
	mgr := settledLeader(t)
	h := &Handler{Manager: mgr}
	caKey, err := mgr.CAPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := call(t, h.RotateCA, http.MethodPost, "/api/v1/cluster/rotate/ca", ""); status != http.StatusAccepted {
		t.Fatal(status)
	}
	status, body := call(t, h.GetRotation, http.MethodGet, "/api/v1/cluster/rotation", "")
	if status != http.StatusOK || !strings.Contains(body, `"phase":"trust"`) || !strings.Contains(body, mgr.LocalNodeID()) || !strings.Contains(body, `"raft_tls":true`) {
		t.Fatalf("status: %d %s", status, body)
	}
	// Until the rotation is done the cluster's CA is the one it moves away
	// from, and the trust phase runs out at its deadline.
	if !strings.Contains(body, `"current_pin":"`+cluster.CAPin(caKey)+`"`) || !strings.Contains(body, `"deadline":"`) {
		t.Fatalf("status in the trust phase: %s", body)
	}
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, "replicated-secret") {
		t.Fatalf("the status carries a key: %s", body)
	}
}

// After a rotation the join command a token page prints names the new CA:
// one with the old fingerprint is refused by the joiner, as it should be.
func TestATokenAfterARotationPinsTheNewCA(t *testing.T) {
	mgr := settledLeader(t)
	h := &Handler{Manager: mgr}
	rot, err := mgr.StartCARotation(0)
	if err != nil {
		t.Fatal(err)
	}
	// a one-manager cluster rotates on its own evidence alone
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		_ = mgr.MaybeAdvanceCARotation()
		if st, _ := mgr.RotationStatus(); st != nil && st.CA.Rotation != nil && st.CA.Rotation.Phase == cluster.CAPhaseDone {
			if st.CA.CurrentPin != rot.NextPin {
				t.Fatalf("done, and the status names %s as the cluster's CA, not the next one %s", st.CA.CurrentPin, rot.NextPin)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the rotation did not finish")
		}
	}
	status, body := call(t, h.CreateToken, http.MethodPost, "/api/v1/cluster/token", `{"kind":"agent"}`)
	if status != http.StatusOK || !strings.Contains(body, `"ca_pin":"`+rot.NextPin+`"`) {
		t.Fatalf("token: %d %s", status, body)
	}
}
