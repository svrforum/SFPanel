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
