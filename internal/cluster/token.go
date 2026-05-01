package cluster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type TokenManager struct {
	mu     sync.Mutex
	tokens map[string]*JoinToken
	secret []byte
	// path is the JSON file used to survive process restarts. Empty in
	// tests / non-persisted callers; in production it's set by
	// NewPersistedTokenManager so the leader retains pending invite tokens
	// across restarts (without this, every leader bounce silently invalidated
	// every operator-issued join token).
	path string
}

func NewTokenManager() *TokenManager {
	secret := make([]byte, 32)
	rand.Read(secret)
	return &TokenManager{
		tokens: make(map[string]*JoinToken),
		secret: secret,
	}
}

// tokenFile is the on-disk representation of a TokenManager. We persist the
// HMAC secret alongside the token map because tokens are signed by it; if
// the secret regenerates on restart, every persisted token would fail the
// HMAC verifyHMAC step and look like ErrTokenNotFound.
type tokenFile struct {
	Secret string                `json:"secret"`
	Tokens map[string]*JoinToken `json:"tokens"`
}

// NewPersistedTokenManager loads the token state from `path` if it exists,
// or generates a fresh manager and writes it back. The file is created with
// mode 0600 because the secret allows minting valid tokens.
func NewPersistedTokenManager(path string) (*TokenManager, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create token dir: %w", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read token file: %w", readErr)
	}

	tm := &TokenManager{
		tokens: make(map[string]*JoinToken),
		path:   path,
	}

	if readErr == nil && len(data) > 0 {
		var tf tokenFile
		if err := json.Unmarshal(data, &tf); err != nil {
			return nil, fmt.Errorf("parse token file: %w", err)
		}
		secret, err := hex.DecodeString(tf.Secret)
		if err != nil || len(secret) != 32 {
			return nil, fmt.Errorf("invalid persisted token secret")
		}
		tm.secret = secret
		if tf.Tokens != nil {
			tm.tokens = tf.Tokens
		}
		// Drop expired/used entries on load so stale state doesn't
		// accumulate forever.
		tm.cleanupLocked()
		slog.Info("join tokens loaded from disk", "component", "cluster", "path", path, "count", len(tm.tokens))
	} else {
		tm.secret = make([]byte, 32)
		if _, err := rand.Read(tm.secret); err != nil {
			return nil, fmt.Errorf("generate token secret: %w", err)
		}
		if err := tm.saveLocked(); err != nil {
			return nil, fmt.Errorf("write initial token file: %w", err)
		}
	}

	return tm, nil
}

func (tm *TokenManager) Create(ttl time.Duration, createdBy string) (*JoinToken, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	mac := hmac.New(sha256.New, tm.secret)
	mac.Write(raw)
	token := hex.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))

	jt := &JoinToken{
		Token:     token,
		ExpiresAt: time.Now().Add(ttl),
		CreatedBy: createdBy,
	}
	tm.tokens[token] = jt

	tm.cleanupLocked()
	if err := tm.saveLocked(); err != nil {
		// Roll back the in-memory addition so memory and disk stay in sync;
		// otherwise a partial failure would leak a token that can never be
		// recovered after restart.
		delete(tm.tokens, token)
		return nil, fmt.Errorf("persist token: %w", err)
	}

	return jt, nil
}

// verifyHMAC re-computes the expected HMAC for a token string and compares in
// constant time. Today the token map alone is enough (tokens only enter via
// Create), but verifying HMAC here means that if tokens ever get persisted
// or serialized out of process, an attacker can't forge an entry by guessing
// a random 24-byte raw half.
func (tm *TokenManager) verifyHMAC(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	raw, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	providedMAC, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, tm.secret)
	mac.Write(raw)
	return hmac.Equal(providedMAC, mac.Sum(nil))
}

// Peek checks token validity without consuming it.
func (tm *TokenManager) Peek(token string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.verifyHMAC(token) {
		return ErrTokenNotFound
	}
	jt, ok := tm.tokens[token]
	if !ok {
		return ErrTokenNotFound
	}
	if jt.Used {
		return ErrTokenUsed
	}
	if time.Now().After(jt.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

// Validate checks the token and marks it as used.
func (tm *TokenManager) Validate(token string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if !tm.verifyHMAC(token) {
		return ErrTokenNotFound
	}
	jt, ok := tm.tokens[token]
	if !ok {
		return ErrTokenNotFound
	}
	if jt.Used {
		return ErrTokenUsed
	}
	if time.Now().After(jt.ExpiresAt) {
		delete(tm.tokens, token)
		// Best-effort persist; if it fails, the token will simply be
		// re-cleaned up on next load.
		_ = tm.saveLocked()
		return ErrTokenExpired
	}

	jt.Used = true
	if err := tm.saveLocked(); err != nil {
		// Revert so a restart wouldn't reanimate the token.
		jt.Used = false
		return fmt.Errorf("persist token consumption: %w", err)
	}
	return nil
}

// RestoreToken marks a token as unused so it can be retried.
func (tm *TokenManager) RestoreToken(token string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if jt, ok := tm.tokens[token]; ok {
		jt.Used = false
		_ = tm.saveLocked()
	}
}

func (tm *TokenManager) cleanupLocked() {
	now := time.Now()
	for k, t := range tm.tokens {
		if now.After(t.ExpiresAt) || t.Used {
			delete(tm.tokens, k)
		}
	}
}

// saveLocked writes the current state to tm.path atomically (via temp + rename).
// Caller must hold tm.mu. Memory-only managers (path == "") are no-ops, so
// existing tests that call NewTokenManager() keep working.
func (tm *TokenManager) saveLocked() error {
	if tm.path == "" {
		return nil
	}
	tf := tokenFile{
		Secret: hex.EncodeToString(tm.secret),
		Tokens: tm.tokens,
	}
	data, err := json.Marshal(&tf)
	if err != nil {
		return fmt.Errorf("marshal token file: %w", err)
	}
	tmpPath := tm.path + ".new"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Rename(tmpPath, tm.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename token file: %w", err)
	}
	return nil
}
