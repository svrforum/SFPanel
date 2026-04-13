package cluster

import (
	"net"
	"testing"
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
