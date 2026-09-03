package disk

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// validLabel matches safe filesystem labels (alphanumeric, spaces, underscores, dots, hyphens; max 16 chars).
var validLabel = regexp.MustCompile(`^[a-zA-Z0-9 _.-]{0,16}$`)

// findDeviceMountpoint is the indirection used by FormatPartition's protected-
// path check. It is overridable in tests so the /proc/mounts dependency is
// not required to exercise the guard.
var findDeviceMountpoint = func(devPath string) (string, error) {
	return findMountPoint(devPath)
}

// ---------- 4. Filesystems ----------

// ListFilesystems returns all mounted filesystems with usage information.
func (h *Handler) ListFilesystems(c echo.Context) error {
	// Remote cluster nodes reached via ?node= may lack coreutils' df (most
	// commonly on busybox-based minimal images). Return an empty list
	// rather than a 500 so the UI can degrade gracefully on that node.
	if !h.Cmd.Exists("df") {
		return response.OK(c, []Filesystem{})
	}
	filesystems, err := listFilesystems(c.Request().Context(), h.Cmd)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFSError,
			fmt.Sprintf("df failed: %s", response.SanitizeOutput(err.Error())))
	}
	sortFilesystems(filesystems)
	return response.OK(c, filesystems)
}

// networkMountTimeout bounds how long one network mount may hold the listing.
//
// A single `df` over every mount blocks in statfs on a share whose server has
// gone away, and it blocks for as long as the caller waits: the dashboard
// polls this every thirty seconds and gave up after thirty, so a dead NFS
// server turned into one hung df per viewer per poll, and the disk page's
// filesystems tab into a spinner. Measured on this host with its NAS off:
// `df -B1` did not return in twenty seconds. Three seconds is long enough
// for a share on a slow WAN link to answer and short enough that a dead one
// costs one badge instead of the page.
const networkMountTimeout = 3 * time.Second

// unresponsiveMemo remembers which mounts recently failed to answer.
//
// Probing a dead mount costs a df that has to be killed, and on a kernel where
// a hard NFS mount still cannot be interrupted that df stays behind in D
// state. One probe per mount per interval bounds how many of those can
// accumulate while a server is down.
var unresponsiveMemo = struct {
	sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

const unresponsiveMemoTTL = 30 * time.Second

// mountEntry is one line of /proc/mounts, as much of it as the listing needs.
type mountEntry struct {
	source, mountPoint, fstype string
}

// readMountTable is overridable in tests.
var readMountTable = func() ([]mountEntry, error) {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	var out []mountEntry
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		out = append(out, mountEntry{source: unescapeMount(f[0]), mountPoint: unescapeMount(f[1]), fstype: f[2]})
	}
	return out, nil
}

// unescapeMount undoes the octal escapes /proc/mounts uses for whitespace.
func unescapeMount(s string) string {
	return strings.NewReplacer(`\\040`, " ", `\\011`, "\t", `\\012`, "\n", `\\134`, `\\`).Replace(s)
}

// isRemoteMount reports whether a mount lives on another machine.
//
// Broader than isNetworkFstype on purpose: that list is what the panel itself
// can attach, while this has to catch everything df would block on — sshfs
// (user@host:/path), GlusterFS (host:/vol), 9p, anything whose source names a
// host. The two signals together cover what df's own remote test does.
func isRemoteMount(m mountEntry) bool {
	if isNetworkFstype(m.fstype) {
		return true
	}
	switch m.fstype {
	case "fuse.sshfs", "glusterfs", "fuse.glusterfs", "ceph", "9p", "afs", "ncpfs", "fuse.rclone":
		return true
	}
	return strings.HasPrefix(m.source, "//") || strings.Contains(m.source, ":/")
}

// listFilesystems runs df in two parts so a dead network mount cannot hold
// the local ones hostage.
//
// Local filesystems come from one `df -l`, which skips remote mounts without
// touching them. Each remote mount is then asked on its own, concurrently,
// under networkMountTimeout; one that does not answer is listed as
// unresponsive with no numbers rather than dropped, because a share that has
// gone quiet is exactly what an operator opens this page to find out.
func listFilesystems(ctx context.Context, cmd exec.Commander) ([]Filesystem, error) {
	const cols = "--output=source,fstype,size,used,avail,pcent,target"

	out, err := cmd.RunCtx(ctx, "df", "-B1", "-l", cols)
	if err != nil {
		return nil, fmt.Errorf("%s", strings.TrimSpace(out))
	}
	local, err := parseDfOutput(out)
	if err != nil {
		return nil, err
	}

	mounts, mErr := readMountTable()
	if mErr != nil {
		// No mount table means no way to find the remote ones; the local
		// list is still correct and still worth returning.
		return local, nil
	}
	var remote []mountEntry
	seen := map[string]bool{}
	for _, m := range mounts {
		if !isRemoteMount(m) || seen[m.mountPoint] {
			continue
		}
		seen[m.mountPoint] = true
		remote = append(remote, m)
	}
	if len(remote) == 0 {
		return local, nil
	}

	results := make([]Filesystem, len(remote))
	var wg sync.WaitGroup
	for i, m := range remote {
		wg.Add(1)
		go func(i int, m mountEntry) {
			defer wg.Done()
			results[i] = probeRemoteMount(ctx, cmd, m, cols)
		}(i, m)
	}
	wg.Wait()
	return append(local, results...), nil
}

// probeRemoteMount asks df about one mount, bounded, and remembers a silence.
func probeRemoteMount(ctx context.Context, cmd exec.Commander, m mountEntry, cols string) Filesystem {
	silent := Filesystem{Source: m.source, FsType: m.fstype, MountPoint: m.mountPoint, Unresponsive: true}

	unresponsiveMemo.Lock()
	until, remembered := unresponsiveMemo.until[m.mountPoint]
	unresponsiveMemo.Unlock()
	if remembered && time.Now().Before(until) {
		return silent
	}

	pctx, cancel := context.WithTimeout(ctx, networkMountTimeout)
	defer cancel()
	out, err := cmd.RunCtx(pctx, "df", "-B1", cols, m.mountPoint)
	if err != nil {
		unresponsiveMemo.Lock()
		unresponsiveMemo.until[m.mountPoint] = time.Now().Add(unresponsiveMemoTTL)
		unresponsiveMemo.Unlock()
		return silent
	}
	parsed, perr := parseDfOutput(out)
	if perr != nil || len(parsed) == 0 {
		return silent
	}
	unresponsiveMemo.Lock()
	delete(unresponsiveMemo.until, m.mountPoint)
	unresponsiveMemo.Unlock()
	return parsed[0]
}

// sortFilesystems orders the list for display.
//
// df emits mounts in kernel order, which puts the most recently mounted last —
// exactly where an operator who just attached a network drive will not look
// for it. Ordering is: network drives, then local block devices, then
// everything else (pseudo filesystems and container layers). Within a group,
// by mount point, so the order does not shuffle between refreshes.
//
// Sorting lives here rather than in parseDfOutput: that function's job is to
// reproduce df faithfully, and its tests assert exactly that.
func sortFilesystems(fs []Filesystem) {
	rank := func(f Filesystem) int {
		switch {
		case isNetworkFstype(f.FsType):
			return 0
		case strings.HasPrefix(f.Source, "/dev/"):
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(fs, func(i, j int) bool {
		ri, rj := rank(fs[i]), rank(fs[j])
		if ri != rj {
			return ri < rj
		}
		return fs[i].MountPoint < fs[j].MountPoint
	})
}

// CheckExpandable analyzes all filesystems and returns candidates that can be expanded.
// It detects the full VM expansion chain: disk free space -> growpart -> pvresize -> lvextend -> resize_fs.
func (h *Handler) CheckExpandable(c echo.Context) error {
	ctx := c.Request().Context()
	// Get current filesystems
	out, err := h.Cmd.RunCtx(ctx, "df", "-B1", "--output=source,fstype,size,used,avail,pcent,target")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFSError,
			fmt.Sprintf("df failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
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

	candidates := make([]ExpandCandidate, 0)

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

		steps := make([]ExpandStep, 0)
		var totalFree int64

		if candidate.IsLVM && h.Cmd.Exists("lvs") {
			// LVM path: check VG free, then trace back to PV -> disk for growpart
			vgName, vgFree := h.getVGInfoForLV(ctx, fs.Source)
			if vgName == "" {
				continue // not actually an LV
			}

			// Check if the disk behind the PV has unallocated space
			pvDevice := h.getPVDeviceForVG(ctx, vgName)
			if pvDevice != "" {
				parentDisk, partNum := getParentDisk(pvDevice)
				if parentDisk != "" && partNum != "" {
					diskFree := h.getDiskFreeBytes(ctx, parentDisk)
					if diskFree > 0 {
						totalFree += diskFree
						if h.Cmd.Exists("growpart") {
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
				if resizeCmd != "" && h.Cmd.Exists(resizeCmd) {
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
				diskFree := h.getDiskFreeBytes(ctx, parentDisk)
				if diskFree > 0 {
					totalFree = diskFree
					if h.Cmd.Exists("growpart") {
						steps = append(steps, ExpandStep{
							Command:     "growpart",
							Description: fmt.Sprintf("Grow partition %s on %s (+%s)", partNum, parentDisk, formatBytesGo(diskFree)),
							Device:      parentDisk + " " + partNum,
						})
					}
					resizeCmd := getResizeCommand(fs.FsType)
					if resizeCmd != "" && h.Cmd.Exists(resizeCmd) {
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
func (h *Handler) ExpandFilesystem(c echo.Context) error {
	ctx := c.Request().Context()
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
	// Validate the device leaf. LVM/dm targets are /dev/mapper/<name>, whose
	// "mapper/<name>" leaf contains a '/' that validateDeviceName rejects —
	// which made the entire isLVM branch below unreachable. Validate the
	// mapper leaf against the LVM name charset instead (still no '/' or '..').
	if strings.HasPrefix(req.Source, "/dev/mapper/") {
		mapperName := strings.TrimPrefix(req.Source, "/dev/mapper/")
		if mapperName == "" || !validLVMName.MatchString(mapperName) || strings.Contains(mapperName, "..") {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, "invalid device name: "+mapperName)
		}
	} else if err := validateDeviceName(strings.TrimPrefix(req.Source, "/dev/")); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	// Detect filesystem type
	blkOut, err := h.Cmd.RunCtx(ctx, "blkid", "-o", "value", "-s", "TYPE", req.Source)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
			"failed to detect filesystem type")
	}
	fsType := strings.TrimSpace(blkOut)

	resizableTypes := map[string]bool{
		"ext2": true, "ext3": true, "ext4": true,
		"xfs": true, "btrfs": true,
	}
	if !resizableTypes[fsType] {
		return response.Fail(c, http.StatusBadRequest, response.ErrExpandError,
			fmt.Sprintf("filesystem type %s does not support expansion", fsType))
	}

	isLVM := strings.HasPrefix(req.Source, "/dev/mapper/")
	executedSteps := make([]string, 0)

	if isLVM && h.Cmd.Exists("lvs") {
		vgName, _ := h.getVGInfoForLV(ctx, req.Source)
		if vgName == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrExpandError, "not an LVM logical volume")
		}

		pvDevice := h.getPVDeviceForVG(ctx, vgName)
		if pvDevice != "" {
			parentDisk, partNum := getParentDisk(pvDevice)
			if parentDisk != "" && partNum != "" {
				diskFree := h.getDiskFreeBytes(ctx, parentDisk)
				if diskFree > 0 {
					// Step 1: growpart
					if h.Cmd.Exists("growpart") {
						gpOut, err := h.Cmd.RunCtx(ctx, "growpart", parentDisk, partNum)
						gpMsg := strings.TrimSpace(gpOut)
						if err != nil && !strings.Contains(gpMsg, "NOCHANGE") {
							return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
								fmt.Sprintf("growpart failed: %s", response.SanitizeOutput(gpMsg)))
						}
						executedSteps = append(executedSteps, "growpart "+parentDisk+" "+partNum)
					}

					// Step 2: pvresize
					if h.Cmd.Exists("pvresize") {
						pvOut, err := h.Cmd.RunCtx(ctx, "pvresize", pvDevice)
						if err != nil {
							return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
								fmt.Sprintf("pvresize failed: %s", response.SanitizeOutput(strings.TrimSpace(pvOut))))
						}
						executedSteps = append(executedSteps, "pvresize "+pvDevice)
					}
				}
			}
		}

		// Step 3: lvextend
		if h.Cmd.Exists("lvextend") {
			lvOut, err := h.Cmd.RunCtx(ctx, "lvextend", "-l", "+100%FREE", req.Source)
			if err != nil {
				errMsg := strings.TrimSpace(lvOut)
				if !strings.Contains(strings.ToLower(errMsg), "insufficient") &&
					!strings.Contains(strings.ToLower(errMsg), "unchanged") &&
					!strings.Contains(strings.ToLower(errMsg), "no free") {
					return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
						fmt.Sprintf("lvextend failed: %s", response.SanitizeOutput(errMsg)))
				}
			} else {
				executedSteps = append(executedSteps, "lvextend -l +100%FREE "+req.Source)
			}
		}
	} else {
		// Non-LVM: growpart then resize
		parentDisk, partNum := getParentDisk(req.Source)
		if parentDisk != "" && partNum != "" {
			diskFree := h.getDiskFreeBytes(ctx, parentDisk)
			if diskFree > 0 && h.Cmd.Exists("growpart") {
				gpOut, err := h.Cmd.RunCtx(ctx, "growpart", parentDisk, partNum)
				gpMsg := strings.TrimSpace(gpOut)
				if err != nil && !strings.Contains(gpMsg, "NOCHANGE") {
					return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
						fmt.Sprintf("growpart failed: %s", response.SanitizeOutput(gpMsg)))
				}
				executedSteps = append(executedSteps, "growpart "+parentDisk+" "+partNum)
			}
		}
	}

	// Final step: resize filesystem
	resizeCmd := getResizeCommand(fsType)
	if resizeCmd == "" || !h.Cmd.Exists(resizeCmd) {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			fmt.Sprintf("%s is not installed", resizeCmd))
	}

	var resOut string
	var resErr error
	switch fsType {
	case "ext2", "ext3", "ext4":
		resOut, resErr = h.Cmd.RunCtx(ctx, "resize2fs", req.Source)
	case "xfs":
		mp, mpErr := findMountPoint(req.Source)
		if mpErr != nil || mp == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrExpandError, "XFS must be mounted")
		}
		resOut, resErr = h.Cmd.RunCtx(ctx, "xfs_growfs", mp)
	case "btrfs":
		mp, mpErr := findMountPoint(req.Source)
		if mpErr != nil || mp == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrExpandError, "Btrfs must be mounted")
		}
		resOut, resErr = h.Cmd.RunCtx(ctx, "btrfs", "filesystem", "resize", "max", mp)
	}

	if resErr != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrExpandError,
			fmt.Sprintf("filesystem resize failed: %s", response.SanitizeOutput(strings.TrimSpace(resOut))))
	}
	executedSteps = append(executedSteps, resizeCmd+" "+req.Source)

	return response.OK(c, map[string]interface{}{
		"message": fmt.Sprintf("filesystem on %s expanded successfully", req.Source),
		"steps":   executedSteps,
	})
}

// getVGInfoForLV returns the VG name and free space (bytes) for an LV device path.
// ctx is propagated to the lvs subprocess so caller cancellation kills the work.
func (h *Handler) getVGInfoForLV(ctx context.Context, lvDevice string) (vgName string, vgFree int64) {
	out, err := h.Cmd.RunCtx(ctx, "lvs", "--noheadings", "--nosuffix", "--units", "b",
		"-o", "vg_name,vg_free", lvDevice)
	if err != nil {
		return "", 0
	}
	fields := strings.Fields(strings.TrimSpace(out))
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
// ctx is propagated to the pvs subprocess so caller cancellation kills the work.
func (h *Handler) getPVDeviceForVG(ctx context.Context, vgName string) string {
	if !h.Cmd.Exists("pvs") {
		return ""
	}
	if err := validateLVMName(vgName); err != nil {
		return ""
	}
	out, err := h.Cmd.RunCtx(ctx, "pvs", "--noheadings", "-o", "pv_name",
		"-S", fmt.Sprintf("vg_name=%s", vgName))
	if err != nil {
		return ""
	}
	pv := strings.TrimSpace(out)
	lines := strings.Split(pv, "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

// getParentDisk extracts the parent disk device and partition number from a partition path.
// e.g., /dev/sda2 -> ("/dev/sda", "2"), /dev/nvme0n1p3 -> ("/dev/nvme0n1", "3")
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
// ctx is propagated to the sfdisk subprocess so caller cancellation kills the work.
func (h *Handler) getDiskFreeBytes(ctx context.Context, disk string) int64 {
	if !h.Cmd.Exists("sfdisk") {
		return 0
	}
	// sfdisk --list-free outputs free regions; we parse total free space
	out, err := h.Cmd.RunCtx(ctx, "sfdisk", "--list-free", "-o", "size", "--bytes", disk)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
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
func parseDfOutput(data string) ([]Filesystem, error) {
	lines := strings.Split(strings.TrimSpace(data), "\n")
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
func (h *Handler) FormatPartition(c echo.Context) error {
	var req FormatPartitionRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	// Refuse to format a device that is currently mounted at a protected
	// system path (e.g., wiping /dev/sda1 while it backs /boot would brick
	// the host). findMountPoint returns "" when not mounted; in that case
	// the format proceeds normally.
	if mp, err := findDeviceMountpoint("/dev/" + req.Device); err == nil && mp != "" {
		if isProtectedMountpoint(mp) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
				"device is mounted at a protected system path")
		}
	}
	if err := validateFsType(req.FsType); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType, err.Error())
	}

	if req.Label != "" {
		if !validLabel.MatchString(req.Label) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				"invalid label: must be alphanumeric/spaces/underscores/dots/hyphens, max 16 characters")
		}
		if strings.HasPrefix(req.Label, "-") {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue,
				"invalid label: must not start with '-'")
		}
	}

	devPath := "/dev/" + req.Device

	// Verify the device exists and is a block/character device, not a regular file or directory.
	devInfo, err := os.Stat(devPath)
	if err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
				fmt.Sprintf("device does not exist: %s", devPath))
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot stat device: %v", err))
	}
	if devInfo.Mode().IsRegular() || devInfo.IsDir() {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
			fmt.Sprintf("%s is not a block device", devPath))
	}

	// Build the mkfs command based on filesystem type
	var mkfsName string
	var mkfsArgs []string
	switch req.FsType {
	case "ext2", "ext3", "ext4":
		mkfsName = "mkfs." + req.FsType
		if !h.Cmd.Exists(mkfsName) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				fmt.Sprintf("%s is not installed", mkfsName))
		}
		mkfsArgs = []string{"-F"}
		if req.Label != "" {
			mkfsArgs = append(mkfsArgs, "-L", req.Label)
		}
		mkfsArgs = append(mkfsArgs, devPath)

	case "xfs":
		mkfsName = "mkfs.xfs"
		if !h.Cmd.Exists(mkfsName) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"mkfs.xfs is not installed. Install xfsprogs: apt install xfsprogs")
		}
		mkfsArgs = []string{"-f"}
		if req.Label != "" {
			mkfsArgs = append(mkfsArgs, "-L", req.Label)
		}
		mkfsArgs = append(mkfsArgs, devPath)

	case "btrfs":
		mkfsName = "mkfs.btrfs"
		if !h.Cmd.Exists(mkfsName) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"mkfs.btrfs is not installed. Install btrfs-progs: apt install btrfs-progs")
		}
		mkfsArgs = []string{"-f"}
		if req.Label != "" {
			mkfsArgs = append(mkfsArgs, "-L", req.Label)
		}
		mkfsArgs = append(mkfsArgs, devPath)

	case "vfat", "fat32":
		mkfsName = "mkfs.vfat"
		if !h.Cmd.Exists(mkfsName) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"mkfs.vfat is not installed. Install dosfstools: apt install dosfstools")
		}
		if req.Label != "" {
			mkfsArgs = append(mkfsArgs, "-n", req.Label)
		}
		mkfsArgs = append(mkfsArgs, devPath)

	case "ntfs":
		mkfsName = "mkfs.ntfs"
		if !h.Cmd.Exists(mkfsName) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"mkfs.ntfs is not installed. Install ntfs-3g: apt install ntfs-3g")
		}
		mkfsArgs = []string{"-F"}
		if req.Label != "" {
			mkfsArgs = append(mkfsArgs, "-L", req.Label)
		}
		mkfsArgs = append(mkfsArgs, devPath)

	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType,
			fmt.Sprintf("unsupported filesystem type: %s", req.FsType))
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), mkfsName, mkfsArgs...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFormatError,
			fmt.Sprintf("format failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("%s formatted as %s", req.Device, req.FsType),
	})
}

// MountFilesystem mounts a device to a mount point.
func (h *Handler) MountFilesystem(c echo.Context) error {
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
	if isProtectedMountpoint(req.MountPoint) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint,
			"mountpoint is a system path and cannot be used")
	}

	devPath := "/dev/" + req.Device

	args := []string{}
	if req.FsType != "" {
		if err := validateFsType(req.FsType); err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType, err.Error())
		}
		args = append(args, "-t", req.FsType)
	}
	if req.Options != "" {
		// Validate options: only allow safe mount option characters
		if !regexp.MustCompile(`^[a-zA-Z0-9,=/_.-]+$`).MatchString(req.Options) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidOptions,
				"mount options contain invalid characters")
		}
		args = append(args, "-o", req.Options)
	}
	args = append(args, devPath, req.MountPoint)

	// Ensure mount point directory exists. Done after every validation so
	// a request rejected on FsType/Options doesn't leave an empty dir behind.
	if err := os.MkdirAll(req.MountPoint, 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
			fmt.Sprintf("failed to create mount point directory: %v", err))
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "mount", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
			fmt.Sprintf("mount failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	// Mount first, record second — the same order the network-share path
	// uses. An fstab entry written before a mount that then fails would
	// promise the next boot something that has never worked.
	persisted := false
	if req.Persist {
		if err := persistBlockMount(devPath, req.MountPoint, req.FsType, req.Options); err != nil {
			// The mount itself succeeded, and saying otherwise would be
			// wrong; what failed is only its survival across a reboot.
			return response.OK(c, map[string]any{
				"message":   fmt.Sprintf("%s mounted at %s, but it will not survive a reboot: %s", req.Device, req.MountPoint, err.Error()),
				"persisted": false,
			})
		}
		persisted = true
	}

	return response.OK(c, map[string]any{
		"message":   fmt.Sprintf("%s mounted at %s", req.Device, req.MountPoint),
		"persisted": persisted,
	})
}

// persistBlockMount records a block-device mount in fstab so it comes back
// after a reboot.
//
// Mirrors the network-share path: same marker so hand-written entries are
// never touched, same single source of truth in /etc/fstab rather than a table
// this panel would have to keep in step, and nofail so a disk that is absent
// at boot — an external drive, a disk pulled for maintenance — does not drop
// the host into emergency mode. That last one is the difference between a
// missing disk and an unbootable server.
func persistBlockMount(devPath, mountPoint, fsType, options string) error {
	if fsType == "" {
		fsType = "auto"
	}
	opts := options
	if opts == "" {
		opts = "defaults"
	}
	if !hasMountOption(opts, "nofail") {
		opts += ",nofail"
	}

	content, err := os.ReadFile(fstabPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read fstab: %w", err)
	}
	lines, err := upsertShareEntry(parseFstab(string(content)), devPath, mountPoint, fsType, opts)
	if err != nil {
		return err
	}
	return writeFstab(lines)
}

// hasMountOption reports whether a comma-separated option list already carries
// name, matching whole options only — "nofailover" is not "nofail".
func hasMountOption(options, name string) bool {
	for _, o := range strings.Split(options, ",") {
		if strings.TrimSpace(o) == name {
			return true
		}
	}
	return false
}

// UnmountFilesystem unmounts a filesystem from a mount point.
func (h *Handler) UnmountFilesystem(c echo.Context) error {
	var req UnmountFilesystemRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDiskPath(req.MountPoint); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint, err.Error())
	}
	if isProtectedMountpoint(req.MountPoint) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint,
			"refusing to unmount system mountpoint")
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "umount", req.MountPoint)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrUnmountError,
			fmt.Sprintf("umount failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	if req.Forget {
		if err := forgetBlockMount(req.MountPoint); err != nil {
			return response.OK(c, map[string]any{
				"message":   fmt.Sprintf("%s unmounted, but its fstab entry could not be removed and it will return on the next boot: %s", req.MountPoint, err.Error()),
				"forgot":    false,
				"unmounted": true,
			})
		}
	}

	return response.OK(c, map[string]any{
		"message":   fmt.Sprintf("%s unmounted", req.MountPoint),
		"forgot":    req.Forget,
		"unmounted": true,
	})
}

// forgetBlockMount drops the fstab entry this panel wrote for mountPoint.
//
// Only its own: removeShareEntry refuses an entry without the marker, so an
// fstab line the operator wrote by hand survives an unmount here. Losing
// somebody's hand-written entry because they clicked unmount would be the
// worse failure by far.
func forgetBlockMount(mountPoint string) error {
	content, err := os.ReadFile(fstabPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read fstab: %w", err)
	}
	lines, err := removeShareEntry(parseFstab(string(content)), mountPoint)
	if err != nil {
		return err
	}
	return writeFstab(lines)
}

// ResizeFilesystem resizes a filesystem on a given device.
func (h *Handler) ResizeFilesystem(c echo.Context) error {
	ctx := c.Request().Context()
	var req ResizeFilesystemRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	devPath := "/dev/" + req.Device

	// For LVM devices: extend LV to use all available VG free space first
	if strings.HasPrefix(devPath, "/dev/mapper/") && h.Cmd.Exists("lvextend") {
		// Verify it's actually an LV (lvs will fail for non-LV mapper devices)
		if _, err := h.Cmd.RunCtx(ctx, "lvs", "--noheadings", devPath); err == nil {
			lvOut, err := h.Cmd.RunCtx(ctx, "lvextend", "-l", "+100%FREE", devPath)
			if err != nil {
				errMsg := strings.TrimSpace(lvOut)
				// "insufficient free space" or "unchanged" are expected when VG is full
				if !strings.Contains(strings.ToLower(errMsg), "insufficient") &&
					!strings.Contains(strings.ToLower(errMsg), "unchanged") &&
					!strings.Contains(strings.ToLower(errMsg), "no free") {
					return response.Fail(c, http.StatusInternalServerError, response.ErrResizeError,
						fmt.Sprintf("lvextend failed: %s", response.SanitizeOutput(errMsg)))
				}
			}
		}
	}

	// Determine filesystem type if not provided
	fsType := req.FsType
	if fsType == "" {
		// Auto-detect using blkid
		blkOut, err := h.Cmd.RunCtx(ctx, "blkid", "-o", "value", "-s", "TYPE", devPath)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrResizeError,
				"failed to detect filesystem type; please specify fs_type")
		}
		fsType = strings.TrimSpace(blkOut)
	}

	var out string
	var resizeErr error
	switch fsType {
	case "ext2", "ext3", "ext4":
		if !h.Cmd.Exists("resize2fs") {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"resize2fs is not installed. Install e2fsprogs: apt install e2fsprogs")
		}
		out, resizeErr = h.Cmd.RunCtx(ctx, "resize2fs", devPath)

	case "xfs":
		if !h.Cmd.Exists("xfs_growfs") {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"xfs_growfs is not installed. Install xfsprogs: apt install xfsprogs")
		}
		mountPoint, mpErr := findMountPoint(devPath)
		if mpErr != nil || mountPoint == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrResizeError,
				"XFS filesystem must be mounted before resizing. Could not find mount point.")
		}
		out, resizeErr = h.Cmd.RunCtx(ctx, "xfs_growfs", mountPoint)

	case "btrfs":
		if !h.Cmd.Exists("btrfs") {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				"btrfs is not installed. Install btrfs-progs: apt install btrfs-progs")
		}
		mountPoint, mpErr := findMountPoint(devPath)
		if mpErr != nil || mountPoint == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrResizeError,
				"Btrfs filesystem must be mounted before resizing. Could not find mount point.")
		}
		out, resizeErr = h.Cmd.RunCtx(ctx, "btrfs", "filesystem", "resize", "max", mountPoint)

	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrResizeError,
			fmt.Sprintf("resize not supported for filesystem type: %s", fsType))
	}

	if resizeErr != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrResizeError,
			fmt.Sprintf("resize failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
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

		if mountDev == devPath || (resolvedDev != "" && mountDev == resolvedDev) {
			return mountPoint, nil
		}

		// Also resolve the mount device for symlink comparison. Guard against
		// the empty-string case: when devPath doesn't exist EvalSymlinks yields
		// resolvedDev=="" and pseudo-filesystems (proc, sysfs, tmpfs, cgroup2)
		// also fail to resolve, so an unguarded ""=="" would wrongly return an
		// unrelated mountpoint and mis-gate format/resize decisions.
		if resolvedDev == "" {
			continue
		}
		resolvedMountDev, _ := filepath.EvalSymlinks(mountDev)
		if resolvedMountDev == resolvedDev {
			return mountPoint, nil
		}
	}

	return "", nil
}
