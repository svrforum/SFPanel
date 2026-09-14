package ai

import "database/sql"

// sessionRow is one ai_sessions row. tmux decides whether the session is
// alive; the row is identity, title and history (spec §1).
type sessionRow struct {
	ID, Tool, Title, RunAs, CWD string
	CreatedAt                   string
	LastAttachedAt              sql.NullString
	EndedAt                     sql.NullString
}

const sessionColumns = `id, tool, title, run_as, cwd, created_at, last_attached_at, ended_at`

func scanSession(sc interface{ Scan(...any) error }) (sessionRow, error) {
	var r sessionRow
	err := sc.Scan(&r.ID, &r.Tool, &r.Title, &r.RunAs, &r.CWD, &r.CreatedAt, &r.LastAttachedAt, &r.EndedAt)
	return r, err
}

func insertSession(db *sql.DB, r sessionRow) error {
	_, err := db.Exec(`INSERT INTO ai_sessions (id, tool, title, run_as, cwd) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.Tool, r.Title, r.RunAs, r.CWD)
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
