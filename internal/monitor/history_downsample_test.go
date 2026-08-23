package monitor

import "testing"

// series builds a flat 24h minute-by-minute series with one spike.
func series(n int, spikeAt, spikeLen int, spikeCPU float64) []MetricsPoint {
	pts := make([]MetricsPoint, n)
	for i := range pts {
		pts[i] = MetricsPoint{
			Time:        int64(i) * 60_000,
			CPU:         10,
			MemPercent:  40,
			DiskPercent: 46,
		}
	}
	for i := spikeAt; i < spikeAt+spikeLen && i < n; i++ {
		pts[i].CPU = spikeCPU
	}
	return pts
}

// The reason downsamplePeaks exists. A three-minute pin inside a day of
// samples must survive the reduction — the stride this replaced kept every
// twelfth point, so the spike showed up or not depending on where the window
// happened to start.
func TestDownsampleKeepsAShortSpike(t *testing.T) {
	pts := series(1440, 700, 3, 95)

	// Every offset, because the old stride's failure depended on phase: a
	// reduction that only works for some alignments is the bug, not the fix.
	for offset := 0; offset < 12; offset++ {
		got := downsamplePeaks(pts[offset:], 120)
		var peak float64
		for _, p := range got {
			if p.CPU > peak {
				peak = p.CPU
			}
		}
		if peak < 95 {
			t.Fatalf("offset %d: peak CPU = %v, want the 95%% spike to survive", offset, peak)
		}
		if len(got) > 120 {
			t.Fatalf("offset %d: %d points, want at most 120", offset, len(got))
		}
	}
}

// A spike in memory must not be hidden by a quiet CPU in the same bucket:
// each series is reduced independently.
func TestDownsampleKeepsEachSeriesIndependently(t *testing.T) {
	pts := series(1440, 0, 0, 0)
	pts[701].MemPercent = 99
	pts[705].DiskPercent = 98

	got := downsamplePeaks(pts, 120)
	var mem, disk float64
	for _, p := range got {
		if p.MemPercent > mem {
			mem = p.MemPercent
		}
		if p.DiskPercent > disk {
			disk = p.DiskPercent
		}
	}
	if mem < 99 {
		t.Errorf("memory peak = %v, want 99", mem)
	}
	if disk < 98 {
		t.Errorf("disk peak = %v, want 98", disk)
	}
}

// Timestamps stay real and ordered, and the last bucket ends where the data
// does — the chart pins its right edge to the newest point.
func TestDownsampleTimestampsAreRealAndOrdered(t *testing.T) {
	pts := series(1440, 0, 0, 0)
	got := downsamplePeaks(pts, 120)

	known := make(map[int64]bool, len(pts))
	for _, p := range pts {
		known[p.Time] = true
	}
	for i, p := range got {
		if !known[p.Time] {
			t.Fatalf("point %d has timestamp %d, which is not a real sample", i, p.Time)
		}
		if i > 0 && p.Time <= got[i-1].Time {
			t.Fatalf("point %d went backwards: %d after %d", i, p.Time, got[i-1].Time)
		}
	}
	if got[len(got)-1].Time != pts[len(pts)-1].Time {
		t.Errorf("last point = %d, want the newest sample %d", got[len(got)-1].Time, pts[len(pts)-1].Time)
	}
}

// Under the cap nothing is touched — an hour of minute samples is 60 points
// and must arrive at full resolution.
func TestDownsampleLeavesShortSeriesAlone(t *testing.T) {
	pts := series(60, 30, 1, 90)
	got := downsamplePeaks(pts, 120)
	if len(got) != 60 {
		t.Fatalf("got %d points, want all 60", len(got))
	}
	for i := range pts {
		if got[i] != pts[i] {
			t.Fatalf("point %d was modified", i)
		}
	}
}
