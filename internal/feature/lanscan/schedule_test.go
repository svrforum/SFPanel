package lanscan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/api/response"
)

const lan = "10.20.30.0/24"

// twoLANs is the test node with a second LAN on eth2.
func twoLANs(t *testing.T) func() ([]Interface, error) {
	return func() ([]Interface, error) {
		return []Interface{eth0(t),
			{Name: "eth2", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1f:1a"), Addrs: prefixes(t, "10.20.31.26/24")}}, nil
	}
}

// scheduled is a handler whose scheduler started at t0, on a clock the test
// moves. Its goroutine wakes once a minute of real time, which no test
// lasts: every pass here is the test calling schedulePass.
func scheduled(t *testing.T, db *sql.DB, nmap bool, scan *fakeScan) (*Handler, *testClock) {
	t.Helper()
	h := newHandler(t, db, nodeCommander(nmap), scan)
	clock := newClock(t0)
	h.now = clock.now
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.StartScheduler(ctx)
	return h, clock
}

func putSchedule(t *testing.T, h *Handler, body string) Schedule {
	t.Helper()
	code, env := serve(t, h, http.MethodPut, "/api/v1/network/scan/schedule", body)
	require.Equal(t, http.StatusOK, code, env.Error)
	var s Schedule
	require.NoError(t, json.Unmarshal(env.Data, &s))
	return s
}

// pass runs one scheduler pass and waits for a scan it started to end.
func pass(t *testing.T, h *Handler) {
	t.Helper()
	h.schedulePass()
	require.Eventually(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.running == nil
	}, 5*time.Second, 2*time.Millisecond)
}

func scheduleOf(t *testing.T, db *sql.DB, network string) scheduleRow {
	t.Helper()
	row, ok, err := loadSchedule(db, network)
	require.NoError(t, err)
	require.True(t, ok)
	return row
}

func scheduleEntryOf(t *testing.T, h *Handler, network string) Schedule {
	t.Helper()
	for _, s := range getStatus(t, h).Schedules {
		if s.Network == network {
			return s
		}
	}
	t.Fatalf("no schedule for %s", network)
	return Schedule{}
}

func (f *fakeScan) portsCalls() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool{}, f.ports...)
}

// Nothing runs in the scheduler's first two minutes, nor on a handler whose
// scheduler never started.
func TestSchedule_NotBeforeTwoMinutes(t *testing.T) {
	scan := &fakeScan{data: scanXML()}
	h, clock := scheduled(t, openDB(t), true, scan)
	s := putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15}`)
	require.Equal(t, Schedule{Network: lan, IntervalMinutes: 15, NextRun: stamp(t0.Add(2 * time.Minute))}, s)

	clock.advance(2*time.Minute - time.Second)
	pass(t, h)
	require.Zero(t, scan.callCount())

	clock.advance(time.Second)
	pass(t, h)
	require.Equal(t, 1, scan.callCount())

	idle := &fakeScan{data: scanXML()}
	never := newHandler(t, openDB(t), nodeCommander(true), idle)
	never.now = newClock(t0.Add(time.Hour)).now
	putSchedule(t, never, `{"network":"10.20.30.0/24","interval_minutes":15}`)
	pass(t, never)
	require.Zero(t, idle.callCount())
}

// A host scan when the interval has passed since the last run, not before;
// last_run is when it started, and next_run follows it.
func TestSchedule_DueAndNotDue(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: scanXML()}
	h, clock := scheduled(t, db, true, scan)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15}`)

	clock.advance(2 * time.Minute)
	first := clock.now()
	pass(t, h)
	require.Equal(t, []bool{false}, scan.portsCalls(), "a host scan")
	require.Equal(t, stamp(first), scheduleOf(t, db, lan).LastRun)
	require.Equal(t, "", scheduleOf(t, db, lan).LastPortsRun)
	entry := scheduleEntryOf(t, h, lan)
	require.Equal(t, stamp(first), entry.LastRun)
	require.Equal(t, stamp(first.Add(15*time.Minute)), entry.NextRun)

	clock.advance(15*time.Minute - time.Second)
	pass(t, h)
	require.Equal(t, 1, scan.callCount(), "not due yet")

	clock.advance(time.Second)
	pass(t, h)
	require.Equal(t, 2, scan.callCount())
	require.Equal(t, stamp(first.Add(15*time.Minute)), scheduleOf(t, db, lan).LastRun)

	// Off: nothing, and no next run.
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":0}`)
	clock.advance(time.Hour)
	pass(t, h)
	require.Equal(t, 2, scan.callCount())
	entry = scheduleEntryOf(t, h, lan)
	require.Equal(t, "", entry.NextRun)
	require.Equal(t, "", entry.PausedReason)
}

// With ports_daily the first scan, and the one a day later, look for ports;
// the ones between only for hosts.
func TestSchedule_PortsDailyFirst(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: scanXML()}
	h, clock := scheduled(t, db, true, scan)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":360,"ports_daily":true}`)

	clock.advance(2 * time.Minute)
	first := clock.now()
	pass(t, h)
	require.Equal(t, []bool{true}, scan.portsCalls())
	row := scheduleOf(t, db, lan)
	require.Equal(t, stamp(first), row.LastRun, "a port scan finds hosts too")
	require.Equal(t, stamp(first), row.LastPortsRun)

	for i := 1; i <= 3; i++ {
		clock.advance(6 * time.Hour)
		pass(t, h)
	}
	require.Equal(t, []bool{true, false, false, false}, scan.portsCalls())
	require.Equal(t, stamp(first), scheduleOf(t, db, lan).LastPortsRun)
	// The next is the port scan, 24 hours after the first.
	require.Equal(t, stamp(first.Add(24*time.Hour)), scheduleEntryOf(t, h, lan).NextRun)

	// 24 hours on, hosts and ports are both due: ports first.
	clock.advance(6 * time.Hour)
	pass(t, h)
	require.Equal(t, []bool{true, false, false, false, true}, scan.portsCalls())
	require.Equal(t, stamp(first.Add(24*time.Hour)), scheduleOf(t, db, lan).LastPortsRun)
}

// Of two networks due, the first in network order starts; the other waits
// for the next pass.
func TestSchedule_OnePerPass(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: scanXML()}
	h, clock := scheduled(t, db, true, scan)
	h.Interfaces = twoLANs(t)
	putSchedule(t, h, `{"network":"10.20.31.0/24","interval_minutes":60}`)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":60}`)

	clock.advance(2 * time.Minute)
	pass(t, h)
	scan.mu.Lock()
	require.Equal(t, []string{lan}, scan.calls)
	scan.mu.Unlock()
	require.Equal(t, "", scheduleOf(t, db, "10.20.31.0/24").LastRun, "only the scan that started is recorded")

	clock.advance(time.Minute)
	pass(t, h)
	scan.mu.Lock()
	require.Equal(t, []string{lan, "10.20.31.0/24"}, scan.calls)
	scan.mu.Unlock()
}

// last_run is written when the scan starts: while it runs, and when it then
// fails, so a failing scan is not tried again every minute.
func TestSchedule_LastRunAtStart(t *testing.T) {
	db := openDB(t)
	hold := make(chan struct{})
	scan := &fakeScan{err: fmt.Errorf("exit status 1"), hold: hold}
	h, clock := scheduled(t, db, true, scan)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15}`)

	clock.advance(2 * time.Minute)
	start := clock.now()
	h.schedulePass()
	st := getStatus(t, h)
	require.NotNil(t, st.Running)
	require.True(t, st.Running.Scheduled)
	require.Equal(t, stamp(start), scheduleOf(t, db, lan).LastRun, "written before the scan ends")

	clock.advance(time.Minute)
	close(hold)
	st = waitLast(t, h)
	require.Equal(t, stateFailed, st.Last.State)
	require.True(t, st.Last.Scheduled)

	pass(t, h)
	require.Equal(t, 1, scan.callCount(), "the failed scan is not retried at once")
}

// No scheduled scan starts while a scan runs, nor is its run recorded.
func TestSchedule_NeverWhileAScanRuns(t *testing.T) {
	db := openDB(t)
	hold := make(chan struct{})
	scan := &fakeScan{data: scanXML(), hold: hold}
	h, clock := scheduled(t, db, true, scan)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15}`)
	clock.advance(2 * time.Minute)

	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusOK, code)
	h.schedulePass()
	require.Equal(t, "", scheduleOf(t, db, lan).LastRun)
	require.False(t, getStatus(t, h).Running.Scheduled)

	close(hold)
	st := waitLast(t, h)
	require.False(t, st.Last.Scheduled)
	pass(t, h)
	require.Equal(t, 2, scan.callCount())
	require.Equal(t, stamp(clock.now()), scheduleOf(t, db, lan).LastRun)
}

// A schedule that is on but cannot run says why, has no next run, starts
// nothing and records nothing.
func TestSchedule_Paused(t *testing.T) {
	cases := []struct {
		name   string
		nmap   bool
		setup  func(t *testing.T, h *Handler, db *sql.DB)
		reason string
	}{
		{"nmap missing", false, func(t *testing.T, h *Handler, db *sql.DB) {}, pausedNoTool},
		{"the node left the network", true, func(t *testing.T, h *Handler, db *sql.DB) {
			h.Interfaces = func() ([]Interface, error) {
				return []Interface{{Name: "eth0", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1a"), Addrs: prefixes(t, "10.20.40.26/24")}}, nil
			}
		}, pausedGone},
		{"another device answers at a node's address", true, func(t *testing.T, h *Handler, db *sql.DB) {
			require.NoError(t, saveResult(db, Result{Network: lan, Hosts: []Host{
				sighting("10.20.30.12", "02:42:ac:14:1e:4d")}}))
		}, pausedConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			scan := &fakeScan{data: scanXML()}
			h, clock := scheduled(t, db, tc.nmap, scan)
			putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15,"ports_daily":true,
				"known":[{"address":"10.20.30.12","macs":["02:42:ac:14:1e:0c"]}]}`)
			tc.setup(t, h, db)

			clock.advance(time.Hour)
			pass(t, h)
			require.Zero(t, scan.callCount())
			require.Equal(t, "", scheduleOf(t, db, lan).LastRun)
			require.Equal(t, "", scheduleOf(t, db, lan).LastPortsRun)
			entry := scheduleEntryOf(t, h, lan)
			require.Equal(t, tc.reason, entry.PausedReason)
			require.Equal(t, "", entry.NextRun)
		})
	}
}

// The conflict rule both ways: the node's own MAC at its address, or no
// stored result, is no conflict; another MAC there is, until a scan shows
// it gone. The scanning node's own answer never is. A manual scan is not
// refused for it.
func TestSchedule_ConflictRule(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: scanXML()}
	h, clock := scheduled(t, db, true, scan)
	known := `"known":[{"address":"10.20.30.12","macs":["02:42:ac:14:1e:0c","02:42:ac:14:1e:8c"]},
		{"address":"10.20.30.26","macs":["02:42:ac:14:1e:99"]}]`
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15,`+known+`}`)
	clock.advance(2 * time.Minute)
	require.Equal(t, "", scheduleEntryOf(t, h, lan).PausedReason, "no stored result: no conflict")

	// The node answers with one of its MACs; this node's own answer at
	// .26 carries eth0's MAC, which the list does not name for .26.
	require.NoError(t, saveResult(db, Result{Network: lan, Hosts: []Host{
		sighting("10.20.30.12", "02:42:ac:14:1e:8c"),
		{IP: "10.20.30.26", MAC: "02:42:ac:14:1e:1a", Self: true, Ports: []Port{}},
		sighting("10.20.30.31", "02:42:ac:14:1e:1f")}}))
	require.Equal(t, "", scheduleEntryOf(t, h, lan).PausedReason)
	pass(t, h)
	require.Equal(t, 1, scan.callCount())

	// Another device at .12.
	scan.mu.Lock()
	scan.data = scanXML(xmlHostSpec{ip: "10.20.30.12", mac: "02:42:ac:14:1e:4d"}, xmlHostSpec{ip: "10.20.30.26", self: true})
	scan.mu.Unlock()
	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	waitLast(t, h)
	require.Equal(t, pausedConflict, scheduleEntryOf(t, h, lan).PausedReason)
	clock.advance(time.Hour)
	pass(t, h)
	require.Equal(t, 2, scan.callCount(), "only the manual scan ran")

	// A manual scan is still allowed, and once it shows the node at its
	// address again the schedule runs.
	scan.mu.Lock()
	scan.data = scanXML(xmlHostSpec{ip: "10.20.30.12", mac: "02:42:ac:14:1e:0c"}, xmlHostSpec{ip: "10.20.30.26", self: true})
	scan.mu.Unlock()
	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	waitLast(t, h)
	require.Equal(t, "", scheduleEntryOf(t, h, lan).PausedReason)
	pass(t, h)
	require.Equal(t, 4, scan.callCount())
	require.True(t, getStatus(t, h).Last.Scheduled)
}

// PUT answers the entry and keeps known as given, MACs lower case; a
// second PUT keeps when the schedule last ran.
func TestPutSchedule_Stores(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: scanXML()}
	h, clock := scheduled(t, db, true, scan)
	clock.advance(2 * time.Minute)
	s := putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":60,"ports_daily":true,
		"known":[{"address":"10.20.30.12","macs":["02-42-AC-14-1E-0C","02:42:ac:14:1e:8c"]}]}`)
	require.Equal(t, Schedule{Network: lan, IntervalMinutes: 60, PortsDaily: true, NextRun: stamp(clock.now())}, s)
	var raw string
	require.NoError(t, db.QueryRow(`SELECT known FROM lan_schedules WHERE network = ?`, lan).Scan(&raw))
	require.JSONEq(t, `[{"address":"10.20.30.12","macs":["02:42:ac:14:1e:0c","02:42:ac:14:1e:8c"]}]`, raw)

	pass(t, h)
	ran := clock.now()
	clock.advance(time.Minute)
	s = putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15}`)
	require.Equal(t, Schedule{Network: lan, IntervalMinutes: 15, LastRun: stamp(ran), NextRun: stamp(ran.Add(15 * time.Minute))}, s)
	require.NoError(t, db.QueryRow(`SELECT known FROM lan_schedules WHERE network = ?`, lan).Scan(&raw))
	require.JSONEq(t, `[]`, raw)
}

func scheduleCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM lan_schedules`).Scan(&n))
	return n
}

// Each guard refuses for its own reason, and writes nothing.
func TestPutSchedule_Refused(t *testing.T) {
	knownOf := func(n int) string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprintf(`{"address":"10.20.30.%d","macs":["02:42:ac:14:1e:0c"]}`, i+1)
		}
		return "[" + strings.Join(list, ",") + "]"
	}
	macsOf := func(n int) string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprintf(`"02:42:ac:14:1e:%02x"`, i)
		}
		return `[{"address":"10.20.30.12","macs":[` + strings.Join(list, ",") + `]}]`
	}
	cases := []struct {
		name, body, code, field string
	}{
		{"a network not this node's", `{"network":"198.51.100.0/24","interval_minutes":60}`, response.ErrLanScanNetworkNotAllowed, ""},
		{"the LAN in another notation", `{"network":"10.20.30.5/24","interval_minutes":60}`, response.ErrLanScanNetworkNotAllowed, ""},
		{"a network too large to scan", `{"network":"172.20.0.0/16","interval_minutes":60}`, response.ErrLanScanNetworkNotAllowed, ""},
		{"Docker's bridge", `{"network":"172.17.0.0/16","interval_minutes":60}`, response.ErrLanScanNetworkNotAllowed, ""},
		{"an interval not offered", `{"network":"10.20.30.0/24","interval_minutes":30}`, response.ErrLanScheduleInvalid, "interval_minutes"},
		{"a negative interval", `{"network":"10.20.30.0/24","interval_minutes":-15}`, response.ErrLanScheduleInvalid, "interval_minutes"},
		{"65 known addresses", `{"network":"10.20.30.0/24","interval_minutes":60,"known":` + knownOf(65) + `}`, response.ErrLanScheduleInvalid, "known"},
		{"an IPv6 known address", `{"network":"10.20.30.0/24","interval_minutes":60,"known":[{"address":"fd00::12","macs":["02:42:ac:14:1e:0c"]}]}`, response.ErrLanScheduleInvalid, "known"},
		{"a known address that is not one", `{"network":"10.20.30.0/24","interval_minutes":60,"known":[{"address":"10.20.30.300","macs":["02:42:ac:14:1e:0c"]}]}`, response.ErrLanScheduleInvalid, "known"},
		{"a known address with no MAC", `{"network":"10.20.30.0/24","interval_minutes":60,"known":[{"address":"10.20.30.12","macs":[]}]}`, response.ErrLanScheduleInvalid, "known"},
		{"a known address with 17 MACs", `{"network":"10.20.30.0/24","interval_minutes":60,"known":` + macsOf(17) + `}`, response.ErrLanScheduleInvalid, "known"},
		{"a known MAC that is not one", `{"network":"10.20.30.0/24","interval_minutes":60,"known":[{"address":"10.20.30.12","macs":["router"]}]}`, response.ErrLanScheduleInvalid, "known"},
		{"a known EUI-64", `{"network":"10.20.30.0/24","interval_minutes":60,"known":[{"address":"10.20.30.12","macs":["02:42:ac:14:1e:0c:00:01"]}]}`, response.ErrLanScheduleInvalid, "known"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			h := newHandler(t, db, nodeCommander(true), nil)
			code, env := serve(t, h, http.MethodPut, "/api/v1/network/scan/schedule", tc.body)
			require.Equal(t, http.StatusBadRequest, code)
			require.NotNil(t, env.Error)
			require.Equal(t, tc.code, env.Error.Code)
			if tc.field != "" {
				require.Equal(t, map[string]string{"field": tc.field}, env.Error.Details)
			}
			require.Zero(t, scheduleCount(t, db), "nothing is written")
		})
	}
	// The limits themselves are accepted.
	db := openDB(t)
	h := newHandler(t, db, nodeCommander(true), nil)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":1440,"known":`+knownOf(64)+`}`)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":1440,"known":`+macsOf(16)+`}`)
}

func TestPutSchedule_BodyNotJSON(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"network":"10.20.30.0/24","interval_minutes":"60"}`} {
		db := openDB(t)
		h := newHandler(t, db, nodeCommander(true), nil)
		code, env := serve(t, h, http.MethodPut, "/api/v1/network/scan/schedule", body)
		require.Equal(t, http.StatusBadRequest, code, body)
		require.Equal(t, response.ErrInvalidRequest, env.Error.Code, body)
		require.Zero(t, scheduleCount(t, db))
	}
}

// GET lists every schedule in network order, one for a network this node
// has left included; a new handler on the same database answers the same
// schedules and devices.
func TestGetScan_SchedulesAndRestart(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: scanXML(xmlHostSpec{ip: "10.20.30.31", mac: "02:42:ac:14:1e:1f", vendor: "Espressif"})}
	h, clock := scheduled(t, db, true, scan)
	h.Interfaces = twoLANs(t)
	putSchedule(t, h, `{"network":"10.20.31.0/24","interval_minutes":0}`)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":60}`)
	clock.advance(2 * time.Minute)
	pass(t, h)
	ran := clock.now()
	code, _ := serve(t, h, http.MethodPut, "/api/v1/network/devices/02:42:ac:14:1e:1f", `{"name":"Plug","kind":"iot"}`)
	require.Equal(t, http.StatusOK, code)

	h.Interfaces = testInterfaces(t) // eth2 is gone
	want := []Schedule{
		{Network: lan, IntervalMinutes: 60, LastRun: stamp(ran), NextRun: stamp(ran.Add(time.Hour))},
		{Network: "10.20.31.0/24"},
	}
	require.Equal(t, want, getStatus(t, h).Schedules)

	fresh := newHandler(t, db, nodeCommander(true), &fakeScan{})
	fresh.now = clock.now
	st := getStatus(t, fresh)
	require.Equal(t, want, st.Schedules)
	require.Len(t, st.Devices, 1)
	require.Equal(t, "Plug", st.Devices[0].Name)
	require.Equal(t, "Espressif", st.Devices[0].Vendor)
	require.Equal(t, stamp(ran), st.Devices[0].LastSeen)
}
