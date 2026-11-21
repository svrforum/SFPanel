package lanscan

import (
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func TestNmapArgs(t *testing.T) {
	require.Equal(t,
		[]string{"-sn", "-oX", "/tmp/x/scan.xml", "10.20.30.0/24"},
		nmapArgs("10.20.30.0/24", false, "/tmp/x/scan.xml"))
	require.Equal(t,
		[]string{"-sS", "-T4", "--max-retries", "2", "--host-timeout", "30s", "-oX", "/tmp/x/scan.xml", "10.20.30.0/24"},
		nmapArgs("10.20.30.0/24", true, "/tmp/x/scan.xml"))
}

func requireSortedByAddress(t *testing.T, hosts []Host) {
	t.Helper()
	for i := 1; i < len(hosts); i++ {
		a, b := netip.MustParseAddr(hosts[i-1].IP), netip.MustParseAddr(hosts[i].IP)
		require.True(t, a.Less(b), "%s before %s", hosts[i-1].IP, hosts[i].IP)
	}
}

func hostByIP(t *testing.T, hosts []Host, ip string) Host {
	t.Helper()
	for _, h := range hosts {
		if h.IP == ip {
			return h
		}
	}
	t.Fatalf("no host %s", ip)
	return Host{}
}

// The host scan as nmap 7.98 wrote it: 27 hosts up, the node itself last in
// the file and without a MAC, <hostnames> empty for most.
func TestParseScan_Discover(t *testing.T) {
	hosts, err := parseScan(fixture(t, "nmap-discover.xml"))
	require.NoError(t, err)
	require.Len(t, hosts, 27)
	requireSortedByAddress(t, hosts)

	require.Equal(t, Host{IP: "10.20.30.1", MAC: "02:42:ac:14:1e:01", Vendor: "Example Electronics",
		Hostname: "router.lan", Ports: []Port{}}, hosts[0])
	require.Equal(t, Host{IP: "10.20.30.10", MAC: "02:42:ac:14:1e:0a", Vendor: "Proxmox Server Solutions GmbH",
		Ports: []Port{}}, hosts[1])
	// A MAC nmap has no vendor for.
	require.Equal(t, Host{IP: "10.20.30.26", MAC: "02:42:ac:14:1e:1a", Hostname: "host-26.lan", Ports: []Port{}},
		hostByIP(t, hosts, "10.20.30.26"))

	var self []Host
	for _, h := range hosts {
		require.NotNil(t, h.Ports)
		require.Empty(t, h.Ports)
		require.False(t, h.TimedOut)
		if h.Self {
			self = append(self, h)
		}
	}
	// In this capture the node that ran the host scan is 10.20.30.25
	// (reason="localhost-response").
	require.Equal(t, []Host{{IP: "10.20.30.25", Hostname: "host-25.lan", Self: true, Ports: []Port{}}}, self)
}

// The port scan: 28 hosts up, only open ports kept.
func TestParseScan_Ports(t *testing.T) {
	hosts, err := parseScan(fixture(t, "nmap-ports.xml"))
	require.NoError(t, err)
	require.Len(t, hosts, 28)
	requireSortedByAddress(t, hosts)

	open := 0
	var self []string
	for _, h := range hosts {
		require.NotNil(t, h.Ports)
		require.False(t, h.TimedOut)
		open += len(h.Ports)
		if h.Self {
			self = append(self, h.IP)
		}
	}
	require.Equal(t, 52, open)
	require.Equal(t, []string{"10.20.30.26"}, self)

	// The gateway. Its <hosthint> earlier in the file has no hostname and
	// is not what is read.
	require.Equal(t, Host{IP: "10.20.30.1", MAC: "02:42:ac:14:1e:01", Vendor: "Example Electronics",
		Hostname: "router.lan", Ports: []Port{{53, "tcp"}, {80, "tcp"}}}, hosts[0])
	// The node itself: no MAC from nmap, its ports open.
	require.Equal(t, Host{IP: "10.20.30.26", Hostname: "host-26.lan", Self: true,
		Ports: []Port{{22, "tcp"}, {8080, "tcp"}, {8443, "tcp"}}}, hostByIP(t, hosts, "10.20.30.26"))
	// 9090 is listed as a <port> in state closed: not open, not kept.
	require.Equal(t, []Port{{22, "tcp"}}, hostByIP(t, hosts, "10.20.30.34").Ports)
	// Two <extraports> (757 closed, 241 filtered) and one open port.
	require.Equal(t, Host{IP: "10.20.30.18", MAC: "02:42:ac:14:1e:12", Hostname: "host-18.lan",
		Ports: []Port{{62078, "tcp"}}}, hostByIP(t, hosts, "10.20.30.18"))
	// A host with every port closed.
	require.Equal(t, []Port{}, hostByIP(t, hosts, "10.20.30.17").Ports)
}

// A host the port scan gave up on (--host-timeout) is written with
// timedout="true" and no <ports> (nmap.dtd). The captures have none.
func TestParseScan_TimedOutHost(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nmaprun>
<nmaprun scanner="nmap" args="nmap -sS -T4 --max-retries 2 --host-timeout 30s -oX scan.xml 10.20.30.0/24" start="1795281124" version="7.98" xmloutputversion="1.05">
<host starttime="1795281131" endtime="1795281161" timedout="true"><status state="up" reason="arp-response" reason_ttl="0"/>
<address addr="10.20.30.40" addrtype="ipv4"/>
<address addr="02:42:AC:14:1E:28" addrtype="mac" vendor="Example Electronics"/>
<hostnames>
</hostnames>
<times srtt="3992" rttvar="995" to="100000"/>
</host>
<host starttime="1795281131" endtime="1795281139"><status state="up" reason="arp-response" reason_ttl="0"/>
<address addr="10.20.30.5" addrtype="ipv4"/>
<address addr="02:42:AC:14:1E:05" addrtype="mac"/>
<hostnames>
<hostname name="host-5.lan" type="PTR"/>
</hostnames>
<ports><extraports state="closed" count="999">
<extrareasons reason="reset" count="999" proto="tcp" ports="1,3-4,6-7,9,13,17,19-21"/>
</extraports>
<port protocol="tcp" portid="443"><state state="open" reason="syn-ack" reason_ttl="64"/><service name="https" method="table" conf="3"/></port>
</ports>
</host>
<host><status state="down" reason="no-response" reason_ttl="0"/>
<address addr="10.20.30.41" addrtype="ipv4"/>
</host>
<runstats><finished time="1795281188" timestr="Sun Nov 22 02:13:08 2026" elapsed="64.00" exit="success"/><hosts up="2" down="254" total="256"/>
</runstats>
</nmaprun>
`
	hosts, err := parseScan([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, []Host{
		{IP: "10.20.30.5", MAC: "02:42:ac:14:1e:05", Hostname: "host-5.lan", Ports: []Port{{443, "tcp"}}},
		{IP: "10.20.30.40", MAC: "02:42:ac:14:1e:28", Vendor: "Example Electronics", TimedOut: true, Ports: []Port{}},
	}, hosts)
}

// nmap killed or timed out mid-scan leaves the file cut short: everything
// up to the last host, and no <runstats>. It is refused, and the error says
// the scan did not finish.
func TestParseScan_TruncatedRefused(t *testing.T) {
	data := fixture(t, "nmap-ports.xml")
	cut := strings.Index(string(data), "<runstats>")
	require.Positive(t, cut)

	hosts, err := parseScan(data[:cut])
	require.Error(t, err)
	require.Nil(t, hosts)
	require.True(t, errors.Is(err, errNotFinished), "error: %v", err)
	require.Contains(t, err.Error(), "nmap did not finish the scan: its XML output ends early")
}

// A file that is well-formed XML but carries no finished run is refused too:
// without the <runstats> check this one would parse as 28 hosts.
func TestParseScan_NoFinishedRunRefused(t *testing.T) {
	data := fixture(t, "nmap-ports.xml")
	cut := strings.Index(string(data), "<runstats>")
	require.Positive(t, cut)
	closed := string(data[:cut]) + "</nmaprun>\n"

	hosts, err := parseScan([]byte(closed))
	require.Error(t, err)
	require.Nil(t, hosts)
	require.True(t, errors.Is(err, errNotFinished), "error: %v", err)
	require.Contains(t, err.Error(), `no <runstats><finished exit="success">`)

	failed := strings.Replace(string(data), `exit="success"`, `exit="error"`, 1)
	require.NotEqual(t, string(data), failed)
	hosts, err = parseScan([]byte(failed))
	require.Error(t, err)
	require.Nil(t, hosts)
	require.Contains(t, err.Error(), `no <runstats><finished exit="success">`)
}
