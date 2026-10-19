package cluster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/config"
)

// signedProxyRequest is an inbound request whose internal proxy header was
// signed with secret.
func signedProxyRequest(t *testing.T, secret string) *http.Request {
	t.Helper()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	n := hex.EncodeToString(nonce)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + ":" + n + ":GET:/api/v1/system/info"))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	r.Header.Set(auth.InternalProxyHeaderV2, ts+":"+n+":"+hex.EncodeToString(mac.Sum(nil)))
	return r
}

// proxyTestManager is a one-node cluster member over dataDir, without the
// Manager's services.
func proxyTestManager(t *testing.T, tlsMgr *TLSManager, dataDir string, bootstrap bool) *Manager {
	t.Helper()
	rn, err := NewRaftNode(RaftConfig{
		NodeID: "n1", BindAddr: fmt.Sprintf("127.0.0.1:%d", freePortPair(t)),
		DataDir: dataDir, Bootstrap: bootstrap,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rn.Shutdown() })
	if bootstrap {
		for i := 0; i < 100 && !rn.IsLeader(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return &Manager{
		config: &config.ClusterConfig{DataDir: dataDir}, tls: tlsMgr, nodeID: "n1",
		raft: rn, heartbeat: NewHeartbeatManager(time.Minute, 3*time.Minute),
	}
}

// After a restart the FSM holds only what the last snapshot held until the
// node learns the commit index — seconds, or the length of an election. A
// rotation the node had applied must hold through that window: signing with,
// or accepting, the CA-derived secret again reopens what the rotation closed.
func TestRestartKeepsTheRotation(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	_, tlsMgr := setupCertDir(t)
	dataDir := filepath.Join(t.TempDir(), "raft")
	m := proxyTestManager(t, tlsMgr, dataDir, true)
	legacy := m.ProxySecret()
	if err := m.SetConfig(configKeyProxySecret, "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig(configKeyProxyRotatedAt, time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	m.ApplyProxySecrets()
	if err := m.raft.Shutdown(); err != nil {
		t.Fatal(err)
	}

	// Restart on a copy: bolt keeps its lock until the process exits.
	restarted := filepath.Join(t.TempDir(), "raft")
	if err := os.CopyFS(restarted, os.DirFS(dataDir)); err != nil {
		t.Fatal(err)
	}
	m2 := proxyTestManager(t, tlsMgr, restarted, false)
	if _, caughtUp := m2.raft.GetFSM().GetConfigValue(configKeyProxyRotatedAt); caughtUp {
		t.Fatal("precondition: the FSM already holds the rotation, so the window is not exercised")
	}
	m2.ApplyProxySecrets() // what activation does at boot
	if got := auth.ClusterProxySecret(); got != "replicated-secret" {
		t.Fatalf("signs with %q after the restart, want the replicated secret", got)
	}
	if auth.IsInternalProxyRequest(signedProxyRequest(t, legacy)) {
		t.Fatal("accepts the CA-derived secret again after the restart")
	}
	if !auth.IsInternalProxyRequest(signedProxyRequest(t, "replicated-secret")) {
		t.Fatal("refuses the replicated secret after the restart")
	}
}

// A member reports CapProxySecret only once it accepts the replicated secret.
// The leader rotates when every member reports it, so a report that meant
// only "runs code that could" let it rotate past a member whose FSM had not
// caught up — which then refused every request signed with the new secret.
func TestCapabilityOnlyOnceTheSecretIsApplied(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	_, tlsMgr := setupCertDir(t)
	m := proxyTestManager(t, tlsMgr, filepath.Join(t.TempDir(), "raft"), true)
	m.ApplyProxySecrets()
	if caps := m.heartbeatCapabilities(); slices.Contains(caps, CapProxySecret) {
		t.Fatalf("reports %v with no replicated secret", caps)
	}
	if err := m.SetConfig(configKeyProxySecret, "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	if caps := m.heartbeatCapabilities(); slices.Contains(caps, CapProxySecret) {
		t.Fatalf("reports %v while the replicated secret is in the FSM but not applied", caps)
	}
	m.ApplyProxySecrets()
	if caps := m.heartbeatCapabilities(); !slices.Contains(caps, CapProxySecret) {
		t.Fatalf("reports %v with the replicated secret applied", caps)
	}
}

// A node applies the replicated secret, and the rotation, when its FSM does —
// not on the next 15-second tick, until which it refuses requests signed
// with a secret its peers already sign with.
func TestProxySecretsFollowTheFSM(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	mgr, _, _, _ := realLeader(t)
	mgr.ApplyProxySecrets() // what activation does
	if err := mgr.SetConfig(configKeyProxySecret, "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); !auth.IsInternalProxyRequest(signedProxyRequest(t, "replicated-secret")); {
		if time.Now().After(deadline) {
			t.Fatal("the replicated secret was not accepted within 2s of the FSM applying it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A node stops accepting the derived secret once its FSM lists an agent — an
// agent holds ca.crt, from which that secret is computed — even inside the
// grace after the switch, which this node measures with its own clock.
func TestAnAgentEndsTheDerivedSecret(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	_, tlsMgr := setupCertDir(t)
	m := proxyTestManager(t, tlsMgr, filepath.Join(t.TempDir(), "raft"), true)
	legacy := m.ProxySecret()
	if err := m.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "n1", Kind: KindManager, Role: RoleVoter})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig(configKeyProxySecret, "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig(configKeyProxyRotatedAt, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	m.ApplyProxySecrets()
	if !auth.IsInternalProxyRequest(signedProxyRequest(t, legacy)) {
		t.Fatal("precondition: managers only, inside the grace — the derived secret should still be accepted")
	}
	if err := m.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: "agent-1", Kind: KindAgent})}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	m.ApplyProxySecrets()
	if auth.IsInternalProxyRequest(signedProxyRequest(t, legacy)) {
		t.Fatal("the derived secret is accepted with an agent in the cluster")
	}
	if got := auth.ClusterProxySecret(); got != "replicated-secret" {
		t.Fatalf("signs with %q, want the replicated secret", got)
	}
	if !auth.IsInternalProxyRequest(signedProxyRequest(t, "replicated-secret")) {
		t.Fatal("the replicated secret is refused")
	}
}

// The leader switches once every other manager can; an agent, which never
// reports CapProxySecret and has no heartbeat to the leader here, does not
// hold the switch back.
func TestRotationWaitsForManagersOnly(t *testing.T) {
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	_, tlsMgr := setupCertDir(t)
	m := proxyTestManager(t, tlsMgr, filepath.Join(t.TempDir(), "raft"), true)
	for _, n := range []Node{{ID: "n1", Kind: KindManager, Role: RoleVoter}, {ID: "agent-1", Kind: KindAgent}} {
		if err := m.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(n)}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetConfig(configKeyProxySecret, "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	m.replicatedSecretSeenAt("replicated-secret", time.Now().Add(-2*proxySecretSettle)) // seen long enough ago
	if err := m.MaybeRotateProxySecret(); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.raft.GetFSM().GetConfigValue(configKeyProxyRotatedAt); v == "" {
		t.Fatal("the rotation waited for an agent")
	}
}

// The settle time before a rotation runs from when the leader saw the secret
// it rotates to, not some earlier one.
func TestReplicatedSecretSeenAtFollowsTheSecret(t *testing.T) {
	m := &Manager{}
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if got := m.replicatedSecretSeenAt("a", t0); !got.Equal(t0) {
		t.Fatalf("first sight: %v", got)
	}
	if got := m.replicatedSecretSeenAt("a", t0.Add(time.Minute)); !got.Equal(t0) {
		t.Fatalf("the same secret again: %v", got)
	}
	if got := m.replicatedSecretSeenAt("b", t0.Add(2*time.Minute)); !got.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("a different secret inherited the first one's time: %v", got)
	}
}
