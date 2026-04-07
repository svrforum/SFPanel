package disk

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	out, err := h.Cmd.Run("lsblk", "-J", "-b", "-o",
		"NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT,PARTLABEL,PARTUUID",
		devPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("lsblk failed for %s: %s", device, strings.TrimSpace(out)))
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

	devPath := "/dev/" + device
	out, err := h.Cmd.Run("parted", "-s", devPath,
		"mkpart", "primary", fsType, req.Start, req.End)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionError,
			fmt.Sprintf("parted mkpart failed: %s", strings.TrimSpace(out)))
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

	devPath := "/dev/" + device
	out, err := h.Cmd.Run("parted", "-s", devPath, "rm", number)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrPartitionError,
			fmt.Sprintf("parted rm failed: %s", strings.TrimSpace(out)))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("partition %d deleted from %s", partNum, device),
	})
}
