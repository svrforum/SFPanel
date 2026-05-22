package terminal

// buildSessionKey composes a unique per-user PTY session key. The username
// component prevents one operator from attaching to another's PTY by guessing
// or reusing a session_id query parameter.
func buildSessionKey(username, sessionID string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	return username + "\x00" + sessionID
}
