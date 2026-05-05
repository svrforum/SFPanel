package portmap

import (
	"sort"
)

// Aggregate merges UFW firewall info, Docker port bindings, and listener
// entries into one sorted []PortMapRow keyed by port.
//
// ufwByPort: map from port number → firewall info (caller pre-indexes
// because UFW rules can match multiple ports via ranges; the caller is
// responsible for expansion).
//
// dnat: Docker port bindings (HostPort).
//
// ss: listener entries from `ParseSs`. Multiple entries on same port
// collapse into one row (first-seen wins for Process; in practice
// docker-proxy always sorts before user processes, which is the
// preferred display).
func Aggregate(ufwByPort map[int]FirewallInfo, dnat []PortBinding, ss []SsEntry) []PortMapRow {
	rows := map[portKey]*PortMapRow{}

	// Listeners → "listening" state.
	for _, e := range ss {
		k := portKey{Port: e.Port, Proto: e.Proto}
		if _, ok := rows[k]; !ok {
			rows[k] = &PortMapRow{Port: e.Port, Proto: e.Proto, State: "listening"}
		}
		row := rows[k]
		if row.Process == nil {
			row.Process = &ProcessInfo{PID: e.PID, Name: e.Name}
		}
	}

	// Docker DNAT → at least "bound" (overrides to "listening" if ss already
	// flagged it — DNAT containers always have docker-proxy listening).
	for _, b := range dnat {
		// Docker bindings are tcp by default; we treat all as tcp here. UDP
		// bindings are rare and surface via ss.
		k := portKey{Port: b.HostPort, Proto: "tcp"}
		if _, ok := rows[k]; !ok {
			rows[k] = &PortMapRow{Port: b.HostPort, Proto: "tcp", State: "bound"}
		}
		row := rows[k]
		row.Container = &ContainerInfo{
			ID:    b.ContainerID,
			Name:  b.ContainerName,
			Stack: b.Stack,
		}
	}

	// UFW → attach to whichever row uses this port. UFW rules without a
	// matching listener / DNAT are not surfaced (no point showing an
	// allow rule for a port nothing uses).
	for port, info := range ufwByPort {
		copyInfo := info
		// Apply to both tcp and udp variants if present.
		for _, proto := range []string{"tcp", "udp"} {
			k := portKey{Port: port, Proto: proto}
			if row, ok := rows[k]; ok {
				row.Firewall = &copyInfo
			}
		}
	}

	out := make([]PortMapRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

type portKey struct {
	Port  int
	Proto string
}
