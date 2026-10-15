package logs

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// journalHost is a Handler on a host without rsyslog: every built-in file
// path points into an empty directory, and journalctl answers out.
func journalHost(t *testing.T, out string, err error) (*Handler, *exec.MockCommander) {
	t.Helper()
	m := exec.NewMockCommander()
	m.SetOutput("exists:journalctl", "", nil)
	m.SetOutput("journalctl", out, err)
	h := &Handler{Cmd: m}
	dir := t.TempDir()
	for id, info := range h.builtinSources() {
		info.Path = filepath.Join(dir, filepath.Base(info.Path))
		h.builtinSources()[id] = info
	}
	return h, m
}

func readLog(t *testing.T, h *Handler, query string) (int, LogOutput, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/logs/read?"+query, nil), rec)
	if err := h.ReadLog(c); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data LogOutput `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	return rec.Code, got.Data, rec.Body.String()
}

func journalCalls(m *exec.MockCommander) [][]string {
	var calls [][]string
	for _, c := range m.Calls {
		if c.Name == "journalctl" {
			calls = append(calls, c.Args)
		}
	}
	return calls
}

// Debian 12 and later, and so Proxmox VE, write no /var/log/kern.log: the
// firewall source read the missing file and the Logs page and the
// dashboard's firewall card stayed empty while the journal held every
// [UFW BLOCK] line.
func TestJournalStandsInForAMissingFile(t *testing.T) {
	line := "2026-10-12T00:18:40.123456+09:00 pve1 kernel: [UFW BLOCK] IN=vmbr0 OUT= SRC=198.51.100.7 DST=192.0.2.10 PROTO=TCP DPT=22"
	h, m := journalHost(t, line+"\n", nil)

	status, got, body := readLog(t, h, "source=firewall&lines=50")
	if status != http.StatusOK || !reflect.DeepEqual(got.Lines, []string{line}) {
		t.Fatalf("status=%d lines=%q, want the journal's line — body=%s", status, got.Lines, body)
	}
	want := [][]string{{"--no-pager", "-q", "-o", "short-iso-precise", "-n", "50", "_TRANSPORT=kernel", "--grep", "UFW|DOCKER-USER"}}
	if calls := journalCalls(m); !reflect.DeepEqual(calls, want) {
		t.Errorf("journalctl calls = %q, want %q", calls, want)
	}

	m.Calls = nil
	readLog(t, h, "source=auth&lines=100")
	want = [][]string{{"--no-pager", "-q", "-o", "short-iso-precise", "-n", "100", "SYSLOG_FACILITY=4", "SYSLOG_FACILITY=10"}}
	if calls := journalCalls(m); !reflect.DeepEqual(calls, want) {
		t.Errorf("auth: journalctl calls = %q, want %q", calls, want)
	}
}

// journalctl --grep exits 1, silently, when nothing matches: no firewall
// lines yet is an empty list, not a read error.
func TestJournalGrepWithNoMatchIsEmpty(t *testing.T) {
	h, _ := journalHost(t, "", errors.New("exit status 1"))
	status, got, body := readLog(t, h, "source=firewall")
	if status != http.StatusOK || len(got.Lines) != 0 {
		t.Errorf("status=%d lines=%q, want 200 and none — body=%s", status, got.Lines, body)
	}
	// Without --grep a failure is a failure.
	h, _ = journalHost(t, "Failed to open journal", errors.New("exit status 1"))
	if status, _, body := readLog(t, h, "source=syslog"); status != http.StatusInternalServerError {
		t.Errorf("syslog: status=%d, want 500 — body=%s", status, body)
	}
}

// A host that has the file keeps reading the file.
func TestFileWinsOverTheJournal(t *testing.T) {
	h, m := journalHost(t, "from the journal\n", nil)
	path := filepath.Join(t.TempDir(), "syslog")
	if err := os.WriteFile(path, []byte("from the file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := h.builtinSources()["syslog"]
	info.Path = path
	h.builtinSources()["syslog"] = info

	_, got, body := readLog(t, h, "source=syslog")
	if !reflect.DeepEqual(got.Lines, []string{"from the file"}) {
		t.Errorf("lines=%q, want the file's — body=%s", got.Lines, body)
	}
	if calls := journalCalls(m); len(calls) != 0 {
		t.Errorf("journalctl ran with the file present: %q", calls)
	}
}

func TestSourcesSayWhereTheyAreRead(t *testing.T) {
	platform.SetForTest(t, platform.Info{PVE: true, NetworkStack: platform.StackIfupdown2})
	h, _ := journalHost(t, "", nil)
	rec := httptest.NewRecorder()
	if err := h.ListSources(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/logs/sources", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data []LogSource `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]LogSource{}
	for _, s := range got.Data {
		byID[s.ID] = s
	}
	if s := byID["kern"]; !s.Exists || !s.Journal || s.Path != "journalctl _TRANSPORT=kernel" {
		t.Errorf("kern = %+v, want a journal source", s)
	}
	if s := byID["firewall"]; s.Path != "journalctl _TRANSPORT=kernel --grep 'UFW|DOCKER-USER'" {
		t.Errorf("firewall path = %q", s.Path)
	}
	// A source only a file has is still missing.
	if s := byID["fail2ban"]; s.Exists || s.Journal {
		t.Errorf("fail2ban = %+v, want missing", s)
	}
	// Proxmox's own firewall log is a built-in on Proxmox.
	if _, ok := byID["pve-firewall"]; !ok {
		t.Errorf("no pve-firewall source on a Proxmox host: %+v", got.Data)
	}

	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	if _, ok := (&Handler{}).builtinSources()["pve-firewall"]; ok {
		t.Error("pve-firewall source off Proxmox")
	}
}
