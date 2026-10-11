package disk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func checkExpandable(t *testing.T, cmd *exec.MockCommander) []ExpandCandidate {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := (&Handler{Cmd: cmd}).CheckExpandable(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data []ExpandCandidate `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return body.Data
}

// The expand check ran one df over every mount, and statfs on a hard network
// mount whose server is gone never returns: Proxmox mounts its NFS and CIFS
// storages under /mnt/pve. Local file systems only, as the list does.
func TestCheckExpandableAsksForLocalFileSystemsOnly(t *testing.T) {
	cmd := mock(map[string]exec.MockResult{"df": {Output: localDf}}, "sfdisk")
	checkExpandable(t, cmd)
	c := findCall(cmd.Calls, "df")
	if c == nil || !reflect.DeepEqual(c.Args[:2], []string{"-B1", "-l"}) {
		t.Errorf("df %v, want -B1 -l first", c)
	}
}

// Free space after a partition is the file system's only once growpart grows
// the partition into it. Without growpart — not on a default Debian or
// Proxmox VE — the expand ran resize2fs alone, which found nothing to do, and
// reported the file system expanded.
func TestCheckExpandableOffersOnlyWhatGrowpartCanGrow(t *testing.T) {
	outputs := func(growpart string) map[string]exec.MockResult {
		return map[string]exec.MockResult{
			"df":       {Output: localDf}, // /dev/sda2 ext4 /
			"sfdisk":   {Output: "Size\n10737418240\n"},
			"growpart": {Output: growpart},
		}
	}
	if got := checkExpandable(t, mock(outputs(""), "sfdisk", "resize2fs")); len(got) != 0 {
		t.Errorf("without growpart: %+v", got)
	}
	if got := checkExpandable(t, mock(outputs("NOCHANGE: partition 2 is size 976773168. it cannot be grown\n"), "sfdisk", "resize2fs", "growpart")); len(got) != 0 {
		t.Errorf("growpart says NOCHANGE: %+v", got)
	}
	cmd := mock(outputs("CHANGE: partition=2 start=4096 old: size=976769072 end=976773168 new: size=997744607 end=997748703\n"), "sfdisk", "resize2fs", "growpart")
	got := checkExpandable(t, cmd)
	if len(got) != 1 || got[0].FreeSpace != 10737418240 || len(got[0].Steps) != 2 || got[0].Steps[0].Command != "growpart" || got[0].Steps[1].Command != "resize2fs" {
		t.Fatalf("growpart says CHANGE: %+v", got)
	}
	if c := findCall(cmd.Calls, "growpart"); c == nil || !reflect.DeepEqual(c.Args, []string{"--dry-run", "/dev/sda", "2"}) {
		t.Errorf("growpart %v", c)
	}
}

const lsblkMixed = `{"blockdevices":[
  {"name":"zd16","size":34359738368,"type":"disk","rota":false,"ro":false,"children":[
    {"name":"zd16p1","size":34358689792,"type":"part","parttype":"0fc63daf-8483-4772-8e79-3d69d8477de4","fstype":"ext4"}]},
  {"name":"zd0","size":17179869184,"type":"disk","fstype":"swap","mountpoint":"[SWAP]","rota":false,"ro":false},
  {"name":"nvme0n1","size":2000398934016,"type":"disk","pttype":"gpt","rota":false,"ro":false,"tran":"nvme","children":[
    {"name":"nvme0n1p1","size":1031168,"type":"part","pttype":"gpt","parttype":"21686148-6449-6e6f-744e-656564454649","parttypename":"BIOS boot"},
    {"name":"nvme0n1p2","size":1073741824,"type":"part","pttype":"gpt","parttype":"c12a7328-f81f-11d2-ba4b-00a0c93ec93b","parttypename":"EFI System","fstype":"vfat","uuid":"1A2B-3C4D"},
    {"name":"nvme0n1p3","size":1999323037696,"type":"part","pttype":"gpt","parttype":"6a898cc3-1dd2-11b2-99a6-080020736631","parttypename":"Solaris /usr & Apple ZFS","fstype":"zfs_member"}]},
  {"name":"sdb","size":8001563222016,"type":"disk","pttype":"gpt","rota":true,"ro":false,"children":[
    {"name":"sdb1","size":1073741824,"type":"part","pttype":"gpt","parttype":"0fc63daf-8483-4772-8e79-3d69d8477de4","parttypename":"Linux filesystem","fstype":"vfat","uuid":"9E8F-7A6B"}]}
]}`

func listDisks(t *testing.T) map[string]BlockDevice {
	t.Helper()
	cmd := mock(map[string]exec.MockResult{"lsblk": {Output: lsblkMixed}}, "lsblk")
	rec := httptest.NewRecorder()
	if err := (&Handler{Cmd: cmd}).ListDisks(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data []BlockDevice `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if c := findCall(cmd.Calls, "lsblk"); c == nil || c.Args[len(c.Args)-1] != "NAME,SIZE,TYPE,FSTYPE,PTTYPE,PARTTYPE,PARTTYPENAME,UUID,MOUNTPOINT,MODEL,SERIAL,ROTA,RO,TRAN,STATE,VENDOR" {
		t.Errorf("lsblk %v", c)
	}
	all := map[string]BlockDevice{}
	var walk func([]BlockDevice)
	walk = func(ds []BlockDevice) {
		for _, d := range ds {
			all[d.Name] = d
			walk(d.Children)
		}
	}
	walk(body.Data)
	return all
}

// The disk list labels boot partitions everywhere, and on Proxmox VE the
// guests' zvols among the disks, so the page can set them apart.
func TestListDisksMarksBootPartitionsAndGuestVolumes(t *testing.T) {
	withZvolLinks(t, map[string]string{"rpool/data/vm-100-disk-0": "zd16", "rpool/swap": "zd0"})
	withFile(t, &proxmoxBootUUIDs, "9E8F-7A6B\n")

	devs := listDisks(t)
	for name, boot := range map[string]bool{"nvme0n1p1": true, "nvme0n1p2": true, "nvme0n1p3": false, "sdb1": false, "zd16p1": false} {
		if devs[name].Boot != boot {
			t.Errorf("on Ubuntu %s boot = %v, want %v", name, devs[name].Boot, boot)
		}
	}
	if devs["nvme0n1p2"].PartTypeName != "EFI System" || devs["nvme0n1p2"].UUID != "1A2B-3C4D" {
		t.Errorf("nvme0n1p2 = %+v", devs["nvme0n1p2"])
	}
	for _, d := range devs {
		if d.Owner != "" {
			t.Errorf("on Ubuntu %s is owned by %q", d.Name, d.Owner)
		}
	}

	onPVE(t)
	devs = listDisks(t)
	for name, owner := range map[string]string{"zd16": "VMID 100", "zd0": "zvol rpool/swap", "nvme0n1": "", "sdb": "", "zd16p1": ""} {
		if devs[name].Owner != owner {
			t.Errorf("on Proxmox %s owner = %q, want %q", name, devs[name].Owner, owner)
		}
	}
	// An ESP proxmox-boot-tool keeps a kernel on, whatever its type says.
	if !devs["sdb1"].Boot {
		t.Error("on Proxmox the proxmox-boot ESP is not a boot partition")
	}
}

// On LVM the disk's free space reaches the volume through growpart and
// pvresize; without a partition growpart can grow, only the VG's own free
// space counts.
func TestCheckExpandableLVMPathNeedsGrowpartForTheDisk(t *testing.T) {
	outputs := func(growpart, vgFree string) map[string]exec.MockResult {
		return map[string]exec.MockResult{
			"df":       {Output: "Filesystem Type 1B-blocks Used Avail Use% Mounted on\n/dev/mapper/pve-root ext4 10737418240 5368709120 5368709120 50% /\n"},
			"lvs":      {Output: `{"report":[{"lv":[{"vg_name":"pve","vg_free":"` + vgFree + `"}]}]}`},
			"pvs":      {Output: `{"report":[{"pv":[{"pv_name":"/dev/sda3"}]}]}`},
			"sfdisk":   {Output: "Size\n21474836480\n"},
			"growpart": {Output: growpart},
		}
	}
	tools := []string{"lvs", "pvs", "sfdisk", "resize2fs", "growpart"}
	if got := checkExpandable(t, mock(outputs("NOCHANGE: partition 3 is size 209715200. it cannot be grown\n", "0"), tools...)); len(got) != 0 {
		t.Errorf("nothing to gain: %+v", got)
	}
	got := checkExpandable(t, mock(outputs("CHANGE: partition=3 start=2099200 old: size=207618048 end=209717248 new: size=249561055 end=251660255\n", "0"), tools...))
	var steps []string
	if len(got) == 1 {
		for _, s := range got[0].Steps {
			steps = append(steps, s.Command)
		}
	}
	if want := []string{"growpart", "pvresize", "lvextend", "resize2fs"}; len(got) != 1 || !reflect.DeepEqual(steps, want) || got[0].FreeSpace != 21474836480 {
		t.Errorf("growpart can grow sda3: %+v (steps %v, want %v)", got, steps, want)
	}
}
