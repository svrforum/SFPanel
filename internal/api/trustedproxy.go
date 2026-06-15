package api

import (
	"net"
	"strings"
)

// parseCIDROrIP turns a single IP ("10.0.0.5") or CIDR ("10.0.0.0/8") string
// into a *net.IPNet. Bare IPs are widened to a /32 (IPv4) or /128 (IPv6)
// network so the trusted-proxy match logic can use a single Contains check.
func parseCIDROrIP(s string) (string, *net.IPNet, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		_, ipnet, err := net.ParseCIDR(s)
		return s, ipnet, err
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return s, nil, &net.ParseError{Type: "IP address", Text: s}
	}
	mask := net.CIDRMask(32, 32)
	if ip.To4() == nil {
		mask = net.CIDRMask(128, 128)
	}
	return s, &net.IPNet{IP: ip, Mask: mask}, nil
}
