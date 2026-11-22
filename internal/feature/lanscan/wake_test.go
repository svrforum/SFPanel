package lanscan

import (
	"bytes"
	"errors"
	"net/http"
	"net/netip"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// sentPacket is one packet the handler handed its sender.
type sentPacket struct {
	from   netip.Addr
	to     netip.AddrPort
	packet []byte
}

// fakeSender stands in for the socket: it records each packet and answers
// err.
type fakeSender struct {
	mu   sync.Mutex
	sent []sentPacket
	err  error
}

func (f *fakeSender) send(from netip.Addr, to netip.AddrPort, packet []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentPacket{from, to, append([]byte(nil), packet...)})
	return f.err
}

func (f *fakeSender) packets() []sentPacket {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentPacket(nil), f.sent...)
}

// waking is a handler on the test node with a third LAN, a /22 on eth3, and
// a sender that sends nothing.
func waking(t *testing.T) (*Handler, *fakeSender) {
	t.Helper()
	h := newHandler(t, openDB(t), nodeCommander(true), nil)
	nodes := testInterfaces(t)
	h.Interfaces = func() ([]Interface, error) {
		list, err := nodes()
		return append(list, Interface{Name: "eth3", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:24:07"),
			Addrs: prefixes(t, "10.20.37.7/22")}), err
	}
	sender := &fakeSender{}
	h.sendWake = sender.send
	return h, sender
}

// The magic packet: six bytes of 0xFF and the MAC sixteen times, three times
// over, to the network's broadcast address on port 9, from this node's own
// address on it. The MAC is read as the other device routes read it.
func TestWakeDevice_Sends(t *testing.T) {
	h, sender := waking(t)
	door := []byte{0x02, 0x42, 0xac, 0x14, 0x1e, 0x32}
	want := append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(door, 16)...)
	require.Len(t, want, 102)
	record(t, h.DB, t0, sighting("10.20.30.50", "02:42:ac:14:1e:32"))

	cases := []struct {
		name, mac, network string
		from, to           string
	}{
		{"on the LAN", "02:42:ac:14:1e:32", "10.20.30.0/24", "10.20.30.26", "10.20.30.255:9"},
		{"on a /22, the MAC escaped and in capitals", "02%3A42%3AAC%3A14%3A1E%3A32", "10.20.36.0/22", "10.20.37.7", "10.20.39.255:9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender.mu.Lock()
			sender.sent = nil
			sender.mu.Unlock()
			code, env := serve(t, h, http.MethodPost, "/api/v1/network/devices/"+tc.mac+"/wake", `{"network":"`+tc.network+`"}`)
			require.Equal(t, http.StatusOK, code, env.Error)
			require.JSONEq(t, `{"sent":3}`, string(env.Data))
			sent := sender.packets()
			require.Len(t, sent, 3)
			for _, p := range sent {
				require.Equal(t, netip.MustParseAddr(tc.from), p.from)
				require.Equal(t, netip.MustParseAddrPort(tc.to), p.to)
				require.Equal(t, want, p.packet)
			}
		})
	}
}

// Each refusal answers its own code, the field where there is one, and
// sends nothing.
func TestWakeDevice_Refused(t *testing.T) {
	known := "02:42:ac:14:1e:32"
	cases := []struct {
		name, mac, body string
		status          int
		code            string
		details         map[string]string
	}{
		{"a MAC of five bytes", "02:42:ac:14:1e", `{"network":"10.20.30.0/24"}`, http.StatusBadRequest, response.ErrLanDeviceInvalid, map[string]string{"field": "mac"}},
		{"not a MAC", "front-door", `{"network":"10.20.30.0/24"}`, http.StatusBadRequest, response.ErrLanDeviceInvalid, map[string]string{"field": "mac"}},
		{"no network", known, `{}`, http.StatusBadRequest, response.ErrInvalidRequest, nil},
		{"a body that is not JSON", known, `{"network":`, http.StatusBadRequest, response.ErrInvalidRequest, nil},
		{"a public network", known, `{"network":"198.51.100.0/24"}`, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed, nil},
		{"Docker's bridge", known, `{"network":"172.17.0.0/16"}`, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed, nil},
		{"a listed network too large to scan", known, `{"network":"172.20.0.0/16"}`, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed, nil},
		{"the LAN in another notation", known, `{"network":"10.20.30.5/24"}`, http.StatusBadRequest, response.ErrLanScanNetworkNotAllowed, nil},
		{"a MAC this node does not hold", "02:42:ac:14:1e:33", `{"network":"10.20.30.0/24"}`, http.StatusNotFound, response.ErrLanDeviceNotFound, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sender := waking(t)
			record(t, h.DB, t0, sighting("10.20.30.50", known))
			code, env := serve(t, h, http.MethodPost, "/api/v1/network/devices/"+tc.mac+"/wake", tc.body)
			require.Equal(t, tc.status, code)
			require.NotNil(t, env.Error)
			require.Equal(t, tc.code, env.Error.Code)
			require.Equal(t, tc.details, env.Error.Details)
			require.Empty(t, sender.packets(), "nothing is sent")
			require.Equal(t, 1, deviceCount(t, h.DB), "nothing is written")
		})
	}
}

// A packet that cannot be sent is a 500, and the ones after it are not
// tried.
func TestWakeDevice_SendFails(t *testing.T) {
	h, sender := waking(t)
	sender.err = errors.New("sendto: network is unreachable")
	record(t, h.DB, t0, sighting("10.20.30.50", "02:42:ac:14:1e:32"))
	code, env := serve(t, h, http.MethodPost, "/api/v1/network/devices/02:42:ac:14:1e:32/wake", `{"network":"10.20.30.0/24"}`)
	require.Equal(t, http.StatusInternalServerError, code)
	require.Equal(t, response.ErrNetworkError, env.Error.Code)
	require.NotContains(t, env.Error.Message, "unreachable", "the socket's error stays in the log")
	require.Len(t, sender.packets(), 1)
}
