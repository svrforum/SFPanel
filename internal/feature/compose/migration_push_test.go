package compose

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// A stack pushed to an agent goes over HTTPS and is signed with that agent's
// key: never in clear, never unsigned, never with the managers' secret, which
// every manager would accept. The agent's kind, panel CA and key come from the
// replicated state of a real one-manager cluster.
func TestPushBundleToAnAgent(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1,
	})
	if err := mgr.Init("push"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	auth.SetProxySecrets("managers-secret", []string{"managers-secret"})

	var mu sync.Mutex
	hits := 0
	var last *http.Request
	panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		hits++
		last = r.Clone(context.Background())
		mu.Unlock()
	})
	plain, secure := httptest.NewServer(panel), httptest.NewTLSServer(panel)
	defer plain.Close()
	defer secure.Close()
	for id, srv := range map[string]*httptest.Server{"a-plain": plain, "a-tls": secure} {
		v, _ := json.Marshal(cluster.Node{ID: id, Name: id, Kind: cluster.KindAgent, APIAddress: srv.Listener.Addr().String()})
		if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secure.Certificate().Raw})
	if err := mgr.SetPanelCA("a-tls", string(ca)); err != nil {
		t.Fatal(err)
	}
	// a-plain has a key, so only its missing panel CA can stop the push.
	if err := mgr.SetConfig(cluster.AgentKeyConfigKey("a-plain"), "plain-agent-key"); err != nil {
		t.Fatal(err)
	}
	seen := func() (int, *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		return hits, last
	}
	h := &Handler{}
	h.SetClusterMgr(mgr)
	push := func(node string) (int, error) {
		status, _, err := h.pushBundle(context.Background(), node, "alice", strings.NewReader("bundle"), 6, "sha", nil, 0)
		return status, err
	}

	if _, err := push("a-plain"); !errors.Is(err, cluster.ErrAgentPanelInsecure) {
		t.Fatalf("agent without a panel CA: %v", err)
	}
	if _, err := push("a-tls"); !errors.Is(err, cluster.ErrNoAgentKey) {
		t.Fatalf("agent without a key: %v", err)
	}
	if n, _ := seen(); n != 0 {
		t.Fatalf("an agent was contacted %d times", n)
	}

	if err := mgr.SetConfig(cluster.AgentKeyConfigKey("a-tls"), "agent-key"); err != nil {
		t.Fatal(err)
	}
	if status, err := push("a-tls"); err != nil || status != http.StatusOK {
		t.Fatalf("push: %d %v", status, err)
	}
	_, got := seen()
	accepts := func(key string) bool {
		auth.SetProxySecrets(key, []string{key})
		r, _ := http.NewRequest(got.Method, got.URL.RequestURI(), nil)
		r.Header.Set(auth.InternalProxyHeaderV2, got.Header.Get(auth.InternalProxyHeaderV2))
		return auth.IsInternalProxyRequest(r)
	}
	// The manager check first: a MAC that does not match records no nonce.
	if accepts("managers-secret") {
		t.Fatal("a manager accepts the header the agent was sent")
	}
	if !accepts("agent-key") {
		t.Fatal("not signed with the agent's key")
	}
}
