package packages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// fakeHost is a Commander that also plays systemd for the transaction tests.
// systemd-run "runs" the unit by writing what it would write to the log its
// command line names; systemctl answers from what has run. Nothing here
// starts a process.
type fakeHost struct {
	mock *exec.MockCommander // answers by command name, and "exists:<name>"

	mu     sync.Mutex
	log    []exec.MockCall
	byArgs map[string]exec.MockResult // "name arg…" → answer, ahead of mock
	envs   map[string][]string        // "name arg…" → the environment it ran with
	units  map[string]string          // unit → its log
	active map[string]bool

	// What a unit writes when it starts, whether it then keeps running
	// until end() or kill(), and the status it exits with otherwise.
	unitOut string
	hold    bool
	exit    int

	// live is what `systemctl list-units` prints.
	live string

	// updateGate, when set, holds apt-get update until it is closed or the
	// caller's context ends, as a real apt-get update bound to it would.
	updateGate chan struct{}
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		mock:    exec.NewMockCommander(),
		byArgs:  map[string]exec.MockResult{},
		envs:    map[string][]string{},
		units:   map[string]string{},
		active:  map[string]bool{},
		unitOut: "Setting up example (1.0) ...\n",
	}
}

func key(name string, args []string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

// answer records the call and says what it returns.
func (f *fakeHost) answer(name string, args []string) (string, error) {
	f.record(name, args)
	return f.result(name, args)
}

func (f *fakeHost) record(name string, args []string) {
	f.mu.Lock()
	f.log = append(f.log, exec.MockCall{Name: name, Args: append([]string(nil), args...)})
	f.mu.Unlock()
}

func (f *fakeHost) result(name string, args []string) (string, error) {
	f.mu.Lock()
	r, ok := f.byArgs[key(name, args)]
	f.mu.Unlock()
	if ok {
		return r.Output, r.Err
	}
	return f.mock.Run(name, args...)
}

func (f *fakeHost) Run(name string, args ...string) (string, error) {
	out, err := f.answer(name, args)
	return f.systemctl(name, args, out, err)
}

// systemctl answers is-active and list-units from what has run.
func (f *fakeHost) systemctl(name string, args []string, out string, err error) (string, error) {
	if name == "systemctl" && len(args) >= 2 {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch args[0] {
		case "is-active":
			if f.active[args[1]] {
				return "active\n", nil
			}
			return "inactive\n", errors.New("exit status 3")
		case "list-units":
			return f.live, nil
		}
	}
	return out, err
}

func (f *fakeHost) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	out, err := f.answer(name, args)
	if name == "systemd-run" && err == nil {
		f.startUnit(args)
	}
	return f.systemctl(name, args, out, err)
}

func (f *fakeHost) RunWithEnv(env []string, name string, args ...string) (string, error) {
	f.recordEnv(name, args, env)
	return f.answer(name, args)
}

func (f *fakeHost) RunWithEnvCtx(ctx context.Context, env []string, name string, args ...string) (string, error) {
	f.recordEnv(name, args, env)
	f.record(name, args)
	if name == "apt-get" && len(args) > 0 && args[0] == "update" {
		f.mu.Lock()
		gate := f.updateGate
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return f.result(name, args)
}

func (f *fakeHost) RunWithInput(_ string, name string, args ...string) (string, error) {
	return f.answer(name, args)
}

func (f *fakeHost) RunCtx(_ context.Context, name string, args ...string) (string, error) {
	return f.answer(name, args)
}

func (f *fakeHost) Exists(name string) bool {
	f.mu.Lock()
	f.log = append(f.log, exec.MockCall{Name: "exists:" + name})
	f.mu.Unlock()
	return f.mock.Exists(name)
}

func (f *fakeHost) recordEnv(name string, args, env []string) {
	f.mu.Lock()
	f.envs[key(name, args)] = env
	f.mu.Unlock()
}

// set answers name with output (and err) whatever its arguments.
func (f *fakeHost) set(name, output string, err error) {
	f.mock.SetOutput(name, output, err)
}

// setArgs answers exactly "name args…" with output and err.
func (f *fakeHost) setArgs(cmdline, output string, err error) {
	f.mu.Lock()
	f.byArgs[cmdline] = exec.MockResult{Output: output, Err: err}
	f.mu.Unlock()
}

// calls returns the recorded calls of name, in order.
func (f *fakeHost) calls(name string) []exec.MockCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []exec.MockCall
	for _, c := range f.log {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeHost) all() []exec.MockCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]exec.MockCall(nil), f.log...)
}

// startUnit is systemd running what systemd-run was given.
func (f *fakeHost) startUnit(args []string) {
	var unit, log string
	for i, a := range args {
		if u, ok := strings.CutPrefix(a, "--unit="); ok {
			unit = u
		}
		if a == "-p" && i+1 < len(args) {
			if p, ok := strings.CutPrefix(args[i+1], "StandardOutput=append:"); ok {
				log = p
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.units[unit] = log
	appendFile(log, f.unitOut)
	if f.hold {
		f.active[unit] = true
		return
	}
	appendFile(log, txExitMarker+strconv.Itoa(f.exit)+"\n")
}

// started is the one unit systemd-run has started, once it has.
func (f *fakeHost) started() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for unit := range f.units {
		return unit
	}
	return ""
}

// end is the held unit's apt-get returning with code.
func (f *fakeHost) end(unit string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	appendFile(f.units[unit], "Processing triggers for man-db (2.13.0-1) ...\n"+txExitMarker+strconv.Itoa(code)+"\n")
	f.active[unit] = false
}

// kill is the held unit stopped before its script could say how it ended.
func (f *fakeHost) kill(unit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active[unit] = false
}

func appendFile(path, text string) {
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.WriteString(text)
}

// txSetup points the transaction machinery at a temp dir and a fake host.
func txSetup(t *testing.T, pve bool) (*Handler, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	prevDir, prevPoll, prevKeep, prevSys, prevLock := txLogDir, txPoll, txKeepalive, systemdActive, dpkgLockHeld
	txLogDir, txPoll, txKeepalive = dir, 2*time.Millisecond, time.Hour
	systemdActive = func() bool { return true }
	dpkgLockHeld = func() bool { return false }
	t.Cleanup(func() {
		txLogDir, txPoll, txKeepalive, systemdActive, dpkgLockHeld = prevDir, prevPoll, prevKeep, prevSys, prevLock
	})
	if pve {
		platform.SetForTest(t, platform.Info{OSID: "debian", OSVersionID: "13", PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2})
	} else {
		platform.SetForTest(t, platform.Info{OSID: "ubuntu", OSVersionID: "24.04", NetworkStack: platform.StackNetplan})
	}
	f := newFakeHost()
	f.set("exists:systemd-run", "", nil)
	return &Handler{Cmd: f}, f, dir
}

// call runs one request through fn and returns what it answered.
func call(t *testing.T, fn echo.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return callCtx(t, context.Background(), fn, method, target, body)
}

func callCtx(t *testing.T, ctx context.Context, fn echo.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(ctx)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	if err := fn(echo.New().NewContext(req, rec)); err != nil {
		t.Error(err)
	}
	return rec
}

// failure decodes an error answer.
func failure(t *testing.T, rec *httptest.ResponseRecorder) (code string, details map[string]string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not a JSON error (%d): %s", rec.Code, rec.Body.String())
	}
	return body.Error.Code, body.Error.Details
}

// currentTx is GET /packages/transaction's transaction.
func currentTx(t *testing.T, h *Handler) *txView {
	t.Helper()
	rec := call(t, h.GetTransaction, http.MethodGet, "/", "")
	var body struct {
		Data struct {
			Transaction *txView `json:"transaction"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return body.Data.Transaction
}

// eventually waits for cond, failing the test with what after two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// settle waits for the handler's transaction to end, so that nothing still
// runs when the test puts the package's settings back.
func settle(t *testing.T, h *Handler) {
	t.Helper()
	h.txMu.Lock()
	tx := h.tx
	h.txMu.Unlock()
	if tx == nil {
		return
	}
	select {
	case <-tx.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the transaction never ended")
	}
}

// sseData is the data lines of an SSE body.
func sseData(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if d, ok := strings.CutPrefix(line, "data: "); ok {
			out = append(out, d)
		}
	}
	return out
}
