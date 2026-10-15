package monitor

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/svrforum/SFPanel/internal/common/platform"
)

func init() {
	// Establish baseline for non-blocking cpu.Percent(0) calls
	cpu.Percent(0, false)
}

type Metrics struct {
	CPU          float64 `json:"cpu"`
	MemTotal     uint64  `json:"mem_total"`
	MemUsed      uint64  `json:"mem_used"`
	MemPercent   float64 `json:"mem_percent"`
	SwapTotal    uint64  `json:"swap_total"`
	SwapUsed     uint64  `json:"swap_used"`
	SwapPercent  float64 `json:"swap_percent"`
	DiskTotal    uint64  `json:"disk_total"`
	DiskUsed     uint64  `json:"disk_used"`
	DiskPercent  float64 `json:"disk_percent"`
	NetBytesSent uint64  `json:"net_bytes_sent"`
	NetBytesRecv uint64  `json:"net_bytes_recv"`
	Timestamp    int64   `json:"timestamp"`
	// MemARC is the part of the ZFS ARC counted as cache rather than used
	// (see memoryUsed); absent on a host without ZFS.
	MemARC uint64 `json:"mem_arc,omitempty"`
}

type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	Kernel          string `json:"kernel"`
	Uptime          uint64 `json:"uptime"`
	NumCPU          int    `json:"num_cpu"`
	// PlatformName is what the host runs, as the panel names it: gopsutil
	// says "debian 13.5" on a Proxmox VE host, whose os-release is Debian's.
	PlatformName string `json:"platform_name"`
	PVEVersion   string `json:"pve_version,omitempty"`
}

// GetMetrics collects a current snapshot of system metrics.
func GetMetrics() (*Metrics, error) {
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}

	vmem, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	swapStat, err := mem.SwapMemory()
	if err != nil {
		return nil, err
	}

	diskStat, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	netCounters, err := net.IOCounters(true)
	if err != nil {
		return nil, err
	}

	bytesSent, bytesRecv := wireBytes(netCounters)

	var cpuVal float64
	if len(cpuPercent) > 0 {
		cpuVal = cpuPercent[0]
	}

	memUsed, memPercent, memARC := memoryUsed(vmem)

	return &Metrics{
		CPU:          cpuVal,
		MemTotal:     vmem.Total,
		MemUsed:      memUsed,
		MemPercent:   memPercent,
		MemARC:       memARC,
		SwapTotal:    swapStat.Total,
		SwapUsed:     swapStat.Used,
		SwapPercent:  swapStat.UsedPercent,
		DiskTotal:    diskStat.Total,
		DiskUsed:     diskStat.Used,
		DiskPercent:  diskStat.UsedPercent,
		NetBytesSent: bytesSent,
		NetBytesRecv: bytesRecv,
		Timestamp:    time.Now().UnixMilli(),
	}, nil
}

// GetCoreMetrics collects only CPU and memory metrics, ignoring disk/network
// errors that would cause the full GetMetrics to fail. This ensures metrics
// history collection is not interrupted by transient disk or network errors.
func GetCoreMetrics() (*Metrics, error) {
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}

	vmem, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	var cpuVal float64
	if len(cpuPercent) > 0 {
		cpuVal = cpuPercent[0]
	}

	memUsed, memPercent, memARC := memoryUsed(vmem)

	return &Metrics{
		CPU:        cpuVal,
		MemTotal:   vmem.Total,
		MemUsed:    memUsed,
		MemPercent: memPercent,
		MemARC:     memARC,
		Timestamp:  time.Now().UnixMilli(),
	}, nil
}

// Where the collector reads the ZFS ARC and the network interfaces; tests
// point them at fixtures.
var (
	arcstatsPath = "/proc/spl/kstat/zfs/arcstats"
	sysClassNet  = "/sys/class/net"
)

// memoryUsed is used memory as every reader of the metrics sees it — the
// dashboard, the history chart, the alert rules and the cluster heartbeat
// all take it from here — with the ZFS ARC's shrinkable part as cache.
//
// The ARC is not page cache: the kernel counts it as used and MemAvailable
// leaves it out, though it hands memory back to anyone who needs it. On a
// Proxmox VE host with a ZFS pool that showed memory 10-50% fuller than it
// is, and a memory alert rule fired for good. Only the part above the ARC's
// floor (c_min) shrinks, so that is what is subtracted, as htop does.
func memoryUsed(vmem *mem.VirtualMemoryStat) (used uint64, percent float64, arc uint64) {
	used, percent = vmem.Used, vmem.UsedPercent
	size, cMin, ok := readARC(arcstatsPath)
	if !ok || size <= cMin {
		return used, percent, 0
	}
	arc = min(size-cMin, used)
	used -= arc
	if vmem.Total > 0 {
		percent = float64(used) / float64(vmem.Total) * 100
	}
	return used, percent, arc
}

// readARC reads the ARC's size and c_min from arcstats ("name type data"
// rows); ok is false without ZFS.
func readARC(path string) (size, cMin uint64, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	var haveSize, haveMin bool
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		switch f[0] {
		case "size":
			size, err = strconv.ParseUint(f[2], 10, 64)
			haveSize = err == nil
		case "c_min":
			cMin, err = strconv.ParseUint(f[2], 10, 64)
			haveMin = err == nil
		}
	}
	return size, cMin, haveSize && haveMin
}

// wireBytes sums the traffic of the physical interfaces. A frame a Proxmox
// guest sends crosses its tap, the firewall bridge's veth pair, vmbr0 and
// the NIC, a container's crosses its veth and docker0, a bonded one the bond
// and its slave — and the all-interface sum counted it at each, several
// times the real traffic. Only an interface backed by a device
// (/sys/class/net/<if>/device) counts, which keeps a bond's slaves and drops
// the bond itself. A host with none, such as a container, counts everything
// but loopback.
func wireBytes(counters []net.IOCountersStat) (sent, recv uint64) {
	var physical bool
	for _, c := range counters {
		if _, err := os.Stat(filepath.Join(sysClassNet, c.Name, "device")); err == nil {
			sent += c.BytesSent
			recv += c.BytesRecv
			physical = true
		}
	}
	if physical {
		return sent, recv
	}
	for _, c := range counters {
		if c.Name != "lo" {
			sent += c.BytesSent
			recv += c.BytesRecv
		}
	}
	return sent, recv
}

// GetDiskPercent returns only the root disk usage percentage.
func GetDiskPercent() (float64, error) {
	d, err := disk.Usage("/")
	if err != nil {
		return 0, err
	}
	return d.UsedPercent, nil
}

// GetHostInfo collects static host information.
func GetHostInfo() (*HostInfo, error) {
	info, err := host.Info()
	if err != nil {
		return nil, err
	}

	return &HostInfo{
		Hostname:        info.Hostname,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		Kernel:          info.KernelVersion,
		Uptime:          info.Uptime,
		NumCPU:          runtime.NumCPU(),
		PlatformName:    platform.Get().DisplayName(),
		PVEVersion:      platform.Get().PVEVersion,
	}, nil
}
