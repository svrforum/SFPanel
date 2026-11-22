package lanscan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	sfdb "github.com/svrforum/SFPanel/internal/db"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sfdb.Open(filepath.Join(t.TempDir(), "panel.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// The node the handler tests run on: the LAN on eth0, Docker's bridge, and a
// private /16 that is listed but too large to scan.
func testInterfaces(t *testing.T) func() ([]Interface, error) {
	return func() ([]Interface, error) {
		return []Interface{
			{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, Addrs: prefixes(t, "127.0.0.1/8")},
			eth0(t),
			{Name: "docker0", Flags: lanFlags, MAC: mac(t, "02:42:ac:11:00:01"), Addrs: prefixes(t, "172.17.0.1/16")},
			{Name: "eth1", Flags: lanFlags, MAC: mac(t, "02:42:ac:14:1e:1b"), Addrs: prefixes(t, "172.20.5.9/16")},
		}, nil
	}
}

// nodeCommander answers `ip -4 route show default` for the test node and,
// with nmap true, says nmap is installed.
func nodeCommander(nmap bool) *exec.MockCommander {
	m := exec.NewMockCommander()
	m.SetOutput("ip", eth0Route, nil)
	if nmap {
		m.SetOutput("exists:nmap", "", nil)
	}
	return m
}

// fakeScan stands in for nmap: it records each call and answers data/err,
// or, with hold set, waits for hold to close or the scan to be stopped.
type fakeScan struct {
	mu    sync.Mutex
	calls []string
	ports []bool
	data  []byte
	err   error
	hold  chan struct{}
}

func (f *fakeScan) run(ctx context.Context, cidr string, ports bool) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cidr)
	f.ports = append(f.ports, ports)
	data, err, hold := f.data, f.err, f.hold
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return data, err
}

func (f *fakeScan) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newHandler(t *testing.T, db *sql.DB, cmd exec.Commander, scan *fakeScan) *Handler {
	h := &Handler{DB: db, Cmd: cmd, Interfaces: testInterfaces(t)}
	if scan != nil {
		h.scan = scan.run
	}
	return h
}

type envelope struct {
	Success bool                `json:"success"`
	Data    json.RawMessage     `json:"data"`
	Error   *response.ErrorBody `json:"error"`
}

func call(t *testing.T, fn echo.HandlerFunc, method, body string) (int, envelope) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/v1/network/scan", nil)
	} else {
		req = httptest.NewRequest(method, "/api/v1/network/scan", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	require.NoError(t, fn(echo.New().NewContext(req, rec)))
	var env envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return rec.Code, env
}

func getStatus(t *testing.T, h *Handler) Status {
	t.Helper()
	code, env := call(t, h.GetScan, http.MethodGet, "")
	require.Equal(t, http.StatusOK, code)
	var st Status
	require.NoError(t, json.Unmarshal(env.Data, &st))
	return st
}

// waitLast polls GET until the scan has ended, and returns that status.
func waitLast(t *testing.T, h *Handler) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := getStatus(t, h); st.Last != nil && st.Running == nil {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the scan did not end")
	return Status{}
}

func nmapCalls(m *exec.MockCommander) int {
	n := 0
	for _, c := range m.Calls {
		if c.Name == "nmap" {
			n++
		}
	}
	return n
}

// The shape the page reads, before any scan: the networks, null running and
// last, and results, devices, conflicts and schedules [] rather than null.
func TestGetScan_Shape(t *testing.T) {
	h := newHandler(t, openDB(t), nodeCommander(true), &fakeScan{})
	code, env := call(t, h.GetScan, http.MethodGet, "")
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{
		"installed": true,
		"networks": [
			{"cidr":"10.20.30.0/24","interface":"eth0","address":"10.20.30.26","mac":"02:42:ac:14:1e:1a",
			 "gateway":"10.20.30.1","scannable":true,"reason":""},
			{"cidr":"172.20.0.0/16","interface":"eth1","address":"172.20.5.9","mac":"02:42:ac:14:1e:1b",
			 "gateway":"","scannable":false,"reason":"too_large"}
		],
		"running": null,
		"last": null,
		"results": [],
		"devices": [],
		"conflicts": [],
		"schedules": []
	}`, string(env.Data))
}

// Anything but a scannable network's cidr, as GET spells it, is refused
// before anything runs. The request's string never reaches nmap.
func TestStartScan_RefusesNetworkNotListed(t *testing.T) {
	cases := []struct{ name, network string }{
		{"a public network", "198.51.100.0/24"},
		{"Docker's bridge", "172.17.0.0/16"},
		{"a wider network around the LAN", "10.20.0.0/16"},
		{"the LAN in another notation", "10.20.30.5/24"},
		{"the LAN with a space", " 10.20.30.0/24"},
		{"a listed network too large to scan", "172.20.0.0/16"},
		{"an nmap option", "-iL /etc/shadow"},
		{"the LAN followed by an option", "10.20.30.0/24 --script=x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := nodeCommander(true)
			scan := &fakeScan{data: fixture(t, "nmap-ports.xml")}
			h := newHandler(t, openDB(t), cmd, scan)
			body, err := json.Marshal(map[string]any{"network": tc.network, "ports": true})
			require.NoError(t, err)

			code, env := call(t, h.StartScan, http.MethodPost, string(body))
			require.Equal(t, http.StatusBadRequest, code)
			require.NotNil(t, env.Error)
			require.Equal(t, response.ErrLanScanNetworkNotAllowed, env.Error.Code)

			// Nothing ran, and nothing is running.
			time.Sleep(20 * time.Millisecond)
			require.Zero(t, scan.callCount())
			require.Zero(t, nmapCalls(cmd))
			st := getStatus(t, h)
			require.Nil(t, st.Running)
			require.Nil(t, st.Last)
		})
	}
}

func TestStartScan_InvalidBody(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"network":""}`, `{"network":42}`} {
		t.Run(body, func(t *testing.T) {
			scan := &fakeScan{}
			h := newHandler(t, openDB(t), nodeCommander(true), scan)
			code, env := call(t, h.StartScan, http.MethodPost, body)
			require.Equal(t, http.StatusBadRequest, code)
			require.Equal(t, response.ErrInvalidRequest, env.Error.Code)
			require.Zero(t, scan.callCount())
		})
	}
}

func TestStartScan_NmapMissing(t *testing.T) {
	cmd := nodeCommander(false)
	scan := &fakeScan{data: fixture(t, "nmap-ports.xml")}
	h := newHandler(t, openDB(t), cmd, scan)

	code, env := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, response.ErrToolNotInstalled, env.Error.Code)
	time.Sleep(20 * time.Millisecond)
	require.Zero(t, scan.callCount())
	require.False(t, getStatus(t, h).Installed)
}

// A node runs one scan at a time.
func TestStartScan_OneAtATime(t *testing.T) {
	hold := make(chan struct{})
	scan := &fakeScan{data: fixture(t, "nmap-discover.xml"), hold: hold}
	h := newHandler(t, openDB(t), nodeCommander(true), scan)

	code, env := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"started":true}`, string(env.Data))

	st := getStatus(t, h)
	require.NotNil(t, st.Running)
	require.Equal(t, "10.20.30.0/24", st.Running.Network)
	require.False(t, st.Running.Ports)
	_, err := time.Parse(time.RFC3339, st.Running.StartedAt)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(st.Running.StartedAt, "Z"), st.Running.StartedAt)

	code, env = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, response.ErrLanScanInProgress, env.Error.Code)

	close(hold)
	require.Equal(t, stateDone, waitLast(t, h).Last.State)
	require.Equal(t, 1, scan.callCount())
}

// A finished scan is stored: GET answers it, and so does a new handler on
// the same database (the panel restarted or was updated). The node itself
// carries the scanned interface's MAC.
func TestScan_DoneIsStored(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: fixture(t, "nmap-ports.xml")}
	h := newHandler(t, db, nodeCommander(true), scan)

	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusOK, code)
	st := waitLast(t, h)
	scan.mu.Lock()
	require.Equal(t, []string{"10.20.30.0/24"}, scan.calls)
	scan.mu.Unlock()
	require.Equal(t, "10.20.30.0/24", st.Last.Network)
	require.True(t, st.Last.Ports)
	require.Equal(t, stateDone, st.Last.State)
	_, err := time.Parse(time.RFC3339, st.Last.At)
	require.NoError(t, err)

	check := func(t *testing.T, st Status) {
		require.Len(t, st.Results, 1)
		r := st.Results[0]
		require.Equal(t, "10.20.30.0/24", r.Network)
		require.Equal(t, "eth0", r.Interface)
		require.Equal(t, "10.20.30.26", r.Address)
		require.Equal(t, "10.20.30.1", r.Gateway)
		require.True(t, r.Ports)
		require.Len(t, r.Hosts, 28)
		self := hostByIP(t, r.Hosts, "10.20.30.26")
		require.True(t, self.Self)
		require.Equal(t, "02:42:ac:14:1e:1a", self.MAC)
		require.Equal(t, []Port{{22, "tcp"}, {8080, "tcp"}, {8443, "tcp"}}, self.Ports)
		started, err := time.Parse(time.RFC3339, r.StartedAt)
		require.NoError(t, err)
		finished, err := time.Parse(time.RFC3339, r.FinishedAt)
		require.NoError(t, err)
		require.False(t, finished.Before(started))
	}
	check(t, st)

	fresh := newHandler(t, db, nodeCommander(true), &fakeScan{})
	again := getStatus(t, fresh)
	require.Nil(t, again.Running)
	require.Nil(t, again.Last)
	check(t, again)
}

// A scan that fails stores nothing: the earlier result stays, and last says
// it failed. The failed output here is well-formed XML with no finished
// run, which only the <runstats> check refuses.
func TestScan_FailedKeepsEarlierResult(t *testing.T) {
	db := openDB(t)
	scan := &fakeScan{data: fixture(t, "nmap-discover.xml")}
	h := newHandler(t, db, nodeCommander(true), scan)

	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":false}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, stateDone, waitLast(t, h).Last.State)

	ports := string(fixture(t, "nmap-ports.xml"))
	cutShort := ports[:strings.Index(ports, "<runstats>")] + "</nmaprun>\n"
	for _, failure := range []struct {
		name string
		data []byte
		err  error
	}{
		{"output without a finished run", []byte(cutShort), nil},
		{"nmap exited with an error", nil, errors.New("exit status 1")},
	} {
		t.Run(failure.name, func(t *testing.T) {
			scan.mu.Lock()
			scan.data, scan.err = failure.data, failure.err
			scan.mu.Unlock()

			code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
			require.Equal(t, http.StatusOK, code)
			st := waitLast(t, h)
			require.Equal(t, stateFailed, st.Last.State)
			require.True(t, st.Last.Ports)

			require.Len(t, st.Results, 1)
			require.False(t, st.Results[0].Ports, "the earlier host scan is still the result")
			require.Len(t, st.Results[0].Hosts, 27)
		})
	}
}

// DELETE stops the scan: nothing is stored and last says cancelled.
func TestScan_Cancel(t *testing.T) {
	scan := &fakeScan{data: fixture(t, "nmap-ports.xml"), hold: make(chan struct{})}
	h := newHandler(t, openDB(t), nodeCommander(true), scan)

	code, env := call(t, h.CancelScan, http.MethodDelete, "")
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"cancelled":false}`, string(env.Data))

	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusOK, code)
	require.Eventually(t, func() bool { return scan.callCount() == 1 }, 5*time.Second, 5*time.Millisecond)

	code, env = call(t, h.CancelScan, http.MethodDelete, "")
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"cancelled":true}`, string(env.Data))

	// DELETE waited for the scan to end: the next GET already says so.
	st := getStatus(t, h)
	require.Nil(t, st.Running)
	require.NotNil(t, st.Last)
	require.Equal(t, stateCancelled, st.Last.State)
	require.Empty(t, st.Results)

	// And a new scan may start at once.
	scan.mu.Lock()
	scan.hold = nil
	scan.mu.Unlock()
	code, _ = call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, stateDone, waitLast(t, h).Last.State)
}

// nmapWriter stands in for the nmap binary behind the Commander: it writes
// the XML where -oX says, and prints a warning the way nmap does on stderr.
type nmapWriter struct {
	*exec.MockCommander
	xml []byte

	mu      sync.Mutex
	args    []string
	dirMode os.FileMode
}

func (w *nmapWriter) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	if name != "nmap" {
		return w.MockCommander.RunCtx(ctx, name, args...)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.args = args
	for i, a := range args {
		if a == "-oX" && i+1 < len(args) {
			st, err := os.Stat(filepath.Dir(args[i+1]))
			if err != nil {
				return "", err
			}
			w.dirMode = st.Mode().Perm()
			if err := os.WriteFile(args[i+1], w.xml, 0o600); err != nil {
				return "", err
			}
		}
	}
	return "Warning: 10.20.30.99 giving up on port because retransmission cap hit (2).\n", nil
}

// The real runner: nmap's arguments, its XML read from a file in a 0700
// directory rather than from its output (where stderr warnings land), and
// the directory gone afterwards.
func TestRunNmap_ReadsXMLFromPrivateFile(t *testing.T) {
	w := &nmapWriter{MockCommander: nodeCommander(true), xml: fixture(t, "nmap-ports.xml")}
	h := newHandler(t, openDB(t), w, nil)

	code, _ := call(t, h.StartScan, http.MethodPost, `{"network":"10.20.30.0/24","ports":true}`)
	require.Equal(t, http.StatusOK, code)
	st := waitLast(t, h)
	require.Equal(t, stateDone, st.Last.State)
	require.Len(t, st.Results, 1)
	require.Len(t, st.Results[0].Hosts, 28)

	w.mu.Lock()
	defer w.mu.Unlock()
	require.Len(t, w.args, 9)
	file := w.args[7]
	require.Equal(t, nmapArgs("10.20.30.0/24", true, file), w.args)
	require.Equal(t, "scan.xml", filepath.Base(file))
	require.Equal(t, os.FileMode(0o700), w.dirMode)
	_, err := os.Stat(filepath.Dir(file))
	require.True(t, os.IsNotExist(err), "the output directory is removed: %v", err)
}
