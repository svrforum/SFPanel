package disk

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ---------- 6. RAID (mdadm) ----------

// ListRAID returns all RAID arrays from /proc/mdstat and mdadm --detail --scan.
func (h *Handler) ListRAID(c echo.Context) error {
	if !h.Cmd.Exists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"mdadm is not installed. Install it: apt install mdadm")
	}

	arrays, err := h.parseAllRAIDArrays(c.Request().Context())
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("failed to list RAID arrays: %v", err))
	}

	return response.OK(c, arrays)
}

// parseAllRAIDArrays parses /proc/mdstat and enriches with mdadm --detail --scan.
// ctx propagates request cancellation into each mdadm --detail subprocess so
// a client abort kills the work instead of letting GOMAXPROCS workers run to
// the default 5-minute timeout.
func (h *Handler) parseAllRAIDArrays(ctx context.Context) ([]RAIDArray, error) {
	// Read /proc/mdstat for basic info
	mdstatData, err := os.ReadFile("/proc/mdstat")
	if err != nil {
		// No RAID configured
		if os.IsNotExist(err) {
			return []RAIDArray{}, nil
		}
		return nil, fmt.Errorf("read /proc/mdstat: %w", err)
	}

	arrays := parseMdstat(string(mdstatData))

	// Enrich with mdadm --detail in parallel. Each invocation takes
	// 100-400 ms (process startup + /sys/block read) and used to run
	// serially, so a host with 4 arrays stalled ListRAID for ~1.5 s
	// before the first byte hit the wire. Cap concurrency at
	// GOMAXPROCS to avoid fork-bombing on hosts with dozens of arrays.
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i := range arrays {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			detail, err := h.getMdadmDetail(ctx, arrays[i].Name)
			if err != nil || detail == nil {
				return
			}
			mergeMdadmDetail(&arrays[i], detail)
		}(i)
	}
	wg.Wait()

	return arrays, nil
}

// mergeMdadmDetail lays what mdadm --detail says of an array over what the
// kernel's line said. mdadm does not say an array is read-only (of a mirror
// it says "clean"), so the kernel's flag stays a word of the state, unless
// mdadm's state already says it (a striped array's "read-auto").
func mergeMdadmDetail(a *RAIDArray, detail *RAIDArray) {
	if detail.Level != "" {
		a.Level = detail.Level
	}
	if detail.State != "" {
		_, flags, flagged := strings.Cut(a.State, ", ")
		a.State = detail.State
		if flagged && !strings.Contains(strings.ToLower(detail.State), "read") {
			a.State += ", " + flags
		}
	}
	if detail.Size > 0 {
		a.Size = detail.Size
	}
	if len(detail.Devices) > 0 {
		a.Devices = detail.Devices
	}
	a.Active = detail.Active
	a.Total = detail.Total
	a.Failed = detail.Failed
	a.Spare = detail.Spare
}

// Each array's first line in /proc/mdstat, as the kernel writes it
// (drivers/md/md.c, md_seq_show):
//
//	md0 : active raid1 sdb1[1] sda1[0]
//	md0 : active (auto-read-only) raid1 sdb1[1] sda1[0]
//	md127 : inactive sdb1[1](S) sdc1[0](S)
//
// A read-only flag follows the state in parentheses, and an inactive array
// has no personality, so no level: what follows the state is its members.
var (
	mdstatArrayRe  = regexp.MustCompile(`^(md\d+)\s*:\s*(\w+)((?:\s+\([\w-]+\))*)\s*(.*)$`)
	mdstatFlagRe   = regexp.MustCompile(`\(([\w-]+)\)`)
	mdstatMemberRe = regexp.MustCompile(`([\w.-]+)\[(\d+)\]((?:\([A-Z]\))*)`)
)

// parseMdstat parses /proc/mdstat to extract RAID array names and basic info.
// A read-only flag is kept as a word of the state, the way mdadm --detail
// joins its own: "active, auto-read-only".
func parseMdstat(data string) []RAIDArray {
	arrays := []RAIDArray{}

	for _, line := range strings.Split(data, "\n") {
		matches := mdstatArrayRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		state := matches[2]
		for _, flag := range mdstatFlagRe.FindAllStringSubmatch(matches[3], -1) {
			state += ", " + flag[1]
		}
		// The level only when the word is not a member: "sdb1[1]".
		rest := strings.TrimSpace(matches[4])
		level := ""
		if first, after, _ := strings.Cut(rest, " "); first != "" && !strings.Contains(first, "[") {
			level, rest = first, after
		}

		array := RAIDArray{
			Name:    matches[1],
			State:   state,
			Level:   level,
			Devices: []RAIDDisk{},
		}

		// Parse device list: "sda1[0] sdb1[1] sdc1[2](S) sdd1[3](F)". A
		// member's flags come one after another — a write-mostly member that
		// failed is "sdb1[1](W)(F)" — and a device-mapper member is "dm-3".
		for _, dm := range mdstatMemberRe.FindAllStringSubmatch(rest, -1) {
			idx, _ := strconv.Atoi(dm[2])
			state := "active"
			switch {
			case strings.Contains(dm[3], "(F)"):
				state = "faulty"
			case strings.Contains(dm[3], "(S)"):
				state = "spare"
			}
			array.Devices = append(array.Devices, RAIDDisk{
				Device: dm[1],
				Index:  idx,
				State:  state,
			})
		}

		arrays = append(arrays, array)
	}

	return arrays
}

// getMdadmDetail runs mdadm --detail on a specific array and parses the output.
// ctx is propagated so caller cancellation kills the subprocess.
func (h *Handler) getMdadmDetail(ctx context.Context, name string) (*RAIDArray, error) {
	devPath := "/dev/" + name
	out, err := h.Cmd.RunCtx(ctx, "mdadm", "--detail", devPath)
	if err != nil {
		return nil, fmt.Errorf("mdadm --detail failed: %s", response.SanitizeOutput(strings.TrimSpace(out)))
	}

	array := &RAIDArray{
		Name:    name,
		Devices: []RAIDDisk{},
	}

	scanner := bufio.NewScanner(strings.NewReader(out))
	inDeviceSection := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "Raid Level :") {
			array.Level = strings.TrimSpace(strings.TrimPrefix(line, "Raid Level :"))
		} else if strings.HasPrefix(line, "Array Size :") {
			// "Array Size : 1048576 (1024.00 MiB 1073.74 MB)"
			parts := strings.Fields(strings.TrimPrefix(line, "Array Size :"))
			if len(parts) > 0 {
				if size, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
					array.Size = size * 1024 // Convert from KB to bytes
				}
			}
		} else if strings.HasPrefix(line, "State :") {
			array.State = strings.TrimSpace(strings.TrimPrefix(line, "State :"))
		} else if strings.HasPrefix(line, "Active Devices :") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Active Devices :"))
			array.Active, _ = strconv.Atoi(val)
		} else if strings.HasPrefix(line, "Total Devices :") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Total Devices :"))
			array.Total, _ = strconv.Atoi(val)
		} else if strings.HasPrefix(line, "Failed Devices :") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Failed Devices :"))
			array.Failed, _ = strconv.Atoi(val)
		} else if strings.HasPrefix(line, "Spare Devices :") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Spare Devices :"))
			array.Spare, _ = strconv.Atoi(val)
		} else if strings.Contains(line, "Number") && strings.Contains(line, "Major") &&
			strings.Contains(line, "Minor") && strings.Contains(line, "RaidDevice") {
			inDeviceSection = true
			continue
		}

		if inDeviceSection && line != "" {
			// Parse device lines: "   0       8       1        0      active sync   /dev/sda1"
			fields := strings.Fields(line)
			if len(fields) >= 7 {
				idx, _ := strconv.Atoi(fields[0])
				state := "active"
				devPath := fields[len(fields)-1]
				device := strings.TrimPrefix(devPath, "/dev/")

				// Determine state from the text fields
				lineState := strings.Join(fields[4:len(fields)-1], " ")
				if strings.Contains(lineState, "faulty") {
					state = "faulty"
				} else if strings.Contains(lineState, "spare") && !strings.Contains(lineState, "active") {
					state = "spare"
				}

				array.Devices = append(array.Devices, RAIDDisk{
					Device: device,
					Index:  idx,
					State:  state,
				})
			}
		}
	}

	return array, nil
}

// GetRAIDDetail returns detailed information about a specific RAID array.
func (h *Handler) GetRAIDDetail(c echo.Context) error {
	if !h.Cmd.Exists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	detail, err := h.getMdadmDetail(c.Request().Context(), name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("failed to get RAID detail: %v", err))
	}

	return response.OK(c, detail)
}

// CreateRAID creates a new RAID array using mdadm.
func (h *Handler) CreateRAID(c echo.Context) error {
	if !h.Cmd.Exists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"mdadm is not installed. Install it: apt install mdadm")
	}

	var req CreateRAIDRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}
	if err := validateRAIDLevel(req.Level); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidLevel, err.Error())
	}
	if len(req.Devices) < 2 {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields,
			"At least two devices are required for RAID")
	}

	// Validate all device names, build device paths, and verify each one
	// is a real block device on this host before invoking mdadm.
	devPaths := make([]string, 0, len(req.Devices))
	for _, dev := range req.Devices {
		if err := validateDeviceName(dev); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
				fmt.Sprintf("invalid device: %s", err.Error()))
		}
		devPath := dev
		if !strings.HasPrefix(dev, "/dev/") {
			devPath = "/dev/" + dev
		}
		if err := verifyBlockDevice(devPath); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
		}
		if r := checkGuestVolume(strings.TrimPrefix(devPath, "/dev/"), true); r != nil {
			return r.send(c)
		}
		if r := h.checkBootPartition(c.Request().Context(), devPath); r != nil {
			return r.send(c)
		}
		devPaths = append(devPaths, devPath)
	}

	raidDev := "/dev/" + req.Name
	raidCount := strconv.Itoa(len(devPaths))

	args := []string{
		"--create", raidDev,
		"--level=" + req.Level,
		"--raid-devices=" + raidCount,
	}
	args = append(args, devPaths...)

	out, err := h.Cmd.RunCtx(c.Request().Context(), "mdadm", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("mdadm --create failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("RAID array %s created (level %s, %d devices)", req.Name, req.Level, len(devPaths)),
	})
}

// DeleteRAID stops and removes a RAID array.
func (h *Handler) DeleteRAID(c echo.Context) error {
	if !h.Cmd.Exists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	devPath := "/dev/" + name

	// Stop the array
	stopOut, err := h.Cmd.RunCtx(c.Request().Context(), "mdadm", "--stop", devPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("mdadm --stop failed: %s", response.SanitizeOutput(strings.TrimSpace(stopOut))))
	}

	// Remove the array
	removeOut, err := h.Cmd.RunCtx(c.Request().Context(), "mdadm", "--remove", devPath)
	if err != nil {
		// --remove may fail if already stopped/removed; this is not fatal
		_ = removeOut
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("RAID array %s stopped and removed", name),
	})
}

// AddRAIDDisk adds a disk to an existing RAID array (as spare or for rebuild).
func (h *Handler) AddRAIDDisk(c echo.Context) error {
	if !h.Cmd.Exists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	var req RAIDDiskRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	raidDev := "/dev/" + name
	diskDev := "/dev/" + req.Device
	if err := verifyBlockDevice(raidDev); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if err := verifyBlockDevice(diskDev); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if r := checkGuestVolume(req.Device, true); r != nil {
		return r.send(c)
	}
	if r := h.checkBootPartition(c.Request().Context(), diskDev); r != nil {
		return r.send(c)
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "mdadm", "--add", raidDev, diskDev)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("mdadm --add failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("device %s added to RAID array %s", req.Device, name),
	})
}

// RemoveRAIDDisk marks a disk as faulty and removes it from a RAID array.
func (h *Handler) RemoveRAIDDisk(c echo.Context) error {
	if !h.Cmd.Exists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	var req RAIDDiskRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	raidDev := "/dev/" + name
	diskDev := "/dev/" + req.Device

	// First mark the device as faulty
	failOut, err := h.Cmd.RunCtx(c.Request().Context(), "mdadm", "--fail", raidDev, diskDev)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("mdadm --fail failed: %s", response.SanitizeOutput(strings.TrimSpace(failOut))))
	}

	// Then remove it
	removeOut, err := h.Cmd.RunCtx(c.Request().Context(), "mdadm", "--remove", raidDev, diskDev)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrRAIDError,
			fmt.Sprintf("mdadm --remove failed: %s", response.SanitizeOutput(strings.TrimSpace(removeOut))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("device %s removed from RAID array %s", req.Device, name),
	})
}
