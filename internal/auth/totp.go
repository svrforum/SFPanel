package auth

import (
	"fmt"
	"sync"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func GenerateSecret(username string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      "SFPanel",
		AccountName: username,
	})
}

// usedCodes tracks TOTP codes that have already been consumed in the current
// 30-second period so the same code can't be replayed by an observer who
// sniffed it on the network. Entries expire with the period they cover.
var (
	usedCodesMu sync.Mutex
	usedCodes   = map[string]int64{} // key: "secret:code:period" → expiry unix
)

const totpPeriod = 30 // seconds, matches pquerna/otp default

func ValidateCode(secret string, code string) bool {
	nowUnix := time.Now().Unix()
	period := nowUnix / totpPeriod
	key := fmt.Sprintf("%s:%s:%d", secret, code, period)

	usedCodesMu.Lock()
	// Opportunistic GC so the map doesn't grow unbounded under normal use.
	for k, exp := range usedCodes {
		if nowUnix > exp {
			delete(usedCodes, k)
		}
	}
	if _, replayed := usedCodes[key]; replayed {
		usedCodesMu.Unlock()
		return false
	}
	// Reserve the slot *before* the crypto verify so a successful code can't
	// race itself. If the code turns out to be invalid we undo the reservation.
	usedCodes[key] = (period + 2) * totpPeriod
	usedCodesMu.Unlock()

	if !totp.Validate(code, secret) {
		usedCodesMu.Lock()
		delete(usedCodes, key)
		usedCodesMu.Unlock()
		return false
	}
	return true
}
