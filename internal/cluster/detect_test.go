package cluster

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"testing"

	"github.com/svrforum/SFPanel/internal/config"
)

func TestIsTailscaleIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"100.100.100.100", true},
		{"100.63.255.255", false},  // just below CGNAT range
		{"100.128.0.0", false},     // just above CGNAT range
		{"192.168.1.1", false},
		{"10.0.0.1", false},
	}
	for _, tt := range tests {
		if got := IsTailscaleIP(net.ParseIP(tt.ip)); got != tt.want {
			t.Errorf("IsTailscaleIP(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestDetectAdvertiseAddress_LeaderDial(t *testing.T) {
	// Start a temporary TCP listener to simulate a leader.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr, err := DetectAdvertiseAddress(ln.Addr().String())
	if err != nil {
		t.Fatalf("DetectAdvertiseAddress: %v", err)
	}
	if addr != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1 for localhost leader, got %s", addr)
	}
}

func TestTailscaleAddressesKeepsTailnetAddressesOnStableUpInterfaces(t *testing.T) {
	tests := []struct {
		name  string
		iface IfaceAddr
		want  bool
	}{
		{"tailscale0 is kept", IfaceAddr{Name: "tailscale0", IP: net.ParseIP("100.64.0.10"), Up: true}, true},
		{"the range counts on any interface", IfaceAddr{Name: "eth1", IP: net.ParseIP("100.64.0.11"), Up: true}, true},
		{"a tailscale interface counts outside the range", IfaceAddr{Name: "tailscale0", IP: net.ParseIP("198.51.100.10"), Up: true}, true},
		{"a LAN address is not Tailscale", IfaceAddr{Name: "eth0", IP: net.ParseIP("192.0.2.10"), Up: true}, false},
		{"a down interface is not reached", IfaceAddr{Name: "tailscale0", IP: net.ParseIP("100.64.0.12")}, false},
		{"a container veth is ephemeral", IfaceAddr{Name: "veth1", IP: net.ParseIP("100.64.0.13"), Up: true}, false},
		{"a compose bridge is ephemeral", IfaceAddr{Name: "br-x", IP: net.ParseIP("100.64.0.14"), Up: true}, false},
		{"loopback is not reached from elsewhere", IfaceAddr{Name: "lo", IP: net.ParseIP("100.64.0.15"), Up: true, Loopback: true}, false},
		{"IPv6 is out of scope", IfaceAddr{Name: "tailscale0", IP: net.ParseIP("fd7a:115c:a1e0::1"), Up: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TailscaleAddresses([]IfaceAddr{tt.iface})
			if kept := len(got) == 1 && got[0] == tt.iface.IP.String(); kept != tt.want || len(got) > 1 {
				t.Errorf("TailscaleAddresses(%+v) = %v, want kept=%v", tt.iface, got, tt.want)
			}
		})
	}
}

func TestTailscaleAddressesSortsAndDeduplicates(t *testing.T) {
	got := TailscaleAddresses([]IfaceAddr{
		{Name: "tailscale0", IP: net.ParseIP("100.64.0.20"), Up: true},
		{Name: "eth1", IP: net.ParseIP("100.64.0.3"), Up: true},
		{Name: "tailscale1", IP: net.ParseIP("100.64.0.20"), Up: true},
	})
	if want := []string{"100.64.0.3", "100.64.0.20"}; !slices.Equal(got, want) {
		t.Errorf("TailscaleAddresses = %v, want %v", got, want)
	}
}

// managerWithIfaces is a Manager advertising advertise whose host has ifaces.
func managerWithIfaces(advertise string, ifaces []IfaceAddr, err error) *Manager {
	return &Manager{
		config:         &config.ClusterConfig{AdvertiseAddress: advertise},
		listIfaceAddrs: func() ([]IfaceAddr, error) { return ifaces, err },
	}
}

func TestSelfAddressesPutTheAdvertiseFirst(t *testing.T) {
	ts := []IfaceAddr{
		{Name: "eth0", IP: net.ParseIP("192.0.2.10"), Up: true},
		{Name: "tailscale0", IP: net.ParseIP("100.64.0.10"), Up: true},
	}
	if got, want := managerWithIfaces("192.0.2.10", ts, nil).selfAddresses(), []string{"192.0.2.10", "100.64.0.10"}; !slices.Equal(got, want) {
		t.Errorf("advertise on the LAN: selfAddresses = %v, want %v", got, want)
	}
	if got, want := managerWithIfaces("100.64.0.10", ts, nil).selfAddresses(), []string{"100.64.0.10"}; !slices.Equal(got, want) {
		t.Errorf("advertise on Tailscale: selfAddresses = %v, want %v", got, want)
	}
}

func TestSelfAddressesHoldAtMostEight(t *testing.T) {
	var ifaces []IfaceAddr
	for i := 1; i <= 12; i++ {
		ifaces = append(ifaces, IfaceAddr{Name: fmt.Sprintf("tailscale%d", i), IP: net.ParseIP(fmt.Sprintf("100.64.0.%d", i)), Up: true})
	}
	got := managerWithIfaces("192.0.2.10", ifaces, nil).selfAddresses()
	if len(got) != maxSelfAddresses || got[0] != "192.0.2.10" {
		t.Errorf("selfAddresses = %v, want the advertise then %d in all", got, maxSelfAddresses)
	}
}

func TestSelfAddressesKeepTheAdvertiseWhenInterfacesCannotBeListed(t *testing.T) {
	got := managerWithIfaces("192.0.2.10", nil, errors.New("no netlink")).selfAddresses()
	if want := []string{"192.0.2.10"}; !slices.Equal(got, want) {
		t.Errorf("selfAddresses = %v, want %v", got, want)
	}
}
