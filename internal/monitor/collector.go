package monitor

import (
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
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
}

type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	Kernel          string `json:"kernel"`
	Uptime          uint64 `json:"uptime"`
	NumCPU          int    `json:"num_cpu"`
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

	netCounters, err := net.IOCounters(false)
	if err != nil {
		return nil, err
	}

	var bytesSent, bytesRecv uint64
	if len(netCounters) > 0 {
		bytesSent = netCounters[0].BytesSent
		bytesRecv = netCounters[0].BytesRecv
	}

	var cpuVal float64
	if len(cpuPercent) > 0 {
		cpuVal = cpuPercent[0]
	}

	return &Metrics{
		CPU:          cpuVal,
		MemTotal:     vmem.Total,
		MemUsed:      vmem.Used,
		MemPercent:   vmem.UsedPercent,
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
	}, nil
}
