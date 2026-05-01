package cluster

import (
	"testing"
	"time"
)

func TestTokenManager_CreateAndValidate(t *testing.T) {
	tm := NewTokenManager()
	token, err := tm.Create(time.Hour, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := tm.Validate(token.Token); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Second validate should fail with ErrTokenUsed
	if err := tm.Validate(token.Token); err != ErrTokenUsed {
		t.Fatalf("expected ErrTokenUsed, got %v", err)
	}
}

func TestTokenManager_Peek_DoesNotConsume(t *testing.T) {
	tm := NewTokenManager()
	token, err := tm.Create(time.Hour, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Peek should succeed
	if err := tm.Peek(token.Token); err != nil {
		t.Fatalf("Peek: %v", err)
	}

	// Peek again should still succeed (not consumed)
	if err := tm.Peek(token.Token); err != nil {
		t.Fatalf("Peek again: %v", err)
	}

	// Validate should still work after Peek
	if err := tm.Validate(token.Token); err != nil {
		t.Fatalf("Validate after Peek: %v", err)
	}
}

func TestTokenManager_Peek_NotFound(t *testing.T) {
	tm := NewTokenManager()
	if err := tm.Peek("nonexistent"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestTokenManager_Peek_Expired(t *testing.T) {
	tm := NewTokenManager()
	token, _ := tm.Create(1*time.Millisecond, "test")
	time.Sleep(5 * time.Millisecond)

	if err := tm.Peek(token.Token); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestTokenManager_Validate_Expired(t *testing.T) {
	tm := NewTokenManager()
	token, _ := tm.Create(1*time.Millisecond, "test")
	time.Sleep(5 * time.Millisecond)

	if err := tm.Validate(token.Token); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestTokenManager_Validate_NotFound(t *testing.T) {
	tm := NewTokenManager()
	if err := tm.Validate("nonexistent"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

// TestTokenManager_Persistence proves a token issued before a "process restart"
// (== rebuilding the manager from the same path) is still valid afterwards.
// Without on-disk persistence every leader bounce silently invalidated every
// pending invite — indistinguishable from a typo on the joining side.
func TestTokenManager_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tokens.json"

	tm1, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatalf("first manager: %v", err)
	}
	jt, err := tm1.Create(time.Hour, "init")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate a process restart by building a fresh manager off the same file.
	tm2, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatalf("second manager: %v", err)
	}
	if err := tm2.Peek(jt.Token); err != nil {
		t.Fatalf("Peek after restart: %v", err)
	}
	if err := tm2.Validate(jt.Token); err != nil {
		t.Fatalf("Validate after restart: %v", err)
	}
	// Used state must also survive a second restart.
	tm3, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatalf("third manager: %v", err)
	}
	// cleanupLocked drops Used tokens on load, so the token should be gone now.
	if err := tm3.Peek(jt.Token); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound for consumed token after reload, got %v", err)
	}
}

// TestTokenManager_PersistenceExpiredCleanup proves expired tokens don't stay
// in the file forever — they get pruned on load via cleanupLocked.
func TestTokenManager_PersistenceExpiredCleanup(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tokens.json"

	tm1, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatalf("first manager: %v", err)
	}
	if _, err := tm1.Create(1*time.Millisecond, "expiring"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	tm2, err := NewPersistedTokenManager(path)
	if err != nil {
		t.Fatalf("second manager: %v", err)
	}
	tm2.mu.Lock()
	count := len(tm2.tokens)
	tm2.mu.Unlock()
	if count != 0 {
		t.Errorf("expected expired token to be pruned on load, got %d tokens", count)
	}
}
