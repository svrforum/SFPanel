package featureauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
)

func refreshWith(h *Handler, raw string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"`+raw+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	_ = h.Refresh(echo.New().NewContext(req, rec))
	return rec
}

// A key change alone ended nobody's session: the browser refreshed itself
// onto the new key (understand-e/jwt.md, probe P2). After EndSessions no
// refresh token issued before it works, and no WebSocket ticket either.
func TestEndSessionsEndsEveryRefreshChainAndTicket(t *testing.T) {
	h, db := newRefreshHandler(t)
	if _, err := db.Exec(`INSERT INTO admin (username, password) VALUES (?, ?)`, "alice", "x"); err != nil {
		t.Fatal(err)
	}
	raw, _ := seedRefreshToken(t, db, "alice")
	ticket := auth.MintWSTicket("alice")
	swapped := false
	if err := h.EndSessions(func() { swapped = true; auth.SetJWTSecret("rotated-key-not-for-prod") }); err != nil {
		t.Fatal(err)
	}
	if !swapped {
		t.Fatal("the key was not changed")
	}
	if rec := refreshWith(h, raw); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), response.ErrInvalidToken) {
		t.Fatalf("a refresh token from before: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := auth.ConsumeWSTicket(ticket); ok {
		t.Fatal("a WebSocket ticket from before still opens a socket")
	}
}

// The key changes once every refresh token is gone and while no refresh can
// run: a refresh between the change and the delete would mint, with the new
// key, a session from a chain the rotation ended.
func TestEndSessionsChangesTheKeyWithNoRefreshTokenLeftAndNoRefreshRunning(t *testing.T) {
	h, db := newRefreshHandler(t)
	seedRefreshToken(t, db, "alice")
	var left int
	var refreshCouldRun bool
	err := h.EndSessions(func() {
		_ = db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens`).Scan(&left)
		if h.refreshMu.TryLock() {
			refreshCouldRun = true
			h.refreshMu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d refresh tokens were left when the key changed", left)
	}
	if refreshCouldRun {
		t.Fatal("a refresh could run while the key changed")
	}
}

// The key changes even when the refresh tokens cannot be deleted, so that
// the retired key stops working at once; the error says the delete is still
// to be done, and the manager calls again.
func TestEndSessionsChangesTheKeyWhenTheDeleteFails(t *testing.T) {
	h, db := newRefreshHandler(t)
	if _, err := db.Exec(`DROP TABLE refresh_tokens`); err != nil {
		t.Fatal(err)
	}
	swapped := false
	err := h.EndSessions(func() { swapped = true })
	if !swapped || err == nil || !strings.Contains(err.Error(), "refresh tokens") {
		t.Fatalf("a delete that failed: key changed %v, error %v", swapped, err)
	}
}
