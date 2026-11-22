package lanscan

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
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
	// Ports says whether this scan looked for ports, or only found hosts.
	Ports bool `json:"ports"`
	// PortsAt is when ports were last looked for, RFC 3339 UTC; "" when
	// they never were. A port scan sets it to when it finished; a host scan
	// carries the earlier result's on, with each host's ports (carryPorts).
	PortsAt string `json:"ports_at"`
	// StartedAt and FinishedAt are RFC 3339, UTC.
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Hosts      []Host `json:"hosts"`
}

// execer is what a write needs: the database itself, or a transaction on it.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// querier is what a read needs: the database itself, or a transaction on it.
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// stamp is how this package writes a time: RFC 3339, UTC, to the second.
// Every time it writes and compares in SQL is in this form, which is of
// fixed width and so sorts as text.
func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// saveResult keeps r as its network's last result, in place of the one
// before.
func saveResult(db execer, r Result) error {
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
		r, ok := readResult(network, raw)
		if !ok {
			continue
		}
		out[network] = r
	}
	return out, rows.Err()
}

// loadResult is the stored result of one network; ok is false when there is
// none, or the row does not read as a Result.
func loadResult(db *sql.DB, network string) (Result, bool, error) {
	var raw string
	err := db.QueryRow(`SELECT result FROM lan_scans WHERE network = ?`, network).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	r, ok := readResult(network, raw)
	return r, ok, nil
}

// readResult reads a stored row. A result stored before ports_at existed
// that is a port scan looked for ports when it finished.
func readResult(network, raw string) (Result, bool) {
	var r Result
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		slog.Warn("stored LAN scan unreadable", "component", "lanscan", "network", network, "error", err)
		return Result{}, false
	}
	if r.Hosts == nil {
		r.Hosts = []Host{}
	}
	if r.PortsAt == "" && r.Ports {
		r.PortsAt = r.FinishedAt
	}
	return r, true
}

// carryPorts gives the hosts of a scan that did not look for ports what the
// earlier result knew of theirs: the ports and timed_out of the same device,
// found by MAC, or by IP where either side has no MAC. A host at an address
// another MAC held before is another device and gets nothing.
func carryPorts(hosts []Host, prev Result) {
	byMAC := map[string]Host{}
	byIP := map[string]Host{}
	for _, h := range prev.Hosts {
		if h.MAC != "" {
			byMAC[h.MAC] = h
		}
		byIP[h.IP] = h
	}
	for i := range hosts {
		before, ok := Host{}, false
		if hosts[i].MAC != "" {
			before, ok = byMAC[hosts[i].MAC]
		}
		if !ok {
			if h, found := byIP[hosts[i].IP]; found && (h.MAC == "" || hosts[i].MAC == "") {
				before, ok = h, true
			}
		}
		if !ok {
			continue
		}
		hosts[i].TimedOut = before.TimedOut
		hosts[i].Ports = append([]Port{}, before.Ports...)
	}
}
