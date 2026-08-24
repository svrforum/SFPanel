package firewall

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// Real captured output from `ufw status numbered` covering IPv4, IPv6, comments,
// LIMIT, REJECT, ports, ranges, and the "(v6)" duplication that ufw emits
// alongside its dual-stack rules.
const ufwStatusNumberedFixture = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere                   # SSH
[ 2] 80/tcp                     ALLOW IN    Anywhere
[ 3] 443                        ALLOW IN    192.168.1.0/24             # admin LAN only
[ 4] 5432/tcp                   DENY IN     Anywhere
[ 5] 22/tcp                     LIMIT IN    Anywhere
[ 6] 8080/tcp                   REJECT IN   Anywhere
[ 7] 22/tcp (v6)                ALLOW IN    Anywhere (v6)              # SSH
[ 8] 80/tcp (v6)                ALLOW IN    Anywhere (v6)
[ 9] 9000:9100/tcp              ALLOW IN    Anywhere
`

func TestParseUFWRules(t *testing.T) {
	got := parseUFWRules(ufwStatusNumberedFixture)
	if len(got) != 9 {
		t.Fatalf("expected 9 rules, got %d", len(got))
	}

	wantSelected := map[int]UFWRule{
		1: {Number: 1, To: "22/tcp", Action: "ALLOW IN", From: "Anywhere", Comment: "SSH", V6: false},
		3: {Number: 3, To: "443", Action: "ALLOW IN", From: "192.168.1.0/24", Comment: "admin LAN only", V6: false},
		5: {Number: 5, To: "22/tcp", Action: "LIMIT IN", From: "Anywhere", V6: false},
		6: {Number: 6, To: "8080/tcp", Action: "REJECT IN", From: "Anywhere", V6: false},
		7: {Number: 7, To: "22/tcp", Action: "ALLOW IN", From: "Anywhere", Comment: "SSH", V6: true},
		9: {Number: 9, To: "9000:9100/tcp", Action: "ALLOW IN", From: "Anywhere", V6: false},
	}
	for _, rule := range got {
		want, ok := wantSelected[rule.Number]
		if !ok {
			continue
		}
		if !reflect.DeepEqual(rule, want) {
			t.Errorf("rule %d: got %+v, want %+v", rule.Number, rule, want)
		}
	}
}

func TestParseUFWRules_Empty(t *testing.T) {
	got := parseUFWRules("Status: inactive\n")
	if len(got) != 0 {
		t.Errorf("expected 0 rules from inactive output, got %d", len(got))
	}
}

func TestParseUFWRules_GarbledLineSkipped(t *testing.T) {
	// A line that doesn't match `[ N] …` should be silently skipped, not
	// crash the parser.
	got := parseUFWRules(`[ 1] 22/tcp ALLOW IN Anywhere
not-a-rule line
[ 2] 80/tcp ALLOW IN Anywhere`)
	if len(got) != 2 {
		t.Errorf("expected 2 rules, got %d", len(got))
	}
}

const ssTcpFixture = `State   Recv-Q  Send-Q   Local Address:Port    Peer Address:Port  Process
LISTEN  0       128      0.0.0.0:22            0.0.0.0:*          users:(("sshd",pid=1234,fd=3))
LISTEN  0       4096     127.0.0.1:9443        0.0.0.0:*          users:(("sfpanel",pid=3361,fd=16))
LISTEN  0       128      [::]:22               [::]:*             users:(("sshd",pid=1234,fd=4))
LISTEN  0       128      [::1]:6443            [::]:*             users:(("kube-apiserver",pid=5555,fd=12))
`

func TestParseSSOutput(t *testing.T) {
	got := parseSSOutput(ssTcpFixture, "tcp")
	if len(got) != 4 {
		t.Fatalf("expected 4 listening ports, got %d", len(got))
	}

	want := []ListeningPort{
		{Protocol: "tcp", Address: "0.0.0.0", Port: 22, PID: 1234, Process: "sshd"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9443, PID: 3361, Process: "sfpanel"},
		{Protocol: "tcp", Address: "::", Port: 22, PID: 1234, Process: "sshd"},
		{Protocol: "tcp", Address: "::1", Port: 6443, PID: 5555, Process: "kube-apiserver"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestParseSSOutput_HeaderAndEmptyLines(t *testing.T) {
	got := parseSSOutput(`State Recv-Q Send-Q Local Address:Port Peer Address:Port Process
Netid State Recv-Q ...

`, "tcp")
	if len(got) != 0 {
		t.Errorf("expected 0 ports from header-only output, got %d", len(got))
	}
}

func TestParseSSOutput_MissingProcessIsAccepted(t *testing.T) {
	// ss without `-p` (or running as non-root) emits no users:((..)) section.
	// We still want the listening port recorded; PID/Process stay zero.
	got := parseSSOutput(`LISTEN 0 128 0.0.0.0:80 0.0.0.0:*
`, "tcp")
	if len(got) != 1 {
		t.Fatalf("expected 1 port, got %d", len(got))
	}
	if got[0].PID != 0 || got[0].Process != "" {
		t.Errorf("expected empty PID/Process, got pid=%d process=%q", got[0].PID, got[0].Process)
	}
	if got[0].Port != 80 {
		t.Errorf("port mismatch, got %d", got[0].Port)
	}
}

func TestParseAddressPort_IPv4(t *testing.T) {
	addr, port := parseAddressPort("127.0.0.1:9443")
	if addr != "127.0.0.1" || port != 9443 {
		t.Errorf("got (%q,%d); want (\"127.0.0.1\",9443)", addr, port)
	}
}

func TestParseAddressPort_IPv6(t *testing.T) {
	addr, port := parseAddressPort("[::1]:6443")
	if addr != "::1" || port != 6443 {
		t.Errorf("got (%q,%d); want (\"::1\",6443)", addr, port)
	}
}

func TestParseAddressPort_Wildcard(t *testing.T) {
	addr, port := parseAddressPort("*:22")
	if addr != "*" || port != 22 {
		t.Errorf("got (%q,%d); want (\"*\",22)", addr, port)
	}
}

// ---- args-aware Commander for EnableUFW tests ----
//
// MockCommander keys outputs only on the binary name, so `ufw status
// numbered` and `ufw show added` collide. For the EnableUFW lockout
// precheck we need to return different output per sub-command — hence
// this small test-local Commander that dispatches on the joined args.

type ufwArgsCommander struct {
	mu      sync.Mutex
	outputs map[string]string // key: name + " " + args
	errs    map[string]error
	exists  map[string]bool
	calls   []string
}

func newUFWArgsCommander() *ufwArgsCommander {
	return &ufwArgsCommander{
		outputs: make(map[string]string),
		errs:    make(map[string]error),
		exists:  make(map[string]bool),
	}
}

func (m *ufwArgsCommander) set(cmdline, out string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputs[cmdline] = out
	if err != nil {
		m.errs[cmdline] = err
	}
}

func (m *ufwArgsCommander) setExists(name string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exists[name] = ok
}

func (m *ufwArgsCommander) record(name string, args ...string) (string, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, key)
	return m.outputs[key], m.errs[key]
}

func (m *ufwArgsCommander) Run(name string, args ...string) (string, error) {
	return m.record(name, args...)
}
func (m *ufwArgsCommander) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return m.record(name, args...)
}
func (m *ufwArgsCommander) RunWithEnv(_ []string, name string, args ...string) (string, error) {
	return m.record(name, args...)
}
func (m *ufwArgsCommander) RunWithInput(_ string, name string, args ...string) (string, error) {
	return m.record(name, args...)
}
func (m *ufwArgsCommander) RunWithEnvCtx(ctx context.Context, _ []string, name string, args ...string) (string, error) {
	return m.RunCtx(ctx, name, args...)
}

func (m *ufwArgsCommander) RunCtx(_ context.Context, name string, args ...string) (string, error) {
	return m.record(name, args...)
}
func (m *ufwArgsCommander) Exists(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exists[name]
}

// TestEnableUFW_AllowsWhenStagedRulesCoverSSH: regression for Task 4.7.
// `ufw status` says inactive but `ufw show added` lists `ufw allow 22/tcp`
// — the precheck must consult the staged-rules list and let enable proceed.
func TestEnableUFW_AllowsWhenStagedRulesCoverSSH(t *testing.T) {
	m := newUFWArgsCommander()
	m.setExists("ufw", true)
	m.set("ufw status numbered", "Status: inactive\n", nil)
	m.set("ufw show added", "Added user rules (see 'ufw status' for running firewall):\nufw allow 22/tcp\n", nil)
	m.set("ufw --force enable", "Firewall is active and enabled on system startup\n", nil)

	h := &Handler{Cmd: m, PanelPort: 9443}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/firewall/enable", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.EnableUFW(c); err != nil {
		t.Fatalf("EnableUFW: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Confirm `ufw --force enable` was actually invoked — proves the
	// precheck saw the staged allow and let enable proceed.
	calledEnable := false
	for _, c := range m.calls {
		if c == "ufw --force enable" {
			calledEnable = true
			break
		}
	}
	if !calledEnable {
		t.Errorf("expected `ufw --force enable` to be called; got %v", m.calls)
	}
}

// TestEnableUFW_RefusesWhenInactiveAndNoStagedSSH: the inverse — UFW
// inactive AND no staged ALLOW for SSH/panel → 409 with the lockout
// message, no enable invocation.
func TestEnableUFW_RefusesWhenInactiveAndNoStagedSSH(t *testing.T) {
	m := newUFWArgsCommander()
	m.setExists("ufw", true)
	m.set("ufw status numbered", "Status: inactive\n", nil)
	m.set("ufw show added", "Added user rules (see 'ufw status' for running firewall):\n", nil)

	h := &Handler{Cmd: m, PanelPort: 9443}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/firewall/enable", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.EnableUFW(c); err != nil {
		t.Fatalf("EnableUFW: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v — body=%s", err, rec.Body.String())
	}
	if resp.Success {
		t.Errorf("expected success=false, got %+v", resp)
	}
	if resp.Error.Code != "UFW_ENABLE_ERROR" {
		t.Errorf("expected code UFW_ENABLE_ERROR, got %q", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "lock you out") {
		t.Errorf("expected lockout message, got %q", resp.Error.Message)
	}
	// Make sure we did NOT invoke enable.
	for _, c := range m.calls {
		if c == "ufw --force enable" {
			t.Errorf("enable was invoked despite lockout precheck failure: %v", m.calls)
		}
	}
}
