package lanscan

import (
	"database/sql"
	"encoding/json"
	"log/slog"
)

// Result is a finished scan of one network, as stored in lan_scans and
// answered in GET /network/scan's results.
type Result struct {
	Network   string `json:"network"`
	Interface string `json:"interface"`
	// Address and Gateway are the network's as they were when it was
	// scanned.
	Address string `json:"address"`
	Gateway string `json:"gateway"`
	// Ports says whether the scan checked ports, or only found hosts.
	Ports bool `json:"ports"`
	// StartedAt and FinishedAt are RFC 3339, UTC.
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Hosts      []Host `json:"hosts"`
}

// saveResult keeps r as its network's last result, in place of the one
// before.
func saveResult(db *sql.DB, r Result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO lan_scans (network, result, finished_at) VALUES (?, ?, ?)
		ON CONFLICT(network) DO UPDATE SET result = excluded.result, finished_at = excluded.finished_at`,
		r.Network, string(data), r.FinishedAt)
	return err
}

// loadResults is every stored result, by network. A row that does not read
// as a Result is left out (and logged) rather than failing the others.
func loadResults(db *sql.DB) (map[string]Result, error) {
	rows, err := db.Query(`SELECT network, result FROM lan_scans`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Result{}
	for rows.Next() {
		var network, raw string
		if err := rows.Scan(&network, &raw); err != nil {
			return nil, err
		}
		var r Result
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			slog.Warn("stored LAN scan unreadable", "component", "lanscan", "network", network, "error", err)
			continue
		}
		if r.Hosts == nil {
			r.Hosts = []Host{}
		}
		out[network] = r
	}
	return out, rows.Err()
}
