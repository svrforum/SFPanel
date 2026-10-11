package disk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ---------- 3. Partitions ----------

// ListPartitions returns partitions for a specific disk device using lsblk.
func (h *Handler) ListPartitions(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	devPath := "/dev/" + device
	out, err := h.Cmd.RunCtx(c.Request().Context(), "lsblk", "-J", "-b", "-o",
		"NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT,PARTLABEL,PARTUUID",
		devPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("lsblk failed for %s: %s", device, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	var raw struct {
		BlockDevices []json.RawMessage `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("failed to parse lsblk output: %v", err))
	}

	return response.OK(c, json.RawMessage(out))
}

// CreatePartition creates a new partition on a disk device using parted.
func (h *Handler) CreatePartition(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	if !h.Cmd.Exists("parted") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"parted is not installed. Install it: apt install parted")
	}

	var req CreatePartitionRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validatePartitionSize(req.Start); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidStart, err.Error())
	}
	if err := validatePartitionSize(req.End); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidEnd, err.Error())
	}

	fsType := "ext4"
	if req.FsType != "" {
		if req.FsType == "linux-swap" || req.FsType == "swap" {
			fsType = "linux-swap"
		} else {
			if err := validateFsType(req.FsType); err != nil {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType, err.Error())
			}
			fsType = req.FsType
		}
	}

	// A partition written into a guest's disk lands in the guest's own
	// partition table. What uses the disk's existing partitions does not
	// matter here: adding one leaves them as they are.
	if r := checkGuestVolume(device, false); r != nil {
		return r.send(c)
	}

	// fs_type alone is the type parted records for the partition, which
	// leaves it empty; format puts that filesystem on it. The tool is checked
	// before the disk is touched, so a missing mkfs.xfs is not found out with
	// a partition already made.
	var before map[int]bool
	if req.Format {
		tool, _, missing, ok := mkfsCommand(fsType, "", "")
		if !ok {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidFSType,
				fmt.Sprintf("cannot format a partition as %s", fsType))
		}
		if !h.Cmd.Exists(tool) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled, missing)
		}
		var err error
		if before, err = partitionNumbers(device); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
				fmt.Sprintf("cannot list the partitions of %s: %v", device, err))
		}
	}

	ctx := c.Request().Context()
	devPath := "/dev/" + device
	out, err := h.Cmd.RunCtx(ctx, "parted", "-s", devPath,
		"mkpart", "primary", fsType, req.Start, req.End)
	if err != nil {
		if strings.Contains(out, "unrecognised disk label") {
			return response.Fail(c, http.StatusConflict, response.ErrDiskNoPartitionTable,
				fmt.Sprintf("%s has no partition table; create one first", device))
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionError,
			fmt.Sprintf("parted mkpart failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}
	h.diskChanged(ctx)

	if req.Format {
		after, err := partitionNumbers(device)
		var added []int
		for n := range after {
			if !before[n] {
				added = append(added, n)
			}
		}
		if err != nil || len(added) != 1 {
			return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionNotFormatted,
				fmt.Sprintf("created a partition on %s but could not tell which one to format", device))
		}
		partPath := partitionDevice(device, added[0])
		tool, args, _, _ := mkfsCommand(fsType, "", partPath)
		out, err := h.Cmd.RunCtx(ctx, tool, args...)
		h.diskChanged(ctx)
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionNotFormatted,
				fmt.Sprintf("created %s but could not format it: %s", partPath, response.SanitizeOutput(strings.TrimSpace(out))))
		}
		return response.OK(c, map[string]string{
			"message":   fmt.Sprintf("%s created and formatted as %s", partPath, strings.TrimPrefix(fsType, "linux-")),
			"partition": strings.TrimPrefix(partPath, "/dev/"),
		})
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("partition created on %s (%s - %s)", device, req.Start, req.End),
	})
}

// DeletePartition deletes a partition by number from a disk device using parted.
func (h *Handler) DeletePartition(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	number := c.Param("number")
	partNum, err := strconv.Atoi(number)
	if err != nil || partNum < 1 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPartition, "Invalid partition number")
	}

	if !h.Cmd.Exists("parted") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"parted is not installed. Install it: apt install parted")
	}

	// The same guard format carries, for the operation that is strictly worse:
	// formatting a mounted partition destroys its contents, deleting it takes
	// the partition table entry with them. Deleting the partition backing /boot
	// or / leaves a host that does not come back.
	//
	// The check is on the partition, not the disk: /dev/sda3 is what parted is
	// told to remove, and /dev/sda itself is not what gets mounted.
	partPath := partitionDevice(device, partNum)
	if mp, err := findDeviceMountpoint(partPath); err == nil && mp != "" {
		if isProtectedMountpoint(mp) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
				fmt.Sprintf("partition is mounted at %s, a protected system path", mp))
		}
		return response.Fail(c, http.StatusConflict, response.ErrInvalidDevice,
			fmt.Sprintf("partition is mounted at %s — unmount it first", mp))
	}
	// Not mounted is not unused: an LVM physical volume, a RAID member or a
	// ZFS vdev never is, and parted removes one from the table on disk before
	// failing to tell the kernel — refused on screen, gone at the next boot.
	busy, err := deviceInUse(partPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot tell whether %s is in use: %v", partPath, err))
	}
	if busy {
		return response.Fail(c, http.StatusConflict, response.ErrDeviceInUse,
			fmt.Sprintf("%s is in use (LVM, RAID, ZFS or swap) — release it first", partPath))
	}
	if r := checkGuestVolume(strings.TrimPrefix(partPath, "/dev/"), true); r != nil {
		return r.send(c)
	}
	facts, err := h.blockFacts(c.Request().Context(), partPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot tell what %s is: %v", partPath, err))
	}
	if r := bootRefusal(facts); r != nil {
		return r.send(c)
	}
	if r := extendedRefusal(device, facts); r != nil {
		return r.send(c)
	}

	devPath := "/dev/" + device
	out, err := h.Cmd.RunCtx(c.Request().Context(), "parted", "-s", devPath, "rm", number)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionError,
			fmt.Sprintf("parted rm failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}
	h.diskChanged(c.Request().Context())

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("partition %d deleted from %s", partNum, device),
	})
}

// partitionNumbers is the set of partitions the kernel knows on disk: each
// /sys/block/<disk>/<partition> holds its number in a file called partition.
// A var so tests can stand in for the kernel.
var partitionNumbers = func(disk string) (map[int]bool, error) {
	entries, err := os.ReadDir(filepath.Join(sysBlockDir, disk))
	if err != nil {
		return nil, err
	}
	numbers := map[int]bool{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(sysBlockDir, disk, e.Name(), "partition"))
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			numbers[n] = true
		}
	}
	return numbers, nil
}

// sysBlockDir lists whole disks, never partitions. A var so tests can point it
// at a scratch directory.
var sysBlockDir = "/sys/block"

// CreatePartitionTable writes an empty GPT partition table to a disk that has
// none. A new disk arrives without one, parted refuses mkpart there
// ("unrecognised disk label"), and the page had no way past it. Anything
// already on the disk refuses it — a partition table, or a filesystem, LVM,
// RAID or ZFS signature across the whole disk — since a new label over it
// loses it.
// POST /disks/:device/partition-table
func (h *Handler) CreatePartitionTable(c echo.Context) error {
	device := c.Param("device")
	if err := validateDeviceName(device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if _, err := os.Stat(filepath.Join(sysBlockDir, device)); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
			fmt.Sprintf("%s is not a whole disk", device))
	}
	for _, tool := range []string{"parted", "blkid"} {
		if !h.Cmd.Exists(tool) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
				fmt.Sprintf("%s is not installed", tool))
		}
	}

	devPath := "/dev/" + device
	busy, err := deviceInUse(devPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot tell whether %s is in use: %v", devPath, err))
	}
	if busy {
		return response.Fail(c, http.StatusConflict, response.ErrDeviceInUse,
			fmt.Sprintf("%s is in use — release it first", devPath))
	}
	// A new, still empty VM disk has no signature for blkid to find.
	if r := checkGuestVolume(device, true); r != nil {
		return r.send(c)
	}

	// blkid -p reads the disk itself, not its cache, and exits 2 when it finds
	// nothing there.
	ctx := c.Request().Context()
	out, err := h.Cmd.RunCtx(ctx, "blkid", "-p", "-o", "export", devPath)
	var exitErr *osexec.ExitError
	switch {
	case err == nil:
		return response.Fail(c, http.StatusConflict, response.ErrDiskNotEmpty,
			fmt.Sprintf("%s already holds data (%s); a partition table would lose it",
				devPath, response.SanitizeOutput(strings.Join(strings.Fields(out), " "))))
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 2:
	default:
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot read %s: %s", devPath, response.SanitizeOutput(strings.TrimSpace(out))))
	}

	if out, err := h.Cmd.RunCtx(ctx, "parted", "-s", devPath, "mklabel", "gpt"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionError,
			fmt.Sprintf("parted mklabel failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}
	h.diskChanged(ctx)
	return response.OK(c, map[string]string{"message": "GPT partition table created on " + device})
}
