package featureauth

import (
	"fmt"
	"log/slog"

	"github.com/svrforum/SFPanel/internal/auth"
)

// EndSessions ends every session on this node and runs swap — the key change
// — while no refresh can run. It deletes every refresh token, so no browser,
// desktop app or phone gets a new access token without signing in, and then
// clears the WebSocket tickets. Holding refreshMu matters: a refresh that read
// its row before the delete would write a new one after it and mint a session
// with whatever key it read. The tokens go before the key: a crash between
// the two leaves no refresh token for the key a restart reads from
// config.yaml. swap runs even when the delete fails, so that the old key stops
// working at once; the error says the delete is still to be done.
func (h *Handler) EndSessions(swap func()) error {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	var err error
	if h.DB != nil {
		if _, dbErr := h.DB.Exec(`DELETE FROM refresh_tokens`); dbErr != nil {
			err = fmt.Errorf("delete the refresh tokens: %w", dbErr)
		}
	}
	swap()
	auth.ClearWSTickets()
	if err == nil {
		slog.Info("ended every session on this node for a new JWT key", "component", "auth")
	}
	return err
}
