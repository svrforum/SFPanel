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
