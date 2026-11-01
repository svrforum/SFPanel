package taskjob

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/logging"
)

// lockedBuffer takes the log lines of goroutines still running.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// records are the JSON records written so far.
func (l *lockedBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(l.b.String()))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("a log line is not JSON: %q", sc.Text())
		}
		out = append(out, rec)
	}
	return out
}

// panelLogs installs the panel's own handler (logging.Setup) as main does,
// after this package was initialised, and restores the defaults after.
func panelLogs(t *testing.T, level string) *lockedBuffer {
	t.Helper()
	old, out, flags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(old)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	buf := &lockedBuffer{}
	logging.Setup(level, buf)
	return buf
}

// The panel installs its JSON handler after every package is initialised. A
// line this package writes must reach that handler as a record of its own —
// its message, level and component as fields — and be kept or dropped by the
// configured level, not written through the log package as text at INFO.
func TestTaskJobLinesReachThePanelsHandlerAsRecords(t *testing.T) {
	buf := panelLogs(t, "info")
	n := newTestNode(t)
	if status, env := n.start(t, spec(runA, 0)); status != http.StatusCreated {
		t.Fatalf("start: %d %q, want 201", status, env.code())
	}
	n.runner.next(t)
	var started map[string]any
	for _, rec := range buf.records(t) {
		if rec["msg"] == "task job started" {
			started = rec
		}
	}
	if started == nil {
		t.Fatalf("no record with msg %q; records = %v", "task job started", buf.records(t))
	}
	if started["level"] != "INFO" || started["component"] != "taskjob" || started["id"] != runA+"-0" {
		t.Errorf("record = %v, want level INFO, component taskjob, id %s", started, runA+"-0")
	}
}
