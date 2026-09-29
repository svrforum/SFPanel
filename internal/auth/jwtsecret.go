package auth

import "sync"

// The key tokens are signed and checked with, as the running panel knows it.
//
// Signers and verifiers read it here at the moment of use; none of them keeps
// a copy. The key changes under a running panel exactly when this node joins a
// cluster and adopts the cluster's key without restarting. Verifiers used to
// capture the startup key while login read the new one, so on a freshly joined
// node every login succeeded and every request after it was refused as an
// invalid token until the service was restarted.
var (
	jwtSecretMu sync.RWMutex
	jwtSecret   string
)

// SetJWTSecret replaces the key. The router sets it at startup; a cluster join
// sets it once the join has committed.
func SetJWTSecret(secret string) {
	jwtSecretMu.Lock()
	jwtSecret = secret
	jwtSecretMu.Unlock()
}

// JWTSecret returns the current key.
func JWTSecret() string {
	jwtSecretMu.RLock()
	defer jwtSecretMu.RUnlock()
	return jwtSecret
}
