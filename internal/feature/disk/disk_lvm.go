package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// ---------- 5. LVM ----------

// ListPVs returns all LVM physical volumes.
func (h *Handler) ListPVs(c echo.Context) error {
	if !h.Cmd.Exists("pvs") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "pvs", "--reportformat", "json",
		"-o", "pv_name,vg_name,pv_size,pv_free,pv_attr")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("pvs failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	report, warnings, err := lvmReport(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("failed to parse pvs output: %v", err))
	}
	pvs, err := parsePVsJSON(report)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("failed to parse pvs output: %v", err))
	}

	return lvmList(c, pvs, warnings)
}

// lvmReport splits what an LVM reporting command printed into its JSON report
// and the lines around it. lvm2 writes warnings to stderr — an old PV header
// on a host upgraded across releases, a PV whose disk is missing, duplicate
// multipath paths — and the panel reads stdout and stderr together, so a
// single warning left nothing that parsed and the whole LVM tab came up empty
// on a host where every volume was fine.
func lvmReport(out string) (report []byte, warnings []string, err error) {
	start := strings.IndexByte(out, '{')
	if start < 0 {
		return nil, nil, fmt.Errorf("no report in the output: %s", response.SanitizeOutput(strings.TrimSpace(out)))
	}
	dec := json.NewDecoder(strings.NewReader(out[start:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("json unmarshal: %w", err)
	}
	for _, part := range []string{out[:start], out[start+int(dec.InputOffset()):]} {
		for _, line := range strings.Split(part, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				warnings = append(warnings, response.SanitizeOutput(line))
			}
		}
	}
	return raw, warnings, nil
}

// lvmList answers a list with LVM's warnings beside it when the page asks for
// them (?warnings=1), and as the bare list otherwise — what a page from before
// the warnings reads, through ?node= from an older panel included.
func lvmList(c echo.Context, items any, warnings []string) error {
	if c.QueryParam("warnings") != "1" {
		return response.OK(c, items)
	}
	if warnings == nil {
		warnings = []string{}
	}
	return response.OK(c, map[string]any{"items": items, "warnings": warnings})
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
func (h *Handler) ListVGs(c echo.Context) error {
	if !h.Cmd.Exists("vgs") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "vgs", "--reportformat", "json",
		"-o", "vg_name,vg_size,vg_free,pv_count,lv_count,vg_attr")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("vgs failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	report, warnings, err := lvmReport(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("failed to parse vgs output: %v", err))
	}
	vgs, err := parseVGsJSON(report)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("failed to parse vgs output: %v", err))
	}

	return lvmList(c, vgs, warnings)
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
func (h *Handler) ListLVs(c echo.Context) error {
	if !h.Cmd.Exists("lvs") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "lvs", "--reportformat", "json",
		"-o", "lv_name,vg_name,lv_size,lv_attr,lv_path,pool_lv,data_percent,metadata_percent")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvs failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	report, warnings, err := lvmReport(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("failed to parse lvs output: %v", err))
	}
	lvs, err := parseLVsJSON(report)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("failed to parse lvs output: %v", err))
	}
	markLVOwners(lvs)

	return lvmList(c, lvs, warnings)
}

// markLVOwners names, on Proxmox VE, what owns each volume: the guest it is
// named for, or the storage whose VG or thin pool holds it — the pool itself
// included, which the panel must not remove from under the storage.
func markLVOwners(lvs []LogicalVolume) {
	if !platform.Get().PVE {
		return
	}
	cfg := readPVEStorage()
	for i := range lvs {
		lv := &lvs[i]
		lv.Owner = pveLVOwner(cfg, lv.VGName, lv.Name, lv.PoolLV)
		if lv.Owner == "" && isThinPool(lv.Attr) {
			if id := cfg.thinPools[lv.VGName+"/"+lv.Name]; id != "" {
				lv.Owner = "storage " + id
			}
		}
	}
}

// isThinPool reads lv_attr: its first letter is the volume type, t for a thin
// pool.
func isThinPool(attr string) bool { return strings.HasPrefix(attr, "t") }

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
				MetaPercent string `json:"metadata_percent"`
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
				Name:            strings.TrimSpace(lv.Name),
				VGName:          strings.TrimSpace(lv.VGName),
				Size:            strings.TrimSpace(lv.Size),
				Attr:            strings.TrimSpace(lv.Attr),
				Path:            strings.TrimSpace(lv.Path),
				PoolLV:          strings.TrimSpace(lv.PoolLV),
				DataPercent:     strings.TrimSpace(lv.DataPercent),
				MetadataPercent: strings.TrimSpace(lv.MetaPercent),
			})
		}
	}
	return result, nil
}

// CreatePV creates a new LVM physical volume on a device.
func (h *Handler) CreatePV(c echo.Context) error {
	if !h.Cmd.Exists("pvcreate") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	var req CreatePVRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}

	devPath, err := normalizeDevicePath(req.Device)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if err := verifyBlockDevice(devPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if r := checkGuestVolume(strings.TrimPrefix(devPath, "/dev/"), true); r != nil {
		return r.send(c)
	}
	if r := h.checkBootPartition(c.Request().Context(), devPath); r != nil {
		return r.send(c)
	}
	out, err := h.Cmd.RunCtx(c.Request().Context(), "pvcreate", devPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("pvcreate failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("physical volume created on %s", req.Device),
	})
}

// CreateVG creates a new LVM volume group.
func (h *Handler) CreateVG(c echo.Context) error {
	if !h.Cmd.Exists("vgcreate") {
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
		pvPath, err := normalizeDevicePath(pv)
		if err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice,
				fmt.Sprintf("invalid PV device: %s", err.Error()))
		}
		pvPaths = append(pvPaths, pvPath)
	}

	args := append([]string{req.Name}, pvPaths...)
	out, err := h.Cmd.RunCtx(c.Request().Context(), "vgcreate", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("vgcreate failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("volume group %s created", req.Name),
	})
}

// CreateLV creates a new LVM logical volume.
func (h *Handler) CreateLV(c echo.Context) error {
	if !h.Cmd.Exists("lvcreate") {
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

	out, err := h.Cmd.RunCtx(c.Request().Context(), "lvcreate", lvSizeFlag(req.Size), req.Size, "-n", req.Name, req.VG)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvcreate failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s created in %s", req.Name, req.VG),
	})
}

// RemovePV removes an LVM physical volume.
func (h *Handler) RemovePV(c echo.Context) error {
	if !h.Cmd.Exists("pvremove") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	devPath, err := normalizeDevicePath(c.Param("name"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	if err := verifyBlockDevice(devPath); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidDevice, err.Error())
	}
	out, runErr := h.Cmd.RunCtx(c.Request().Context(), "pvremove", devPath)
	if runErr != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("pvremove failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("physical volume %s removed", devPath),
	})
}

// RemoveVG removes an LVM volume group.
func (h *Handler) RemoveVG(c echo.Context) error {
	if !h.Cmd.Exists("vgremove") {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrToolNotInstalled,
			"LVM tools are not installed. Install lvm2: apt install lvm2")
	}

	name := c.Param("name")
	if err := validateLVMName(name); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, err.Error())
	}

	out, err := h.Cmd.RunCtx(c.Request().Context(), "vgremove", name)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("vgremove failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("volume group %s removed", name),
	})
}

// RemoveLV removes an LVM logical volume.
func (h *Handler) RemoveLV(c echo.Context) error {
	if !h.Cmd.Exists("lvremove") {
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

	ctx := c.Request().Context()
	lvPath := vg + "/" + name
	lvs, err := h.readLVs(ctx, vg)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvs failed: %v", err))
	}
	target, dependents := findLV(lvs, name)
	if target == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound,
			fmt.Sprintf("logical volume %s not found", lvPath))
	}
	// Removing a thin pool removes every thin volume in it. lvremove asks
	// first ("Removing pool pve/data will remove 12 dependent volume(s).
	// Proceed?"), but -f answered for it: deleting local-lvm's pool took
	// every stopped guest's disk with it.
	if isThinPool(target.Attr) && len(dependents) > 0 {
		return response.FailWith(c, http.StatusConflict, response.ErrLVHasDependents,
			fmt.Sprintf("thin pool %s still holds %s", lvPath, strings.Join(dependents, ", ")),
			map[string]string{"lv": lvPath})
	}
	if r := checkGuestLV(vg, name, target.PoolLV); r != nil {
		return r.send(c)
	}
	if isThinPool(target.Attr) {
		if r := checkStoragePool(vg, name); r != nil {
			return r.send(c)
		}
	}
	if target.open() {
		return response.FailWith(c, http.StatusConflict, response.ErrDeviceInUse,
			fmt.Sprintf("%s is in use (mounted, swap, or a running guest) — release it first", lvPath),
			map[string]string{"device": lvPath})
	}

	// No -f, so lvremove still asks before anything that takes more than this
	// volume with it — a pool's volumes, an origin's snapshots — and with no
	// terminal to answer, the answer is no. It also asks before removing an
	// active volume, so the volume is deactivated first; that fails for one
	// in use, which is the open check again at the moment it counts.
	if target.active() {
		if out, err := h.Cmd.RunCtx(ctx, "lvchange", "-an", lvPath); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
				fmt.Sprintf("lvchange -an failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
		}
	}
	out, err := h.Cmd.RunCtx(ctx, "lvremove", lvPath)
	if err != nil {
		if target.active() {
			if reOut, reErr := h.Cmd.RunCtx(ctx, "lvchange", "-ay", lvPath); reErr != nil {
				slog.Warn("could not reactivate a logical volume lvremove left in place",
					"component", "disk", "lv", lvPath, "error", reErr, "output", strings.TrimSpace(reOut))
			}
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvremove failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s/%s removed", vg, name),
	})
}

// ResizeLV resizes an LVM logical volume.
func (h *Handler) ResizeLV(c echo.Context) error {
	if !h.Cmd.Exists("lvresize") {
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
	// "+5G" grows by 5G, what the dialog's own example said and the panel
	// refused; a leading "-" stays refused, a shrink is a smaller size.
	size := strings.TrimSpace(req.Size)
	grow := strings.HasPrefix(size, "+")
	if err := validateLVSize(strings.TrimPrefix(size, "+")); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidSize, err.Error())
	}

	ctx := c.Request().Context()
	lvPath := req.VG + "/" + req.Name
	lvs, err := h.readLVs(ctx, req.VG)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvs failed: %v", err))
	}
	target, _ := findLV(lvs, req.Name)
	if target == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound,
			fmt.Sprintf("logical volume %s not found", lvPath))
	}
	if r := checkGuestLV(req.VG, req.Name, target.PoolLV); r != nil {
		return r.send(c)
	}

	// A smaller size cuts off whatever lies past the new end. LVM asks
	// before it reduces a volume, and with no terminal the answer was always
	// no: a shrink never worked, and nothing said why.
	shrink := false
	if !grow {
		if shrink, err = h.lvShrinks(ctx, req.VG, size, target.Size); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError, err.Error())
		}
	}
	if shrink && !req.Shrink {
		return response.FailWith(c, http.StatusConflict, response.ErrLVShrinkConfirmRequired,
			fmt.Sprintf("%s would get smaller; confirm the shrink", lvPath),
			map[string]string{"lv": lvPath})
	}
	if shrink && target.open() {
		return response.FailWith(c, http.StatusConflict, response.ErrDeviceInUse,
			fmt.Sprintf("%s is in use; shrinking it would cut off data in use", lvPath),
			map[string]string{"device": lvPath})
	}

	args := []string{lvSizeFlag(size), size, "/dev/" + req.VG + "/" + req.Name}
	if shrink {
		args = append([]string{"--yes"}, args...)
	}
	out, err := h.Cmd.RunCtx(ctx, "lvresize", args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrLVMError,
			fmt.Sprintf("lvresize failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	return response.OK(c, map[string]string{
		"message": fmt.Sprintf("logical volume %s/%s resized to %s", req.VG, req.Name, req.Size),
	})
}

// lvSizeFlag is the lvcreate/lvresize option a size goes with: a percentage
// (100%FREE, +50%VG) counts extents and needs -l; given to -L, LVM refused
// it as an invalid size.
func lvSizeFlag(size string) string {
	if strings.Contains(size, "%") {
		return "-l"
	}
	return "-L"
}

// lvState is a logical volume as the remove and resize checks need it.
type lvState struct {
	Name   string `json:"lv_name"`
	Attr   string `json:"lv_attr"`
	PoolLV string `json:"pool_lv"`
	Open   string `json:"lv_device_open"`
	Size   string `json:"lv_size"` // bytes
}

// open reports whether something holds the volume's device: a mount, swap,
// a running guest, a device stacked on it.
func (l lvState) open() bool {
	return l.Open == "open" || (len(l.Attr) > 5 && l.Attr[5] == 'o')
}

// active reports whether the volume's device exists (lv_attr's fifth letter).
func (l lvState) active() bool { return len(l.Attr) > 4 && l.Attr[4] == 'a' }

// readLVs lists the logical volumes of vg.
func (h *Handler) readLVs(ctx context.Context, vg string) ([]lvState, error) {
	out, err := h.Cmd.RunCtx(ctx, "lvs", "--reportformat", "json", "--units", "b", "--nosuffix",
		"-o", "lv_name,lv_attr,pool_lv,lv_device_open,lv_size", vg)
	if err != nil {
		return nil, fmt.Errorf("%s", response.SanitizeOutput(strings.TrimSpace(out)))
	}
	report, _, err := lvmReport(out)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Report []struct {
			LV []lvState `json:"lv"`
		} `json:"report"`
	}
	if err := json.Unmarshal(report, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	var lvs []lvState
	for _, r := range raw.Report {
		lvs = append(lvs, r.LV...)
	}
	return lvs, nil
}

// findLV picks name out of lvs, with the volumes a thin pool of that name
// holds.
func findLV(lvs []lvState, name string) (target *lvState, dependents []string) {
	for i := range lvs {
		switch {
		case lvs[i].Name == name:
			target = &lvs[i]
		case lvs[i].PoolLV == name:
			dependents = append(dependents, lvs[i].Name)
		}
	}
	return target, dependents
}

// lvShrinks reports whether size (no leading +) is smaller than the volume's
// current size in bytes. A percentage is what LVM makes of it: that share of
// the VG's free or total extents, as an absolute size. lvresize -l 100%FREE
// sets the volume to the size of the free space, which for a volume larger
// than the free space is a shrink; +100%FREE is the grow.
func (h *Handler) lvShrinks(ctx context.Context, vg, size, current string) (bool, error) {
	cur, err := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
	if err != nil {
		return false, fmt.Errorf("cannot read the size of the volume: %q", current)
	}
	num, suffix, isPct := strings.Cut(size, "%")
	if !isPct {
		target, err := lvmSizeBytes(size)
		return target < cur, err
	}
	pct, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return false, fmt.Errorf("invalid LV size: %s", size)
	}
	out, err := h.Cmd.RunCtx(ctx, "vgs", "--reportformat", "json", "--units", "b", "--nosuffix",
		"-o", "vg_extent_size,vg_extent_count,vg_free_count", vg)
	if err != nil {
		return false, fmt.Errorf("vgs failed: %s", response.SanitizeOutput(strings.TrimSpace(out)))
	}
	report, _, err := lvmReport(out)
	if err != nil {
		return false, err
	}
	var raw struct {
		Report []struct {
			VG []struct {
				ExtentSize  string `json:"vg_extent_size"`
				ExtentCount string `json:"vg_extent_count"`
				FreeCount   string `json:"vg_free_count"`
			} `json:"vg"`
		} `json:"report"`
	}
	if json.Unmarshal(report, &raw) != nil || len(raw.Report) == 0 || len(raw.Report[0].VG) != 1 {
		return false, fmt.Errorf("cannot read the extents of volume group %s", vg)
	}
	v := raw.Report[0].VG[0]
	extentSize, err1 := strconv.ParseFloat(strings.TrimSpace(v.ExtentSize), 64)
	total, err2 := strconv.ParseFloat(strings.TrimSpace(v.ExtentCount), 64)
	free, err3 := strconv.ParseFloat(strings.TrimSpace(v.FreeCount), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return false, fmt.Errorf("cannot read the extents of volume group %s", vg)
	}
	var extents float64
	switch suffix {
	case "FREE", "PVS":
		extents = free
	case "VG":
		extents = total
	default:
		// %ORIGIN and the rest depend on more than the VG; take it for a
		// shrink, which only asks for the confirmation.
		return true, nil
	}
	return math.Floor(extents*pct/100)*extentSize < float64(cur), nil
}

// lvmSizeBytes reads an -L size in bytes. LVM's size units are binary in
// either case (k is KiB, G is GiB) and the default is MiB.
func lvmSizeBytes(size string) (int64, error) {
	num, unit := size, "m"
	if i := strings.IndexFunc(size, func(r rune) bool { return (r < '0' || r > '9') && r != '.' }); i >= 0 {
		num, unit = size[:i], strings.ToLower(size[i:])
	}
	shift := map[string]uint{"b": 0, "k": 10, "m": 20, "g": 30, "t": 40, "p": 50, "e": 60}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid LV size: %s", size)
	}
	if unit == "s" {
		return int64(f * 512), nil
	}
	sh, ok := shift[unit]
	if !ok {
		return 0, fmt.Errorf("invalid LV size: %s", size)
	}
	if v := f * float64(uint64(1)<<sh); v < math.MaxInt64 {
		return int64(v), nil
	}
	return math.MaxInt64, nil
}
