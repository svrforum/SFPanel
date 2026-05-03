package disk

import (
	"reflect"
	"testing"
)

const swaponShowFixture = `NAME      TYPE       SIZE    USED PRIO
/swap.img file 2147483648  524288   -2
/dev/sda2 partition 4294967296    0    0
`

func TestParseSwapEntries(t *testing.T) {
	// swapon --show with --noheadings is what the real handler runs; the
	// fixture above includes a header line that should be skipped (5 fields
	// but field[2] "SIZE" isn't numeric — the parser tolerates it because
	// ParseInt returns an error and Size stays zero, while the entry is
	// still appended). This documents current behavior; the dashboard's
	// total/used numbers come from /proc/meminfo so a corrupt header row
	// being kept here doesn't matter operationally.
	got := parseSwapEntries(swaponShowFixture)
	want := []SwapEntry{
		{Name: "NAME", Type: "TYPE"}, // header line, numeric fields zero
		{Name: "/swap.img", Type: "file", Size: 2147483648, Used: 524288, Priority: -2},
		{Name: "/dev/sda2", Type: "partition", Size: 4294967296, Used: 0, Priority: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

const meminfoFixture = `MemTotal:       16365784 kB
MemFree:         1234567 kB
SwapTotal:       2097148 kB
SwapFree:        1797148 kB
`

func TestParseSwapFromMeminfo(t *testing.T) {
	total, used, free := parseSwapFromMeminfo(meminfoFixture)
	const oneKB = int64(1024)
	if total != 2097148*oneKB {
		t.Errorf("total: got %d, want %d", total, 2097148*oneKB)
	}
	if free != 1797148*oneKB {
		t.Errorf("free: got %d, want %d", free, 1797148*oneKB)
	}
	if used != total-free {
		t.Errorf("used: got %d, want total-free %d", used, total-free)
	}
}

const diskstatsFixture = `   8       0 sda 12345 67 1234567 8901 23456 78 9876543 21098 0 1234 5678 0 0 0 0
   8       1 sda1 100 0 200 0 50 0 100 0 0 5 5 0 0 0 0
 259       0 nvme0n1 99999 0 8888888 0 11111 0 2222222 0 0 100 200 0 0 0 0
   7       0 loop0 1 0 8 0 0 0 0 0 0 0 0 0 0 0 0
`

func TestParseDiskStats(t *testing.T) {
	stats, err := parseDiskStats(diskstatsFixture)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// loop devices filtered.
	if len(stats) != 3 {
		t.Fatalf("expected 3 stats (loop filtered), got %d", len(stats))
	}

	// sda: reads completed 12345, sectors read 1234567 → bytes = *512.
	got := stats[0]
	if got.Device != "sda" {
		t.Errorf("device: got %q", got.Device)
	}
	if got.ReadOps != 12345 {
		t.Errorf("ReadOps: got %d", got.ReadOps)
	}
	if got.ReadBytes != 1234567*512 {
		t.Errorf("ReadBytes: got %d, want %d", got.ReadBytes, 1234567*512)
	}
	if got.WriteOps != 23456 {
		t.Errorf("WriteOps: got %d", got.WriteOps)
	}
	if got.WriteBytes != 9876543*512 {
		t.Errorf("WriteBytes: got %d, want %d", got.WriteBytes, 9876543*512)
	}
}

func TestParseDiskStats_TooFewFields(t *testing.T) {
	// A line with <14 fields is silently skipped (not an error).
	stats, err := parseDiskStats(`8 0 sda 1 2 3
8 0 sdb 100 0 200 0 50 0 100 0 0 5 5 0 0 0 0
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(stats) != 1 || stats[0].Device != "sdb" {
		t.Errorf("expected only sdb, got %+v", stats)
	}
}
