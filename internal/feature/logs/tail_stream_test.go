package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The same bytes tail -n would produce, and a count of everything that went
// by — while holding only n lines.
func TestTailOfStreamKeepsLastNAndCountsAll(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 1000; i++ {
		sb.WriteString("line ")
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString("\n")
	}
	out, total := tailOfStream(strings.NewReader(sb.String()), 3)
	if total != 1000 {
		t.Errorf("total = %d, want 1000", total)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), out)
	}
	if lines[2] != "line "+strings.Repeat("x", 1000%7) {
		t.Errorf("last line = %q, want the 1000th", lines[2])
	}
}

func TestTailOfStreamFewerThanN(t *testing.T) {
	out, total := tailOfStream(strings.NewReader("a\nb\n"), 10)
	if total != 2 || string(out) != "a\nb\n" {
		t.Errorf("got total=%d out=%q", total, out)
	}
}

func TestTailOfStreamEmpty(t *testing.T) {
	out, total := tailOfStream(strings.NewReader(""), 10)
	if total != 0 || out != nil {
		t.Errorf("got total=%d out=%q, want 0 and nil", total, out)
	}
}

// A kernel log line can exceed the scanner's default 64 KB token; the tail
// must not silently stop there.
func TestTailOfStreamSurvivesLongLines(t *testing.T) {
	long := strings.Repeat("y", 200*1024)
	in := "first\n" + long + "\nlast\n"
	out, total := tailOfStream(strings.NewReader(in), 1)
	if total != 3 {
		t.Errorf("total = %d, want 3 — the long line stopped the scan", total)
	}
	if string(out) != "last\n" {
		t.Errorf("tail = %q, want the line after the long one", out)
	}
}

// The real pipe: grep over a file, through the ring, with grep's exit status
// 1 on no match treated as an empty result rather than an error.
func TestFilteredTailOverAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kern.log")
	var sb strings.Builder
	for i := 1; i <= 500; i++ {
		if i%5 == 0 {
			fmt.Fprintf(&sb, "kernel: [UFW BLOCK] IN=eth0 SEQ=%06d\n", i)
		} else {
			fmt.Fprintf(&sb, "kernel: usb 1-1: new device SEQ=%06d\n", i)
		}
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out, total := filteredTail(context.Background(), "UFW|DOCKER-USER", path, 2)
	if total != 100 {
		t.Errorf("total = %d, want 100 matches", total)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[1], "SEQ=000500") || !strings.Contains(lines[1], "UFW") {
		t.Errorf("tail = %q, want the last two UFW lines ending at 500", lines)
	}

	out, total = filteredTail(context.Background(), "NEVER-MATCHES", path, 2)
	if total != 0 || out != nil {
		t.Errorf("no match: got total=%d out=%q, want 0 and nil", total, out)
	}
}
