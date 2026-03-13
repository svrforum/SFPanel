package handlers

import (
	"fmt"
	"regexp"
	"strings"
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
	Status    string `json:"status"` // "ok", "warn", "fail"
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

// ExpandStep describes one step in the filesystem expansion chain.
type ExpandStep struct {
	Command     string `json:"command"`     // e.g. "growpart", "pvresize", "lvextend", "resize2fs"
	Description string `json:"description"` // human-readable
	Device      string `json:"device"`      // target device for this step
}

// ExpandCandidate describes a filesystem that can be expanded, along with the
// chain of commands that will be executed.
type ExpandCandidate struct {
	Source     string       `json:"source"`      // e.g. /dev/mapper/ubuntu--vg-ubuntu--lv
	FsType     string       `json:"fstype"`
	MountPoint string       `json:"mount_point"`
	CurrentSize int64       `json:"current_size"`
	FreeSpace   int64       `json:"free_space"`  // available space that can be reclaimed
	IsLVM       bool        `json:"is_lvm"`
	Steps       []ExpandStep `json:"steps"`
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

