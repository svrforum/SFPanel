package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// DiskHandler exposes REST handlers for host disk management
// (block devices, SMART, partitions, filesystems, LVM, RAID, swap, I/O stats).
type DiskHandler struct{}

// ---------- Types ----------

// BlockDevice represents a block device from lsblk output.
type BlockDevice struct {
	Name       string        `json:"name"`
	Size       int64         `json:"size"`
	Type       string        `json:"type"`        // disk, part, lvm, raid, loop
	FsType     string        `json:"fstype"`
	MountPoint string        `json:"mountpoint"`
	Model      string        `json:"model"`
	Serial     string        `json:"serial"`
	Rotational bool          `json:"rotational"`  // true=HDD, false=SSD
	ReadOnly   bool          `json:"readonly"`
	Transport  string        `json:"transport"`   // sata, nvme, usb, etc
	State      string        `json:"state"`
	Vendor     string        `json:"vendor"`
	Children   []BlockDevice `json:"children,omitempty"`
}

// SmartInfo holds S.M.A.R.T. data for a disk device.
type SmartInfo struct {
	DevicePath    string     `json:"device_path"`
	ModelName     string     `json:"model_name"`
	SerialNumber  string     `json:"serial_number"`
	FirmwareVer   string     `json:"firmware_version"`
	Healthy       *bool      `json:"healthy"`        // nil = unknown (SMART not supported)
	SmartSupported bool      `json:"smart_supported"`
	Temperature   int        `json:"temperature"`
	PowerOnHours  int        `json:"power_on_hours"`
	Attributes    []SmartAttr `json:"attributes"`
}

// SmartAttr represents a single S.M.A.R.T. attribute.
type SmartAttr struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Value     int    `json:"value"`
	Worst     int    `json:"worst"`
	Threshold int    `json:"threshold"`
	RawValue  string `json:"raw_value"`
}

// Filesystem represents a mounted filesystem from df output.
type Filesystem struct {
	Source     string  `json:"source"`
	FsType     string  `json:"fstype"`
	Size       int64   `json:"size"`
	Used       int64   `json:"used"`
	Available  int64   `json:"available"`
	UsePercent float64 `json:"use_percent"`
	MountPoint string  `json:"mount_point"`
}

// PhysicalVolume represents an LVM physical volume.
type PhysicalVolume struct {
	Name   string `json:"name"`
	VGName string `json:"vg_name"`
	Size   string `json:"size"`
	Free   string `json:"free"`
	Attr   string `json:"attr"`
}

// VolumeGroup represents an LVM volume group.
type VolumeGroup struct {
	Name    string `json:"name"`
	Size    string `json:"size"`
	Free    string `json:"free"`
	PVCount int    `json:"pv_count"`
	LVCount int    `json:"lv_count"`
	Attr    string `json:"attr"`
}

// LogicalVolume represents an LVM logical volume.
type LogicalVolume struct {
	Name        string `json:"name"`
	VGName      string `json:"vg_name"`
	Size        string `json:"size"`
	Attr        string `json:"attr"`
	Path        string `json:"path"`
	PoolLV      string `json:"pool_lv"`
	DataPercent string `json:"data_percent"`
}

// RAIDArray represents an mdadm RAID array.
type RAIDArray struct {
	Name    string     `json:"name"`
	Level   string     `json:"level"`
	State   string     `json:"state"`
	Size    int64      `json:"size"`
	Devices []RAIDDisk `json:"devices"`
	Active  int        `json:"active"`
	Total   int        `json:"total"`
	Failed  int        `json:"failed"`
	Spare   int        `json:"spare"`
}

// RAIDDisk represents a single disk within a RAID array.
type RAIDDisk struct {
	Device string `json:"device"`
	State  string `json:"state"` // active, spare, faulty
	Index  int    `json:"index"`
}

// SwapEntry represents a single swap area.
type SwapEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`     // partition, file
	Size     int64  `json:"size"`
	Used     int64  `json:"used"`
	Priority int    `json:"priority"`
}

// SwapInfo holds aggregate swap information plus individual entries.
type SwapInfo struct {
	Total      int64       `json:"total"`
	Used       int64       `json:"used"`
	Free       int64       `json:"free"`
	Swappiness int         `json:"swappiness"`
	Entries    []SwapEntry `json:"entries"`
}

// IOStat represents I/O statistics for a single block device.
type IOStat struct {
	Device     string `json:"device"`
	ReadOps    uint64 `json:"read_ops"`
	WriteOps   uint64 `json:"write_ops"`
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	IOTime     uint64 `json:"io_time"`
}

// DiskUsageEntry represents a directory's disk usage with optional children.
type DiskUsageEntry struct {
	Path     string           `json:"path"`
	Size     int64            `json:"size"`
	Children []DiskUsageEntry `json:"children,omitempty"`
}

// ---------- Request types ----------

// CreatePartitionRequest is the payload for POST /disks/:device/partitions.
type CreatePartitionRequest struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	FsType string `json:"fs_type"`
}

// FormatPartitionRequest is the payload for POST /filesystems/format.
type FormatPartitionRequest struct {
	Device string `json:"device"`
	FsType string `json:"fs_type"`
	Label  string `json:"label"`
}

// MountFilesystemRequest is the payload for POST /filesystems/mount.
type MountFilesystemRequest struct {
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	FsType     string `json:"fs_type"`
	Options    string `json:"options"`
}

// UnmountFilesystemRequest is the payload for POST /filesystems/unmount.
type UnmountFilesystemRequest struct {
	MountPoint string `json:"mount_point"`
}

// ResizeFilesystemRequest is the payload for POST /filesystems/resize.
type ResizeFilesystemRequest struct {
	Device string `json:"device"`
	FsType string `json:"fs_type"`
}

// CreatePVRequest is the payload for POST /lvm/pvs.
type CreatePVRequest struct {
	Device string `json:"device"`
}

// CreateVGRequest is the payload for POST /lvm/vgs.
type CreateVGRequest struct {
	Name string   `json:"name"`
	PVs  []string `json:"pvs"`
}

// CreateLVRequest is the payload for POST /lvm/lvs.
type CreateLVRequest struct {
	Name string `json:"name"`
	VG   string `json:"vg"`
	Size string `json:"size"`
}

// ResizeLVRequest is the payload for POST /lvm/lvs/resize.
type ResizeLVRequest struct {
	VG   string `json:"vg"`
	Name string `json:"name"`
	Size string `json:"size"`
}

// CreateRAIDRequest is the payload for POST /raid.
type CreateRAIDRequest struct {
	Name    string   `json:"name"`
	Level   string   `json:"level"`
	Devices []string `json:"devices"`
}

// RAIDDiskRequest is the payload for POST /raid/:name/add and /raid/:name/remove.
type RAIDDiskRequest struct {
	Device string `json:"device"`
}

// CreateSwapRequest is the payload for POST /swap.
type CreateSwapRequest struct {
	Path   string `json:"path"`    // for file-based swap
	SizeMB int64  `json:"size_mb"` // size in MB for file-based swap
	Device string `json:"device"`  // for partition-based swap
}

// RemoveSwapRequest is the payload for DELETE /swap.
type RemoveSwapRequest struct {
	Path string `json:"path"`
}

// ResizeSwapRequest is the payload for PUT /swap/resize.
type ResizeSwapRequest struct {
	Path      string `json:"path"`       // swap file path
	NewSizeMB int64  `json:"new_size_mb"` // new size in MB
}

// SetSwappinessRequest is the payload for PUT /swap/swappiness.
type SetSwappinessRequest struct {
	Value int `json:"value"`
}

// DiskUsageRequest is the payload for POST /disks/usage.
type DiskUsageRequest struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

// ---------- Validation ----------

// validDeviceName matches safe device names: alphanumeric, hyphens, underscores, forward slashes.
// Examples: sda, sda1, nvme0n1p1, md0, dm-0, vg0/lv0
var validDeviceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_-]*$`)

// validLVMName matches safe LVM names (VG and LV names).
var validLVMName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validPath matches safe filesystem paths (no shell metacharacters).
var validDiskPath = regexp.MustCompile(`^[a-zA-Z0-9/._-]+$`)

// validateDeviceName checks that a device name is safe for use in commands.
func validateDeviceName(name string) error {
	if name == "" {
		return fmt.Errorf("device name is required")
	}
	if !validDeviceName.MatchString(name) {
		return fmt.Errorf("invalid device name: %s", name)
	}
	// Reject path traversal attempts
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid device name: path traversal not allowed")
	}
	return nil
}

// validateLVMName checks that an LVM name is safe for use in commands.
func validateLVMName(name string) error {
	if name == "" {
		return fmt.Errorf("LVM name is required")
	}
	if !validLVMName.MatchString(name) {
		return fmt.Errorf("invalid LVM name: %s", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid LVM name: path traversal not allowed")
	}
	return nil
}

// validateDiskPath checks that a filesystem path is safe for use in disk commands.
func validateDiskPath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if !validDiskPath.MatchString(path) {
		return fmt.Errorf("invalid path: %s (only alphanumeric, /, ., _, - allowed)", path)
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("invalid path: path traversal not allowed")
	}
	return nil
}

// validateFsType checks that a filesystem type is a known safe value.
func validateFsType(fsType string) error {
	allowed := map[string]bool{
		"ext2": true, "ext3": true, "ext4": true,
		"xfs": true, "btrfs": true, "vfat": true,
		"fat32": true, "ntfs": true, "swap": true,
		"linux-swap": true,
	}
	if !allowed[fsType] {
		return fmt.Errorf("unsupported filesystem type: %s", fsType)
	}
	return nil
}

// validatePartitionSize checks that a partition size/offset string is safe.
var validPartitionSize = regexp.MustCompile(`^[0-9]+((\.[0-9]+)?)(s|B|kB|KB|MB|GB|TB|KiB|MiB|GiB|TiB|%)?$`)

func validatePartitionSize(size string) error {
	if size == "" {
		return fmt.Errorf("size is required")
	}
	if !validPartitionSize.MatchString(size) {
		return fmt.Errorf("invalid size/offset: %s", size)
	}
	return nil
}

// validateLVSize checks that an LVM size string is safe (e.g. 10G, 500M, 100%FREE).
var validLVSize = regexp.MustCompile(`^[0-9]+((\.[0-9]+)?)[bBsSkKmMgGtTpPeE]?$|^[0-9]+%[A-Z]+$`)

func validateLVSize(size string) error {
	if size == "" {
		return fmt.Errorf("size is required")
	}
	if !validLVSize.MatchString(size) {
		return fmt.Errorf("invalid LV size: %s", size)
	}
	return nil
}

// validateRAIDLevel checks that a RAID level is valid.
func validateRAIDLevel(level string) error {
	allowed := map[string]bool{
		"0": true, "1": true, "4": true, "5": true,
		"6": true, "10": true, "raid0": true, "raid1": true,
		"raid4": true, "raid5": true, "raid6": true, "raid10": true,
	}
	if !allowed[level] {
		return fmt.Errorf("unsupported RAID level: %s", level)
	}
	return nil
}

// commandExists checks if a command is available in PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

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

// ---------- 3. Partitions ----------

// ListPartitions returns partitions for a specific disk device using lsblk.
func (h *DiskHandler) ListPartitions(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	devPath := "/dev/" + device
	out, err := exec.Command("lsblk", "-J", "-b", "-o",
		"NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT,PARTLABEL,PARTUUID",
		devPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DISK_ERROR",
			fmt.Sprintf("lsblk failed for %s: %s", device, strings.TrimSpace(string(out))))
	}

	var raw struct {
		BlockDevices []json.RawMessage `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "DISK_ERROR",
			fmt.Sprintf("failed to parse lsblk output: %v", err))
	}

	return response.OK(c, json.RawMessage(out))
}

// CreatePartition creates a new partition on a disk device using parted.
func (h *DiskHandler) CreatePartition(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	if !commandExists("parted") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"parted is not installed. Install it: apt install parted")
	}

	var req CreatePartitionRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validatePartitionSize(req.Start); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_START", err.Error())
	}
	if err := validatePartitionSize(req.End); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_END", err.Error())
	}

	fsType := "ext4"
	if req.FsType != "" {
		if req.FsType == "linux-swap" || req.FsType == "swap" {
			fsType = "linux-swap"
		} else {
			if err := validateFsType(req.FsType); err != nil {
				return response.Fail(c, http.StatusBadRequest, "INVALID_FSTYPE", err.Error())
			}
			fsType = req.FsType
		}
	}

	devPath := "/dev/" + device
	out, err := exec.Command("parted", "-s", devPath,
		"mkpart", "primary", fsType, req.Start, req.End).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "PARTITION_ERROR",
			fmt.Sprintf("parted mkpart failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("partition created on %s (%s - %s)", device, req.Start, req.End),
	})
}

// DeletePartition deletes a partition by number from a disk device using parted.
func (h *DiskHandler) DeletePartition(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	number := c.Param("number")
	partNum, err := strconv.Atoi(number)
	if err != nil || partNum < 1 {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PARTITION", "Invalid partition number")
	}

	if !commandExists("parted") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"parted is not installed. Install it: apt install parted")
	}

	devPath := "/dev/" + device
	out, err := exec.Command("parted", "-s", devPath, "rm", number).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "PARTITION_ERROR",
			fmt.Sprintf("parted rm failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("partition %d deleted from %s", partNum, device),
	})
}

// ---------- 4. Filesystems ----------

// ListFilesystems returns all mounted filesystems with usage information.
func (h *DiskHandler) ListFilesystems(c echo.Context) error {
	out, err := exec.Command("df", "-B1", "--output=source,fstype,size,used,avail,pcent,target").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FS_ERROR",
			fmt.Sprintf("df failed: %s", strings.TrimSpace(string(out))))
	}

	filesystems, err := parseDfOutput(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FS_ERROR",
			fmt.Sprintf("failed to parse df output: %v", err))
	}

	return response.OK(c, filesystems)
}

// parseDfOutput parses the output of df -B1 --output=source,fstype,size,used,avail,pcent,target.
func parseDfOutput(data []byte) ([]Filesystem, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return []Filesystem{}, nil
	}

	result := make([]Filesystem, 0, len(lines)-1)
	for _, line := range lines[1:] { // Skip header
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		fs := Filesystem{
			Source: fields[0],
			FsType: fields[1],
		}

		if size, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
			fs.Size = size
		}
		if used, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
			fs.Used = used
		}
		if avail, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
			fs.Available = avail
		}

		// Parse percentage (e.g., "45%")
		pctStr := strings.TrimSuffix(fields[5], "%")
		if pct, err := strconv.ParseFloat(pctStr, 64); err == nil {
			fs.UsePercent = pct
		}

		// Mount point might contain spaces; rejoin remaining fields
		fs.MountPoint = strings.Join(fields[6:], " ")

		result = append(result, fs)
	}

	return result, nil
}

// FormatPartition formats a device with the specified filesystem type.
func (h *DiskHandler) FormatPartition(c echo.Context) error {
	var req FormatPartitionRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}
	if err := validateFsType(req.FsType); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_FSTYPE", err.Error())
	}

	devPath := "/dev/" + req.Device

	// Build the mkfs command based on filesystem type
	var cmd *exec.Cmd
	switch req.FsType {
	case "ext2", "ext3", "ext4":
		mkfsCmd := "mkfs." + req.FsType
		if !commandExists(mkfsCmd) {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				fmt.Sprintf("%s is not installed", mkfsCmd))
		}
		args := []string{"-F"}
		if req.Label != "" {
			args = append(args, "-L", req.Label)
		}
		args = append(args, devPath)
		cmd = exec.Command(mkfsCmd, args...)

	case "xfs":
		if !commandExists("mkfs.xfs") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"mkfs.xfs is not installed. Install xfsprogs: apt install xfsprogs")
		}
		args := []string{"-f"}
		if req.Label != "" {
			args = append(args, "-L", req.Label)
		}
		args = append(args, devPath)
		cmd = exec.Command("mkfs.xfs", args...)

	case "btrfs":
		if !commandExists("mkfs.btrfs") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"mkfs.btrfs is not installed. Install btrfs-progs: apt install btrfs-progs")
		}
		args := []string{"-f"}
		if req.Label != "" {
			args = append(args, "-L", req.Label)
		}
		args = append(args, devPath)
		cmd = exec.Command("mkfs.btrfs", args...)

	case "vfat", "fat32":
		if !commandExists("mkfs.vfat") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"mkfs.vfat is not installed. Install dosfstools: apt install dosfstools")
		}
		args := []string{}
		if req.Label != "" {
			args = append(args, "-n", req.Label)
		}
		args = append(args, devPath)
		cmd = exec.Command("mkfs.vfat", args...)

	case "ntfs":
		if !commandExists("mkfs.ntfs") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"mkfs.ntfs is not installed. Install ntfs-3g: apt install ntfs-3g")
		}
		args := []string{"-F"}
		if req.Label != "" {
			args = append(args, "-L", req.Label)
		}
		args = append(args, devPath)
		cmd = exec.Command("mkfs.ntfs", args...)

	default:
		return response.Fail(c, http.StatusBadRequest, "INVALID_FSTYPE",
			fmt.Sprintf("unsupported filesystem type: %s", req.FsType))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "FORMAT_ERROR",
			fmt.Sprintf("format failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("%s formatted as %s", req.Device, req.FsType),
	})
}

// MountFilesystem mounts a device to a mount point.
func (h *DiskHandler) MountFilesystem(c echo.Context) error {
	var req MountFilesystemRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}
	if err := validateDiskPath(req.MountPoint); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_MOUNTPOINT", err.Error())
	}

	devPath := "/dev/" + req.Device

	// Ensure mount point directory exists
	if err := os.MkdirAll(req.MountPoint, 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, "MOUNT_ERROR",
			fmt.Sprintf("failed to create mount point directory: %v", err))
	}

	args := []string{}
	if req.FsType != "" {
		if err := validateFsType(req.FsType); err != nil {
			return response.Fail(c, http.StatusBadRequest, "INVALID_FSTYPE", err.Error())
		}
		args = append(args, "-t", req.FsType)
	}
	if req.Options != "" {
		// Validate options: only allow safe characters
		if !validDiskPath.MatchString(req.Options) && !regexp.MustCompile(`^[a-zA-Z0-9,=_-]+$`).MatchString(req.Options) {
			return response.Fail(c, http.StatusBadRequest, "INVALID_OPTIONS",
				"mount options contain invalid characters")
		}
		args = append(args, "-o", req.Options)
	}
	args = append(args, devPath, req.MountPoint)

	out, err := exec.Command("mount", args...).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "MOUNT_ERROR",
			fmt.Sprintf("mount failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("%s mounted at %s", req.Device, req.MountPoint),
	})
}

// UnmountFilesystem unmounts a filesystem from a mount point.
func (h *DiskHandler) UnmountFilesystem(c echo.Context) error {
	var req UnmountFilesystemRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDiskPath(req.MountPoint); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_MOUNTPOINT", err.Error())
	}

	out, err := exec.Command("umount", req.MountPoint).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "UNMOUNT_ERROR",
			fmt.Sprintf("umount failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("%s unmounted", req.MountPoint),
	})
}

// ResizeFilesystem resizes a filesystem on a given device.
func (h *DiskHandler) ResizeFilesystem(c echo.Context) error {
	var req ResizeFilesystemRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	devPath := "/dev/" + req.Device

	// Determine filesystem type if not provided
	fsType := req.FsType
	if fsType == "" {
		// Auto-detect using blkid
		blkOut, err := exec.Command("blkid", "-o", "value", "-s", "TYPE", devPath).Output()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "RESIZE_ERROR",
				"failed to detect filesystem type; please specify fs_type")
		}
		fsType = strings.TrimSpace(string(blkOut))
	}

	var cmd *exec.Cmd
	switch fsType {
	case "ext2", "ext3", "ext4":
		if !commandExists("resize2fs") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"resize2fs is not installed. Install e2fsprogs: apt install e2fsprogs")
		}
		cmd = exec.Command("resize2fs", devPath)

	case "xfs":
		if !commandExists("xfs_growfs") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"xfs_growfs is not installed. Install xfsprogs: apt install xfsprogs")
		}
		// xfs_growfs requires the mount point, not the device
		mountPoint, err := findMountPoint(devPath)
		if err != nil || mountPoint == "" {
			return response.Fail(c, http.StatusBadRequest, "RESIZE_ERROR",
				"XFS filesystem must be mounted before resizing. Could not find mount point.")
		}
		cmd = exec.Command("xfs_growfs", mountPoint)

	case "btrfs":
		if !commandExists("btrfs") {
			return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
				"btrfs is not installed. Install btrfs-progs: apt install btrfs-progs")
		}
		mountPoint, err := findMountPoint(devPath)
		if err != nil || mountPoint == "" {
			return response.Fail(c, http.StatusBadRequest, "RESIZE_ERROR",
				"Btrfs filesystem must be mounted before resizing. Could not find mount point.")
		}
		cmd = exec.Command("btrfs", "filesystem", "resize", "max", mountPoint)

	default:
		return response.Fail(c, http.StatusBadRequest, "RESIZE_ERROR",
			fmt.Sprintf("resize not supported for filesystem type: %s", fsType))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "RESIZE_ERROR",
			fmt.Sprintf("resize failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("filesystem on %s resized successfully", req.Device),
	})
}

// findMountPoint finds the mount point for a given device path by reading /proc/mounts.
func findMountPoint(devPath string) (string, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", fmt.Errorf("read /proc/mounts: %w", err)
	}

	// Resolve any symlinks in the device path for comparison
	resolvedDev, _ := filepath.EvalSymlinks(devPath)

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		mountDev := fields[0]
		mountPoint := fields[1]

		if mountDev == devPath || mountDev == resolvedDev {
			return mountPoint, nil
		}

		// Also resolve the mount device for symlink comparison
		resolvedMountDev, _ := filepath.EvalSymlinks(mountDev)
		if resolvedMountDev == resolvedDev {
			return mountPoint, nil
		}
	}

	return "", nil
}

// ---------- 5. LVM ----------

// ListPVs returns all LVM physical volumes.
func (h *DiskHandler) ListPVs(c echo.Context) error {
	if !commandExists("pvs") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := exec.Command("pvs", "--reportformat", "json",
		"-o", "pv_name,vg_name,pv_size,pv_free,pv_attr").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("pvs failed: %s", strings.TrimSpace(string(out))))
	}

	pvs, err := parsePVsJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("failed to parse pvs output: %v", err))
	}

	return response.OK(c, pvs)
}

// parsePVsJSON parses the JSON output from pvs --reportformat json.
func parsePVsJSON(data []byte) ([]PhysicalVolume, error) {
	var raw struct {
		Report []struct {
			PV []struct {
				Name   string `json:"pv_name"`
				VGName string `json:"vg_name"`
				Size   string `json:"pv_size"`
				Free   string `json:"pv_free"`
				Attr   string `json:"pv_attr"`
			} `json:"pv"`
		} `json:"report"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	result := []PhysicalVolume{}
	for _, report := range raw.Report {
		for _, pv := range report.PV {
			result = append(result, PhysicalVolume{
				Name:   strings.TrimSpace(pv.Name),
				VGName: strings.TrimSpace(pv.VGName),
				Size:   strings.TrimSpace(pv.Size),
				Free:   strings.TrimSpace(pv.Free),
				Attr:   strings.TrimSpace(pv.Attr),
			})
		}
	}
	return result, nil
}

// ListVGs returns all LVM volume groups.
func (h *DiskHandler) ListVGs(c echo.Context) error {
	if !commandExists("vgs") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := exec.Command("vgs", "--reportformat", "json",
		"-o", "vg_name,vg_size,vg_free,pv_count,lv_count,vg_attr").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("vgs failed: %s", strings.TrimSpace(string(out))))
	}

	vgs, err := parseVGsJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("failed to parse vgs output: %v", err))
	}

	return response.OK(c, vgs)
}

// parseVGsJSON parses the JSON output from vgs --reportformat json.
func parseVGsJSON(data []byte) ([]VolumeGroup, error) {
	var raw struct {
		Report []struct {
			VG []struct {
				Name    string `json:"vg_name"`
				Size    string `json:"vg_size"`
				Free    string `json:"vg_free"`
				PVCount string `json:"pv_count"`
				LVCount string `json:"lv_count"`
				Attr    string `json:"vg_attr"`
			} `json:"vg"`
		} `json:"report"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	result := []VolumeGroup{}
	for _, report := range raw.Report {
		for _, vg := range report.VG {
			pvCount, _ := strconv.Atoi(strings.TrimSpace(vg.PVCount))
			lvCount, _ := strconv.Atoi(strings.TrimSpace(vg.LVCount))
			result = append(result, VolumeGroup{
				Name:    strings.TrimSpace(vg.Name),
				Size:    strings.TrimSpace(vg.Size),
				Free:    strings.TrimSpace(vg.Free),
				PVCount: pvCount,
				LVCount: lvCount,
				Attr:    strings.TrimSpace(vg.Attr),
			})
		}
	}
	return result, nil
}

// ListLVs returns all LVM logical volumes.
func (h *DiskHandler) ListLVs(c echo.Context) error {
	if !commandExists("lvs") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := exec.Command("lvs", "--reportformat", "json",
		"-o", "lv_name,vg_name,lv_size,lv_attr,lv_path,pool_lv,data_percent").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("lvs failed: %s", strings.TrimSpace(string(out))))
	}

	lvs, err := parseLVsJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("failed to parse lvs output: %v", err))
	}

	return response.OK(c, lvs)
}

// parseLVsJSON parses the JSON output from lvs --reportformat json.
func parseLVsJSON(data []byte) ([]LogicalVolume, error) {
	var raw struct {
		Report []struct {
			LV []struct {
				Name        string `json:"lv_name"`
				VGName      string `json:"vg_name"`
				Size        string `json:"lv_size"`
				Attr        string `json:"lv_attr"`
				Path        string `json:"lv_path"`
				PoolLV      string `json:"pool_lv"`
				DataPercent string `json:"data_percent"`
			} `json:"lv"`
		} `json:"report"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	result := []LogicalVolume{}
	for _, report := range raw.Report {
		for _, lv := range report.LV {
			result = append(result, LogicalVolume{
				Name:        strings.TrimSpace(lv.Name),
				VGName:      strings.TrimSpace(lv.VGName),
				Size:        strings.TrimSpace(lv.Size),
				Attr:        strings.TrimSpace(lv.Attr),
				Path:        strings.TrimSpace(lv.Path),
				PoolLV:      strings.TrimSpace(lv.PoolLV),
				DataPercent: strings.TrimSpace(lv.DataPercent),
			})
		}
	}
	return result, nil
}

// CreatePV creates a new LVM physical volume on a device.
func (h *DiskHandler) CreatePV(c echo.Context) error {
	if !commandExists("pvcreate") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreatePVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	devPath := "/dev/" + req.Device
	out, err := exec.Command("pvcreate", devPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("pvcreate failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("physical volume created on %s", req.Device),
	})
}

// CreateVG creates a new LVM volume group.
func (h *DiskHandler) CreateVG(c echo.Context) error {
	if !commandExists("vgcreate") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreateVGRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateLVMName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", err.Error())
	}
	if len(req.PVs) == 0 {
		return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS",
			"At least one physical volume is required")
	}

	// Validate all PV paths
	pvPaths := make([]string, 0, len(req.PVs))
	for _, pv := range req.PVs {
		// PVs can be provided as full paths (/dev/sdb1) or device names (sdb1)
		pvPath := pv
		if !strings.HasPrefix(pv, "/dev/") {
			if err := validateDeviceName(pv); err != nil {
				return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE",
					fmt.Sprintf("invalid PV device: %s", err.Error()))
			}
			pvPath = "/dev/" + pv
		} else {
			// Validate the part after /dev/
			devName := strings.TrimPrefix(pv, "/dev/")
			if err := validateDeviceName(devName); err != nil {
				return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE",
					fmt.Sprintf("invalid PV device: %s", err.Error()))
			}
		}
		pvPaths = append(pvPaths, pvPath)
	}

	args := append([]string{req.Name}, pvPaths...)
	out, err := exec.Command("vgcreate", args...).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("vgcreate failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("volume group %s created", req.Name),
	})
}

// CreateLV creates a new LVM logical volume.
func (h *DiskHandler) CreateLV(c echo.Context) error {
	if !commandExists("lvcreate") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreateLVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateLVMName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", err.Error())
	}
	if err := validateLVMName(req.VG); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_VG", err.Error())
	}
	if err := validateLVSize(req.Size); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_SIZE", err.Error())
	}

	out, err := exec.Command("lvcreate", "-L", req.Size, "-n", req.Name, req.VG).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("lvcreate failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s created in %s", req.Name, req.VG),
	})
}

// RemovePV removes an LVM physical volume.
func (h *DiskHandler) RemovePV(c echo.Context) error {
	if !commandExists("pvremove") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
	}

	devPath := "/dev/" + name
	out, err := exec.Command("pvremove", devPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("pvremove failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("physical volume %s removed", name),
	})
}

// RemoveVG removes an LVM volume group.
func (h *DiskHandler) RemoveVG(c echo.Context) error {
	if !commandExists("vgremove") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	name := c.Param("name")
	if err := validateLVMName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", err.Error())
	}

	out, err := exec.Command("vgremove", name).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("vgremove failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("volume group %s removed", name),
	})
}

// RemoveLV removes an LVM logical volume.
func (h *DiskHandler) RemoveLV(c echo.Context) error {
	if !commandExists("lvremove") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	vg := c.Param("vg")
	name := c.Param("name")
	if err := validateLVMName(vg); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_VG", err.Error())
	}
	if err := validateLVMName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", err.Error())
	}

	lvPath := vg + "/" + name
	out, err := exec.Command("lvremove", "-f", lvPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("lvremove failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s/%s removed", vg, name),
	})
}

// ResizeLV resizes an LVM logical volume.
func (h *DiskHandler) ResizeLV(c echo.Context) error {
	if !commandExists("lvresize") {
		return response.Fail(c, http.StatusServiceUnavailable, "TOOL_NOT_INSTALLED",
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req ResizeLVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateLVMName(req.VG); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_VG", err.Error())
	}
	if err := validateLVMName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_NAME", err.Error())
	}
	if err := validateLVSize(req.Size); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_SIZE", err.Error())
	}

	lvPath := "/dev/" + req.VG + "/" + req.Name
	out, err := exec.Command("lvresize", "-L", req.Size, lvPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "LVM_ERROR",
			fmt.Sprintf("lvresize failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s/%s resized to %s", req.VG, req.Name, req.Size),
	})
}

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
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if req.Device != "" {
		// Partition-based swap
		if err := validateDeviceName(req.Device); err != nil {
			return response.Fail(c, http.StatusBadRequest, "INVALID_DEVICE", err.Error())
		}

		devPath := "/dev/" + req.Device

		// Create swap signature
		mkswapOut, err := exec.Command("mkswap", devPath).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
				fmt.Sprintf("mkswap failed: %s", strings.TrimSpace(string(mkswapOut))))
		}

		// Enable the swap
		swaponOut, err := exec.Command("swapon", devPath).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
				fmt.Sprintf("swapon failed: %s", strings.TrimSpace(string(swaponOut))))
		}

		return response.OK(c, map[string]string{
			"message": fmt.Sprintf("swap enabled on %s", req.Device),
		})
	}

	if req.Path != "" {
		// File-based swap
		if err := validateDiskPath(req.Path); err != nil {
			return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
		}
		if req.SizeMB <= 0 {
			return response.Fail(c, http.StatusBadRequest, "INVALID_SIZE",
				"Size in MB is required for file-based swap")
		}

		sizeMB := strconv.FormatInt(req.SizeMB, 10)

		// Create the swap file with dd
		ddOut, err := exec.Command("dd", "if=/dev/zero", "of="+req.Path,
			"bs=1M", "count="+sizeMB).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
				fmt.Sprintf("dd failed: %s", strings.TrimSpace(string(ddOut))))
		}

		// Set permissions
		if err := os.Chmod(req.Path, 0600); err != nil {
			return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
				fmt.Sprintf("chmod failed: %v", err))
		}

		// Create swap signature
		mkswapOut, err := exec.Command("mkswap", req.Path).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
				fmt.Sprintf("mkswap failed: %s", strings.TrimSpace(string(mkswapOut))))
		}

		// Enable the swap
		swaponOut, err := exec.Command("swapon", req.Path).CombinedOutput()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
				fmt.Sprintf("swapon failed: %s", strings.TrimSpace(string(swaponOut))))
		}

		return response.OK(c, map[string]string{
			"message": fmt.Sprintf("swap file created and enabled at %s (%d MB)", req.Path, req.SizeMB),
		})
	}

	return response.Fail(c, http.StatusBadRequest, "MISSING_FIELDS",
		"Either device or path must be specified")
}

// RemoveSwap disables a swap area.
func (h *DiskHandler) RemoveSwap(c echo.Context) error {
	var req RemoveSwapRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDiskPath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	out, err := exec.Command("swapoff", req.Path).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
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
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if req.Value < 0 || req.Value > 200 {
		return response.Fail(c, http.StatusBadRequest, "INVALID_VALUE",
			"Swappiness value must be between 0 and 200")
	}

	valStr := fmt.Sprintf("vm.swappiness=%d", req.Value)
	out, err := exec.Command("sysctl", valStr).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "SWAP_ERROR",
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
		return response.Fail(c, http.StatusBadRequest, "MISSING_PATH", "path query param required")
	}
	if err := validateDiskPath(path); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	info, err := os.Stat(path)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, "FILE_NOT_FOUND", err.Error())
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
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDiskPath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}
	if req.NewSizeMB <= 0 {
		return response.Fail(c, http.StatusBadRequest, "INVALID_SIZE",
			"New size in MB must be positive")
	}

	// Verify it's a regular file (not a partition)
	info, err := os.Stat(req.Path)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, "FILE_NOT_FOUND",
			fmt.Sprintf("swap file not found: %v", err))
	}
	if !info.Mode().IsRegular() {
		return response.Fail(c, http.StatusBadRequest, "NOT_A_FILE",
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
		return response.Fail(c, http.StatusInternalServerError, "IO_ERROR",
			fmt.Sprintf("failed to read /proc/diskstats: %v", err))
	}

	stats, err := parseDiskStats(string(data))
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "IO_ERROR",
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
		return response.Fail(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
	}

	if err := validateDiskPath(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH", err.Error())
	}

	// Ensure path exists
	info, err := os.Stat(req.Path)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, "NOT_FOUND",
			fmt.Sprintf("path does not exist: %s", req.Path))
	}
	if !info.IsDir() {
		return response.Fail(c, http.StatusBadRequest, "INVALID_PATH",
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
			return response.Fail(c, http.StatusInternalServerError, "USAGE_ERROR",
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
