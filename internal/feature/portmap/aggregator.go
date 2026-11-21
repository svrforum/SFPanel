package portmap

import (
	"sort"
)

// Aggregate merges UFW firewall info, Docker port bindings, and listener
// entries into one sorted []PortMapRow keyed by (port, proto).
//
// ufwByPort: map from port number → firewall info (caller pre-indexes
// because UFW rules can match multiple ports via ranges; the caller is
// responsible for expansion).
//
// dnat: Docker port bindings, each carrying its own protocol.
//
// ss: listener entries from `ParseSs`. Multiple entries on same port
// collapse into one row (first-seen wins for Process; in practice
// docker-proxy always sorts before user processes, which is the
// preferred display).
//
// A row's Addresses gather every listener's address and every binding's
// host IP for that port and protocol.
func Aggregate(ufwByPort map[int]FirewallInfo, dnat []PortBinding, ss []SsEntry) []PortMapRow {
	rows := map[portKey]*PortMapRow{}
	addrs := map[portKey]map[string]bool{}
	addAddr := func(k portKey, addr string) {
		if addr == "" {
			return
		}
		if addrs[k] == nil {
			addrs[k] = map[string]bool{}
		}
		addrs[k][addr] = true
	}

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
		addAddr(k, e.Addr)
	}

	// Docker DNAT → at least "bound" (overrides to "listening" if ss already
	// flagged it — DNAT containers always have docker-proxy listening).
	// Bindings carry their own proto: UDP services (DNS, WireGuard) no
	// longer pun as TCP. Multiple containers on the same host port are
	// appended rather than last-write-wins; two replicas of the same
	// stack and two unrelated stacks colliding on a port both surface.
	for _, b := range dnat {
		proto := b.Proto
		if proto == "" {
			proto = "tcp"
		}
		k := portKey{Port: b.HostPort, Proto: proto}
		if _, ok := rows[k]; !ok {
			rows[k] = &PortMapRow{Port: b.HostPort, Proto: proto, State: "bound"}
		}
		row := rows[k]
		row.Containers = append(row.Containers, ContainerInfo{
			ID:    b.ContainerID,
			Name:  b.ContainerName,
			Stack: b.Stack,
		})
		addAddr(k, b.HostIP)
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
	for k, r := range rows {
		r.Addresses = make([]string, 0, len(addrs[k]))
		for addr := range addrs[k] {
			r.Addresses = append(r.Addresses, addr)
		}
		sort.Strings(r.Addresses)
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
