package lanscan

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// t0 is when the registry tests begin.
var t0 = time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)

// testClock is a clock the tests move by hand.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *testClock { return &testClock{t: t} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// xmlHost is a host for scanXML: mac "" writes no MAC address, as nmap does
// for its own host and for one it did not reach over ARP.
type xmlHostSpec struct {
	ip, mac, vendor, hostname string
	self, timedOut            bool
	ports                     []int
}

// scanXML writes a finished nmap run with these hosts up.
func scanXML(hosts ...xmlHostSpec) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<nmaprun scanner=\"nmap\" version=\"7.98\">\n")
	for _, h := range hosts {
		reason := "arp-response"
		if h.self {
			reason = "localhost-response"
		}
		timed := ""
		if h.timedOut {
			timed = ` timedout="true"`
		}
		fmt.Fprintf(&b, "<host%s><status state=\"up\" reason=\"%s\"/>\n<address addr=\"%s\" addrtype=\"ipv4\"/>\n", timed, reason, h.ip)
		if h.mac != "" {
			fmt.Fprintf(&b, "<address addr=\"%s\" addrtype=\"mac\" vendor=\"%s\"/>\n", strings.ToUpper(h.mac), h.vendor)
		}
		b.WriteString("<hostnames>")
		if h.hostname != "" {
			fmt.Fprintf(&b, "<hostname name=\"%s\" type=\"PTR\"/>", h.hostname)
		}
		b.WriteString("</hostnames>\n")
		if len(h.ports) > 0 {
			b.WriteString("<ports>")
			for _, p := range h.ports {
				fmt.Fprintf(&b, "<port protocol=\"tcp\" portid=\"%d\"><state state=\"open\"/></port>", p)
			}
			b.WriteString("</ports>\n")
		}
		b.WriteString("</host>\n")
	}
	b.WriteString("<runstats><finished exit=\"success\"/></runstats>\n</nmaprun>\n")
	return []byte(b.String())
}

// sighting is a host as recordScan is handed it.
func sighting(ip, mac string) Host {
	return Host{IP: ip, MAC: mac, Ports: []Port{}}
}

// record stores a scan of 10.20.30.0/24 that found hosts, finished at at.
func record(t *testing.T, db *sql.DB, at time.Time, hosts ...Host) {
	t.Helper()
	r := Result{Network: "10.20.30.0/24", Interface: "eth0", StartedAt: stamp(at), FinishedAt: stamp(at), Hosts: hosts}
	_, err := recordScan(db, r, at)
	require.NoError(t, err)
}

func device(t *testing.T, db *sql.DB, mac string) Device {
	t.Helper()
	d, ok, err := getDevice(db, mac)
	require.NoError(t, err)
	require.True(t, ok, "no device %s", mac)
	return d
}

type claim struct{ firstAt, lastAt string }

func claims(t *testing.T, db *sql.DB) map[string]claim {
	t.Helper()
	rows, err := db.Query(`SELECT network, ip, mac, first_at, last_at FROM lan_claims`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]claim{}
	for rows.Next() {
		var network, ip, mac string
		var c claim
		require.NoError(t, rows.Scan(&network, &ip, &mac, &c.firstAt, &c.lastAt))
		out[network+" "+ip+" "+mac] = c
	}
	require.NoError(t, rows.Err())
	return out
}

func conflicts(t *testing.T, db *sql.DB) []Conflict {
	t.Helper()
	c, err := loadConflicts(db)
	require.NoError(t, err)
	return c
}

// A device follows its MAC: the network and address are the latest, a
// vendor or hostname is only replaced by one that says something, and
// first_seen is set once.
func TestRegistry_Upsert(t *testing.T) {
	db := openDB(t)
	plug := "02:42:ac:14:1e:1f"
	record(t, db, t0, Host{IP: "10.20.30.31", MAC: plug, Vendor: "Espressif", Hostname: "plug.lan", Ports: []Port{}})
	require.Equal(t, Device{MAC: plug, Network: "10.20.30.0/24", IP: "10.20.30.31", Vendor: "Espressif",
		Hostname: "plug.lan", FirstSeen: stamp(t0), LastSeen: stamp(t0)}, device(t, db, plug))

	// It moved, and this time nmap found neither name.
	record(t, db, t0.Add(time.Hour), sighting("10.20.30.32", plug))
	require.Equal(t, Device{MAC: plug, Network: "10.20.30.0/24", IP: "10.20.30.32", Vendor: "Espressif",
		Hostname: "plug.lan", FirstSeen: stamp(t0), LastSeen: stamp(t0.Add(time.Hour))}, device(t, db, plug))

	// A new name replaces the old.
	record(t, db, t0.Add(2*time.Hour), Host{IP: "10.20.30.32", MAC: plug, Vendor: "Espressif Inc.", Hostname: "plug-2.lan", Ports: []Port{}})
	d := device(t, db, plug)
	require.Equal(t, "Espressif Inc.", d.Vendor)
	require.Equal(t, "plug-2.lan", d.Hostname)
	require.Equal(t, stamp(t0), d.FirstSeen)

	// A host without a MAC is not a device.
	record(t, db, t0.Add(3*time.Hour), sighting("10.20.30.40", ""))
	all, err := loadDevices(db)
	require.NoError(t, err)
	require.Len(t, all, 1)
}

// A device labelled before this node saw it gets its first_seen at the
// first sighting, and keeps its label.
func TestRegistry_LabelledBeforeSeen(t *testing.T) {
	db := openDB(t)
	tv := "02:42:ac:14:1e:2a"
	_, err := labelDevice(db, tv, "Living room TV", "", "tv", stamp(t0), nil)
	require.NoError(t, err)
	require.Equal(t, "", device(t, db, tv).FirstSeen)

	record(t, db, t0.Add(time.Hour), sighting("10.20.30.42", tv))
	d := device(t, db, tv)
	require.Equal(t, stamp(t0.Add(time.Hour)), d.FirstSeen)
	require.Equal(t, "Living room TV", d.Name)
	require.Equal(t, "tv", d.Kind)
	require.Equal(t, stamp(t0), d.LabelledAt)
}

// A claim seen again within 24 hours runs on; after a longer gap it starts
// over; one not seen for over 48 hours is dropped.
func TestRegistry_Claims(t *testing.T) {
	db := openDB(t)
	a, b := "02:42:ac:14:1e:0a", "02:42:ac:14:1e:0b"
	keyA, keyB := "10.20.30.0/24 10.20.30.10 "+a, "10.20.30.0/24 10.20.30.11 "+b

	record(t, db, t0, sighting("10.20.30.10", a), sighting("10.20.30.11", b))
	require.Equal(t, claim{stamp(t0), stamp(t0)}, claims(t, db)[keyA])

	// Again after exactly 24 hours: still the same claim.
	at := t0.Add(24 * time.Hour)
	record(t, db, at, sighting("10.20.30.10", a))
	require.Equal(t, claim{stamp(t0), stamp(at)}, claims(t, db)[keyA])

	// Again after 24 hours and a second: a new one.
	at2 := at.Add(24*time.Hour + time.Second)
	record(t, db, at2, sighting("10.20.30.10", a))
	require.Equal(t, claim{stamp(at2), stamp(at2)}, claims(t, db)[keyA])
	// b was last seen 48 h 1 s before at2: gone.
	_, ok := claims(t, db)[keyB]
	require.False(t, ok, "a claim last seen over 48 hours ago is dropped")
}

// Exactly 48 hours is not over 48 hours.
func TestRegistry_ClaimKeptAt48Hours(t *testing.T) {
	db := openDB(t)
	a, b := "02:42:ac:14:1e:0a", "02:42:ac:14:1e:0b"
	record(t, db, t0, sighting("10.20.30.11", b))
	record(t, db, t0.Add(48*time.Hour), sighting("10.20.30.10", a))
	require.Len(t, claims(t, db), 2)
}

// The overlap rule. An address handed from one device to the next is no
// conflict; one that goes A, B and A again is, as A's period covers B's.
func TestRegistry_Conflicts(t *testing.T) {
	a, b, c := "02:42:ac:14:1e:4a", "02:42:ac:14:1e:4b", "02:42:ac:14:1e:4c"
	ip := "10.20.30.12"

	t.Run("handover", func(t *testing.T) {
		db := openDB(t)
		record(t, db, t0, sighting(ip, a))
		record(t, db, t0.Add(time.Hour), sighting(ip, b))
		record(t, db, t0.Add(2*time.Hour), sighting(ip, b))
		require.Equal(t, []Conflict{}, conflicts(t, db))
	})
	t.Run("A, B, A", func(t *testing.T) {
		db := openDB(t)
		record(t, db, t0, sighting(ip, b))
		record(t, db, t0.Add(time.Hour), sighting(ip, a))
		record(t, db, t0.Add(2*time.Hour), sighting(ip, b))
		require.Equal(t, []Conflict{{Network: "10.20.30.0/24", IP: ip, MACs: []string{a, b}}}, conflicts(t, db))
	})
	t.Run("three MACs", func(t *testing.T) {
		db := openDB(t)
		record(t, db, t0, sighting(ip, c))
		record(t, db, t0.Add(time.Hour), sighting(ip, b))
		record(t, db, t0.Add(2*time.Hour), sighting(ip, a))
		record(t, db, t0.Add(3*time.Hour), sighting(ip, c))
		require.Equal(t, []Conflict{{Network: "10.20.30.0/24", IP: ip, MACs: []string{a, b, c}}}, conflicts(t, db))
	})
	t.Run("a third MAC after the conflict is not in it", func(t *testing.T) {
		db := openDB(t)
		record(t, db, t0, sighting(ip, a))
		record(t, db, t0.Add(time.Hour), sighting(ip, b))
		record(t, db, t0.Add(2*time.Hour), sighting(ip, a))
		record(t, db, t0.Add(3*time.Hour), sighting(ip, c))
		require.Equal(t, []Conflict{{Network: "10.20.30.0/24", IP: ip, MACs: []string{a, b}}}, conflicts(t, db))
	})
	t.Run("a gap of over 24 hours ends a period", func(t *testing.T) {
		db := openDB(t)
		record(t, db, t0, sighting(ip, a))
		record(t, db, t0.Add(time.Hour), sighting(ip, b))
		record(t, db, t0.Add(26*time.Hour), sighting(ip, a))
		require.Equal(t, []Conflict{}, conflicts(t, db))
	})
	t.Run("the same address on another network is not the same address", func(t *testing.T) {
		db := openDB(t)
		record(t, db, t0, sighting(ip, a))
		other := Result{Network: "10.20.31.0/24", StartedAt: stamp(t0), FinishedAt: stamp(t0), Hosts: []Host{sighting(ip, b)}}
		_, err := recordScan(db, other, t0)
		require.NoError(t, err)
		require.Equal(t, []Conflict{}, conflicts(t, db))
	})
}

func missed(t *testing.T, db *sql.DB, mac string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT missed FROM lan_devices WHERE mac = ?`, mac).Scan(&n))
	return n
}

// Each scan of a network sets the devices it found back to 0 and adds one
// to the network's others; another network's devices, and a MAC labelled
// here and never seen, are left alone. A device that moved to another
// network is that network's from then.
func TestRegistry_Missed(t *testing.T) {
	db := openDB(t)
	a, b, c, d := "02:42:ac:14:1e:0a", "02:42:ac:14:1e:0b", "02:42:ac:14:1e:0c", "02:42:ac:14:1e:0d"
	other := func(at time.Time, hosts ...Host) {
		t.Helper()
		_, err := recordScan(db, Result{Network: "10.20.31.0/24", StartedAt: stamp(at), FinishedAt: stamp(at), Hosts: hosts}, at)
		require.NoError(t, err)
	}
	record(t, db, t0, sighting("10.20.30.10", a), sighting("10.20.30.11", b))
	other(t0, sighting("10.20.31.12", c))
	_, err := labelDevice(db, d, "Spare", "", "", stamp(t0), nil)
	require.NoError(t, err)
	counts := func() []int {
		return []int{missed(t, db, a), missed(t, db, b), missed(t, db, c), missed(t, db, d)}
	}
	require.Equal(t, []int{0, 0, 0, 0}, counts())

	record(t, db, t0.Add(time.Hour), sighting("10.20.30.10", a))
	require.Equal(t, []int{0, 1, 0, 0}, counts(), "found resets, not found adds, the rest untouched")
	record(t, db, t0.Add(2*time.Hour))
	require.Equal(t, []int{1, 2, 0, 0}, counts())
	record(t, db, t0.Add(3*time.Hour), sighting("10.20.30.11", b))
	require.Equal(t, []int{2, 0, 0, 0}, counts())
	other(t0.Add(3 * time.Hour))
	require.Equal(t, []int{2, 0, 1, 0}, counts(), "the other network's scan counts its own devices only")

	// a moves to the other network: found there, then no longer the LAN's.
	other(t0.Add(4*time.Hour), sighting("10.20.31.10", a))
	record(t, db, t0.Add(5*time.Hour), sighting("10.20.30.11", b))
	require.Equal(t, []int{0, 0, 2, 0}, counts())
}

// A device nobody labelled goes once it has not been seen for over 14
// days; a labelled one stays.
func TestRegistry_PruneUnlabelled(t *testing.T) {
	db := openDB(t)
	phone, nas, plug := "02:42:ac:14:1e:50", "02:42:ac:14:1e:51", "02:42:ac:14:1e:52"
	record(t, db, t0, sighting("10.20.30.50", phone), sighting("10.20.30.51", nas))
	_, err := labelDevice(db, nas, "Backup NAS", "", "nas", stamp(t0), nil)
	require.NoError(t, err)

	// 14 days on: not yet over.
	record(t, db, t0.Add(14*24*time.Hour), sighting("10.20.30.52", plug))
	device(t, db, phone)

	record(t, db, t0.Add(14*24*time.Hour+time.Second), sighting("10.20.30.52", plug))
	_, ok, err := getDevice(db, phone)
	require.NoError(t, err)
	require.False(t, ok, "an unlabelled device unseen for over 14 days is dropped")
	require.Equal(t, "Backup NAS", device(t, db, nas).Name)
	device(t, db, plug)
}

// A host scan keeps the ports the port scan before it found, for the same
// device: by MAC when it moved, by address when it has no MAC, and not at
// all for another device at the same address. ports_at is the port scan's.
func TestScan_HostScanKeepsPorts(t *testing.T) {
	db := openDB(t)
	clock := newClock(t0)
	scan := &fakeScan{}
	h := newHandler(t, db, nodeCommander(true), scan)
	h.now = clock.now

	scan.data = scanXML(
		xmlHostSpec{ip: "10.20.30.26", self: true, ports: []int{22}},
		xmlHostSpec{ip: "10.20.30.31", mac: "02:42:ac:14:1e:1f", ports: []int{80, 443}},
		xmlHostSpec{ip: "10.20.30.40", ports: []int{8080}},
		xmlHostSpec{ip: "10.20.30.50", mac: "02:42:ac:14:1e:32", ports: []int{22}},
		xmlHostSpec{ip: "10.20.30.60", mac: "02:42:ac:14:1e:3c", timedOut: true},
	)
	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusOK, code)
	st := waitLast(t, h)
	require.Equal(t, stateDone, st.Last.State)
	require.Equal(t, stamp(t0), st.Results[0].PortsAt)

	clock.advance(time.Hour)
	scan.mu.Lock()
	scan.data = scanXML(
		xmlHostSpec{ip: "10.20.30.26", self: true},
		xmlHostSpec{ip: "10.20.30.33", mac: "02:42:ac:14:1e:1f"}, // moved
		xmlHostSpec{ip: "10.20.30.40"},                           // no MAC
		xmlHostSpec{ip: "10.20.30.50", mac: "02:42:ac:14:1e:99"}, // another device
		xmlHostSpec{ip: "10.20.30.60", mac: "02:42:ac:14:1e:3c"},
		xmlHostSpec{ip: "10.20.30.70", mac: "02:42:ac:14:1e:46"}, // new
	)
	scan.mu.Unlock()
	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	st = waitLast(t, h)
	require.Equal(t, stateDone, st.Last.State)

	r := st.Results[0]
	require.False(t, r.Ports, "this scan did not look for ports")
	require.Equal(t, stamp(t0), r.PortsAt, "ports were last looked for by the port scan")
	require.Equal(t, stamp(t0.Add(time.Hour)), r.FinishedAt)
	require.Len(t, r.Hosts, 6)
	require.Equal(t, []Port{{22, "tcp"}}, hostByIP(t, r.Hosts, "10.20.30.26").Ports, "the node itself, by its MAC")
	require.Equal(t, []Port{{80, "tcp"}, {443, "tcp"}}, hostByIP(t, r.Hosts, "10.20.30.33").Ports, "by MAC")
	require.Equal(t, []Port{{8080, "tcp"}}, hostByIP(t, r.Hosts, "10.20.30.40").Ports, "by address")
	require.Equal(t, []Port{}, hostByIP(t, r.Hosts, "10.20.30.50").Ports, "another device")
	require.True(t, hostByIP(t, r.Hosts, "10.20.30.60").TimedOut)
	require.Equal(t, []Port{}, hostByIP(t, r.Hosts, "10.20.30.70").Ports)
	require.False(t, hostByIP(t, r.Hosts, "10.20.30.70").TimedOut)

	// A second host scan carries them on again.
	clock.advance(time.Hour)
	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	st = waitLast(t, h)
	require.Equal(t, stamp(t0), st.Results[0].PortsAt)
	require.Equal(t, []Port{{80, "tcp"}, {443, "tcp"}}, hostByIP(t, st.Results[0].Hosts, "10.20.30.33").Ports)
}

// A host scan with no port scan before it has never looked for ports.
func TestScan_PortsAtNeverChecked(t *testing.T) {
	scan := &fakeScan{data: fixture(t, "nmap-discover.xml")}
	h := newHandler(t, openDB(t), nodeCommander(true), scan)
	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	st := waitLast(t, h)
	require.Equal(t, "", st.Results[0].PortsAt)
}

// A port scan stored before ports_at existed looked for ports when it
// finished.
func TestLoadResults_PortsAtOfAnOlderRow(t *testing.T) {
	db := openDB(t)
	_, err := db.Exec(`INSERT INTO lan_scans (network, result, finished_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"10.20.30.0/24", `{"network":"10.20.30.0/24","ports":true,"finished_at":"2026-11-21T10:00:00Z","hosts":[]}`, "2026-11-21T10:00:00Z",
		"10.20.31.0/24", `{"network":"10.20.31.0/24","ports":false,"finished_at":"2026-11-21T11:00:00Z","hosts":[]}`, "2026-11-21T11:00:00Z")
	require.NoError(t, err)
	stored, err := loadResults(db)
	require.NoError(t, err)
	require.Equal(t, "2026-11-21T10:00:00Z", stored["10.20.30.0/24"].PortsAt)
	require.Equal(t, "", stored["10.20.31.0/24"].PortsAt)
}

// GET answers the registry and the conflicts after a scan; a new handler on
// the same database answers the same.
func TestGetScan_DevicesAndConflicts(t *testing.T) {
	db := openDB(t)
	clock := newClock(t0)
	scan := &fakeScan{data: fixture(t, "nmap-discover.xml")}
	h := newHandler(t, db, nodeCommander(true), scan)
	h.now = clock.now

	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	st := waitLast(t, h)
	require.Equal(t, stateDone, st.Last.State)
	require.False(t, st.Last.Scheduled)

	check := func(t *testing.T, st Status) {
		// 27 hosts; the node itself (10.20.30.25 in this capture) carries
		// eth0's MAC, which 10.20.30.26 also answers with: 26 MACs.
		require.Len(t, st.Devices, 26)
		for i := 1; i < len(st.Devices); i++ {
			require.Less(t, st.Devices[i-1].MAC, st.Devices[i].MAC)
		}
		var plug Device
		for _, d := range st.Devices {
			if d.MAC == "02:42:ac:14:1e:1f" {
				plug = d
			}
		}
		require.Equal(t, Device{MAC: "02:42:ac:14:1e:1f", Network: "10.20.30.0/24", IP: "10.20.30.31",
			Hostname: "host-31.lan", FirstSeen: stamp(t0), LastSeen: stamp(t0)}, plug)
		require.Equal(t, []Conflict{}, st.Conflicts)
	}
	check(t, st)
	check(t, getStatus(t, newHandler(t, db, nodeCommander(true), &fakeScan{})))

	// Another device takes 10.20.30.12 while the first still answers there
	// an hour later: a conflict.
	clock.advance(time.Hour)
	scan.mu.Lock()
	scan.data = scanXML(xmlHostSpec{ip: "10.20.30.12", mac: "02:42:ac:14:1e:4d"})
	scan.mu.Unlock()
	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	waitLast(t, h)
	clock.advance(time.Hour)
	scan.mu.Lock()
	scan.data = fixture(t, "nmap-discover.xml")
	scan.mu.Unlock()
	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	st = waitLast(t, h)
	require.Equal(t, []Conflict{{Network: "10.20.30.0/24", IP: "10.20.30.12",
		MACs: []string{"02:42:ac:14:1e:0c", "02:42:ac:14:1e:4d"}}}, st.Conflicts)
}
