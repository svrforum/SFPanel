package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// ---------- 6. RAID (mdadm) ----------

// ListRAID returns all RAID arrays from /proc/mdstat and mdadm --detail --scan.
func (h *DiskHandler) ListRAID(c echo.Context) error {
	if !commandExists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"mdadm is not installed. Install it: apt install mdadm")
	}

	arrays, err := parseAllRAIDArrays()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("failed to list RAID arrays: %v", err))
	}

	return response.OK(c, arrays)
}

// parseAllRAIDArrays parses /proc/mdstat and enriches with mdadm --detail --scan.
func parseAllRAIDArrays() ([]RAIDArray, error) {
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

	// Enrich with mdadm --detail for each array
	for i := range arrays {
		detail, err := getMdadmDetail(arrays[i].Name)
		if err == nil && detail != nil {
			// Merge detail data into the array
			if detail.Level != "" {
				arrays[i].Level = detail.Level
			}
			if detail.State != "" {
				arrays[i].State = detail.State
			}
			if detail.Size > 0 {
				arrays[i].Size = detail.Size
			}
			if len(detail.Devices) > 0 {
				arrays[i].Devices = detail.Devices
			}
			arrays[i].Active = detail.Active
			arrays[i].Total = detail.Total
			arrays[i].Failed = detail.Failed
			arrays[i].Spare = detail.Spare
		}
	}

	return arrays, nil
}

// parseMdstat parses /proc/mdstat to extract RAID array names and basic info.
func parseMdstat(data string) []RAIDArray {
	arrays := []RAIDArray{}
	lines := strings.Split(data, "\n")

	// Match lines like: "md0 : active raid1 sda1[0] sdb1[1]"
	mdRe := regexp.MustCompile(`^(md\d+)\s*:\s*(\w+)\s+(\w+)\s+(.*)$`)

	for _, line := range lines {
		matches := mdRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		array := RAIDArray{
			Name:    matches[1],
			State:   matches[2],
			Level:   matches[3],
			Devices: []RAIDDisk{},
		}

		// Parse device list: "sda1[0] sdb1[1] sdc1[2](S) sdd1[3](F)"
		devStr := matches[4]
		devRe := regexp.MustCompile(`(\w+)\[(\d+)\](\([A-Z]*\))?`)
		devMatches := devRe.FindAllStringSubmatch(devStr, -1)
		for _, dm := range devMatches {
			idx, _ := strconv.Atoi(dm[2])
			state := "active"
			if len(dm) > 3 {
				switch dm[3] {
				case "(S)":
					state = "spare"
				case "(F)":
					state = "faulty"
				}
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
func getMdadmDetail(name string) (*RAIDArray, error) {
	devPath := "/dev/" + name
	out, err := exec.Command("mdadm", "--detail", devPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("mdadm --detail failed: %s", strings.TrimSpace(string(out)))
	}

	array := &RAIDArray{
		Name:    name,
		Devices: []RAIDDisk{},
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
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
func (h *DiskHandler) GetRAIDDetail(c echo.Context) error {
	if !commandExists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	detail, err := getMdadmDetail(name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("failed to get RAID detail: %v", err))
	}

	return response.OK(c, detail)
}

// CreateRAID creates a new RAID array using mdadm.
func (h *DiskHandler) CreateRAID(c echo.Context) error {
	if !commandExists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"mdadm is not installed. Install it: apt install mdadm")
	}

	var req CreateRAIDRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", err.Error())
	}
	if err := validateRAIDLevel(req.Level); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_LEVEL", err.Error())
	}
	if len(req.Devices) < 2 {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS",
			"At least two devices are required for RAID")
	}

	// Validate all device names and build device paths
	devPaths := make([]string, 0, len(req.Devices))
	for _, dev := range req.Devices {
		if err := validateDeviceName(dev); err != nil {
			return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE",
				fmt.Sprintf("invalid device: %s", err.Error()))
		}
		devPath := dev
		if !strings.HasPrefix(dev, "/dev/") {
			devPath = "/dev/" + dev
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

	out, err := exec.Command("mdadm", args...).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("mdadm --create failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("RAID array %s created (level %s, %d devices)", req.Name, req.Level, len(devPaths)),
	})
}

// DeleteRAID stops and removes a RAID array.
func (h *DiskHandler) DeleteRAID(c echo.Context) error {
	if !commandExists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	devPath := "/dev/" + name

	// Stop the array
	stopOut, err := exec.Command("mdadm", "--stop", devPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("mdadm --stop failed: %s", strings.TrimSpace(string(stopOut))))
	}

	// Remove the array
	removeOut, err := exec.Command("mdadm", "--remove", devPath).CombinedOutput()
	if err != nil {
		// --remove may fail if already stopped/removed; this is not fatal
		_ = removeOut
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("RAID array %s stopped and removed", name),
	})
}

// AddRAIDDisk adds a disk to an existing RAID array (as spare or for rebuild).
func (h *DiskHandler) AddRAIDDisk(c echo.Context) error {
	if !commandExists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	var req RAIDDiskRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	raidDev := "/dev/" + name
	diskDev := "/dev/" + req.Device

	out, err := exec.Command("mdadm", "--add", raidDev, diskDev).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("mdadm --add failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("device %s added to RAID array %s", req.Device, name),
	})
}

// RemoveRAIDDisk marks a disk as faulty and removes it from a RAID array.
func (h *DiskHandler) RemoveRAIDDisk(c echo.Context) error {
	if !commandExists("mdadm") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"mdadm is not installed. Install it: apt install mdadm")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	var req RAIDDiskRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	raidDev := "/dev/" + name
	diskDev := "/dev/" + req.Device

	// First mark the device as faulty
	failOut, err := exec.Command("mdadm", "--fail", raidDev, diskDev).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("mdadm --fail failed: %s", strings.TrimSpace(string(failOut))))
	}

	// Then remove it
	removeOut, err := exec.Command("mdadm", "--remove", raidDev, diskDev).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RAID_ERROR",
			fmt.Sprintf("mdadm --remove failed: %s", strings.TrimSpace(string(removeOut))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("device %s removed from RAID array %s", req.Device, name),
	})
}
