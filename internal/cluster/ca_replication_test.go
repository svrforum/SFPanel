package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
)

// newSeededCA issues a CA into a temp dir and returns its cert+key PEM bytes.
func newSeededCA(t *testing.T) (caCert, caKey []byte) {
	t.Helper()
	dir := t.TempDir()
	src := NewTLSManager(dir)
	if err := src.InitCA("test-cluster"); err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	cert, err := src.LoadCACert()
	if err != nil {
		t.Fatalf("LoadCACert: %v", err)
	}
	key, err := src.LoadCAKey()
	if err != nil {
		t.Fatalf("LoadCAKey: %v", err)
	}
	return cert, key
}

// fsmWithConfig builds an FSM and applies CmdSetConfig entries for each k/v.
func fsmWithConfig(t *testing.T, kv map[string]string) *FSM {
	t.Helper()
	fsm := NewFSM()
	for k, v := range kv {
		val, _ := json.Marshal(v)
		data, _ := json.Marshal(Command{Type: CmdSetConfig, Key: k, Value: val})
		if res := fsm.Apply(&raft.Log{Index: 1, Data: data}); res != nil {
			t.Fatalf("apply CmdSetConfig %q: %v", k, res)
		}
	}
	return fsm
}

// TestTLSManager_CAKeyHelpers covers the ca.key disk helpers: a fresh dir with
// only ca.crt reports HasCAKey()==false (the exact issue #5 asymmetry), and a
// SaveCAKey round-trips through LoadCAKey.
func TestTLSManager_CAKeyHelpers(t *testing.T) {
	_, caKey := newSeededCA(t)

	dir := t.TempDir()
	mgr := NewTLSManager(dir)

	if mgr.HasCAKey() {
		t.Fatal("HasCAKey should be false on an empty cert dir")
	}

	// Simulate the issue #5 state: ca.crt present, ca.key absent.
	if err := mgr.SaveCACert([]byte("dummy-ca-cert")); err != nil {
		t.Fatalf("SaveCACert: %v", err)
	}
	if !mgr.HasCA() {
		t.Fatal("HasCA should be true after SaveCACert")
	}
	if mgr.HasCAKey() {
		t.Fatal("HasCAKey must stay false when only ca.crt is present (issue #5 asymmetry)")
	}

	if err := mgr.SaveCAKey(caKey); err != nil {
		t.Fatalf("SaveCAKey: %v", err)
	}
	if !mgr.HasCAKey() {
		t.Fatal("HasCAKey should be true after SaveCAKey")
	}
	got, err := mgr.LoadCAKey()
	if err != nil {
		t.Fatalf("LoadCAKey: %v", err)
	}
	if string(got) != string(caKey) {
		t.Fatal("LoadCAKey did not round-trip the written key")
	}
	// ca.key must be written 0600 (private key, matches SaveNodeCert/InitCA).
	info, err := os.Stat(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatalf("stat ca.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("ca.key perm = %o, want 0600", perm)
	}
}

// TestEnsureCAKey_MaterializesFromFSM is the core of the fix: a leader that has
// ca.crt but no ca.key materializes the key from replicated FSM state and can
// then sign a joining node's cert — the operation that failed in issue #5.
func TestEnsureCAKey_MaterializesFromFSM(t *testing.T) {
	caCert, caKey := newSeededCA(t)

	// Destination node: has the public ca.crt (as every cluster node does) but
	// NOT the private ca.key — the exact broken-leader state from issue #5.
	dir := t.TempDir()
	tlsMgr := NewTLSManager(dir)
	if err := tlsMgr.SaveCACert(caCert); err != nil {
		t.Fatalf("SaveCACert: %v", err)
	}

	fsm := fsmWithConfig(t, map[string]string{
		configKeyCACert: string(caCert),
		configKeyCAKey:  string(caKey),
	})
	m := &Manager{tls: tlsMgr, raft: &RaftNode{fsm: fsm}}

	// Sign attempt would fail before materialization (no ca.key on disk).
	if tlsMgr.HasCAKey() {
		t.Fatal("precondition: ca.key must be absent on disk")
	}
	if err := m.ensureCAKey(); err != nil {
		t.Fatalf("ensureCAKey: %v", err)
	}
	if !tlsMgr.HasCAKey() {
		t.Fatal("ensureCAKey should have materialized ca.key to disk")
	}
	// The materialized key must actually sign a node cert now.
	if _, _, err := tlsMgr.IssueNodeCert("joiner", []string{"127.0.0.1"}); err != nil {
		t.Fatalf("IssueNodeCert after materialize: %v", err)
	}
}

// TestEnsureCAKey_UnavailableWhenAbsent: when the key exists neither on disk nor
// in replicated state, ensureCAKey returns the actionable ErrCAKeyUnavailable
// instead of a raw file-not-found deep inside signing.
func TestEnsureCAKey_UnavailableWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	tlsMgr := NewTLSManager(dir) // empty: no ca.crt, no ca.key
	fsm := NewFSM()              // empty config
	m := &Manager{tls: tlsMgr, raft: &RaftNode{fsm: fsm}}

	err := m.ensureCAKey()
	if !errors.Is(err, ErrCAKeyUnavailable) {
		t.Fatalf("ensureCAKey err = %v, want ErrCAKeyUnavailable", err)
	}
}

// TestEnsureCAKey_NoopWhenPresent: a node that already holds ca.key is a no-op
// and must not clobber the existing key.
func TestEnsureCAKey_NoopWhenPresent(t *testing.T) {
	_, caKey := newSeededCA(t)
	dir := t.TempDir()
	tlsMgr := NewTLSManager(dir)
	if err := tlsMgr.SaveCAKey(caKey); err != nil {
		t.Fatalf("SaveCAKey: %v", err)
	}
	// Empty FSM: if ensureCAKey ignored the on-disk key it would fail here.
	m := &Manager{tls: tlsMgr, raft: &RaftNode{fsm: NewFSM()}}

	if err := m.ensureCAKey(); err != nil {
		t.Fatalf("ensureCAKey should be a no-op when ca.key present: %v", err)
	}
	got, _ := tlsMgr.LoadCAKey()
	if string(got) != string(caKey) {
		t.Fatal("ensureCAKey clobbered an existing ca.key")
	}
}

// TestSeedClusterCA_GuardsNonLeader: seeding is a Raft Apply, so it must refuse
// on a node with no raft / not leader rather than panic or silently no-op.
func TestSeedClusterCA_GuardsNonLeader(t *testing.T) {
	m := &Manager{} // raft == nil
	if err := m.SeedClusterCA(); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("SeedClusterCA on nil-raft manager = %v, want ErrNotLeader", err)
	}
}

// TestSaveCAKey_AtomicUnderConcurrency guards the atomic write: ensureCAKey can
// fire SaveCAKey from parallel HandleJoin goroutines on a freshly-promoted
// leader while loadCA reads ca.key without a lock. A concurrent reader must
// never observe a half-written key. With a plain truncating write this races;
// with temp+rename every successful read is the complete key.
func TestSaveCAKey_AtomicUnderConcurrency(t *testing.T) {
	_, caKey := newSeededCA(t)
	dir := t.TempDir()
	mgr := NewTLSManager(dir)
	if err := mgr.SaveCAKey(caKey); err != nil { // prime so readers don't just hit ENOENT
		t.Fatalf("prime SaveCAKey: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	stop := make(chan struct{})

	for i := 0; i < 8; i++ { // writers hammering identical content
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := mgr.SaveCAKey(caKey); err != nil {
					fail(fmt.Errorf("writer: %w", err))
					return
				}
			}
		}()
	}
	for i := 0; i < 8; i++ { // readers: any successful read must be the whole key
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				got, err := mgr.LoadCAKey()
				if err != nil {
					continue // brief absence during rename is acceptable
				}
				if string(got) != string(caKey) {
					fail(errors.New("torn read: ca.key content mismatch mid-write"))
					return
				}
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if firstErr != nil {
		t.Fatal(firstErr)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "ca.key.*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files must be cleaned up, found: %v", leftovers)
	}
	// The final key must still be intact and usable.
	if got, err := mgr.LoadCAKey(); err != nil || string(got) != string(caKey) {
		t.Fatalf("final ca.key not intact after concurrency: err=%v match=%v", err, string(got) == string(caKey))
	}
}
