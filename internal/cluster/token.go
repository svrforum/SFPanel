package cluster

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type TokenManager struct {
	mu     sync.Mutex
	tokens map[string]*JoinToken
	secret []byte
}

func NewTokenManager() *TokenManager {
	secret := make([]byte, 32)
	rand.Read(secret)
	return &TokenManager{
		tokens: make(map[string]*JoinToken),
		secret: secret,
	}
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
		return ErrTokenExpired
	}

	jt.Used = true
	return nil
}

// RestoreToken marks a token as unused so it can be retried.
func (tm *TokenManager) RestoreToken(token string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if jt, ok := tm.tokens[token]; ok {
		jt.Used = false
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
