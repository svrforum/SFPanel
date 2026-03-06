package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
)

// ---------- 4. Filesystems ----------

// ListFilesystems returns all mounted filesystems with usage information.
func (h *DiskHandler) ListFilesystems(c echo.Context) error {
	out, err := exec.Command("df", "-B1", "--output=source,fstype,size,used,avail,pcent,target").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFSError,
			fmt.Sprintf("df failed: %s", strings.TrimSpace(string(out))))
	}

	filesystems, err := parseDfOutput(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFSError,
			fmt.Sprintf("failed to parse df output: %v", err))
	}

	return response.OK(c, filesystems)
}

// CheckExpandable analyzes all filesystems and returns candidates that can be expanded.
// It detects the full VM expansion chain: disk free space → growpart → pvresize → lvextend → resize_fs.
func (h *DiskHandler) CheckExpandable(c echo.Context) error {
	// Get current filesystems
	out, err := exec.Command("df", "-B1", "--output=source,fstype,size,used,avail,pcent,target").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFSError,
			fmt.Sprintf("df failed: %s", strings.TrimSpace(string(out))))
	}
	filesystems, err := parseDfOutput(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFSError,
			fmt.Sprintf("failed to parse df output: %v", err))
	}

	resizableTypes := map[string]bool{
		"ext2": true, "ext3": true, "ext4": true,
		"xfs": true, "btrfs": true,
	}

	var candidates []ExpandCandidate

	for _, fs := range filesystems {
		if !resizableTypes[fs.FsType] {
			continue
		}
		if !strings.HasPrefix(fs.Source, "/dev/") {
			continue
		}
		if (fs.FsType == "xfs" || fs.FsType == "btrfs") && fs.MountPoint == "" {
			continue
		}

		candidate := ExpandCandidate{
			Source:      fs.Source,
			FsType:      fs.FsType,
			MountPoint:  fs.MountPoint,
			CurrentSize: fs.Size,
			IsLVM:       strings.HasPrefix(fs.Source, "/dev/mapper/"),
		}

		var steps []ExpandStep
		var totalFree int64

		if candidate.IsLVM && commandExists("lvs") {
			// LVM path: check VG free, then trace back to PV → disk for growpart
			vgName, vgFree := getVGInfoForLV(fs.Source)
			if vgName == "" {
				continue // not actually an LV
			}

			// Check if the disk behind the PV has unallocated space
			pvDevice := getPVDeviceForVG(vgName)
			if pvDevice != "" {
				parentDisk, partNum := getParentDisk(pvDevice)
				if parentDisk != "" && partNum != "" {
					diskFree := getDiskFreeBytes(parentDisk)
					if diskFree > 0 {
						totalFree += diskFree
						if commandExists("growpart") {
							steps = append(steps, ExpandStep{
								Command:     "growpart",
								Description: fmt.Sprintf("Grow partition %s on %s (+%s)", partNum, parentDisk, formatBytesGo(diskFree)),
								Device:      parentDisk + " " + partNum,
							})
						}
						steps = append(steps, ExpandStep{
							Command:     "pvresize",
							Description: fmt.Sprintf("Resize PV %s", pvDevice),
							Device:      pvDevice,
						})
					}
				}
			}

			// VG free space (existing or will be gained from growpart+pvresize)
			if vgFree > 0 || totalFree > 0 {
				totalFree += vgFree
				steps = append(steps, ExpandStep{
					Command:     "lvextend",
					Description: fmt.Sprintf("Extend LV %s to use all VG free space", fs.Source),
					Device:      fs.Source,
				})
			}

			if len(steps) > 0 {
				// Final step: resize filesystem
				resizeCmd := getResizeCommand(fs.FsType)
				if resizeCmd != "" && commandExists(resizeCmd) {
					target := fs.Source
					if fs.FsType == "xfs" || fs.FsType == "btrfs" {
						target = fs.MountPoint
					}
					steps = append(steps, ExpandStep{
						Command:     resizeCmd,
						Description: fmt.Sprintf("Resize %s filesystem", fs.FsType),
						Device:      target,
					})
				}
			}
		} else {
			// Non-LVM path: check if partition's parent disk has free space
			parentDisk, partNum := getParentDisk(fs.Source)
			if parentDisk != "" && partNum != "" {
				diskFree := getDiskFreeBytes(parentDisk)
				if diskFree > 0 {
					totalFree = diskFree
					if commandExists("growpart") {
						steps = append(steps, ExpandStep{
							Command:     "growpart",
							Description: fmt.Sprintf("Grow partition %s on %s (+%s)", partNum, parentDisk, formatBytesGo(diskFree)),
							Device:      parentDisk + " " + partNum,
						})
					}
					resizeCmd := getResizeCommand(fs.FsType)
					if resizeCmd != "" && commandExists(resizeCmd) {
						target := fs.Source
						if fs.FsType == "xfs" || fs.FsType == "btrfs" {
							target = fs.MountPoint
						}
						steps = append(steps, ExpandStep{
							Command:     resizeCmd,
							Description: fmt.Sprintf("Resize %s filesystem", fs.FsType),
							Device:      target,
						})
					}
				}
			}
		}

		if len(steps) > 0 && totalFree > 0 {
			candidate.Steps = steps
			candidate.FreeSpace = totalFree
			candidates = append(candidates, candidate)
		}
	}

	if candidates == nil {
		candidates = []ExpandCandidate{}
	}

	return response.OK(c, candidates)
}

// ExpandFilesystem executes the full expansion chain for a given filesystem source.
func (h *DiskHandler) ExpandFilesystem(c echo.Context) error {
	var req struct {
		Source string `json:"source"` // full path like /dev/mapper/ubuntu--vg-ubuntu--lv or /dev/sda2
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if req.Source == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "source is required")
	}

	// Validate the source path
	if !strings.HasPrefix(req.Source, "/dev/") {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, "source must start with /dev/")
	}
	// Strip /dev/ for validation, then add back
	devName := strings.TrimPrefix(req.Source, "/dev/")
	if err := validateDeviceName(devName); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	// Detect filesystem type
	blkOut, err := exec.Command("blkid", "-o", "value", "-s", "TYPE", req.Source).Output()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
			"failed to detect filesystem type")
	}
	fsType := strings.TrimSpace(string(blkOut))

	resizableTypes := map[string]bool{
		"ext2": true, "ext3": true, "ext4": true,
		"xfs": true, "btrfs": true,
	}
	if !resizableTypes[fsType] {
		return response.Fail(c, http.StatusBadRequest, response.ErrExpandError,
			fmt.Sprintf("filesystem type %s does not support expansion", fsType))
	}

	isLVM := strings.HasPrefix(req.Source, "/dev/mapper/")
	var executedSteps []string

	if isLVM && commandExists("lvs") {
		vgName, _ := getVGInfoForLV(req.Source)
		if vgName == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrExpandError, "not an LVM logical volume")
		}

		pvDevice := getPVDeviceForVG(vgName)
		if pvDevice != "" {
			parentDisk, partNum := getParentDisk(pvDevice)
			if parentDisk != "" && partNum != "" {
				diskFree := getDiskFreeBytes(parentDisk)
				if diskFree > 0 {
					// Step 1: growpart
					if commandExists("growpart") {
						gpOut, err := exec.Command("growpart", parentDisk, partNum).CombinedOutput()
						gpMsg := strings.TrimSpace(string(gpOut))
						if err != nil && !strings.Contains(gpMsg, "NOCHANGE") {
							return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
								fmt.Sprintf("growpart failed: %s", gpMsg))
						}
						executedSteps = append(executedSteps, "growpart "+parentDisk+" "+partNum)
					}

					// Step 2: pvresize
					if commandExists("pvresize") {
						pvOut, err := exec.Command("pvresize", pvDevice).CombinedOutput()
						if err != nil {
							return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
								fmt.Sprintf("pvresize failed: %s", strings.TrimSpace(string(pvOut))))
						}
						executedSteps = append(executedSteps, "pvresize "+pvDevice)
					}
				}
			}
		}

		// Step 3: lvextend
		if commandExists("lvextend") {
			lvOut, err := exec.Command("lvextend", "-l", "+100%FREE", req.Source).CombinedOutput()
			if err != nil {
				errMsg := strings.TrimSpace(string(lvOut))
				if !strings.Contains(strings.ToLower(errMsg), "insufficient") &&
					!strings.Contains(strings.ToLower(errMsg), "unchanged") &&
					!strings.Contains(strings.ToLower(errMsg), "no free") {
					return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
						fmt.Sprintf("lvextend failed: %s", errMsg))
				}
			} else {
				executedSteps = append(executedSteps, "lvextend -l +100%FREE "+req.Source)
			}
		}
	} else {
		// Non-LVM: growpart then resize
		parentDisk, partNum := getParentDisk(req.Source)
		if parentDisk != "" && partNum != "" {
			diskFree := getDiskFreeBytes(parentDisk)
			if diskFree > 0 && commandExists("growpart") {
				gpOut, err := exec.Command("growpart", parentDisk, partNum).CombinedOutput()
				gpMsg := strings.TrimSpace(string(gpOut))
				if err != nil && !strings.Contains(gpMsg, "NOCHANGE") {
					return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
						fmt.Sprintf("growpart failed: %s", gpMsg))
				}
				executedSteps = append(executedSteps, "growpart "+parentDisk+" "+partNum)
			}
		}
	}

	// Final step: resize filesystem
	resizeCmd := getResizeCommand(fsType)
	if resizeCmd == "" || !commandExists(resizeCmd) {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			fmt.Sprintf("%s is not installed", resizeCmd))
	}

	var cmd *exec.Cmd
	switch fsType {
	case "ext2", "ext3", "ext4":
		cmd = exec.Command("resize2fs", req.Source)
	case "xfs":
		mp, err := findMountPoint(req.Source)
		if err != nil || mp == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrExpandError, "XFS must be mounted")
		}
		cmd = exec.Command("xfs_growfs", mp)
	case "btrfs":
		mp, err := findMountPoint(req.Source)
		if err != nil || mp == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrExpandError, "Btrfs must be mounted")
		}
		cmd = exec.Command("btrfs", "filesystem", "resize", "max", mp)
	}

	resOut, err := cmd.CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
			fmt.Sprintf("filesystem resize failed: %s", strings.TrimSpace(string(resOut))))
	}
	executedSteps = append(executedSteps, resizeCmd+" "+req.Source)

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("filesystem on %s expanded successfully", req.Source),
		"steps":   executedSteps,
	})
}

// getVGInfoForLV returns the VG name and free space (bytes) for an LV device path.
func getVGInfoForLV(lvDevice string) (vgName string, vgFree int64) {
	out, err := exec.Command("lvs", "--noheadings", "--nosuffix", "--units", "b",
		"-o", "vg_name,vg_free", lvDevice).Output()
	if err != nil {
		return "", 0
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return "", 0
	}
	vgName = fields[0]
	if f, err := strconv.ParseFloat(fields[1], 64); err == nil {
		vgFree = int64(f)
	}
	return
}

// getPVDeviceForVG returns the first PV device path for a given VG name.
func getPVDeviceForVG(vgName string) string {
	if !commandExists("pvs") {
		return ""
	}
	out, err := exec.Command("pvs", "--noheadings", "-o", "pv_name",
		"-S", fmt.Sprintf("vg_name=%s", vgName)).Output()
	if err != nil {
		return ""
	}
	pv := strings.TrimSpace(string(out))
	lines := strings.Split(pv, "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

// getParentDisk extracts the parent disk device and partition number from a partition path.
// e.g., /dev/sda2 → ("/dev/sda", "2"), /dev/nvme0n1p3 → ("/dev/nvme0n1", "3")
func getParentDisk(partDevice string) (disk string, partNum string) {
	// Handle /dev/nvme*p* and /dev/loop*p*
	if idx := strings.LastIndex(partDevice, "p"); idx > 0 {
		suffix := partDevice[idx+1:]
		if _, err := strconv.Atoi(suffix); err == nil {
			prefix := partDevice[:idx]
			// Make sure prefix ends with a digit (nvme0n1, loop0, etc.)
			if len(prefix) > 0 && prefix[len(prefix)-1] >= '0' && prefix[len(prefix)-1] <= '9' {
				return prefix, suffix
			}
		}
	}
	// Handle /dev/sda1, /dev/vda2, etc.
	i := len(partDevice) - 1
	for i >= 0 && partDevice[i] >= '0' && partDevice[i] <= '9' {
		i--
	}
	if i < len(partDevice)-1 && i >= 0 {
		disk = partDevice[:i+1]
		partNum = partDevice[i+1:]
		// Verify disk ends with a letter (sd[a-z], vd[a-z], etc.)
		if disk[len(disk)-1] >= 'a' && disk[len(disk)-1] <= 'z' {
			return disk, partNum
		}
	}
	return "", ""
}

// getDiskFreeBytes returns the total unallocated space (bytes) on a disk device.
func getDiskFreeBytes(disk string) int64 {
	if !commandExists("sfdisk") {
		return 0
	}
	// sfdisk --list-free outputs free regions; we parse total free space
	out, err := exec.Command("sfdisk", "--list-free", "-o", "size", "--bytes", disk).Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var total int64
	for _, line := range lines[1:] { // skip header
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if v, err := strconv.ParseInt(line, 10, 64); err == nil {
			total += v
		}
	}
	return total
}

// getResizeCommand returns the command name for resizing a given filesystem type.
func getResizeCommand(fsType string) string {
	switch fsType {
	case "ext2", "ext3", "ext4":
		return "resize2fs"
	case "xfs":
		return "xfs_growfs"
	case "btrfs":
		return "btrfs"
	}
	return ""
}

// formatBytesGo formats bytes into a human-readable string.
func formatBytesGo(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if err := validateFsType(req.FsType); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType, err.Error())
	}

	devPath := "/dev/" + req.Device

	// Build the mkfs command based on filesystem type
	var cmd *exec.Cmd
	switch req.FsType {
	case "ext2", "ext3", "ext4":
		mkfsCmd := "mkfs." + req.FsType
		if !commandExists(mkfsCmd) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
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
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
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
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
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
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
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
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"mkfs.ntfs is not installed. Install ntfs-3g: apt install ntfs-3g")
		}
		args := []string{"-F"}
		if req.Label != "" {
			args = append(args, "-L", req.Label)
		}
		args = append(args, devPath)
		cmd = exec.Command("mkfs.ntfs", args...)

	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType,
			fmt.Sprintf("unsupported filesystem type: %s", req.FsType))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFormatError,
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if err := validateDiskPath(req.MountPoint); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint, err.Error())
	}

	devPath := "/dev/" + req.Device

	// Ensure mount point directory exists
	if err := os.MkdirAll(req.MountPoint, 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
			fmt.Sprintf("failed to create mount point directory: %v", err))
	}

	args := []string{}
	if req.FsType != "" {
		if err := validateFsType(req.FsType); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType, err.Error())
		}
		args = append(args, "-t", req.FsType)
	}
	if req.Options != "" {
		// Validate options: only allow safe characters
		if !validDiskPath.MatchString(req.Options) && !regexp.MustCompile(`^[a-zA-Z0-9,=_-]+$`).MatchString(req.Options) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidOptions,
				"mount options contain invalid characters")
		}
		args = append(args, "-o", req.Options)
	}
	args = append(args, devPath, req.MountPoint)

	out, err := exec.Command("mount", args...).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDiskPath(req.MountPoint); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint, err.Error())
	}

	out, err := exec.Command("umount", req.MountPoint).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUnmountError,
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
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	devPath := "/dev/" + req.Device

	// For LVM devices: extend LV to use all available VG free space first
	if strings.HasPrefix(devPath, "/dev/mapper/") && commandExists("lvextend") {
		// Verify it's actually an LV (lvs will fail for non-LV mapper devices)
		if _, err := exec.Command("lvs", "--noheadings", devPath).Output(); err == nil {
			lvOut, err := exec.Command("lvextend", "-l", "+100%FREE", devPath).CombinedOutput()
			if err != nil {
				errMsg := strings.TrimSpace(string(lvOut))
				// "insufficient free space" or "unchanged" are expected when VG is full
				if !strings.Contains(strings.ToLower(errMsg), "insufficient") &&
					!strings.Contains(strings.ToLower(errMsg), "unchanged") &&
					!strings.Contains(strings.ToLower(errMsg), "no free") {
					return response.Fail(c, http.StatusInternalServerError, response.ErrResizeError,
						fmt.Sprintf("lvextend failed: %s", errMsg))
				}
			}
		}
	}

	// Determine filesystem type if not provided
	fsType := req.FsType
	if fsType == "" {
		// Auto-detect using blkid
		blkOut, err := exec.Command("blkid", "-o", "value", "-s", "TYPE", devPath).Output()
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrResizeError,
				"failed to detect filesystem type; please specify fs_type")
		}
		fsType = strings.TrimSpace(string(blkOut))
	}

	var cmd *exec.Cmd
	switch fsType {
	case "ext2", "ext3", "ext4":
		if !commandExists("resize2fs") {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"resize2fs is not installed. Install e2fsprogs: apt install e2fsprogs")
		}
		cmd = exec.Command("resize2fs", devPath)

	case "xfs":
		if !commandExists("xfs_growfs") {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"xfs_growfs is not installed. Install xfsprogs: apt install xfsprogs")
		}
		// xfs_growfs requires the mount point, not the device
		mountPoint, err := findMountPoint(devPath)
		if err != nil || mountPoint == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrResizeError,
				"XFS filesystem must be mounted before resizing. Could not find mount point.")
		}
		cmd = exec.Command("xfs_growfs", mountPoint)

	case "btrfs":
		if !commandExists("btrfs") {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"btrfs is not installed. Install btrfs-progs: apt install btrfs-progs")
		}
		mountPoint, err := findMountPoint(devPath)
		if err != nil || mountPoint == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrResizeError,
				"Btrfs filesystem must be mounted before resizing. Could not find mount point.")
		}
		cmd = exec.Command("btrfs", "filesystem", "resize", "max", mountPoint)

	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrResizeError,
			fmt.Sprintf("resize not supported for filesystem type: %s", fsType))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrResizeError,
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
