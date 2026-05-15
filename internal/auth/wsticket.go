package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// wsTicketTTL is how long a ws-ticket stays valid before it must be replaced
// by a fresh mint. 60 seconds covers a slow page load + WS handshake while
// keeping the replay window tight.
const wsTicketTTL = 60 * time.Second

// wsTicketBytes is the raw entropy before hex-encoding. 24 bytes = 192 bits;
// 48 hex chars in the URL, no PII / claim leak unlike a JWT.
const wsTicketBytes = 24

type wsTicketEntry struct {
	username string
	expires  time.Time
}

var (
	wsTickets   = make(map[string]wsTicketEntry)
	wsTicketsMu sync.Mutex
)

// MintWSTicket creates a one-time ticket bound to username, valid for
// wsTicketTTL. The caller (POST /auth/ws-ticket) returns the raw string to
// the JS client, which appends it to the WebSocket URL as ?ticket= — the
// JWT itself never lands in URL/access logs/browser history.
func MintWSTicket(username string) string {
	raw := make([]byte, wsTicketBytes)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	ticket := hex.EncodeToString(raw)

	wsTicketsMu.Lock()
	defer wsTicketsMu.Unlock()

	// Opportunistic cleanup of stale entries when the table grows.
	if len(wsTickets) > 1024 {
		now := time.Now()
		for k, v := range wsTickets {
			if now.After(v.expires) {
				delete(wsTickets, k)
			}
		}
	}

	wsTickets[ticket] = wsTicketEntry{
		username: username,
		expires:  time.Now().Add(wsTicketTTL),
	}
	return ticket
}

// ConsumeWSTicket returns the username bound to a ticket and removes it from
// the store. A second use of the same ticket fails — the WS handshake has
// completed exactly once.
func ConsumeWSTicket(ticket string) (string, bool) {
	if ticket == "" {
		return "", false
	}
	wsTicketsMu.Lock()
	defer wsTicketsMu.Unlock()
	entry, ok := wsTickets[ticket]
	if !ok {
		return "", false
	}
	delete(wsTickets, ticket)
	if time.Now().After(entry.expires) {
		return "", false
	}
	return entry.username, true
}
