package ai

import (
	"database/sql"
	"time"
)

// sessionRow is one ai_sessions row. tmux decides whether the session is
// alive; the row is identity, title and history (spec §1).
type sessionRow struct {
	ID, Tool, Title, RunAs, CWD string
	// Profile is the tool configuration directory the session runs against;
	// "" is the tool's own default directory (see profiles.go).
	Profile string
	// Launch is the JSON of the launch options the session was created with
	// (launch.go); "" is a tool started bare, which every row written before
	// migration 38 already means.
	Launch         string
	CreatedAt      string
	LastAttachedAt sql.NullString
	EndedAt        sql.NullString
}

const sessionColumns = `id, tool, title, run_as, cwd, profile, launch, created_at, last_attached_at, ended_at`

func scanSession(sc interface{ Scan(...any) error }) (sessionRow, error) {
	var r sessionRow
	err := sc.Scan(&r.ID, &r.Tool, &r.Title, &r.RunAs, &r.CWD, &r.Profile, &r.Launch, &r.CreatedAt, &r.LastAttachedAt, &r.EndedAt)
	return r, err
}

func insertSession(db *sql.DB, r sessionRow) error {
	_, err := db.Exec(`INSERT INTO ai_sessions (id, tool, title, run_as, cwd, profile, launch) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Tool, r.Title, r.RunAs, r.CWD, r.Profile, r.Launch)
	return err
}

func listSessionRows(db *sql.DB) ([]sessionRow, error) {
	// rowid, not id, breaks a tie: created_at has second resolution, and id is
	// 6 random bytes — ordering two same-second sessions by it is a coin toss,
	// while rowid is insertion order, which is what "creation order" means.
	rows, err := db.Query(`SELECT ` + sessionColumns + ` FROM ai_sessions ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sessionRow{}
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func getSessionRow(db *sql.DB, id string) (sessionRow, bool, error) {
	r, err := scanSession(db.QueryRow(`SELECT `+sessionColumns+` FROM ai_sessions WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return sessionRow{}, false, nil
	}
	if err != nil {
		return sessionRow{}, false, err
	}
	return r, true, nil
}

func updateSessionTitle(db *sql.DB, id, title string) (bool, error) {
	res, err := db.Exec(`UPDATE ai_sessions SET title = ? WHERE id = ?`, title, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// setSessionEnded stamps ended_at the first time a session is seen gone and
// clears it when the session is restarted.
func setSessionEnded(db *sql.DB, id string, ended bool) error {
	var err error
	if ended {
		_, err = db.Exec(`UPDATE ai_sessions SET ended_at = CURRENT_TIMESTAMP WHERE id = ? AND ended_at IS NULL`, id)
	} else {
		_, err = db.Exec(`UPDATE ai_sessions SET ended_at = NULL WHERE id = ?`, id)
	}
	return err
}

func touchSessionAttached(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE ai_sessions SET last_attached_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func deleteSessionRow(db *sql.DB, id string) (bool, error) {
	res, err := db.Exec(`DELETE FROM ai_sessions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// recentSessionDirs feeds the working-directory picker: each directory
// once, most recently used first.
func recentSessionDirs(db *sql.DB, limit int) ([]string, error) {
	rows, err := db.Query(`SELECT cwd FROM ai_sessions GROUP BY cwd ORDER BY MAX(created_at) DESC, cwd LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// profileLastUsed is the newest session per profile for one account and tool,
// so the picker can show which login was used last.
func profileLastUsed(db *sql.DB, runAs, tool string) (map[string]string, error) {
	rows, err := db.Query(`SELECT profile, MAX(created_at) FROM ai_sessions WHERE run_as = ? AND tool = ? GROUP BY profile`, runAs, tool)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var profile string
		var last sql.NullString
		if err := rows.Scan(&profile, &last); err != nil {
			return nil, err
		}
		if last.Valid {
			out[profile] = normalizeStoredTime(last.String)
		}
	}
	return out, rows.Err()
}

// normalizeStoredTime turns SQLite's own CURRENT_TIMESTAMP text into RFC 3339
// and leaves a value that is already RFC 3339 alone. It is needed because an
// aggregate carries no declared column type: the driver parses a direct
// created_at read into a time and hands it back as RFC 3339, but
// MAX(created_at) comes back as the stored "2006-01-02 15:04:05" (UTC). One
// field carrying two formats is exactly the bug sessionsSnapshot documents
// for ended_at.
func normalizeStoredTime(s string) string {
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}
