package lanscan

import (
	"database/sql"
	"errors"
	"net/netip"
	"sort"
	"time"
)

const (
	// claimGap is how long a MAC may go unseen at an address and still be
	// holding it when it is seen there again; after a longer gap its claim
	// starts over.
	claimGap = 24 * time.Hour
	// claimKeep is how long a claim is kept after it was last seen.
	claimKeep = 48 * time.Hour
	// unlabelledKeep is how long a device nobody labelled is kept after it
	// was last seen, so that phones with a random MAC for every network
	// they join do not pile up. A labelled one stays until it is deleted.
	unlabelledKeep = 14 * 24 * time.Hour
)

// Device is one MAC in this node's registry: the label the operator wrote
// and the device as this node's scans last saw it. A MAC labelled here but
// never seen by this node (another node saw it) has the sighting fields "".
type Device struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
	Note string `json:"note"`
	Kind string `json:"kind"`
	// LabelledAt is when the label was last written, RFC 3339; "" when it
	// never was.
	LabelledAt string `json:"labelled_at"`
	// Acked is the operator having seen the device as new.
	Acked bool `json:"acked"`
	// Watch is the operator watching the device: the lan_device_down alert
	// rule reports it missing and back. Written with the label.
	Watch    bool   `json:"watch"`
	Network  string `json:"network"`
	IP       string `json:"ip"`
	Vendor   string `json:"vendor"`
	Hostname string `json:"hostname"`
	// FirstSeen and LastSeen are RFC 3339 UTC.
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

// Conflict is an address two or more MACs held over overlapping periods.
type Conflict struct {
	Network string `json:"network"`
	IP      string `json:"ip"`
	// MACs are those that overlap another, sorted.
	MACs []string `json:"macs"`
}

// registryNews is what a stored scan changed in the registry that the
// alerts are told (ScanEvent's New, Down and Back).
type registryNews struct {
	New  []SeenDevice
	Down []KnownDevice
	Back []KnownDevice
}

// recordScan stores a successful scan of r.Network finished at at, all at
// once: the result, each host with a MAC into the registry and the claims,
// how many scans in a row missed each device of the network, and what has
// aged out of the registry and the claims. It answers what that changed for
// the alerts.
func recordScan(db *sql.DB, r Result, at time.Time) (registryNews, error) {
	tx, err := db.Begin()
	if err != nil {
		return registryNews{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveResult(tx, r); err != nil {
		return registryNews{}, err
	}
	before, err := loadPriors(tx)
	if err != nil {
		return registryNews{}, err
	}
	// Every device of the network has been missed once more; the sightings
	// below set those the scan found back to 0.
	if _, err := tx.Exec(`UPDATE lan_devices SET missed = missed + 1 WHERE network = ?`, r.Network); err != nil {
		return registryNews{}, err
	}
	if err := recordSightings(tx, r.Network, r.Hosts, at); err != nil {
		return registryNews{}, err
	}
	news, err := readNews(tx, newsOf(r.Network, r.Hosts, before))
	if err != nil {
		return registryNews{}, err
	}
	if err := pruneRegistry(tx, at); err != nil {
		return registryNews{}, err
	}
	return news, tx.Commit()
}

// prior is a registry device as it was before a scan was recorded.
type prior struct {
	network string
	watch   bool
	missed  int
}

// loadPriors is the whole registry as it is, by MAC: what a scan's news is
// judged against.
func loadPriors(tx *sql.Tx) (map[string]prior, error) {
	rows, err := tx.Query(`SELECT mac, network, watch, missed FROM lan_devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]prior{}
	for rows.Next() {
		var mac string
		var p prior
		if err := rows.Scan(&mac, &p.network, &p.watch, &p.missed); err != nil {
			return nil, err
		}
		out[mac] = p
	}
	return out, rows.Err()
}

// newsMACs are the MACs of a scan's news, before their devices are read.
type newsMACs struct{ new, down, back []string }

// newsOf works out, from the registry as it was before a scan of network
// that found hosts, which MACs are news:
//   - new: a MAC the registry had no row for, unless the registry held no
//     device of the network at all: a network's first scan finds everything
//     on it, and that is not news.
//   - down: a watched device of the network the scan did not find, that the
//     scan before it did not find either (missed was 1, and is now 2). Once:
//     the next scan that misses it makes 3.
//   - back: a watched device the scan found that two or more scans in a row
//     had missed, on whichever network they were.
func newsOf(network string, hosts []Host, before map[string]prior) newsMACs {
	found := map[string]bool{}
	var order []string
	for _, h := range hosts {
		if h.MAC != "" && !found[h.MAC] {
			found[h.MAC] = true
			order = append(order, h.MAC)
		}
	}
	known := false
	for _, p := range before {
		if p.network == network {
			known = true
			break
		}
	}
	var out newsMACs
	for _, mac := range order {
		p, ok := before[mac]
		switch {
		case !ok:
			if known {
				out.new = append(out.new, mac)
			}
		case p.watch && p.missed >= 2:
			out.back = append(out.back, mac)
		}
	}
	for mac, p := range before {
		if p.network == network && !found[mac] && p.watch && p.missed+1 == 2 {
			out.down = append(out.down, mac)
		}
	}
	return out
}

// readNews reads the devices of a scan's news as the scan left them, each
// list in address order.
func readNews(tx *sql.Tx, macs newsMACs) (registryNews, error) {
	read := func(list []string) ([]Device, error) {
		out := make([]Device, 0, len(list))
		for _, mac := range list {
			d, err := scanDevice(tx.QueryRow(`SELECT `+deviceColumns+` FROM lan_devices WHERE mac = ?`, mac))
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].IP != out[j].IP {
				return addrLess(out[i].IP, out[j].IP)
			}
			return out[i].MAC < out[j].MAC
		})
		return out, nil
	}
	var news registryNews
	added, err := read(macs.new)
	if err != nil {
		return registryNews{}, err
	}
	for _, d := range added {
		news.New = append(news.New, SeenDevice{IP: d.IP, MAC: d.MAC, Vendor: d.Vendor, Hostname: d.Hostname})
	}
	down, err := read(macs.down)
	if err != nil {
		return registryNews{}, err
	}
	for _, d := range down {
		news.Down = append(news.Down, knownDevice(d))
	}
	back, err := read(macs.back)
	if err != nil {
		return registryNews{}, err
	}
	for _, d := range back {
		news.Back = append(news.Back, knownDevice(d))
	}
	return news, nil
}

func knownDevice(d Device) KnownDevice {
	return KnownDevice{MAC: d.MAC, Name: d.Name, IP: d.IP, Vendor: d.Vendor, Hostname: d.Hostname, LastSeen: d.LastSeen}
}

// recordSightings upserts every host that has a MAC: the device as seen now
// (a vendor or hostname only replaced by one that is not "", first_seen set
// once, missed back to 0) and its claim on the address. A claim seen again within claimGap of
// its last_at runs on; one that is new or comes back after a longer gap
// starts at now.
func recordSightings(tx execer, network string, hosts []Host, at time.Time) error {
	now := stamp(at)
	gapStart := stamp(at.Add(-claimGap))
	for _, h := range hosts {
		if h.MAC == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO lan_devices (mac, network, ip, vendor, hostname, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(mac) DO UPDATE SET
				network = excluded.network,
				ip = excluded.ip,
				vendor = CASE WHEN excluded.vendor <> '' THEN excluded.vendor ELSE lan_devices.vendor END,
				hostname = CASE WHEN excluded.hostname <> '' THEN excluded.hostname ELSE lan_devices.hostname END,
				first_seen = CASE WHEN lan_devices.first_seen = '' THEN excluded.first_seen ELSE lan_devices.first_seen END,
				last_seen = excluded.last_seen,
				missed = 0`,
			h.MAC, network, h.IP, h.Vendor, h.Hostname, now, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lan_claims (network, ip, mac, first_at, last_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(network, ip, mac) DO UPDATE SET
				first_at = CASE WHEN lan_claims.last_at >= ? THEN lan_claims.first_at ELSE excluded.first_at END,
				last_at = excluded.last_at`,
			network, h.IP, h.MAC, now, now, gapStart); err != nil {
			return err
		}
	}
	return nil
}

// pruneRegistry drops the claims last seen more than claimKeep before at,
// and the devices nobody labelled last seen more than unlabelledKeep before.
func pruneRegistry(tx execer, at time.Time) error {
	if _, err := tx.Exec(`DELETE FROM lan_claims WHERE last_at < ?`, stamp(at.Add(-claimKeep))); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM lan_devices WHERE labelled_at = '' AND last_seen < ?`, stamp(at.Add(-unlabelledKeep)))
	return err
}

const deviceColumns = `mac, name, note, kind, labelled_at, acked, watch, network, ip, vendor, hostname, first_seen, last_seen`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDevice(row rowScanner) (Device, error) {
	var d Device
	err := row.Scan(&d.MAC, &d.Name, &d.Note, &d.Kind, &d.LabelledAt, &d.Acked, &d.Watch,
		&d.Network, &d.IP, &d.Vendor, &d.Hostname, &d.FirstSeen, &d.LastSeen)
	return d, err
}

// loadDevices is the whole registry, by MAC. Never nil.
func loadDevices(db *sql.DB) ([]Device, error) {
	rows, err := db.Query(`SELECT ` + deviceColumns + ` FROM lan_devices ORDER BY mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// getDevice is one device; ok is false when the registry has no such MAC.
func getDevice(db *sql.DB, mac string) (Device, bool, error) {
	d, err := scanDevice(db.QueryRow(`SELECT `+deviceColumns+` FROM lan_devices WHERE mac = ?`, mac))
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, err
	}
	return d, true, nil
}

// labelDevice writes a device's label, and its watch flag when watch is not
// nil, creating the row for a MAC this node has never seen, unless the stored
// label is newer than labelledAt: then it changes nothing. Either way it
// returns the device as stored.
func labelDevice(db *sql.DB, mac, name, note, kind, labelledAt string, watch *bool) (Device, error) {
	tx, err := db.Begin()
	if err != nil {
		return Device{}, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := scanDevice(tx.QueryRow(`SELECT `+deviceColumns+` FROM lan_devices WHERE mac = ?`, mac))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Device{}, err
	case newer(stored.LabelledAt, labelledAt):
		return stored, nil
	}
	// NULL, for no watch given, leaves the stored flag as it is.
	var w any
	if watch != nil {
		w = *watch
	}
	if _, err := tx.Exec(`INSERT INTO lan_devices (mac, name, note, kind, labelled_at, watch) VALUES (?, ?, ?, ?, ?, COALESCE(?, 0))
		ON CONFLICT(mac) DO UPDATE SET name = excluded.name, note = excluded.note, kind = excluded.kind,
			labelled_at = excluded.labelled_at, watch = COALESCE(?, lan_devices.watch)`,
		mac, name, note, kind, labelledAt, w, w); err != nil {
		return Device{}, err
	}
	d, err := scanDevice(tx.QueryRow(`SELECT `+deviceColumns+` FROM lan_devices WHERE mac = ?`, mac))
	if err != nil {
		return Device{}, err
	}
	return d, tx.Commit()
}

// newer says whether the label time a is later than b. A time that does not
// parse is no time at all, and never newer.
func newer(a, b string) bool {
	ta, err := time.Parse(time.RFC3339, a)
	if err != nil {
		return false
	}
	tb, err := time.Parse(time.RFC3339, b)
	if err != nil {
		return true
	}
	return ta.After(tb)
}

// ackDevice marks a device as seen by the operator; found is false when the
// registry has no such MAC.
func ackDevice(db *sql.DB, mac string) (bool, error) {
	res, err := db.Exec(`UPDATE lan_devices SET acked = 1 WHERE mac = ?`, mac)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// deleteDevice forgets a device and the addresses it held; deleted is false
// when the registry had no such MAC.
func deleteDevice(db *sql.DB, mac string) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM lan_devices WHERE mac = ?`, mac)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM lan_claims WHERE mac = ?`, mac); err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// loadConflicts is every address two or more MACs held over overlapping
// periods: A's [first_at, last_at] and B's meet. An address handed from one
// device to another (DHCP) has periods that do not, and is no conflict. By
// network, then address; never nil.
func loadConflicts(db *sql.DB) ([]Conflict, error) {
	rows, err := db.Query(`SELECT a.network, a.ip, a.mac, b.mac FROM lan_claims a JOIN lan_claims b
		ON a.network = b.network AND a.ip = b.ip AND a.mac < b.mac
		AND a.first_at <= b.last_at AND b.first_at <= a.last_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ network, ip string }
	macs := map[key]map[string]bool{}
	for rows.Next() {
		var k key
		var a, b string
		if err := rows.Scan(&k.network, &k.ip, &a, &b); err != nil {
			return nil, err
		}
		if macs[k] == nil {
			macs[k] = map[string]bool{}
		}
		macs[k][a] = true
		macs[k][b] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Conflict, 0, len(macs))
	for k, set := range macs {
		c := Conflict{Network: k.network, IP: k.ip, MACs: make([]string, 0, len(set))}
		for m := range set {
			c.MACs = append(c.MACs, m)
		}
		sort.Strings(c.MACs)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Network != out[j].Network {
			return networkLess(out[i].Network, out[j].Network)
		}
		return addrLess(out[i].IP, out[j].IP)
	})
	return out, nil
}

// networkLess orders networks by address, then prefix length; one that does
// not parse sorts by its text after those that do.
func networkLess(a, b string) bool {
	pa, errA := netip.ParsePrefix(a)
	pb, errB := netip.ParsePrefix(b)
	switch {
	case errA == nil && errB == nil:
		if c := pa.Addr().Compare(pb.Addr()); c != 0 {
			return c < 0
		}
		return pa.Bits() < pb.Bits()
	case errA == nil:
		return true
	case errB == nil:
		return false
	}
	return a < b
}

// addrLess orders addresses numerically; one that does not parse sorts by
// its text after those that do.
func addrLess(a, b string) bool {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	switch {
	case errA == nil && errB == nil:
		return pa.Less(pb)
	case errA == nil:
		return true
	case errB == nil:
		return false
	}
	return a < b
}
