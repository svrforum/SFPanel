package lanscan

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
)

const (
	// wakePort is where a magic packet is sent: UDP 9 (discard), which a
	// Wake-on-LAN card listens on and nothing on the LAN answers.
	wakePort = 9
	// wakeCount is how many packets one wake sends. UDP is not
	// acknowledged, and a sleeping card can miss one.
	wakeCount = 3
)

type wakeRequest struct {
	Network string `json:"network"`
}

// WakeDevice sends a Wake-on-LAN magic packet for a device of this node's
// registry, three times, to the broadcast address of one of this node's
// scannable networks, from this node's own address on it. The network is
// checked as POST /network/scan checks it, and only a MAC the registry holds
// is woken: the route wakes devices the panel knows, and is not a packet
// generator. Whether the device woke is not known here; the next scan finds
// it.
//
//	POST /api/v1/network/devices/:mac/wake {"network":"10.20.30.0/24"}
func (h *Handler) WakeDevice(c echo.Context) error {
	mac, ok := deviceMAC(c)
	if !ok {
		return invalidDevice(c, "mac", "The MAC address must be six bytes, as 02:42:ac:14:1e:1f")
	}
	var req wakeRequest
	if err := c.Bind(&req); err != nil || req.Network == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Body must be {\"network\": \"<cidr>\"}")
	}
	nets, err := h.networks()
	if err != nil {
		slog.Warn("network interfaces unreadable", "component", "lanscan", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot list this node's network interfaces")
	}
	target := scannableNetwork(nets, req.Network)
	if target == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed,
			"Only a network this node lists as scannable can be woken on")
	}
	_, found, err := getDevice(h.DB, mac)
	if err != nil {
		slog.Warn("LAN device registry unreadable", "component", "lanscan", "mac", mac, "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Cannot read the device registry")
	}
	if !found {
		return response.Fail(c, http.StatusNotFound, response.ErrLanDeviceNotFound, "This node has no device with that MAC")
	}

	log := slog.With("component", "lanscan", "mac", mac, "network", target.CIDR)
	from, to, err := wakeAddrs(*target)
	if err != nil {
		// The network is this node's own list's, so this does not happen.
		log.Error("wake-on-LAN addresses unreadable", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot send the Wake-on-LAN packet")
	}
	packet, err := magicPacket(mac)
	if err != nil {
		log.Error("wake-on-LAN packet not built", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot send the Wake-on-LAN packet")
	}
	send := h.sendWake
	if send == nil {
		send = sendUDP
	}
	for i := 0; i < wakeCount; i++ {
		if err := send(from, to, packet); err != nil {
			log.Warn("wake-on-LAN packet not sent", "from", from.String(), "to", to.String(), "sent", i, "error", err)
			return response.Fail(c, http.StatusInternalServerError, response.ErrNetworkError, "Cannot send the Wake-on-LAN packet")
		}
	}
	return response.OK(c, map[string]int{"sent": wakeCount})
}

// magicPacket is the Wake-on-LAN magic packet for mac: six bytes of 0xFF,
// then the MAC sixteen times, 102 bytes.
func magicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("not a 6-byte MAC: %q", mac)
	}
	return append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(hw, 16)...), nil
}

// wakeAddrs are where a magic packet for n goes from — this node's own
// address on it, so that it leaves by n's interface — and to: n's broadcast
// address, port 9.
func wakeAddrs(n Network) (from netip.Addr, to netip.AddrPort, err error) {
	from, err = netip.ParseAddr(n.Address)
	if err != nil {
		return netip.Addr{}, netip.AddrPort{}, err
	}
	prefix, err := netip.ParsePrefix(n.CIDR)
	if err != nil {
		return netip.Addr{}, netip.AddrPort{}, err
	}
	if !prefix.Addr().Is4() || !prefix.Contains(from) {
		return netip.Addr{}, netip.AddrPort{}, fmt.Errorf("%s is not an IPv4 address in %s", from, prefix)
	}
	base := prefix.Masked().Addr().As4()
	var last [4]byte
	binary.BigEndian.PutUint32(last[:], binary.BigEndian.Uint32(base[:])|^uint32(0)>>prefix.Bits())
	return from, netip.AddrPortFrom(netip.AddrFrom4(last), wakePort), nil
}

// sendUDP sends one datagram from from (any port) to to. Go sets
// SO_BROADCAST on its UDP sockets, which a broadcast address needs.
func sendUDP(from netip.Addr, to netip.AddrPort, packet []byte) error {
	conn, err := net.DialUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(from, 0)), net.UDPAddrFromAddrPort(to))
	if err != nil {
		return err
	}
	n, err := conn.Write(packet)
	if cerr := conn.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != len(packet) {
		err = fmt.Errorf("sent %d of %d bytes", n, len(packet))
	}
	return err
}
