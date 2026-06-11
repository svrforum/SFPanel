package cluster

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupCertDir issues a CA and a node cert into a temp dir and returns the path.
func setupCertDir(t *testing.T) (string, *TLSManager) {
	t.Helper()
	dir := t.TempDir()
	mgr := NewTLSManager(dir)
	if err := mgr.InitCA("test-cluster"); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	certPEM, keyPEM, err := mgr.IssueNodeCert("node-1", []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueNodeCert: %v", err)
	}
	if err := mgr.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatalf("SaveNodeCert: %v", err)
	}
	return dir, mgr
}

// L-07: a new node cert written to disk is picked up on the next handshake,
// not after process restart. The debounce is bypassed by rewinding lastStatTime.
func TestTLSManager_HotReloadNodeCert(t *testing.T) {
	dir, mgr := setupCertDir(t)

	srvCfg, err := mgr.ServerTLSConfig()
	if err != nil {
		t.Fatalf("ServerTLSConfig: %v", err)
	}
	if srvCfg.GetCertificate == nil {
		t.Fatalf("ServerTLSConfig did not wire GetCertificate")
	}

	first, err := srvCfg.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate initial: %v", err)
	}
	if len(first.Certificate) == 0 {
		t.Fatalf("initial cert empty")
	}
	// tls.LoadX509KeyPair leaves Leaf nil; compare raw DER bytes instead.
	firstDER := string(first.Certificate[0])

	// Issue a fresh cert (different serial) and overwrite node.{crt,key}.
	// Bump the mtime so the debounced reload detects a change.
	certPEM2, keyPEM2, err := mgr.IssueNodeCert("node-1", []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueNodeCert #2: %v", err)
	}
	if err := mgr.SaveNodeCert(certPEM2, keyPEM2); err != nil {
		t.Fatalf("SaveNodeCert #2: %v", err)
	}
	future := time.Now().Add(5 * time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "node.crt"), future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Bypass the rate limit: rewind lastStatTime so the next getNodeCert
	// performs an actual stat + reload.
	mgr.mu.Lock()
	mgr.lastStatTime = time.Time{}
	mgr.mu.Unlock()

	second, err := srvCfg.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after rotation: %v", err)
	}
	secondDER := string(second.Certificate[0])

	if firstDER == secondDER {
		t.Fatalf("expected cert to change after disk rotation, but DER unchanged")
	}
}

// L-07: when the cert file exists but the key file is missing (half-written
// rotation), the manager continues serving the previous cached cert rather
// than returning an error and breaking connectivity.
func TestTLSManager_HalfRotationKeepsCached(t *testing.T) {
	dir, mgr := setupCertDir(t)

	cfg, err := mgr.ServerTLSConfig()
	if err != nil {
		t.Fatalf("ServerTLSConfig: %v", err)
	}
	first, err := cfg.GetCertificate(nil)
	if err != nil {
		t.Fatalf("initial GetCertificate: %v", err)
	}

	// Delete the key but leave the cert, then bump mtime to force a reload.
	if err := os.Remove(filepath.Join(dir, "node.key")); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	future := time.Now().Add(5 * time.Minute)
	_ = os.Chtimes(filepath.Join(dir, "node.crt"), future, future)
	mgr.mu.Lock()
	mgr.lastStatTime = time.Time{}
	mgr.mu.Unlock()

	second, err := cfg.GetCertificate(nil)
	if err != nil {
		t.Fatalf("expected fallback to cached cert, got error: %v", err)
	}
	if string(second.Certificate[0]) != string(first.Certificate[0]) {
		t.Fatalf("expected cached cert during half-rotation, got different DER")
	}
}

// The Raft listener (grpc_port + 1) has no interceptor layer, so its TLS
// config must demand a verified client cert outright — while the gRPC port
// keeps VerifyClientCertIfGiven so PreFlight/Join can land pre-cert (gated
// per-method by the interceptors in grpc_server.go).
func TestRaftServerTLSConfig_RequiresClientCert(t *testing.T) {
	_, mgr := setupCertDir(t)

	raftCfg, err := mgr.RaftServerTLSConfig()
	if err != nil {
		t.Fatalf("RaftServerTLSConfig: %v", err)
	}
	if raftCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("raft listener ClientAuth = %v, want RequireAndVerifyClientCert", raftCfg.ClientAuth)
	}
	if raftCfg.ClientCAs == nil {
		t.Fatalf("raft listener must verify clients against the cluster CA pool")
	}
	if raftCfg.GetCertificate == nil {
		t.Fatalf("raft listener must keep the hot-reload GetCertificate callback")
	}

	grpcCfg, err := mgr.ServerTLSConfig()
	if err != nil {
		t.Fatalf("ServerTLSConfig: %v", err)
	}
	if grpcCfg.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Fatalf("gRPC listener ClientAuth = %v, want VerifyClientCertIfGiven (PreFlight/Join run pre-cert)", grpcCfg.ClientAuth)
	}
}

// The Raft dial side must present a client cert or the listener's
// RequireAndVerifyClientCert would break legitimate peers.
func TestClientTLSConfig_PresentsNodeCert(t *testing.T) {
	_, mgr := setupCertDir(t)

	cfg, err := mgr.ClientTLSConfig()
	if err != nil {
		t.Fatalf("ClientTLSConfig: %v", err)
	}
	if cfg.GetClientCertificate == nil {
		t.Fatalf("ClientTLSConfig must wire GetClientCertificate")
	}
	cert, err := cfg.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("GetClientCertificate: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("client config returned an empty certificate")
	}
}

// L-07: repeated GetCertificate calls within the debounce window do NOT
// perform extra os.Stat calls on the cert file.
func TestTLSManager_ReloadDebounce(t *testing.T) {
	_, mgr := setupCertDir(t)

	// Prime the cache.
	cfg, err := mgr.ServerTLSConfig()
	if err != nil {
		t.Fatalf("ServerTLSConfig: %v", err)
	}
	if _, err := cfg.GetCertificate(nil); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Record baseline lastStatTime. Subsequent calls inside the debounce
	// window should leave it unchanged.
	mgr.mu.Lock()
	baseline := mgr.lastStatTime
	mgr.mu.Unlock()

	for i := 0; i < 1000; i++ {
		if _, err := cfg.GetCertificate(nil); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}

	mgr.mu.Lock()
	after := mgr.lastStatTime
	mgr.mu.Unlock()
	if !after.Equal(baseline) {
		t.Fatalf("debounce ineffective: lastStatTime advanced from %v to %v", baseline, after)
	}
}
