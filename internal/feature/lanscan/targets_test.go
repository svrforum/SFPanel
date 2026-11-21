package lanscan

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

const lanFlags = net.FlagUp | net.FlagBroadcast | net.FlagMulticast | net.FlagRunning

func mac(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	hw, err := net.ParseMAC(s)
	require.NoError(t, err)
	return hw
}

func prefixes(t *testing.T, list ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

// The LAN interface every case starts from: eth0 on 10.20.30.26/24.
func eth0(t *testing.T) Interface {
	return Interface{Name: "eth0", Flags: lanFlags, MAC: mac(t, "02:42:AC:14:1E:1A"), Addrs: prefixes(t, "10.20.30.26/24")}
}

const eth0Route = "default via 10.20.30.1 dev eth0 proto dhcp src 10.20.30.26 metric 100\n"

// One case per rule. Each adds an interface (or changes eth0) and says what
// the list must then be; eth0's own entry stays first throughout.
func TestNetworks_Rules(t *testing.T) {
	lan := Network{CIDR: "10.20.30.0/24", Interface: "eth0", Address: "10.20.30.26",
		MAC: "02:42:ac:14:1e:1a", Gateway: "10.20.30.1", Scannable: true}

	cases := []struct {
		name   string
		ifaces func(t *testing.T) []Interface
		routes string
		want   []Network
	}{
		{"the LAN interface is listed, MAC lower case, with its gateway",
			func(t *testing.T) []Interface { return []Interface{eth0(t)} },
			eth0Route, []Network{lan}},
		{"docker0 is left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "docker0", Flags: lanFlags, MAC: mac(t, "02:42:ac:11:00:01"), Addrs: prefixes(t, "172.17.0.1/16")}}
			}, eth0Route, []Network{lan}},
		{"a compose network's br- bridge is left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "br-3f2a9c1d7e44", Flags: lanFlags, MAC: mac(t, "02:42:ac:12:00:01"), Addrs: prefixes(t, "172.18.0.1/24")}}
			}, eth0Route, []Network{lan}},
		{"a veth is left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "veth1a2b3c4", Flags: lanFlags, MAC: mac(t, "02:42:ac:12:00:02"), Addrs: prefixes(t, "10.99.0.1/24")}}
			}, eth0Route, []Network{lan}},
		{"virbr, lxcbr and lxdbr are left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t),
					{Name: "virbr0", Flags: lanFlags, MAC: mac(t, "02:42:ac:13:00:01"), Addrs: prefixes(t, "10.122.0.1/24")},
					{Name: "lxcbr0", Flags: lanFlags, MAC: mac(t, "02:42:ac:13:00:02"), Addrs: prefixes(t, "10.0.3.1/24")},
					{Name: "lxdbr0", Flags: lanFlags, MAC: mac(t, "02:42:ac:13:00:03"), Addrs: prefixes(t, "10.10.10.1/24")}}
			}, eth0Route, []Network{lan}},
		{"a public address is left out",
			func(t *testing.T) []Interface {
				i := eth0(t)
				i.Addrs = append(i.Addrs, prefixes(t, "198.51.100.7/24")...)
				return []Interface{i}
			}, eth0Route, []Network{lan}},
		{"a link-local address is left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "169.254.12.34/16")}}
			}, eth0Route, []Network{lan}},
		{"IPv6 is left out",
			func(t *testing.T) []Interface {
				i := eth0(t)
				i.Addrs = append(i.Addrs, prefixes(t, "fd00:1::26/64", "fe80::1/64")...)
				return []Interface{i}
			}, eth0Route, []Network{lan}},
		{"an interface without a MAC is left out (Tailscale, WireGuard)",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "wg0", Flags: net.FlagUp | net.FlagBroadcast, Addrs: prefixes(t, "10.8.0.1/24")}}
			}, eth0Route, []Network{lan}},
		{"an interface that does not broadcast is left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "tun0", Flags: net.FlagUp | net.FlagPointToPoint, MAC: mac(t, "02:42:ac:14:1e:1c"), Addrs: prefixes(t, "10.9.0.1/24")}}
			}, eth0Route, []Network{lan}},
		{"loopback is left out",
			func(t *testing.T) []Interface {
				return []Interface{{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, Addrs: prefixes(t, "127.0.0.1/8")}, eth0(t)}
			}, eth0Route, []Network{lan}},
		{"an interface that is down is left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "eth1", Flags: net.FlagBroadcast, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "10.40.0.5/24")}}
			}, eth0Route, []Network{lan}},
		{"a /16 is listed as too large",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "172.20.5.9/16")}}
			}, eth0Route, []Network{lan,
				{CIDR: "172.20.0.0/16", Interface: "eth1", Address: "172.20.5.9", MAC: "02:42:ac:14:1e:1b", Scannable: false, Reason: "too_large"}}},
		{"a /20 and a /30 are scannable, a /19 is not",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "10.50.17.9/20", "10.60.0.5/30", "10.70.33.9/19")}}
			}, eth0Route, []Network{lan,
				{CIDR: "10.50.16.0/20", Interface: "eth1", Address: "10.50.17.9", MAC: "02:42:ac:14:1e:1b", Scannable: true},
				{CIDR: "10.60.0.4/30", Interface: "eth1", Address: "10.60.0.5", MAC: "02:42:ac:14:1e:1b", Scannable: true},
				{CIDR: "10.70.32.0/19", Interface: "eth1", Address: "10.70.33.9", MAC: "02:42:ac:14:1e:1b", Scannable: false, Reason: "too_large"}}},
		{"a /31 and a /32 are left out",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "10.60.0.0/31", "10.61.0.1/32")}}
			}, eth0Route, []Network{lan}},
		{"Proxmox VE's vmbr0 is the LAN and is kept",
			func(t *testing.T) []Interface {
				return []Interface{{Name: "vmbr0", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:0a"), Addrs: prefixes(t, "10.20.30.10/24")}}
			}, "default via 10.20.30.1 dev vmbr0 proto kernel onlink\n",
			[]Network{{CIDR: "10.20.30.0/24", Interface: "vmbr0", Address: "10.20.30.10", MAC: "02:42:ac:14:1e:0a", Gateway: "10.20.30.1", Scannable: true}}},
		{"the gateway comes from that interface's default route only",
			func(t *testing.T) []Interface {
				return []Interface{eth0(t), {Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "172.16.9.5/24")}}
			}, "default via 172.16.9.1 dev eth0 metric 50\ndefault via 10.20.30.1 dev eth0 metric 100\n",
			// eth0's first default route points outside 10.20.30.0/24, so
			// its second is the gateway; eth1 has no default route of its
			// own and gets none, even though 172.16.9.1 is in its network.
			[]Network{lan, {CIDR: "172.16.9.0/24", Interface: "eth1", Address: "172.16.9.5", MAC: "02:42:ac:14:1e:1b", Scannable: true}}},
		{"no default route, no gateway",
			func(t *testing.T) []Interface { return []Interface{eth0(t)} },
			"default dev wg0 scope link\n",
			[]Network{{CIDR: "10.20.30.0/24", Interface: "eth0", Address: "10.20.30.26", MAC: "02:42:ac:14:1e:1a", Scannable: true}}},
		{"a multipath default route's nexthops count per interface",
			func(t *testing.T) []Interface { return []Interface{eth0(t)} },
			"default proto static metric 1024\n\tnexthop via 10.20.30.1 dev eth0 weight 1\n\tnexthop via 10.20.31.1 dev eth1 weight 1\n",
			[]Network{lan}},
		{"one network on two interfaces is listed once, for the first",
			func(t *testing.T) []Interface {
				i := eth0(t)
				i.Addrs = append(i.Addrs, prefixes(t, "10.20.30.27/24")...)
				return []Interface{i, {Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "10.20.30.28/24")}}
			}, eth0Route, []Network{lan}},
		{"nothing to list is [], not null",
			func(t *testing.T) []Interface { return nil }, "", []Network{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := networks(tc.ifaces(t), tc.routes)
			require.NotNil(t, got)
			require.Equal(t, tc.want, got)
		})
	}
}
