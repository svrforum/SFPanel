package portmap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSs_IPv4SingleProcess(t *testing.T) {
	// `ss -tlnp -H` output (no header, tab-separated columns).
	in := `LISTEN 0      128          *:8444         *:*    users:(("sfpanel",pid=1410507,fd=10))
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 1)
	require.Equal(t, 8444, got[0].Port)
	require.Equal(t, "tcp", got[0].Proto)
	require.Equal(t, 1410507, got[0].PID)
	require.Equal(t, "sfpanel", got[0].Name)
}

func TestParseSs_IPv6(t *testing.T) {
	in := `LISTEN 0      128       [::]:22       [::]:*    users:(("sshd",pid=1024,fd=4))
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 1)
	require.Equal(t, 22, got[0].Port)
	require.Equal(t, "sshd", got[0].Name)
}

func TestParseSs_MultiProcess(t *testing.T) {
	in := `LISTEN 0      128          *:5432         *:*    users:(("docker-proxy",pid=9912,fd=4),("postgres",pid=10001,fd=3))
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 2)
	require.Equal(t, 5432, got[0].Port)
	require.Equal(t, "docker-proxy", got[0].Name)
	require.Equal(t, "postgres", got[1].Name)
}

func TestParseSs_NoUsersClause(t *testing.T) {
	// Non-root invocation: no users:(...) suffix.
	in := `LISTEN 0      128          *:80           *:*
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 1)
	require.Equal(t, 80, got[0].Port)
	require.Equal(t, 0, got[0].PID)
	require.Equal(t, "", got[0].Name)
}

func TestParseSs_UDP(t *testing.T) {
	in := `UNCONN 0      0            *:53           *:*    users:(("dnsmasq",pid=512,fd=2))
`
	got := ParseSs(in, "udp")
	require.Len(t, got, 1)
	require.Equal(t, 53, got[0].Port)
	require.Equal(t, "udp", got[0].Proto)
}

func TestParseSs_EmptyInput(t *testing.T) {
	require.Empty(t, ParseSs("", "tcp"))
	require.Empty(t, ParseSs("   \n  ", "tcp"))
}

// Each local-address form ss prints: the port, brackets and %zone go, the
// wildcard "*" stays.
func TestParseSs_LocalAddress(t *testing.T) {
	cases := []struct {
		local string
		port  int
		addr  string
	}{
		{"0.0.0.0:22", 22, "0.0.0.0"},
		{"[::]:80", 80, "::"},
		{"*:3628", 3628, "*"},
		{"127.0.0.53%lo:53", 53, "127.0.0.53"},
		{"[::1]:631", 631, "::1"},
		{"[fe80::1]%eth0:546", 546, "fe80::1"},
		{"10.20.30.26:8443", 8443, "10.20.30.26"},
	}
	for _, tc := range cases {
		t.Run(tc.local, func(t *testing.T) {
			in := "LISTEN 0      4096   " + tc.local + "   0.0.0.0:*    users:((\"svc\",pid=42,fd=3))\n"
			got := ParseSs(in, "tcp")
			require.Len(t, got, 1)
			require.Equal(t, tc.port, got[0].Port)
			require.Equal(t, tc.addr, got[0].Addr)
			require.Equal(t, "svc", got[0].Name)
		})
	}
}

// A listener without a users:(...) clause (ss run without root) still
// carries its address.
func TestParseSs_LocalAddressWithoutUsers(t *testing.T) {
	got := ParseSs("UNCONN 0      0      127.0.0.53%lo:53      0.0.0.0:*\n", "udp")
	require.Len(t, got, 1)
	require.Equal(t, "127.0.0.53", got[0].Addr)
}
