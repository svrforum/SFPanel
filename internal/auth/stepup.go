package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// StepUpHeader carries a manager's word, on a request it relays to an agent,
// that it checked the operator's password for that request: an agent has no
// accounts to check one against, and the password is not sent to it.
const StepUpHeader = "X-SFPanel-Step-Up"

// StepUpError is a refused step-up re-authentication. Status is 403 for a
// wrong or unverifiable password, 400 for a missing one and 429 when
// rate-limited — never 401, which tells the browser its session ended.
type StepUpError struct {
	Status        int
	Code, Message string
}

func (e *StepUpError) Error() string { return e.Message }

// StepUpProofWith binds the word to the one request it rides on: that
// request's internal-proxy header — unique to it, and refused if replayed —
// and its body.
func StepUpProofWith(key, proxyHeader string, body []byte) string {
	sum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "step-up\n%s\n%x", proxyHeader, sum)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyStepUpProof checks proof with each secret this node accepts — on an
// agent its own keys, which only its managers hold: two while it moves to a
// new one (cluster.AgentKeys), when a manager may have signed with either.
// The proof is bound to the request's internal-proxy header, which is
// single-use, so taking any of them weakens nothing.
func VerifyStepUpProof(proxyHeader string, body []byte, proof string) bool {
	proxySecretsMu.RLock()
	accept := proxyAcceptSecrets
	proxySecretsMu.RUnlock()
	if proxyHeader == "" || proof == "" {
		return false
	}
	for _, key := range accept {
		if key != "" && hmac.Equal([]byte(proof), []byte(StepUpProofWith(key, proxyHeader, body))) {
			return true
		}
	}
	return false
}
