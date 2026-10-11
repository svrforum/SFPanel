package disk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// lvm2 writes its warnings to stderr and the panel reads stdout and stderr
// together, so one warning ahead of the report — an old PV header on a
// Proxmox host upgraded across releases, a PV whose disk is gone — left
// nothing that parsed as JSON, and the whole LVM tab came up empty.
const lvmWarning = "  WARNING: PV /dev/sda3 in VG pve is using an old PV header, modify the VG to update.\n"

const pvsReport = `  {
      "report": [
          {
              "pv": [
                  {"pv_name":"/dev/sda3", "vg_name":"pve", "pv_size":"<99.50g", "pv_free":"<12.38g", "pv_attr":"a--"}
              ]
          }
      ]
  }
`

func listLVM(t *testing.T, run func(*Handler, echo.Context) error, name, out, query string) *httptest.ResponseRecorder {
	t.Helper()
	cmd := toolsAvailable(name)
	cmd.Outputs[name] = exec.MockResult{Output: out}
	req := httptest.NewRequest(http.MethodGet, "/lvm/x"+query, nil)
	rec := httptest.NewRecorder()
	if err := run(&Handler{Cmd: cmd}, echo.New().NewContext(req, rec)); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestLVMListsReadPastWarnings(t *testing.T) {
	rec := listLVM(t, (*Handler).ListPVs, "pvs", lvmWarning+pvsReport, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pvs with a warning: %d %s", rec.Code, rec.Body.String())
	}
	var plain struct {
		Data []PhysicalVolume `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil || len(plain.Data) != 1 || plain.Data[0].Name != "/dev/sda3" {
		t.Fatalf("an older page reads a bare list; got %s (%v)", rec.Body.String(), err)
	}

	// The page asks for the warnings too, and shows them.
	rec = listLVM(t, (*Handler).ListPVs, "pvs", lvmWarning+pvsReport+"  WARNING: one more after the report.\n", "?warnings=1")
	var withWarnings struct {
		Data struct {
			Items    []PhysicalVolume `json:"items"`
			Warnings []string         `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withWarnings); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(withWarnings.Data.Items) != 1 {
		t.Errorf("items = %+v", withWarnings.Data.Items)
	}
	want := []string{
		"WARNING: PV /dev/sda3 in VG pve is using an old PV header, modify the VG to update.",
		"WARNING: one more after the report.",
	}
	if !reflect.DeepEqual(withWarnings.Data.Warnings, want) {
		t.Errorf("warnings = %q, want %q", withWarnings.Data.Warnings, want)
	}

	// No warnings is an empty list, not null.
	rec = listLVM(t, (*Handler).ListVGs, "vgs", `{"report":[{"vg":[{"vg_name":"pve","vg_size":"99g","vg_free":"12g","pv_count":"1","lv_count":"3","vg_attr":"wz--n-"}]}]}`, "?warnings=1")
	if body := rec.Body.String(); rec.Code != http.StatusOK || !contains(body, `"warnings":[]`) || !contains(body, `"name":"pve"`) {
		t.Errorf("vgs: %d %s", rec.Code, body)
	}

	rec = listLVM(t, (*Handler).ListLVs, "lvs", lvmWarning+`{"report":[{"lv":[{"lv_name":"data","vg_name":"pve","lv_size":"40g","lv_attr":"twi-aotz--","lv_path":"","pool_lv":"","data_percent":"81.20","metadata_percent":"3.10"}]}]}`, "")
	var lvs struct {
		Data []LogicalVolume `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lvs); err != nil || len(lvs.Data) != 1 {
		t.Fatalf("lvs with a warning: %d %s", rec.Code, rec.Body.String())
	}
	if lvs.Data[0].DataPercent != "81.20" || lvs.Data[0].MetadataPercent != "3.10" {
		t.Errorf("a thin pool's fill: %+v", lvs.Data[0])
	}
}

// The expand helpers read `lvs --noheadings` field by field, so a warning made
// "WARNING:" the volume group's name and the LVM path offered nothing.
func TestExpandHelpersReadPastLVMWarnings(t *testing.T) {
	cmd := toolsAvailable("lvs", "pvs")
	cmd.Outputs["lvs"] = exec.MockResult{Output: lvmWarning + `{"report":[{"lv":[{"vg_name":"pve","vg_free":"1073741824"}]}]}`}
	cmd.Outputs["pvs"] = exec.MockResult{Output: lvmWarning + `{"report":[{"pv":[{"pv_name":"/dev/sda3"}]}]}`}
	h := &Handler{Cmd: cmd}
	vg, free := h.getVGInfoForLV(context.Background(), "/dev/mapper/pve-root")
	if vg != "pve" || free != 1073741824 {
		t.Errorf("getVGInfoForLV = %q, %d; want pve, 1073741824", vg, free)
	}
	if pv := h.getPVDeviceForVG(context.Background(), "pve"); pv != "/dev/sda3" {
		t.Errorf("getPVDeviceForVG = %q, want /dev/sda3", pv)
	}
}
