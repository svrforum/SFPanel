package lanscan

import (
	"net"
	"net/netip"
	"strings"
)

// Interface is one of the host's network interfaces, as the network list is
// built from it. systemInterfaces reads them from the kernel; tests hand in
// their own.
type Interface struct {
	Name  string
	Flags net.Flags
	MAC   net.HardwareAddr
	// Addrs are the interface's addresses with their prefix length, the
	// address itself kept (10.20.30.26/24, not 10.20.30.0/24).
	Addrs []netip.Prefix
}

// Network is a network this node is attached to, as GET /network/scan lists
// it. Only a Scannable one may be named in POST /network/scan.
type Network struct {
	// CIDR is the network itself (10.20.30.0/24). It is the string a scan
	// request has to name, and the one nmap is given.
	CIDR      string `json:"cidr"`
	Interface string `json:"interface"`
	// Address is this node's own address in it.
	Address string `json:"address"`
	// MAC is the interface's, lower case.
	MAC string `json:"mac"`
	// Gateway is the next hop of the interface's default route when it lies
	// inside this network, else "".
	Gateway   string `json:"gateway"`
	Scannable bool   `json:"scannable"`
	// Reason says why a listed network is not Scannable: reasonTooLarge.
	Reason string `json:"reason"`
}

const (
	// minScanBits is the widest network a scan may cover: a /20 is 4096
	// addresses, which a host scan answers in well under its limit.
	minScanBits = 20
	// maxScanBits is the narrowest network that is listed. A /31 or /32 is a
	// point-to-point link or a single address, not a network with devices.
	maxScanBits = 30

	// reasonTooLarge is a private network wider than a /20: listed, so the
	// page can say why it offers no scan, but never scanned.
	reasonTooLarge = "too_large"
)

// excludedNamePrefixes are interfaces that are not a LAN though they look
// like one (up, broadcast, a MAC, a private address):
//   - the bridges this host made for its own containers and virtual
//     machines — Docker's docker0 and br-<id> and the veth pair of every
//     container, Podman's, Kubernetes' cni and flannel, libvirt's virbr,
//     LXC's, LXD's and Incus's bridges, VirtualBox's and VMware's host-only
//     networks. What is behind them is this host's own guests, which the
//     panel already lists.
//   - ZeroTier's zt<id>: an Ethernet-like overlay whose other members are
//     machines somewhere on the internet, not devices on this LAN.
//
// Proxmox VE's vmbr0 is not among them: it is the bridge the host itself is
// on the LAN through.
var excludedNamePrefixes = []string{
	"docker", "br-", "veth", "podman", "cni", "flannel",
	"virbr", "lxcbr", "lxdbr", "incusbr", "vboxnet", "vmnet",
	"zt",
}

// networks lists the networks a scan may be asked for, in interface order:
// the private (RFC 1918) IPv4 networks of interfaces that are up, broadcast
// and have an Ethernet MAC, minus the container and VM bridges. Public,
// link-local and carrier-grade NAT (Tailscale) addresses are not listed;
// neither are /31 and /32. A network wider than a /20 is listed as not
// scannable. defaultRoutes is the output of `ip -4 route show default`,
// which names each interface's gateway.
//
// A network reached through two interfaces, or twice through one, is
// listed once, for the first.
func networks(ifaces []Interface, defaultRoutes string) []Network {
	gateways := defaultGateways(defaultRoutes)
	out := []Network{}
	seen := map[netip.Prefix]bool{}
	for _, ifc := range ifaces {
		if !onLAN(ifc) {
			continue
		}
		for _, p := range ifc.Addrs {
			if !p.IsValid() {
				continue
			}
			addr := p.Addr().Unmap()
			if !addr.Is4() || !addr.IsPrivate() || p.Bits() > maxScanBits {
				continue
			}
			cidr := netip.PrefixFrom(addr, p.Bits()).Masked()
			if seen[cidr] {
				continue
			}
			seen[cidr] = true
			n := Network{
				CIDR:      cidr.String(),
				Interface: ifc.Name,
				Address:   addr.String(),
				MAC:       ifc.MAC.String(),
				Scannable: true,
			}
			for _, gw := range gateways[ifc.Name] {
				if cidr.Contains(gw) {
					n.Gateway = gw.String()
					break
				}
			}
			if p.Bits() < minScanBits {
				n.Scannable = false
				n.Reason = reasonTooLarge
			}
			out = append(out, n)
		}
	}
	return out
}

// onLAN says whether an interface can be on a LAN with other devices: it is
// up, it broadcasts, it has a 6-byte MAC (loopback, WireGuard and Tailscale
// have none), and it is not a bridge this host made for its guests.
func onLAN(ifc Interface) bool {
	if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || len(ifc.MAC) != 6 {
		return false
	}
	for _, prefix := range excludedNamePrefixes {
		if strings.HasPrefix(ifc.Name, prefix) {
			return false
		}
	}
	return true
}

// defaultGateways reads `ip -4 route show default`: the next hops of the
// default routes, by the interface each leaves through, in the order ip
// prints them (lowest metric first). A multipath route's nexthop lines carry
// the same via/dev pair and count the same way. A route without a via
// (default dev wg0) has no gateway.
func defaultGateways(text string) map[string][]netip.Addr {
	out := map[string][]netip.Addr{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		var via netip.Addr
		dev := ""
		for i := 0; i+1 < len(f); i++ {
			switch f[i] {
			case "via":
				if a, err := netip.ParseAddr(f[i+1]); err == nil && a.Is4() {
					via = a
				}
			case "dev":
				dev = f[i+1]
			}
		}
		if via.IsValid() && dev != "" {
			out[dev] = append(out[dev], via)
		}
	}
	return out
}

// systemInterfaces reads the host's interfaces from the kernel. An
// interface whose addresses cannot be read is listed without them.
func systemInterfaces() ([]Interface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(list))
	for _, ifc := range list {
		item := Interface{Name: ifc.Name, Flags: ifc.Flags, MAC: ifc.HardwareAddr}
		addrs, err := ifc.Addrs()
		if err == nil {
			for _, a := range addrs {
				ipnet, ok := a.(*net.IPNet)
				if !ok {
					continue
				}
				ones, bits := ipnet.Mask.Size()
				ip := ipnet.IP
				if v4 := ip.To4(); v4 != nil {
					ip = v4
					if bits == 128 {
						ones -= 96
					}
				}
				addr, ok := netip.AddrFromSlice(ip)
				if !ok {
					continue
				}
				item.Addrs = append(item.Addrs, netip.PrefixFrom(addr, ones))
			}
		}
		out = append(out, item)
	}
	return out, nil
}
