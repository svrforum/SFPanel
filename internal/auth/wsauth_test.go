package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestIsLoopbackRequest(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:54321", true},
		{"127.0.0.5:80", true},
		{"[::1]:54321", true},
		{"::1:54321", true},
		{"10.0.0.1:54321", false},
		{"192.168.1.5:443", false},
		{"203.0.113.7:9443", false},
		{"[2001:db8::1]:443", false},
		{"garbage", false},
		{"", false},
	}
	for _, tc := range cases {
		r := &http.Request{RemoteAddr: tc.remote}
		if got := isLoopbackRequest(r); got != tc.want {
			t.Errorf("isLoopbackRequest(%q) = %v, want %v", tc.remote, got, tc.want)
		}
	}
}

func TestAuthenticateWSRequest_LegacyTokenLoopbackOnly(t *testing.T) {
	// Mint a valid JWT.
	secret := "test-secret-32bytes-long-padding!"
	token, err := GenerateToken("alice", secret, 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Loopback: legacy ?token= path accepted.
	r1, _ := http.NewRequest("GET", "/ws/metrics?token="+token, nil)
	r1.RemoteAddr = "127.0.0.1:54321"
	if got := AuthenticateWSRequest(r1, secret); got != "alice" {
		t.Errorf("loopback ?token=: got %q, want %q", got, "alice")
	}

	// Non-loopback: legacy ?token= path rejected.
	r2, _ := http.NewRequest("GET", "/ws/metrics?token="+token, nil)
	r2.RemoteAddr = "192.168.1.42:54321"
	if got := AuthenticateWSRequest(r2, secret); got != "" {
		t.Errorf("non-loopback ?token=: got %q, want empty (rejected)", got)
	}
}
