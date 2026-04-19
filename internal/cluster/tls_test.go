package cluster

import (
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
	firstSerial := first.Leaf
	if firstSerial == nil {
		// Leaf is only populated when Certificates is preloaded; fall back
		// to comparing the raw DER bytes.
	}
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
