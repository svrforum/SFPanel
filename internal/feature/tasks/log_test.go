package featuretasks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/svrforum/SFPanel/internal/cluster"
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
// line the engine writes must reach that handler as a record of its own —
// its message, level and component as fields — not as text written through
// the log package at INFO.
func TestTaskLinesReachThePanelsHandlerAsRecords(t *testing.T) {
	buf := panelLogs(t, "info")
	e := newFakeEnv(t, "m1", manager("m1", "leader"), agent("a1", "web-1"))
	run := newRun(t, e, steps(true, cmdTrue), [][]string{{"a1"}})
	mustRun(t, e, run)

	var ended map[string]any
	for _, rec := range buf.records(t) {
		if rec["msg"] == "task run ended" {
			ended = rec
		}
	}
	if ended == nil {
		t.Fatalf("no record with msg %q; records = %v", "task run ended", buf.records(t))
	}
	if ended["level"] != "INFO" || ended["component"] != "tasks" || ended["status"] != cluster.RunSucceeded {
		t.Errorf("record = %v, want level INFO, component tasks, status succeeded", ended)
	}
}
