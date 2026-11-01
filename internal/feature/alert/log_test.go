package alert

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

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

// record is the first JSON record whose msg is msg, or nil.
func (l *lockedBuffer) record(t *testing.T, msg string) map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	sc := bufio.NewScanner(strings.NewReader(l.b.String()))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("a log line is not JSON: %q", sc.Text())
		}
		if rec["msg"] == msg {
			return rec
		}
	}
	return nil
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The panel installs its JSON handler (logging.Setup) after every package is
// initialised. A line the alert manager writes must reach that handler as a
// record of its own — message, level and component as fields — not as text
// written through the log package at INFO, which a panel at log_level warn
// drops whatever the line's own level.
func TestAlertLinesReachThePanelsHandlerAsRecords(t *testing.T) {
	old, out, flags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(old)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	buf := &lockedBuffer{}
	logging.Setup("info", buf)

	m := NewManager(openAlertTestDB(t))
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Start(context.Background())
	}()
	defer func() {
		m.Stop()
		<-done
	}()

	var rec map[string]any
	for deadline := time.Now().Add(5 * time.Second); rec == nil && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		rec = buf.record(t, "alert manager started")
	}
	if rec == nil {
		t.Fatalf("no record with msg %q; log = %s", "alert manager started", buf.String())
	}
	if rec["level"] != "INFO" || rec["component"] != "alert" || rec["interval"] != "60s" {
		t.Errorf("record = %v, want level INFO, component alert, interval 60s", rec)
	}
}
