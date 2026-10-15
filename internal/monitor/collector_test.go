package monitor

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

const gib = 1 << 30

// The head of /proc/spl/kstat/zfs/arcstats on a Proxmox VE 9 host, values
// rounded.
const arcstatsFixture = `13 1 0x01 147 39984 4823649383 1764081398214477
name                            type data
hits                            4    3254983123
misses                          4    10223456
c                               4    13958643712
c_min                           4    4294967296
c_max                           4    13958643712
size                            4    12884901888
`

func withARC(t *testing.T, content string) {
	t.Helper()
	prev := arcstatsPath
	t.Cleanup(func() { arcstatsPath = prev })
	arcstatsPath = filepath.Join(t.TempDir(), "arcstats")
	if content != "" {
		if err := os.WriteFile(arcstatsPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadARC(t *testing.T) {
	withARC(t, arcstatsFixture)
	size, cMin, ok := readARC(arcstatsPath)
	if !ok || size != 12*gib || cMin != 4*gib {
		t.Errorf("readARC = %d, %d, %v; want 12 GiB, 4 GiB, true", size, cMin, ok)
	}
	if _, _, ok := readARC(filepath.Join(t.TempDir(), "absent")); ok {
		t.Error("readARC without ZFS reported an ARC")
	}
}

// 12 GiB of ARC over a 4 GiB floor on a 128 GiB host: 8 GiB of what the
// kernel calls used is cache the ARC hands back. The dashboard, the history
// and the memory alert rule all read this one figure.
func TestMemoryUsedCountsTheShrinkableARCAsCache(t *testing.T) {
	withARC(t, arcstatsFixture)
	vm := &mem.VirtualMemoryStat{Total: 128 * gib, Used: 100 * gib, UsedPercent: 100.0 / 128 * 100}
	used, percent, arc := memoryUsed(vm)
	if used != 92*gib || arc != 8*gib || math.Abs(percent-71.875) > 1e-9 {
		t.Errorf("memoryUsed = %d GiB, %.3f%%, arc %d GiB; want 92 GiB, 71.875%%, 8 GiB", used/gib, percent, arc/gib)
	}

	// No ZFS: the kernel's figure stands.
	withARC(t, "")
	if used, percent, arc := memoryUsed(vm); used != vm.Used || percent != vm.UsedPercent || arc != 0 {
		t.Errorf("without ZFS: %d, %v, %d; want the kernel's figure", used, percent, arc)
	}

	// An ARC at its floor has nothing to give back.
	withARC(t, "name type data\nsize 4 1073741824\nc_min 4 4294967296\n")
	if used, _, arc := memoryUsed(vm); used != vm.Used || arc != 0 {
		t.Errorf("ARC below c_min: used %d arc %d; want unchanged", used, arc)
	}
}

func TestWireBytesCountsPhysicalInterfacesOnly(t *testing.T) {
	prev := sysClassNet
	t.Cleanup(func() { sysClassNet = prev })
	sysClassNet = t.TempDir()
	mk := func(name string, device bool) {
		if err := os.MkdirAll(filepath.Join(sysClassNet, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if device {
			if err := os.Mkdir(filepath.Join(sysClassNet, name, "device"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Two NICs bonded into bond0 under vmbr0, with one VM's tap and its
	// firewall veth pair: the same frame passes all of them.
	for _, n := range []string{"bond0", "vmbr0", "tap100i0", "fwpr100p0", "fwln100i0", "lo"} {
		mk(n, false)
	}
	mk("eno1", true)
	mk("eno2", true)
	counters := []net.IOCountersStat{
		{Name: "eno1", BytesSent: 600, BytesRecv: 6000},
		{Name: "eno2", BytesSent: 400, BytesRecv: 4000},
		{Name: "bond0", BytesSent: 1000, BytesRecv: 10000},
		{Name: "vmbr0", BytesSent: 1000, BytesRecv: 10000},
		{Name: "tap100i0", BytesSent: 900, BytesRecv: 9000},
		{Name: "fwpr100p0", BytesSent: 900, BytesRecv: 9000},
		{Name: "fwln100i0", BytesSent: 900, BytesRecv: 9000},
		{Name: "lo", BytesSent: 5000, BytesRecv: 5000},
	}
	if sent, recv := wireBytes(counters); sent != 1000 || recv != 10000 {
		t.Errorf("wireBytes = %d/%d, want 1000/10000 (the two NICs)", sent, recv)
	}

	// A host with no device-backed interface (a container) counts all but lo.
	virtual := []net.IOCountersStat{
		{Name: "eth0", BytesSent: 70, BytesRecv: 700},
		{Name: "lo", BytesSent: 5000, BytesRecv: 5000},
	}
	if sent, recv := wireBytes(virtual); sent != 70 || recv != 700 {
		t.Errorf("wireBytes without NICs = %d/%d, want 70/700", sent, recv)
	}
}
