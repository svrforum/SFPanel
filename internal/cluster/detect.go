package cluster

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/paneltls"
)

// DetectAdvertiseAddress determines the best local IP to advertise to the cluster,
// using the leader's address as a routing hint.
//
// Strategy:
//  1. If leader is on a Tailscale IP (100.64.0.0/10), find local Tailscale IP.
//  2. Find a local IP on the same subnet as the leader.
//  3. Dial the leader and use the local side of the TCP connection.
//  4. Return error (no silent 127.0.0.1 fallback).
func DetectAdvertiseAddress(leaderAddr string) (string, error) {
	leaderHost, _, err := net.SplitHostPort(leaderAddr)
	if err != nil {
		leaderHost = leaderAddr
	}

	leaderIP := net.ParseIP(leaderHost)

	// Strategy 1: Tailscale
	if leaderIP != nil && IsTailscaleIP(leaderIP) {
		if tsIP, ok := findLocalTailscaleIP(); ok {
			return tsIP, nil
		}
	}

	// Strategy 2: Same subnet
	if leaderIP != nil {
		if localIP, ok := findSameSubnetIP(leaderIP); ok {
			return localIP, nil
		}
	}

	// Strategy 3: Dial the leader to discover local IP
	conn, err := net.DialTimeout("tcp", leaderAddr, 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("cannot reach leader at %s to detect local IP: %w", leaderAddr, err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.TCPAddr)
	return localAddr.IP.String(), nil
}

// DetectFallbackIP returns the first non-loopback IPv4 address.
// Used only during cluster init when no leader address is available.
func DetectFallbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}

// IsTailscaleIP returns true if the IP is in the Tailscale CGNAT range (100.64.0.0/10).
func IsTailscaleIP(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	// 100.64.0.0/10 = first byte 100, second byte 64-127
	return ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}

// IfaceAddr is one address on one local interface, with what of the
// interface decides whether others can reach this host at it.
type IfaceAddr struct {
	Name         string
	IP           net.IP
	Up, Loopback bool
}

// TailscaleAddresses returns, sorted and once each, the IPv4 addresses a
// tailnet gives this host that others can reach it at: any address in the
// Tailscale range (100.64.0.0/10) — Tailscale's interface is not always
// tailscale0 — and any address on an interface named tailscale*, which a
// self-hosted control server can number from another range. Only on an
// interface that is up, not loopback, and not one Docker creates and
// destroys with a stack (paneltls.EphemeralInterface, the rule the panel
// certificate's addresses follow). IPv6 is left out.
func TailscaleAddresses(ifaces []IfaceAddr) []string {
	var out []string
	for _, a := range ifaces {
		if !a.Up || a.Loopback || paneltls.EphemeralInterface(a.Name) {
			continue
		}
		if ip4 := a.IP.To4(); ip4 != nil && (IsTailscaleIP(ip4) || strings.HasPrefix(a.Name, "tailscale")) {
			out = append(out, ip4.String())
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		return slices.Compare(net.ParseIP(a).To4(), net.ParseIP(b).To4())
	})
	return slices.Compact(out)
}

// LocalTailscaleAddresses is TailscaleAddresses over this host's interfaces,
// for the CLI, which runs no Manager.
func LocalTailscaleAddresses() ([]string, error) {
	ifaces, err := localIfaceAddrs()
	return TailscaleAddresses(ifaces), err
}

// localIfaceAddrs lists every address on this host's interfaces.
func localIfaceAddrs() ([]IfaceAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []IfaceAddr
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			out = append(out, IfaceAddr{
				Name:     iface.Name,
				IP:       ipnet.IP,
				Up:       iface.Flags&net.FlagUp != 0,
				Loopback: iface.Flags&net.FlagLoopback != 0,
			})
		}
	}
	return out, nil
}

// findLocalTailscaleIP returns the first local IPv4 address in the Tailscale CGNAT range.
func findLocalTailscaleIP() (string, bool) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", false
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip != nil && IsTailscaleIP(ip) {
			return ip.String(), true
		}
	}
	return "", false
}

// findSameSubnetIP finds a local IP on the same subnet as the target IP.
func findSameSubnetIP(targetIP net.IP) (string, bool) {
	targetIP = targetIP.To4()
	if targetIP == nil {
		return "", false
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			// Check if both IPs are in this interface's subnet
			if ipnet.Contains(targetIP) {
				return ip.String(), true
			}
		}
	}
	return "", false
}
