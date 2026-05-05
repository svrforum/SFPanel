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
