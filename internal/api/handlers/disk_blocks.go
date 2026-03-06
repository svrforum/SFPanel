package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// ---------- 0. Tool Status ----------

// CheckSmartmontools reports whether smartctl (smartmontools) is installed.
func (h *DiskHandler) CheckSmartmontools(c echo.Context) error {
	installed := commandExists("smartctl")
	return response.OK(c, map[string]bool{"installed": installed})
}

// InstallSmartmontools installs smartmontools via apt.
func (h *DiskHandler) InstallSmartmontools(c echo.Context) error {
	out, err := exec.Command("apt-get", "install", "-y", "smartmontools").CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "INSTALL_ERROR",
			fmt.Sprintf("apt install failed: %s", output))
	}
	return response.OK(c, map[string]interface{}{
		"message": "smartmontools installed successfully",
		"output":  output,
	})
}

// ---------- 1. Overview ----------

// ListDisks returns all block devices with their hierarchy.
func (h *DiskHandler) ListDisks(c echo.Context) error {
	out, err := exec.Command("lsblk", "-J", "-b", "-o",
		"NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT,MODEL,SERIAL,ROTA,RO,TRAN,STATE,VENDOR").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DISK_ERROR",
			fmt.Sprintf("lsblk failed: %s", strings.TrimSpace(string(out))))
	}

	devices, err := parseLsblkJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DISK_ERROR",
			fmt.Sprintf("failed to parse lsblk output: %v", err))
	}

	return response.OK(c, devices)
}

// parseLsblkJSON parses the JSON output from lsblk into BlockDevice structs.
func parseLsblkJSON(data []byte) ([]BlockDevice, error) {
	var raw struct {
		BlockDevices []lsblkDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	result := make([]BlockDevice, 0, len(raw.BlockDevices))
	// Filter out loop, ram, and fd (floppy) devices as they are
	// virtual/irrelevant for disk management.
	skipTypes := map[string]bool{"loop": true, "ram": true}
	skipPrefixes := []string{"loop", "ram", "fd"}
	for _, d := range raw.BlockDevices {
		// Skip virtual devices
		if skipTypes[d.Type] {
			continue
		}
		skip := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(d.Name, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		result = append(result, convertLsblkDevice(d))
	}
	return result, nil
}

// lsblkDevice is the raw JSON structure from lsblk -J.
type lsblkDevice struct {
	Name       string        `json:"name"`
	Size       interface{}   `json:"size"`
	Type       string        `json:"type"`
	FsType     *string       `json:"fstype"`
	MountPoint *string       `json:"mountpoint"`
	Model      *string       `json:"model"`
	Serial     *string       `json:"serial"`
	Rota       interface{}   `json:"rota"`
	RO         interface{}   `json:"ro"`
	Tran       *string       `json:"tran"`
	State      *string       `json:"state"`
	Vendor     *string       `json:"vendor"`
	Children   []lsblkDevice `json:"children,omitempty"`
}

// convertLsblkDevice converts a raw lsblk device to our BlockDevice type.
func convertLsblkDevice(d lsblkDevice) BlockDevice {
	bd := BlockDevice{
		Name: d.Name,
		Type: d.Type,
	}

	switch v := d.Size.(type) {
	case float64:
		bd.Size = int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			bd.Size = n
		}
	}

	if d.FsType != nil {
		bd.FsType = *d.FsType
	}
	if d.MountPoint != nil {
		bd.MountPoint = *d.MountPoint
	}
	if d.Model != nil {
		bd.Model = strings.TrimSpace(*d.Model)
	}
	if d.Serial != nil {
		bd.Serial = strings.TrimSpace(*d.Serial)
	}
	if d.Tran != nil {
		bd.Transport = *d.Tran
	}
	if d.State != nil {
		bd.State = *d.State
	}
	if d.Vendor != nil {
		bd.Vendor = strings.TrimSpace(*d.Vendor)
	}

	// Rotational: true/1 = HDD, false/0 = SSD (lsblk may return bool or number)
	switch v := d.Rota.(type) {
	case bool:
		bd.Rotational = v
	case float64:
		bd.Rotational = v == 1
	}
	// Read-only: true/1 = read-only
	switch v := d.RO.(type) {
	case bool:
		bd.ReadOnly = v
	case float64:
		bd.ReadOnly = v == 1
	}

	if len(d.Children) > 0 {
		bd.Children = make([]BlockDevice, 0, len(d.Children))
		for _, child := range d.Children {
			bd.Children = append(bd.Children, convertLsblkDevice(child))
		}
	}

	return bd
}

// ---------- 2. S.M.A.R.T. ----------

// GetSmartInfo returns S.M.A.R.T. data for a specific disk device.
func (h *DiskHandler) GetSmartInfo(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	if !commandExists("smartctl") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"smartctl is not installed. Install smartmontools: apt install smartmontools")
	}

	devPath := "/dev/" + device
	out, err := exec.Command("smartctl", "-j", "-a", devPath).CombinedOutput()
	if err != nil {
		// smartctl returns non-zero exit codes for various SMART statuses;
		// we still try to parse the JSON output.
		if len(out) == 0 {
			return response.Fail(c, http.StatusInternalServerError, "SMART_ERROR",
				fmt.Sprintf("smartctl failed: %v", err))
		}
	}

	info, err := parseSmartctlJSON(devPath, out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "SMART_ERROR",
			fmt.Sprintf("failed to parse smartctl output: %v", err))
	}

	return response.OK(c, info)
}

// parseSmartctlJSON parses the JSON output from smartctl -j -a.
func parseSmartctlJSON(devPath string, data []byte) (*SmartInfo, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	info := &SmartInfo{
		DevicePath: devPath,
		Attributes: []SmartAttr{},
	}

	// Model name
	if mn, ok := raw["model_name"].(string); ok {
		info.ModelName = mn
	}

	// Serial number
	if sn, ok := raw["serial_number"].(string); ok {
		info.SerialNumber = sn
	}

	// Firmware version
	if fv, ok := raw["firmware_version"].(string); ok {
		info.FirmwareVer = fv
	}

	// Health status
	if health, ok := raw["smart_status"].(map[string]interface{}); ok {
		if passed, ok := health["passed"].(bool); ok {
			info.Healthy = &passed
			info.SmartSupported = true
		}
	}

	// Temperature
	if temp, ok := raw["temperature"].(map[string]interface{}); ok {
		if current, ok := temp["current"].(float64); ok {
			info.Temperature = int(current)
		}
	}

	// Power on hours
	if poh, ok := raw["power_on_time"].(map[string]interface{}); ok {
		if hours, ok := poh["hours"].(float64); ok {
			info.PowerOnHours = int(hours)
		}
	}

	// ATA SMART attributes
	if ataAttrs, ok := raw["ata_smart_attributes"].(map[string]interface{}); ok {
		if table, ok := ataAttrs["table"].([]interface{}); ok {
			for _, entry := range table {
				attr, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				sa := SmartAttr{}
				if id, ok := attr["id"].(float64); ok {
					sa.ID = int(id)
				}
				if name, ok := attr["name"].(string); ok {
					sa.Name = name
				}
				if val, ok := attr["value"].(float64); ok {
					sa.Value = int(val)
				}
				if worst, ok := attr["worst"].(float64); ok {
					sa.Worst = int(worst)
				}
				if thresh, ok := attr["thresh"].(float64); ok {
					sa.Threshold = int(thresh)
				}
				if rawVal, ok := attr["raw"].(map[string]interface{}); ok {
					if str, ok := rawVal["string"].(string); ok {
						sa.RawValue = str
					} else if val, ok := rawVal["value"].(float64); ok {
						sa.RawValue = strconv.FormatInt(int64(val), 10)
					}
				}
				info.Attributes = append(info.Attributes, sa)
			}
		}
	}

	// NVMe SMART attributes (different structure)
	if nvmeAttrs, ok := raw["nvme_smart_health_information_log"].(map[string]interface{}); ok {
		if temp, ok := nvmeAttrs["temperature"].(float64); ok {
			info.Temperature = int(temp)
		}
		if poh, ok := nvmeAttrs["power_on_hours"].(float64); ok {
			info.PowerOnHours = int(poh)
		}
		// Convert NVMe attributes to SmartAttr format for consistency
		nvmeFields := []struct {
			key  string
			name string
			id   int
		}{
			{"critical_warning", "Critical Warning", 1},
			{"temperature", "Temperature", 2},
			{"available_spare", "Available Spare", 3},
			{"available_spare_threshold", "Available Spare Threshold", 4},
			{"percentage_used", "Percentage Used", 5},
			{"data_units_read", "Data Units Read", 6},
			{"data_units_written", "Data Units Written", 7},
			{"host_reads", "Host Read Commands", 8},
			{"host_writes", "Host Write Commands", 9},
			{"controller_busy_time", "Controller Busy Time", 10},
			{"power_cycles", "Power Cycles", 11},
			{"power_on_hours", "Power On Hours", 12},
			{"unsafe_shutdowns", "Unsafe Shutdowns", 13},
			{"media_errors", "Media Errors", 14},
		}
		for _, f := range nvmeFields {
			if val, ok := nvmeAttrs[f.key].(float64); ok {
				info.Attributes = append(info.Attributes, SmartAttr{
					ID:       f.id,
					Name:     f.name,
					Value:    int(val),
					RawValue: strconv.FormatInt(int64(val), 10),
				})
			}
		}
	}

	return info, nil
}
