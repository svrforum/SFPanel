package lanscan

import (
	"database/sql"
	"time"
)

// ScanEvent is what a successful scan, once stored, tells Handler.Notify:
// what the alert rules for a LAN look at.
type ScanEvent struct {
	Network    string
	FinishedAt time.Time
	// Scheduled is a scan the schedule started, not the operator.
	Scheduled bool
	// New are the MACs this scan put in the registry for the first time,
	// in address order. Empty when the registry held no device of this
	// network before the scan: a network's first scan is not news.
	New []SeenDevice
	// Down are the watched devices of this network whose missed became
	// exactly 2 with this scan: the second scan in a row that did not find
	// them, and only that one.
	Down []KnownDevice
	// Back are the watched devices this scan found whose missed was 2 or
	// more before it.
	Back []KnownDevice
	// Conflicts are the certain ones first, then the suspected ones.
	Conflicts []AddressConflict
}

// SeenDevice is a device as this scan found it.
type SeenDevice struct{ IP, MAC, Vendor, Hostname string }

// KnownDevice is a registry device: Name is its label's, and IP, Vendor,
// Hostname and LastSeen are as the registry holds them after the scan.
type KnownDevice struct{ MAC, Name, IP, Vendor, Hostname, LastSeen string }

// AddressConflict is an address of the network two devices claim.
type AddressConflict struct {
	IP string
	// MACs is, for a certain conflict, the one MAC that answered; for a
	// suspected one, the MACs that overlapped, sorted.
	MACs []string
	// Vendor is, for a certain conflict, the answering host's vendor; ""
	// for a suspected one.
	Vendor string
	// Certain is a device answering at an address the schedule's known
	// list gives one of the cluster's own nodes; a suspected one is two MACs
	// in lan_claims holding the address over the same period.
	Certain bool
}

// addressConflicts are the conflicts of network once a scan that found
// hosts is stored. Certain ones first: a host answering at an address the
// network's schedule names for one of the cluster's nodes, with a MAC that
// is none of that node's — the rule a schedule pauses on (knownConflicts).
// Then the suspected ones lan_claims holds for the network (loadConflicts),
// leaving out an address already listed as certain.
func addressConflicts(db *sql.DB, network string, hosts []Host) ([]AddressConflict, error) {
	out := []AddressConflict{}
	certain := map[string]bool{}
	row, ok, err := loadSchedule(db, network)
	if err != nil {
		return nil, err
	}
	if ok {
		for _, h := range knownConflicts(row, hosts) {
			if certain[h.IP] {
				continue
			}
			certain[h.IP] = true
			out = append(out, AddressConflict{IP: h.IP, MACs: []string{h.MAC}, Vendor: h.Vendor, Certain: true})
		}
	}
	suspected, err := loadConflicts(db)
	if err != nil {
		return nil, err
	}
	for _, c := range suspected {
		if c.Network != network || certain[c.IP] {
			continue
		}
		out = append(out, AddressConflict{IP: c.IP, MACs: c.MACs})
	}
	return out, nil
}
