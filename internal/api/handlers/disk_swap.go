package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// ---------- 7. Swap ----------

// GetSwapInfo returns swap information including entries and system totals.
func (h *DiskHandler) GetSwapInfo(c echo.Context) error {
	info := SwapInfo{
		Entries: []SwapEntry{},
	}

	// Parse swap entries from swapon --show
	swapOut, err := exec.Command("swapon", "--show=NAME,TYPE,SIZE,USED,PRIO",
		"--bytes", "--noheadings").CombinedOutput()
	if err == nil {
		info.Entries = parseSwapEntries(string(swapOut))
	}

	// Read totals from /proc/meminfo
	memData, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		info.Total, info.Used, info.Free = parseSwapFromMeminfo(string(memData))
	}

	// Read swappiness from /proc/sys/vm/swappiness
	swappinessData, err := os.ReadFile("/proc/sys/vm/swappiness")
	if err == nil {
		if val, err := strconv.Atoi(strings.TrimSpace(string(swappinessData))); err == nil {
			info.Swappiness = val
		}
	}

	return response.OK(c, info)
}

// parseSwapEntries parses swapon --show output into SwapEntry structs.
func parseSwapEntries(data string) []SwapEntry {
	entries := []SwapEntry{}
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		entry := SwapEntry{
			Name: fields[0],
			Type: fields[1],
		}

		if size, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
			entry.Size = size
		}
		if used, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
			entry.Used = used
		}
		if prio, err := strconv.Atoi(fields[4]); err == nil {
			entry.Priority = prio
		}

		entries = append(entries, entry)
	}
	return entries
}

// parseSwapFromMeminfo extracts swap total, used, and free from /proc/meminfo.
func parseSwapFromMeminfo(data string) (total, used, free int64) {
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "SwapTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					total = val * 1024 // /proc/meminfo reports in kB
				}
			}
		} else if strings.HasPrefix(line, "SwapFree:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					free = val * 1024
				}
			}
		}
	}
	used = total - free
	return
}

// CreateSwap creates a new swap area (file-based or partition-based).
func (h *DiskHandler) CreateSwap(c echo.Context) error {
	var req CreateSwapRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Device != "" {
		// Partition-based swap
		if err := validateDeviceName(req.Device); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
		}

		devPath := "/dev/" + req.Device

		// Create swap signature
		mkswapOut, err := exec.Command("mkswap", devPath).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
				fmt.Sprintf("mkswap failed: %s", strings.TrimSpace(string(mkswapOut))))
		}

		// Enable the swap
		swaponOut, err := exec.Command("swapon", devPath).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
				fmt.Sprintf("swapon failed: %s", strings.TrimSpace(string(swaponOut))))
		}

		return response.OK(c, map[string]string{
			"message": fmt.Sprintf("swap enabled on %s", req.Device),
		})
	}

	if req.Path != "" {
		// File-based swap
		if err := validateDiskPath(req.Path); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
		}
		if req.SizeMB <= 0 {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSize,
				"Size in MB is required for file-based swap")
		}

		sizeMB := strconv.FormatInt(req.SizeMB, 10)

		// Create the swap file with dd
		ddOut, err := exec.Command("dd", "if=/dev/zero", "of="+req.Path,
			"bs=1M", "count="+sizeMB).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
				fmt.Sprintf("dd failed: %s", strings.TrimSpace(string(ddOut))))
		}

		// Set permissions
		if err := os.Chmod(req.Path, 0600); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
				fmt.Sprintf("chmod failed: %v", err))
		}

		// Create swap signature
		mkswapOut, err := exec.Command("mkswap", req.Path).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
				fmt.Sprintf("mkswap failed: %s", strings.TrimSpace(string(mkswapOut))))
		}

		// Enable the swap
		swaponOut, err := exec.Command("swapon", req.Path).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
				fmt.Sprintf("swapon failed: %s", strings.TrimSpace(string(swaponOut))))
		}

		return response.OK(c, map[string]string{
			"message": fmt.Sprintf("swap file created and enabled at %s (%d MB)", req.Path, req.SizeMB),
		})
	}

	return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields,
		"Either device or path must be specified")
}

// RemoveSwap disables a swap area.
func (h *DiskHandler) RemoveSwap(c echo.Context) error {
	var req RemoveSwapRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDiskPath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	out, err := exec.Command("swapoff", req.Path).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
			fmt.Sprintf("swapoff failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("swap disabled on %s", req.Path),
	})
}

// SetSwappiness sets the vm.swappiness kernel parameter.
func (h *DiskHandler) SetSwappiness(c echo.Context) error {
	var req SetSwappinessRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Value < 0 || req.Value > 200 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
			"Swappiness value must be between 0 and 200")
	}

	valStr := fmt.Sprintf("vm.swappiness=%d", req.Value)
	out, err := exec.Command("sysctl", valStr).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrSwapError,
			fmt.Sprintf("sysctl failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("swappiness set to %d", req.Value),
	})
}

// CheckSwapResize returns constraints for resizing a swap file
// (available disk space, RAM, current swap usage).
func (h *DiskHandler) CheckSwapResize(c echo.Context) error {
	path := c.QueryParam("path")
	if path == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingPath, "path query param required")
	}
	if err := validateDiskPath(path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	info, err := os.Stat(path)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileNotFound, err.Error())
	}
	currentSizeMB := info.Size() / 1024 / 1024

	// Available disk space on the partition containing the swap file
	dir := filepath.Dir(path)
	dfOut, err := exec.Command("df", "-B1", "--output=avail", dir).CombinedOutput()
	var diskFreeMB int64
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
		if len(lines) >= 2 {
			avail, _ := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64)
			diskFreeMB = avail / 1024 / 1024
		}
	}

	// Swap usage for this specific file
	var swapUsedMB int64
	swapData, _ := os.ReadFile("/proc/swaps")
	for _, line := range strings.Split(string(swapData), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == path {
			used, _ := strconv.ParseInt(fields[3], 10, 64) // in KB
			swapUsedMB = used / 1024
			break
		}
	}

	// Available RAM
	var ramFreeMB int64
	memData, _ := os.ReadFile("/proc/meminfo")
	for _, line := range strings.Split(string(memData), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kB, _ := strconv.ParseInt(fields[1], 10, 64)
				ramFreeMB = kB / 1024
			}
			break
		}
	}

	// Max size = current size + disk free (swap file space will be reclaimed)
	maxSizeMB := currentSizeMB + diskFreeMB
	// swapoff safety: need enough RAM to hold swap used data
	swapoffSafe := ramFreeMB > swapUsedMB

	return response.OK(c, map[string]interface{}{
		"current_size_mb": currentSizeMB,
		"disk_free_mb":    diskFreeMB,
		"max_size_mb":     maxSizeMB,
		"swap_used_mb":    swapUsedMB,
		"ram_free_mb":     ramFreeMB,
		"swapoff_safe":    swapoffSafe,
	})
}

// ResizeSwap resizes a file-based swap area (swapoff → dd resize → mkswap → swapon).
// Returns step-by-step results.
func (h *DiskHandler) ResizeSwap(c echo.Context) error {
	var req ResizeSwapRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDiskPath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	if req.NewSizeMB <= 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSize,
			"New size in MB must be positive")
	}

	// Verify it's a regular file (not a partition)
	info, err := os.Stat(req.Path)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrFileNotFound,
			fmt.Sprintf("swap file not found: %v", err))
	}
	if !info.Mode().IsRegular() {
		return response.Fail(c, http.StatusBadRequest, response.ErrNotAFile,
			"Resize is only supported for file-based swap, not partitions")
	}

	type stepResult struct {
		Name   string `json:"name"`
		Status string `json:"status"` // "success" or "failed"
		Output string `json:"output"`
	}
	steps := []stepResult{}

	// Step 1: swapoff
	swapoffOut, err := exec.Command("swapoff", req.Path).CombinedOutput()
	if err != nil {
		steps = append(steps, stepResult{"swapoff", "failed", strings.TrimSpace(string(swapoffOut))})
		return response.OK(c, map[string]interface{}{"success": false, "steps": steps})
	}
	steps = append(steps, stepResult{"swapoff", "success", strings.TrimSpace(string(swapoffOut))})

	// Step 2: dd (create file with new size)
	sizeMB := strconv.FormatInt(req.NewSizeMB, 10)
	ddOut, err := exec.Command("dd", "if=/dev/zero", "of="+req.Path,
		"bs=1M", "count="+sizeMB).CombinedOutput()
	if err != nil {
		steps = append(steps, stepResult{"dd", "failed", strings.TrimSpace(string(ddOut))})
		// Rollback: try to re-enable old swap
		_ = exec.Command("mkswap", req.Path).Run()
		_ = exec.Command("swapon", req.Path).Run()
		steps = append(steps, stepResult{"rollback", "success", "attempted to restore original swap"})
		return response.OK(c, map[string]interface{}{"success": false, "steps": steps})
	}
	steps = append(steps, stepResult{"dd", "success", strings.TrimSpace(string(ddOut))})

	// Step 3: chmod
	if err := os.Chmod(req.Path, 0600); err != nil {
		steps = append(steps, stepResult{"chmod", "failed", err.Error()})
		return response.OK(c, map[string]interface{}{"success": false, "steps": steps})
	}
	steps = append(steps, stepResult{"chmod", "success", "permissions set to 0600"})

	// Step 4: mkswap
	mkswapOut, err := exec.Command("mkswap", req.Path).CombinedOutput()
	if err != nil {
		steps = append(steps, stepResult{"mkswap", "failed", strings.TrimSpace(string(mkswapOut))})
		return response.OK(c, map[string]interface{}{"success": false, "steps": steps})
	}
	steps = append(steps, stepResult{"mkswap", "success", strings.TrimSpace(string(mkswapOut))})

	// Step 5: swapon
	swaponOut, err := exec.Command("swapon", req.Path).CombinedOutput()
	if err != nil {
		steps = append(steps, stepResult{"swapon", "failed", strings.TrimSpace(string(swaponOut))})
		return response.OK(c, map[string]interface{}{"success": false, "steps": steps})
	}
	steps = append(steps, stepResult{"swapon", "success", strings.TrimSpace(string(swaponOut))})

	return response.OK(c, map[string]interface{}{
		"success": true,
		"steps":   steps,
		"message": fmt.Sprintf("swap file resized to %d MB at %s", req.NewSizeMB, req.Path),
	})
}

// ---------- 8. I/O Stats ----------

// GetIOStats returns I/O statistics for all block devices from /proc/diskstats.
func (h *DiskHandler) GetIOStats(c echo.Context) error {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrIOError,
			fmt.Sprintf("failed to read /proc/diskstats: %v", err))
	}

	stats, err := parseDiskStats(string(data))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrIOError,
			fmt.Sprintf("failed to parse /proc/diskstats: %v", err))
	}

	return response.OK(c, stats)
}

// parseDiskStats parses /proc/diskstats into IOStat structs.
// Format: https://www.kernel.org/doc/Documentation/ABI/testing/procfs-diskstats
// Fields (0-indexed after major/minor/name):
//
//	0:  reads completed
//	1:  reads merged
//	2:  sectors read
//	3:  time reading (ms)
//	4:  writes completed
//	5:  writes merged
//	6:  sectors written
//	7:  time writing (ms)
//	8:  I/Os currently in progress
//	9:  time doing I/Os (ms)
//	10: weighted time doing I/Os (ms)
func parseDiskStats(data string) ([]IOStat, error) {
	stats := []IOStat{}
	scanner := bufio.NewScanner(strings.NewReader(data))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		// Minimum fields: major(0) minor(1) name(2) + 11 stat fields = 14
		if len(fields) < 14 {
			continue
		}

		device := fields[2]

		// Skip loop and ram devices for cleaner output
		if strings.HasPrefix(device, "loop") || strings.HasPrefix(device, "ram") {
			continue
		}

		stat := IOStat{
			Device: device,
		}

		// fields[3] = reads completed
		if val, err := strconv.ParseUint(fields[3], 10, 64); err == nil {
			stat.ReadOps = val
		}
		// fields[5] = sectors read (each sector = 512 bytes)
		if val, err := strconv.ParseUint(fields[5], 10, 64); err == nil {
			stat.ReadBytes = val * 512
		}
		// fields[7] = writes completed
		if val, err := strconv.ParseUint(fields[7], 10, 64); err == nil {
			stat.WriteOps = val
		}
		// fields[9] = sectors written
		if val, err := strconv.ParseUint(fields[9], 10, 64); err == nil {
			stat.WriteBytes = val * 512
		}
		// fields[12] = time doing I/Os (ms)
		if val, err := strconv.ParseUint(fields[12], 10, 64); err == nil {
			stat.IOTime = val
		}

		stats = append(stats, stat)
	}

	return stats, nil
}

// ---------- 9. Disk Usage ----------

// GetDiskUsage returns disk usage for a given path with configurable depth.
func (h *DiskHandler) GetDiskUsage(c echo.Context) error {
	var req DiskUsageRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDiskPath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}

	// Ensure path exists
	info, err := os.Stat(req.Path)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound,
			fmt.Sprintf("path does not exist: %s", req.Path))
	}
	if !info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath,
			fmt.Sprintf("path is not a directory: %s", req.Path))
	}

	// Default depth
	depth := req.Depth
	if depth <= 0 {
		depth = 1
	}
	if depth > 10 {
		depth = 10 // Limit depth to prevent excessive output
	}

	depthStr := strconv.Itoa(depth)
	out, err := exec.Command("du", "-b", "--max-depth="+depthStr, req.Path).CombinedOutput()
	if err != nil {
		// du may return non-zero on permission errors but still produce useful output
		if len(out) == 0 {
			return response.Fail(c, http.StatusInternalServerError, response.ErrUsageError,
				fmt.Sprintf("du failed: %v", err))
		}
	}

	entries := parseDuOutput(string(out), req.Path)
	return response.OK(c, entries)
}

// parseDuOutput parses du -b --max-depth output into a tree structure.
func parseDuOutput(data string, rootPath string) []DiskUsageEntry {
	// Parse all entries first
	type rawEntry struct {
		size int64
		path string
	}
	var allEntries []rawEntry

	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "12345\t/path/to/dir"
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}

		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		allEntries = append(allEntries, rawEntry{size: size, path: parts[1]})
	}

	// Sort by path for consistent output
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].path < allEntries[j].path
	})

	// Build tree structure
	entryMap := make(map[string]*DiskUsageEntry)
	for _, e := range allEntries {
		entry := &DiskUsageEntry{
			Path:     e.path,
			Size:     e.size,
			Children: []DiskUsageEntry{},
		}
		entryMap[e.path] = entry
	}

	// Link children to parents
	var result []DiskUsageEntry
	for _, e := range allEntries {
		parentPath := filepath.Dir(e.path)
		entry := entryMap[e.path]

		if e.path == rootPath {
			// This is the root entry
			continue
		}

		if parent, ok := entryMap[parentPath]; ok {
			parent.Children = append(parent.Children, *entry)
		}
	}

	// Return the root entry with its children, or the flat list if no root
	if root, ok := entryMap[rootPath]; ok {
		result = append(result, *root)
	} else {
		// Fallback: return flat list
		for _, e := range allEntries {
			entry := entryMap[e.path]
			result = append(result, *entry)
		}
	}

	return result
}
