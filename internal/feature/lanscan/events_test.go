package lanscan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// notifying is a handler whose Notify hands each event to the channel it
// answers, on a clock the test moves.
func notifying(t *testing.T, db *sql.DB) (*Handler, *fakeScan, *testClock, chan ScanEvent) {
	t.Helper()
	scan := &fakeScan{}
	h := newHandler(t, db, nodeCommander(true), scan)
	clock := newClock(t0)
	h.now = clock.now
	events := make(chan ScanEvent, 16)
	h.Notify = func(ev ScanEvent) { events <- ev }
	return h, scan, clock, events
}

// startAndWait has start begin a scan, which waits until its goroutine is
// known, and returns once that goroutine has ended — Notify called or not —
// with what Notify was told, or nil.
func startAndWait(t *testing.T, h *Handler, scan *fakeScan, events chan ScanEvent, start func()) *ScanEvent {
	t.Helper()
	hold := make(chan struct{})
	scan.mu.Lock()
	scan.hold = hold
	scan.mu.Unlock()
	start()
	h.mu.Lock()
	done := h.done
	h.mu.Unlock()
	require.NotNil(t, done, "no scan is running")
	close(hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the scan's goroutine did not end")
	}
	select {
	case ev := <-events:
		return &ev
	default:
		return nil
	}
}

// scanLAN runs a host scan of the LAN that finds hosts and answers what
// Notify was told.
func scanLAN(t *testing.T, h *Handler, scan *fakeScan, events chan ScanEvent, hosts ...xmlHostSpec) *ScanEvent {
	t.Helper()
	scan.mu.Lock()
	scan.data, scan.err = scanXML(hosts...), nil
	scan.mu.Unlock()
	return startAndWait(t, h, scan, events, func() {
		code, env := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
		require.Equal(t, http.StatusOK, code, env.Error)
	})
}

var self = xmlHostSpec{ip: "10.20.30.26", self: true}

// New devices are the MACs a scan puts in the registry for the first time,
// in address order, with what the scan found of them. A network's first
// scan tells of none, even with another network's devices in the registry;
// a MAC labelled here before any scan saw it is not new.
func TestScanEvent_New(t *testing.T) {
	db := openDB(t)
	h, scan, clock, events := notifying(t, db)
	h.Interfaces = twoLANs(t)
	labelled := "02:42:ac:14:1e:2a"
	_, err := labelDevice(db, labelled, "Living room TV", "", "tv", stamp(t0), nil)
	require.NoError(t, err)

	// The other LAN first: its devices are not the LAN's.
	scan.mu.Lock()
	scan.data = scanXML(xmlHostSpec{ip: "10.20.31.5", mac: "02:42:ac:14:1f:05"})
	scan.mu.Unlock()
	ev := startAndWait(t, h, scan, events, func() {
		code, env := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.31.0/24","ports":false}`)
		require.Equal(t, http.StatusOK, code, env.Error)
	})
	require.NotNil(t, ev)
	require.Equal(t, "10.20.31.0/24", ev.Network)
	require.Empty(t, ev.New, "a network's first scan")

	clock.advance(time.Hour)
	ev = scanLAN(t, h, scan, events, self, xmlHostSpec{ip: "10.20.30.31", mac: "02:42:ac:14:1e:1f"})
	require.NotNil(t, ev)
	require.Equal(t, "10.20.30.0/24", ev.Network)
	require.Equal(t, t0.Add(time.Hour), ev.FinishedAt)
	require.False(t, ev.Scheduled)
	require.Empty(t, ev.New, "the LAN's first scan: everything on it is found at once, and that is not news")

	clock.advance(time.Hour)
	ev = scanLAN(t, h, scan, events, self,
		xmlHostSpec{ip: "10.20.30.31", mac: "02:42:ac:14:1e:1f"},
		xmlHostSpec{ip: "10.20.30.51", mac: "02:42:ac:14:1e:33", vendor: "Espressif", hostname: "plug.lan"},
		xmlHostSpec{ip: "10.20.30.40", mac: "02:42:ac:14:1e:28"},
		xmlHostSpec{ip: "10.20.30.42", mac: labelled},
		xmlHostSpec{ip: "10.20.30.60"}) // no MAC: not a device
	require.NotNil(t, ev)
	require.Equal(t, []SeenDevice{
		{IP: "10.20.30.40", MAC: "02:42:ac:14:1e:28"},
		{IP: "10.20.30.51", MAC: "02:42:ac:14:1e:33", Vendor: "Espressif", Hostname: "plug.lan"},
	}, ev.New)

	clock.advance(time.Hour)
	ev = scanLAN(t, h, scan, events, self, xmlHostSpec{ip: "10.20.30.51", mac: "02:42:ac:14:1e:33"})
	require.NotNil(t, ev)
	require.Empty(t, ev.New, "seen before")
}

// A watched device is down on the second scan in a row that misses it, and
// only then; it is back on the scan that finds it after two or more misses,
// and not after one. A device nobody watches is neither.
func TestScanEvent_DownAndBack(t *testing.T) {
	db := openDB(t)
	h, scan, clock, events := notifying(t, db)
	door, plug := "02:42:ac:14:1e:32", "02:42:ac:14:1e:1f"
	code, env := serve(t, h, http.MethodPut, "/api/v1/network/devices/"+door, `{"name":"Front door","kind":"camera","watch":true}`)
	require.Equal(t, http.StatusOK, code, env.Error)
	doorAt := xmlHostSpec{ip: "10.20.30.50", mac: door, vendor: "Example Electronics", hostname: "door.lan"}
	plugAt := xmlHostSpec{ip: "10.20.30.31", mac: plug}

	ev := scanLAN(t, h, scan, events, self, doorAt, plugAt)
	require.Empty(t, ev.Down)
	require.Empty(t, ev.Back)
	firstSeen := stamp(clock.now())

	steps := []struct {
		name       string
		found      []xmlHostSpec
		down, back bool
	}{
		{"missed once", nil, false, false},
		{"missed twice: down", nil, true, false},
		{"missed three times: not again", nil, false, false},
		{"found after three misses: back", []xmlHostSpec{doorAt}, false, true},
		{"missed once", nil, false, false},
		{"found after one miss: not back", []xmlHostSpec{doorAt}, false, false},
		{"missed once", nil, false, false},
		{"missed twice: down again", nil, true, false},
		{"found after two misses: back", []xmlHostSpec{doorAt}, false, true},
	}
	lastSeen := firstSeen
	for i, step := range steps {
		clock.advance(time.Hour)
		ev := scanLAN(t, h, scan, events, append([]xmlHostSpec{self}, step.found...)...)
		require.NotNil(t, ev, "step %d", i)
		if step.down {
			require.Equal(t, []KnownDevice{{MAC: door, Name: "Front door", IP: "10.20.30.50", Vendor: "Example Electronics",
				Hostname: "door.lan", LastSeen: lastSeen}}, ev.Down, "step %d: %s", i, step.name)
		} else {
			require.Empty(t, ev.Down, "step %d: %s", i, step.name)
		}
		if step.back {
			require.Equal(t, []KnownDevice{{MAC: door, Name: "Front door", IP: "10.20.30.50", Vendor: "Example Electronics",
				Hostname: "door.lan", LastSeen: stamp(clock.now())}}, ev.Back, "step %d: %s", i, step.name)
		} else {
			require.Empty(t, ev.Back, "step %d: %s", i, step.name)
		}
		if len(step.found) > 0 {
			lastSeen = stamp(clock.now())
		}
	}
	require.Equal(t, len(steps), missed(t, db, plug), "the plug, unwatched, was missed by every step and never told of")
}

// The certain conflict is a host answering at a node's address with another
// MAC; the suspected ones are lan_claims' overlaps. Certain ones come first,
// and an address listed as certain is not listed again as suspected.
func TestScanEvent_Conflicts(t *testing.T) {
	db := openDB(t)
	h, scan, clock, events := notifying(t, db)
	node, intruder := "02:42:ac:14:1e:0c", "02:42:ac:14:1e:4d"
	e, f := "02:42:ac:14:1e:4e", "02:42:ac:14:1e:4f"
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":0,
		"known":[{"address":"10.20.30.12","macs":["`+node+`"]},{"address":"10.20.30.26","macs":["02:42:ac:14:1e:99"]}]}`)
	at := func(ip, mac string) xmlHostSpec {
		return xmlHostSpec{ip: ip, mac: mac, vendor: "Example Electronics"}
	}
	// A suspected conflict on another network is that network's.
	for i, mac := range []string{"02:42:ac:14:1f:4e", "02:42:ac:14:1f:4f", "02:42:ac:14:1f:4e"} {
		when := t0.Add(time.Duration(i-3) * time.Hour)
		_, err := recordScan(db, Result{Network: "10.20.31.0/24", StartedAt: stamp(when), FinishedAt: stamp(when),
			Hosts: []Host{sighting("10.20.31.13", mac)}}, when)
		require.NoError(t, err)
	}
	require.Len(t, conflicts(t, db), 1)

	ev := scanLAN(t, h, scan, events, self, at("10.20.30.12", node), at("10.20.30.13", e))
	require.Equal(t, []AddressConflict{}, ev.Conflicts, "the node at its address, and this node's own answer, are no conflict")

	clock.advance(time.Hour)
	ev = scanLAN(t, h, scan, events, self, at("10.20.30.12", intruder), at("10.20.30.13", f))
	require.Equal(t, []AddressConflict{
		{IP: "10.20.30.12", MACs: []string{intruder}, Vendor: "Example Electronics", Certain: true},
	}, ev.Conflicts, "a handover is no suspected conflict yet")

	clock.advance(time.Hour)
	ev = scanLAN(t, h, scan, events, self, at("10.20.30.12", node), at("10.20.30.13", e))
	require.Equal(t, []AddressConflict{
		{IP: "10.20.30.12", MACs: []string{node, intruder}},
		{IP: "10.20.30.13", MACs: []string{e, f}},
	}, ev.Conflicts, "the node answers at its address again; the periods now overlap")

	clock.advance(time.Hour)
	ev = scanLAN(t, h, scan, events, self, at("10.20.30.12", intruder), at("10.20.30.13", e))
	require.Equal(t, []AddressConflict{
		{IP: "10.20.30.12", MACs: []string{intruder}, Vendor: "Example Electronics", Certain: true},
		{IP: "10.20.30.13", MACs: []string{e, f}},
	}, ev.Conflicts)
}

// A scan that failed or was cancelled stores nothing and tells nothing; the
// scan after it tells of itself, once.
func TestScanEvent_NotForAFailedOrCancelledScan(t *testing.T) {
	db := openDB(t)
	h, scan, _, events := notifying(t, db)

	scan.mu.Lock()
	scan.err = errors.New("exit status 1")
	scan.mu.Unlock()
	ev := startAndWait(t, h, scan, events, func() {
		code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
		require.Equal(t, http.StatusOK, code)
	})
	require.Nil(t, ev, "failed")
	require.Equal(t, stateFailed, getStatus(t, h).Last.State)

	// Held until it is cancelled. DELETE waits for the scan's goroutine to
	// end, Notify included.
	scan.mu.Lock()
	scan.data, scan.err, scan.hold = scanXML(self), nil, make(chan struct{})
	scan.mu.Unlock()
	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	require.Eventually(t, func() bool { return scan.callCount() == 2 }, 5*time.Second, time.Millisecond)
	code, env := call(t, h.CancelScan, http.MethodDelete, "")
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"cancelled":true}`, string(env.Data))
	require.Equal(t, stateCancelled, getStatus(t, h).Last.State)
	require.Empty(t, events, "cancelled")

	ev = scanLAN(t, h, scan, events, self)
	require.NotNil(t, ev)
	require.Equal(t, "10.20.30.0/24", ev.Network)
	require.Empty(t, events, "told once")
}

// Notify is called once the scan's state is let go: one that reads the
// handler does not wait on the scan it is told of, and reads it as done.
// A scheduled scan says so.
func TestScanEvent_OutsideTheLock(t *testing.T) {
	db := openDB(t)
	h, scan, clock, _ := notifying(t, db)
	type seen struct {
		ev   ScanEvent
		code int
		st   Status
	}
	told := make(chan seen, 4)
	h.Notify = func(ev ScanEvent) {
		code, env := call(t, h.GetScan, http.MethodGet, "")
		var st Status
		if code == http.StatusOK {
			_ = json.Unmarshal(env.Data, &st)
		}
		told <- seen{ev, code, st}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.StartScheduler(ctx)
	putSchedule(t, h, `{"network":"10.20.30.0/24","interval_minutes":15}`)
	clock.advance(scheduleHoldoff)
	scan.mu.Lock()
	scan.data = scanXML(self)
	scan.hold = nil
	scan.mu.Unlock()
	h.schedulePass()

	select {
	case s := <-told:
		require.Equal(t, http.StatusOK, s.code)
		require.Nil(t, s.st.Running)
		require.NotNil(t, s.st.Last)
		require.Equal(t, stateDone, s.st.Last.State)
		require.True(t, s.ev.Scheduled)
		require.Equal(t, "10.20.30.0/24", s.ev.Network)
	case <-time.After(5 * time.Second):
		t.Fatal("Notify was not called, or waits on the handler's lock")
	}
}
