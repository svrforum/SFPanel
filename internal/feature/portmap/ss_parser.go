package portmap

import (
	"regexp"
	"strconv"
	"strings"
)

// ssAddrRe extracts port from "*:8444" or "[::]:8444" or "127.0.0.1:80".
var ssAddrRe = regexp.MustCompile(`:(\d+)$`)

// ssUserRe extracts pid and process name from `users:(("sfpanel",pid=1410507,fd=10))`.
// The tuple can repeat for multi-process listeners.
var ssUserRe = regexp.MustCompile(`\("([^"]+)",pid=(\d+),fd=\d+\)`)

// ParseSs parses output of `ss -tlnp -H` (or -ulnp). proto is "tcp" or "udp".
// Returns one SsEntry per (port, listener-process) tuple.
func ParseSs(out, proto string) []SsEntry {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	entries := []SsEntry{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// `ss -H` columns: State Recv-Q Send-Q Local-Address:Port Peer-Address:Port [users:(...)]
		// Local-Address:Port is usually field index 3, but the column layout can
		// shift; fall back to field 4 when field 3 has no ':' (mirrors
		// firewall_ufw.go's parseSSOutput).
		localAddr := fields[3]
		if len(fields) > 4 && !strings.Contains(fields[3], ":") {
			localAddr = fields[4]
		}
		m := ssAddrRe.FindStringSubmatch(localAddr)
		if m == nil {
			continue
		}
		port, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// Look for users:(...) somewhere in the line (may not exist if not root).
		users := ssUserRe.FindAllStringSubmatch(line, -1)
		if len(users) == 0 {
			entries = append(entries, SsEntry{Port: port, Proto: proto})
			continue
		}
		for _, u := range users {
			pid, _ := strconv.Atoi(u[2])
			entries = append(entries, SsEntry{
				Port:  port,
				Proto: proto,
				PID:   pid,
				Name:  u[1],
			})
		}
	}
	return entries
}
