package disk

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// VG pve as a default Proxmox VE LVM install leaves it: root and swap, the
// thin pool behind local-lvm with a guest's disk in it, plus a volume of the
// operator's and an empty pool. Sizes are bytes (lvs --units b --nosuffix).
const pveLVs = `{"report":[{"lv":[
  {"lv_name":"data","vg_name":"pve","lv_attr":"twi-aotz--","pool_lv":"","lv_device_open":"open","lv_size":"42949672960"},
  {"lv_name":"root","vg_name":"pve","lv_attr":"-wi-ao----","pool_lv":"","lv_device_open":"open","lv_size":"10737418240"},
  {"lv_name":"vm-100-disk-0","vg_name":"pve","lv_attr":"Vwi-aotz--","pool_lv":"data","lv_device_open":"open","lv_size":"34359738368"},
  {"lv_name":"scratch","vg_name":"pve","lv_attr":"-wi-a-----","pool_lv":"","lv_device_open":"","lv_size":"5368709120"},
  {"lv_name":"notes","vg_name":"pve","lv_attr":"Vwi-a-tz--","pool_lv":"data","lv_device_open":"","lv_size":"1073741824"},
  {"lv_name":"spare","vg_name":"pve","lv_attr":"twi-a-tz--","pool_lv":"","lv_device_open":"","lv_size":"1073741824"}
]}]}`

func lvmMock(extra map[string]exec.MockResult) *exec.MockCommander {
	outputs := map[string]exec.MockResult{"lvs": {Output: lvmWarning + pveLVs}}
	for k, v := range extra {
		outputs[k] = v
	}
	return mock(outputs, "lvs", "lvremove", "lvresize", "lvcreate", "vgs")
}

// lvremove -f answered LVM's own question for it: deleting pve/data — the
// pool behind local-lvm — took every thin volume in it, every stopped guest's
// disk, before stopping at the first one in use.
func TestRemoveLVRefusesAThinPoolThatHoldsVolumes(t *testing.T) {
	cmd := lvmMock(nil)
	rec := call(t, (*Handler).RemoveLV, cmd, http.MethodDelete, "", "vg", "pve", "name", "data")
	assertRefused(t, rec, http.StatusConflict, "LV_HAS_DEPENDENTS", map[string]string{"lv": "pve/data"})
	if ran(cmd, "lvremove") || ran(cmd, "lvchange") {
		t.Errorf("ran %v", callNames(cmd.Calls))
	}
}

func TestRemoveLVRefusesAVolumeInUse(t *testing.T) {
	cmd := lvmMock(nil)
	rec := call(t, (*Handler).RemoveLV, cmd, http.MethodDelete, "", "vg", "pve", "name", "root")
	assertRefused(t, rec, http.StatusConflict, "DEVICE_IN_USE", map[string]string{"device": "pve/root"})
	if ran(cmd, "lvremove") || ran(cmd, "lvchange") {
		t.Errorf("ran %v", callNames(cmd.Calls))
	}
}

// On Proxmox VE a guest's volume, a volume in a storage's thin pool and the
// pool itself are Proxmox's to remove; elsewhere a name means nothing.
func TestRemoveLVOnProxmox(t *testing.T) {
	withFile(t, &pveStorageCfg, storageCfg+"\nlvmthin: spare-pool\n\tthinpool spare\n\tvgname pve\n")

	// Not on Proxmox: the guest's volume is refused because it is open,
	// which is all Ubuntu knows about it.
	rec := call(t, (*Handler).RemoveLV, lvmMock(nil), http.MethodDelete, "", "vg", "pve", "name", "vm-100-disk-0")
	assertRefused(t, rec, http.StatusConflict, "DEVICE_IN_USE", nil)

	onPVE(t)
	for name, owner := range map[string]string{
		"vm-100-disk-0": "VMID 100",
		"notes":         "storage local-lvm",
		"spare":         "storage spare-pool",
	} {
		cmd := lvmMock(nil)
		rec := call(t, (*Handler).RemoveLV, cmd, http.MethodDelete, "", "vg", "pve", "name", name)
		assertRefused(t, rec, http.StatusConflict, "DISK_GUEST_VOLUME", map[string]string{"device": "pve/" + name, "owner": owner})
		if ran(cmd, "lvremove") || ran(cmd, "lvchange") {
			t.Errorf("%s: ran %v", name, callNames(cmd.Calls))
		}
	}
}

// Without -f, lvremove still asks before anything that takes more than the
// volume with it, and the answer with no terminal is no. It also asks before
// removing an active volume, so that one is deactivated first.
func TestRemoveLVDeactivatesThenRemovesWithoutForce(t *testing.T) {
	cmd := lvmMock(nil)
	rec := call(t, (*Handler).RemoveLV, cmd, http.MethodDelete, "", "vg", "pve", "name", "scratch")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var got []string
	for _, c := range cmd.Calls {
		if c.Name == "lvchange" || c.Name == "lvremove" {
			got = append(got, c.Name+" "+strings.Join(c.Args, " "))
		}
	}
	if want := []string{"lvchange -an pve/scratch", "lvremove pve/scratch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}

	// A refusal from lvremove leaves the volume as it was: active again.
	cmd = lvmMock(map[string]exec.MockResult{"lvremove": {Output: "Logical volume pve/scratch not removed.", Err: errors.New("exit status 5")}})
	rec = call(t, (*Handler).RemoveLV, cmd, http.MethodDelete, "", "vg", "pve", "name", "scratch")
	assertRefused(t, rec, http.StatusInternalServerError, "LVM_ERROR", nil)
	if last := cmd.Calls[len(cmd.Calls)-1]; last.Name != "lvchange" || strings.Join(last.Args, " ") != "-ay pve/scratch" {
		t.Errorf("last ran %v, want lvchange -ay pve/scratch", last)
	}

	rec = call(t, (*Handler).RemoveLV, lvmMock(nil), http.MethodDelete, "", "vg", "pve", "name", "nosuch")
	assertRefused(t, rec, http.StatusNotFound, "NOT_FOUND", nil)
}

func resizeLV(t *testing.T, cmd *exec.MockCommander, body string) (string, int, string) {
	t.Helper()
	rec := call(t, (*Handler).ResizeLV, cmd, http.MethodPost, body)
	if c := findCall(cmd.Calls, "lvresize"); c != nil {
		return strings.Join(c.Args, " "), rec.Code, rec.Body.String()
	}
	return "", rec.Code, rec.Body.String()
}

// VG pve with 4 MiB extents; free is the count of free extents.
func vgsExtents(free string) exec.MockResult {
	return exec.MockResult{Output: `{"report":[{"vg":[{"vg_extent_size":"4194304","vg_extent_count":"25471","vg_free_count":"` + free + `"}]}]}`}
}

// A smaller size cuts off the end of the volume. LVM asks first, and with no
// terminal the answer was always no; a shrink now needs saying so.
func TestResizeLVShrinkNeedsConfirmation(t *testing.T) {
	cmd := lvmMock(nil)
	rec := call(t, (*Handler).ResizeLV, cmd, http.MethodPost, `{"vg":"pve","name":"scratch","size":"4G"}`)
	assertRefused(t, rec, http.StatusConflict, "LV_SHRINK_CONFIRM_REQUIRED", map[string]string{"lv": "pve/scratch"})
	if ran(cmd, "lvresize") {
		t.Error("lvresize ran for an unconfirmed shrink")
	}

	args, code, body := resizeLV(t, lvmMock(nil), `{"vg":"pve","name":"scratch","size":"4G","shrink":true}`)
	if code != http.StatusOK || args != "--yes -L 4G /dev/pve/scratch" {
		t.Errorf("a confirmed shrink: %d %s, ran lvresize %q", code, body, args)
	}

	// Shrinking a volume something holds cuts off data in use.
	cmd = lvmMock(nil)
	rec = call(t, (*Handler).ResizeLV, cmd, http.MethodPost, `{"vg":"pve","name":"root","size":"5G","shrink":true}`)
	assertRefused(t, rec, http.StatusConflict, "DEVICE_IN_USE", map[string]string{"device": "pve/root"})
	if ran(cmd, "lvresize") {
		t.Error("lvresize shrank a volume in use")
	}
}

// The dialog's examples: "+5G" was refused by the size check and "100%FREE"
// went to -L, which LVM refused.
func TestResizeLVSizes(t *testing.T) {
	for _, tc := range []struct {
		name, size, free string
		want             string // lvresize's arguments; "" for the shrink confirmation
	}{
		{"grow by an amount", "+5G", "", "-L +5G /dev/pve/scratch"},
		{"grow into the free space", "+100%FREE", "", "-l +100%FREE /dev/pve/scratch"},
		{"a larger size", "6G", "", "-L 6G /dev/pve/scratch"},
		{"the free space as a size, larger than now", "100%FREE", "2560", "-l 100%FREE /dev/pve/scratch"},
		{"the free space as a size, smaller than now", "100%FREE", "256", ""},
		{"half the group", "50%VG", "0", "-l 50%VG /dev/pve/scratch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := lvmMock(map[string]exec.MockResult{"vgs": vgsExtents(tc.free)})
			args, code, body := resizeLV(t, cmd, `{"vg":"pve","name":"scratch","size":"`+tc.size+`"}`)
			if tc.want == "" {
				if code != http.StatusConflict || !strings.Contains(body, "LV_SHRINK_CONFIRM_REQUIRED") || args != "" {
					t.Errorf("%d %s, ran %q", code, body, args)
				}
				return
			}
			if code != http.StatusOK || args != tc.want {
				t.Errorf("%d %s, ran lvresize %q, want %q", code, body, args, tc.want)
			}
		})
	}

	cmd := lvmMock(nil)
	rec := call(t, (*Handler).ResizeLV, cmd, http.MethodPost, `{"vg":"pve","name":"scratch","size":"-1G"}`)
	assertRefused(t, rec, http.StatusBadRequest, "INVALID_SIZE", nil)
}

// A guest's disk is resized from Proxmox; growing a storage's pool is how the
// storage grows and stays allowed.
func TestResizeLVOnProxmox(t *testing.T) {
	onPVE(t)
	withFile(t, &pveStorageCfg, storageCfg)
	cmd := lvmMock(nil)
	rec := call(t, (*Handler).ResizeLV, cmd, http.MethodPost, `{"vg":"pve","name":"vm-100-disk-0","size":"+1G"}`)
	assertRefused(t, rec, http.StatusConflict, "DISK_GUEST_VOLUME", map[string]string{"device": "pve/vm-100-disk-0", "owner": "VMID 100"})
	if ran(cmd, "lvresize") {
		t.Error("lvresize ran on a guest's disk")
	}
	if args, code, body := resizeLV(t, lvmMock(nil), `{"vg":"pve","name":"data","size":"+5G"}`); code != http.StatusOK || args != "-L +5G /dev/pve/data" {
		t.Errorf("growing local-lvm's pool: %d %s %q", code, body, args)
	}
}

func TestCreateLVSizes(t *testing.T) {
	for size, flag := range map[string]string{"10G": "-L", "100%FREE": "-l", "25%VG": "-l"} {
		cmd := lvmMock(nil)
		rec := call(t, (*Handler).CreateLV, cmd, http.MethodPost, `{"vg":"pve","name":"extra","size":"`+size+`"}`)
		c := findCall(cmd.Calls, "lvcreate")
		if rec.Code != http.StatusOK || c == nil || strings.Join(c.Args, " ") != flag+" "+size+" -n extra pve" {
			t.Errorf("%s: %d %s, ran %v", size, rec.Code, rec.Body.String(), c)
		}
	}
}

// The LV list names what Proxmox owns, so the page can leave it alone.
func TestListLVsNamesProxmoxOwners(t *testing.T) {
	withFile(t, &pveStorageCfg, storageCfg)
	list := func() map[string]string {
		rec := listLVM(t, (*Handler).ListLVs, "lvs", pveLVs, "")
		var body struct {
			Data []LogicalVolume `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		owners := map[string]string{}
		for _, lv := range body.Data {
			owners[lv.Name] = lv.Owner
		}
		return owners
	}
	for name, owner := range list() {
		if owner != "" {
			t.Errorf("on Ubuntu %s is owned by %q", name, owner)
		}
	}
	onPVE(t)
	want := map[string]string{"data": "storage local-lvm", "root": "", "vm-100-disk-0": "VMID 100", "scratch": "", "notes": "storage local-lvm", "spare": ""}
	if got := list(); !reflect.DeepEqual(got, want) {
		t.Errorf("owners = %v, want %v", got, want)
	}
}

func TestLVMSizeBytes(t *testing.T) {
	for in, want := range map[string]int64{"4G": 4 << 30, "4g": 4 << 30, "512": 512 << 20, "1.5T": 3 << 39, "100k": 100 << 10, "2048s": 1 << 20, "10b": 10} {
		if got, err := lvmSizeBytes(in); err != nil || got != want {
			t.Errorf("lvmSizeBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := lvmSizeBytes("4x"); err == nil {
		t.Error("4x accepted")
	}
}
