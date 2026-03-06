package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ---------- 5. LVM ----------

// ListPVs returns all LVM physical volumes.
func (h *DiskHandler) ListPVs(c echo.Context) error {
	if !commandExists("pvs") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := exec.Command("pvs", "--reportformat", "json",
		"-o", "pv_name,vg_name,pv_size,pv_free,pv_attr").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("pvs failed: %s", strings.TrimSpace(string(out))))
	}

	pvs, err := parsePVsJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
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
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := exec.Command("vgs", "--reportformat", "json",
		"-o", "vg_name,vg_size,vg_free,pv_count,lv_count,vg_attr").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("vgs failed: %s", strings.TrimSpace(string(out))))
	}

	vgs, err := parseVGsJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
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
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := exec.Command("lvs", "--reportformat", "json",
		"-o", "lv_name,vg_name,lv_size,lv_attr,lv_path,pool_lv,data_percent").CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvs failed: %s", strings.TrimSpace(string(out))))
	}

	lvs, err := parseLVsJSON(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
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
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreatePVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateDeviceName(req.Device); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	devPath := "/dev/" + req.Device
	out, err := exec.Command("pvcreate", devPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("pvcreate failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("physical volume created on %s", req.Device),
	})
}

// CreateVG creates a new LVM volume group.
func (h *DiskHandler) CreateVG(c echo.Context) error {
	if !commandExists("vgcreate") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreateVGRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateLVMName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}
	if len(req.PVs) == 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields,
			"At least one physical volume is required")
	}

	// Validate all PV paths
	pvPaths := make([]string, 0, len(req.PVs))
	for _, pv := range req.PVs {
		// PVs can be provided as full paths (/dev/sdb1) or device names (sdb1)
		pvPath := pv
		if !strings.HasPrefix(pv, "/dev/") {
			if err := validateDeviceName(pv); err != nil {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
					fmt.Sprintf("invalid PV device: %s", err.Error()))
			}
			pvPath = "/dev/" + pv
		} else {
			// Validate the part after /dev/
			devName := strings.TrimPrefix(pv, "/dev/")
			if err := validateDeviceName(devName); err != nil {
				return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
					fmt.Sprintf("invalid PV device: %s", err.Error()))
			}
		}
		pvPaths = append(pvPaths, pvPath)
	}

	args := append([]string{req.Name}, pvPaths...)
	out, err := exec.Command("vgcreate", args...).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("vgcreate failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("volume group %s created", req.Name),
	})
}

// CreateLV creates a new LVM logical volume.
func (h *DiskHandler) CreateLV(c echo.Context) error {
	if !commandExists("lvcreate") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreateLVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateLVMName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}
	if err := validateLVMName(req.VG); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidVG, err.Error())
	}
	if err := validateLVSize(req.Size); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSize, err.Error())
	}

	out, err := exec.Command("lvcreate", "-L", req.Size, "-n", req.Name, req.VG).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvcreate failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s created in %s", req.Name, req.VG),
	})
}

// RemovePV removes an LVM physical volume.
func (h *DiskHandler) RemovePV(c echo.Context) error {
	if !commandExists("pvremove") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	name := c.Param("name")
	if err := validateDeviceName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}

	devPath := "/dev/" + name
	out, err := exec.Command("pvremove", devPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("pvremove failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("physical volume %s removed", name),
	})
}

// RemoveVG removes an LVM volume group.
func (h *DiskHandler) RemoveVG(c echo.Context) error {
	if !commandExists("vgremove") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	name := c.Param("name")
	if err := validateLVMName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}

	out, err := exec.Command("vgremove", name).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("vgremove failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("volume group %s removed", name),
	})
}

// RemoveLV removes an LVM logical volume.
func (h *DiskHandler) RemoveLV(c echo.Context) error {
	if !commandExists("lvremove") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	vg := c.Param("vg")
	name := c.Param("name")
	if err := validateLVMName(vg); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidVG, err.Error())
	}
	if err := validateLVMName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}

	lvPath := vg + "/" + name
	out, err := exec.Command("lvremove", "-f", lvPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvremove failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s/%s removed", vg, name),
	})
}

// ResizeLV resizes an LVM logical volume.
func (h *DiskHandler) ResizeLV(c echo.Context) error {
	if !commandExists("lvresize") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req ResizeLVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	if err := validateLVMName(req.VG); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidVG, err.Error())
	}
	if err := validateLVMName(req.Name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}
	if err := validateLVSize(req.Size); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSize, err.Error())
	}

	lvPath := "/dev/" + req.VG + "/" + req.Name
	out, err := exec.Command("lvresize", "-L", req.Size, lvPath).CombinedOutput()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvresize failed: %s", strings.TrimSpace(string(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s/%s resized to %s", req.VG, req.Name, req.Size),
	})
}
