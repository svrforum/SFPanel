package disk

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// guardedCase is one handler that changes a device, asked to change name.
type guardedCase struct {
	name    string
	run     func(*Handler, echo.Context) error
	method  string
	body    string
	params  []string
	changer string // the command that must not run
}

// guardedCases are every handler the guest-volume guard sits in front of,
// each aimed at device (a disk; part is a partition of it, number its number).
func guardedCases(t *testing.T, disk, part, number string) []guardedCase {
	mountPoint := filepath.Join(t.TempDir(), "mnt")
	return []guardedCase{
		{"format", (*Handler).FormatPartition, http.MethodPost, `{"device":"` + part + `","fs_type":"ext4"}`, nil, "mkfs.ext4"},
		{"mount", (*Handler).MountFilesystem, http.MethodPost, `{"device":"` + part + `","mount_point":"` + mountPoint + `"}`, nil, "mount"},
		{"swap on the device", (*Handler).CreateSwap, http.MethodPost, `{"device":"/dev/` + part + `","force":true}`, nil, "mkswap"},
		{"partition delete", (*Handler).DeletePartition, http.MethodDelete, "", []string{"device", disk, "number", number}, "parted"},
		{"pvcreate", (*Handler).CreatePV, http.MethodPost, `{"device":"` + part + `"}`, nil, "pvcreate"},
		{"RAID create", (*Handler).CreateRAID, http.MethodPost, `{"name":"md9","level":"raid1","devices":["` + part + `","` + part + `"]}`, nil, "mdadm"},
		{"RAID add", (*Handler).AddRAIDDisk, http.MethodPost, `{"device":"` + part + `"}`, []string{"name", "md0"}, "mdadm"},
	}
}

// diskCases are the handlers that take the whole disk.
func diskCases(disk string) []guardedCase {
	return []guardedCase{
		{"partition create", (*Handler).CreatePartition, http.MethodPost, `{"start":"1MiB","end":"100%","fs_type":"ext4"}`, []string{"device", disk}, "parted"},
		{"partition table", (*Handler).CreatePartitionTable, http.MethodPost, "", []string{"device", disk}, "parted"},
	}
}

func guardMock() *exec.MockCommander {
	return mock(map[string]exec.MockResult{
		"lsblk": {Output: lsblkDataPartition},
	}, "parted", "blkid", "mkfs.ext4", "mkswap", "swapon", "pvcreate", "mdadm", "udevadm")
}

// wholeDisks makes the names whole disks to CreatePartitionTable.
func wholeDisks(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.Mkdir(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := sysBlockDir
	sysBlockDir = dir
	t.Cleanup(func() { sysBlockDir = orig })
}

// A stopped guest's zvol is open to nobody and its name is all that tells.
// mkfs, mkswap, parted, pvcreate and mdadm went through zd16 — a VM's disk on
// a Proxmox ZFS install — whether the VM ran or not.
func TestProxmoxGuestVolumesAreRefusedByName(t *testing.T) {
	onPVE(t)
	unmountedAndFree(t)
	fakeBlockDevices(t, "zd16", "zd16p1", "md0")
	wholeDisks(t, "zd16")
	newFakeProc(t, nil)
	sys := newFakeSysfs(t)
	sys.disk("zd16", "230:16")
	sys.part("zd16", "zd16p1", "230:17")
	withZvolLinks(t, map[string]string{
		"rpool/data/vm-100-disk-0":       "zd16",
		"rpool/data/vm-100-disk-0-part1": "zd16p1",
	})

	cases := append(guardedCases(t, "zd16", "zd16p1", "1"), diskCases("zd16")...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := guardMock()
			rec := call(t, tc.run, cmd, tc.method, tc.body, tc.params...)
			device := "zd16p1"
			if tc.params != nil && tc.params[0] == "device" && len(tc.params) == 2 {
				device = "zd16"
			}
			assertRefused(t, rec, http.StatusConflict, "DISK_GUEST_VOLUME", map[string]string{"device": device, "owner": "VMID 100"})
			if ran(cmd, tc.changer) {
				t.Errorf("%s ran on a guest's volume", tc.changer)
			}
		})
	}
}

// RBD images and NBD exports are guests' too; a zvol no link names is still
// a zvol.
func TestProxmoxGuestVolumeKinds(t *testing.T) {
	onPVE(t)
	unmountedAndFree(t)
	fakeBlockDevices(t, "rbd0", "nbd3", "zd32")
	newFakeProc(t, nil)
	withZvolLinks(t, map[string]string{"rpool/swap": "zd0"})
	for dev, owner := range map[string]string{"rbd0": "RBD", "nbd3": "NBD", "zd32": "zvol"} {
		rec := call(t, (*Handler).FormatPartition, guardMock(), http.MethodPost, `{"device":"`+dev+`","fs_type":"ext4"}`)
		assertRefused(t, rec, http.StatusConflict, "DISK_GUEST_VOLUME", map[string]string{"device": dev, "owner": owner})
	}
}

// Elsewhere a zvol is whatever its host made it — swap, a file system — and
// its name says nothing about a guest. Ubuntu behaves as before.
func TestGuestNamesCountOnlyOnProxmox(t *testing.T) {
	unmountedAndFree(t)
	fakeBlockDevices(t, "zd16")
	newFakeProc(t, nil)
	cmd := guardMock()
	rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"zd16","fs_type":"ext4"}`)
	if rec.Code != http.StatusOK || !ran(cmd, "mkfs.ext4") {
		t.Errorf("format of a zvol on Ubuntu: %d %s", rec.Code, rec.Body.String())
	}
}

// Guest disks on LVM: dm devices named the way Proxmox names guest volumes,
// or in a VG or thin pool storage.cfg gives to a storage.
func TestProxmoxGuestLVsAreRefusedByNameAndStorage(t *testing.T) {
	onPVE(t)
	unmountedAndFree(t)
	withFile(t, &pveStorageCfg, storageCfg)
	newFakeProc(t, nil)
	sys := newFakeSysfs(t)
	sys.disk("sda", "8:0")
	sys.part("sda", "sda3", "8:3")
	sys.lv("dm-2", "253:2", "pve-data_tdata", "sda3")
	sys.lv("dm-4", "253:4", "pve-data-tpool", "dm-2")
	sys.lv("dm-1", "253:1", "pve-root", "sda3")
	sys.lv("dm-5", "253:5", "pve-vm--101--disk--0", "dm-4")
	sys.lv("dm-6", "253:6", "pve-scratch", "dm-4")
	sys.lv("dm-7", "253:7", "sanvg-shared--disk", "sda3")
	fakeBlockDevices(t, "dm-1", "dm-2", "dm-4", "dm-5", "dm-6", "dm-7")

	for dev, owner := range map[string]string{
		"dm-5": "VMID 101",          // vm-101-disk-0, named for its guest
		"dm-6": "storage local-lvm", // a thin volume in local-lvm's pool
		"dm-4": "storage local-lvm", // the pool itself
		"dm-2": "storage local-lvm", // and what it is built from
		"dm-7": "storage san-lvm",   // anything in an "lvm" storage's VG
	} {
		rec := call(t, (*Handler).FormatPartition, guardMock(), http.MethodPost, `{"device":"`+dev+`","fs_type":"ext4"}`)
		assertRefused(t, rec, http.StatusConflict, "DISK_GUEST_VOLUME", map[string]string{"device": dev, "owner": owner})
	}

	// The host's own root LV is no guest's.
	cmd := guardMock()
	if rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"dm-1","fs_type":"ext4"}`); rec.Code != http.StatusOK {
		t.Errorf("pve-root: %d %s", rec.Code, rec.Body.String())
	}
}

// QEMU opens a disk without O_EXCL, so the kernel's in-use check passed a
// running VM's disk. Any host: a process of a guest holding the device, the
// disk it is on, or a device stacked on it, refuses the change.
func TestProcessHoldersAreFoundOnAnyHost(t *testing.T) {
	devs := map[string]string{"/dev/sdb": "8:16", "/dev/sdb1": "8:17", "/dev/dm-3": "253:3"}
	layout := func(t *testing.T) {
		sys := newFakeSysfs(t)
		sys.disk("sdb", "8:16")
		sys.part("sdb", "sdb1", "8:17")
		sys.lv("dm-3", "253:3", "vg0-data", "sdb1")
		fakeBlockDevices(t, "sdb", "sdb1", "md0")
		wholeDisks(t, "sdb")
		unmountedAndFree(t)
	}
	for _, tc := range []struct {
		name, comm, cgroup, fd string
		code                   string
		details                map[string]string
	}{
		{"a Proxmox VM's QEMU has the disk the partition is on", "kvm", "0::/qemu.slice/100.scope", "/dev/sdb:rw",
			"DISK_GUEST_VOLUME", map[string]string{"device": "sdb1", "owner": "VM 100"}},
		{"a container's process has a volume stacked on it", "postgres", "0::/lxc/105/ns/system.slice/postgresql.service", "/dev/dm-3:rw",
			"DISK_GUEST_VOLUME", map[string]string{"device": "sdb1", "owner": "CT 105"}},
		{"libvirt's QEMU holding it read-only", "qemu-system-x86", `0::/machine.slice/machine-qemu\x2d1\x2dweb.scope/libvirt/emulator`, "/dev/sdb1:ro",
			"DISK_GUEST_VOLUME", map[string]string{"device": "sdb1", "owner": "qemu-system-x86 (pid 4100)"}},
		{"a writer that is no guest", "dd", "0::/user.slice/user-0.slice/session-3.scope", "/dev/sdb1:rw",
			"DEVICE_IN_USE", map[string]string{"device": "sdb1", "holder": "dd (pid 4100)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout(t)
			proc := newFakeProc(t, devs)
			proc.process(4100, tc.comm, tc.cgroup, tc.fd)
			for _, hc := range guardedCases(t, "sdb", "sdb1", "1") {
				cmd := guardMock()
				rec := call(t, hc.run, cmd, hc.method, hc.body, hc.params...)
				assertRefused(t, rec, http.StatusConflict, tc.code, tc.details)
				if ran(cmd, hc.changer) {
					t.Errorf("%s: %s ran", hc.name, hc.changer)
				}
			}
		})
	}

	// smartd, multipathd and udev read disks without owning them; the panel
	// itself does not count either.
	t.Run("readers and the panel itself do not count", func(t *testing.T) {
		layout(t)
		proc := newFakeProc(t, devs)
		proc.process(900, "smartd", "0::/system.slice/smartmontools.service", "/dev/sdb:ro")
		proc.process(901, "multipathd", "0::/system.slice/multipathd.service", "/dev/sdb1:ro")
		proc.process(os.Getpid(), "sfpanel", "0::/system.slice/sfpanel.service", "/dev/sdb1:rw")
		cmd := guardMock()
		rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"sdb1","fs_type":"ext4"}`)
		if rec.Code != http.StatusOK || !ran(cmd, "mkfs.ext4") {
			t.Errorf("%d %s", rec.Code, rec.Body.String())
		}
	})
}

// Adding a partition leaves the disk's existing ones as they are, so a VM
// using one of them does not stop it; a change to the whole disk does.
func TestAddingAPartitionIgnoresWhatUsesTheOthers(t *testing.T) {
	sys := newFakeSysfs(t)
	sys.disk("sdb", "8:16")
	sys.part("sdb", "sdb1", "8:17")
	fakeBlockDevices(t, "sdb")
	wholeDisks(t, "sdb")
	unmountedAndFree(t)
	proc := newFakeProc(t, map[string]string{"/dev/sdb1": "8:17"})
	proc.process(1964, "kvm", "0::/qemu.slice/100.scope", "/dev/sdb1:rw")

	cmd := guardMock()
	if rec := call(t, (*Handler).CreatePartition, cmd, http.MethodPost, `{"start":"50%","end":"100%","fs_type":"ext4"}`, "device", "sdb"); rec.Code != http.StatusOK || !ran(cmd, "parted") {
		t.Errorf("partition create: %d %s", rec.Code, rec.Body.String())
	}
	for _, tc := range []guardedCase{
		{"partition table", (*Handler).CreatePartitionTable, http.MethodPost, "", []string{"device", "sdb"}, "parted"},
		{"format the whole disk", (*Handler).FormatPartition, http.MethodPost, `{"device":"sdb","fs_type":"ext4"}`, nil, "mkfs.ext4"},
	} {
		cmd := guardMock()
		rec := call(t, tc.run, cmd, tc.method, tc.body, tc.params...)
		assertRefused(t, rec, http.StatusConflict, "DISK_GUEST_VOLUME", map[string]string{"device": "sdb", "owner": "VM 100"})
		if ran(cmd, tc.changer) {
			t.Errorf("%s: %s ran", tc.name, tc.changer)
		}
	}
}

// When /proc cannot be read the device is not known to be free.
func TestGuestGuardFailsClosedWithoutProc(t *testing.T) {
	sys := newFakeSysfs(t)
	sys.disk("sdb", "8:16")
	fakeBlockDevices(t, "sdb")
	unmountedAndFree(t)
	orig := procDir
	procDir = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { procDir = orig })
	cmd := guardMock()
	rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"sdb","fs_type":"ext4"}`)
	assertRefused(t, rec, http.StatusInternalServerError, "DISK_ERROR", nil)
	if ran(cmd, "mkfs.ext4") {
		t.Error("mkfs ran though the holders were unknown")
	}
}

func TestSplitDMName(t *testing.T) {
	for _, tc := range []struct{ in, vg, lv, layer string }{
		{"pve-root", "pve", "root", ""},
		{"pve-vm--100--disk--0", "pve", "vm-100-disk-0", ""},
		{"pve-data-tpool", "pve", "data", "tpool"},
		{"pve-data_tdata", "pve", "data_tdata", ""},
		{"my--vg-my--lv", "my-vg", "my-lv", ""},
		{"nohyphen", "", "", ""},
	} {
		vg, lv, layer := splitDMName(tc.in)
		if vg != tc.vg || lv != tc.lv || layer != tc.layer {
			t.Errorf("splitDMName(%q) = %q %q %q, want %q %q %q", tc.in, vg, lv, layer, tc.vg, tc.lv, tc.layer)
		}
	}
}

// Linux packs a device number's major and minor into dev_t in pieces; nvme
// and device-mapper minors run past 255.
func TestDevNumber(t *testing.T) {
	mkdev := func(major, minor uint64) uint64 {
		return (major&0xfff)<<8 | (major&^0xfff)<<32 | minor&0xff | (minor&^0xff)<<12
	}
	for _, tc := range []struct{ major, minor uint64 }{{8, 17}, {259, 3}, {253, 300}, {230, 70000}, {4096, 1}} {
		if got, want := devNumber(mkdev(tc.major, tc.minor)), fmt.Sprintf("%d:%d", tc.major, tc.minor); got != want {
			t.Errorf("devNumber = %s, want %s", got, want)
		}
	}
	// What a regular file or a character device leads to is no block device.
	if _, ok := statBlockDeviceNumber("/dev/null"); ok {
		t.Error("/dev/null counted as a block device")
	}
	if _, ok := statBlockDeviceNumber(filepath.Join(t.TempDir())); ok {
		t.Error("a directory counted as a block device")
	}
}

func TestGuestOfCgroup(t *testing.T) {
	for _, tc := range []struct{ in, vmid, ctid string }{
		{"0::/qemu.slice/100.scope", "100", ""},
		{"0::/qemu.slice/100.scope/vcpu0", "100", ""},
		{"12:pids:/qemu.slice/208.scope\n0::/qemu.slice/208.scope", "208", ""},
		{"0::/lxc/105/ns/init.scope", "", "105"},
		{"0::/lxc.payload.106/system.slice", "", "106"},
		{"0::/lxc.monitor/105", "", ""},
		{"0::/system.slice/pvedaemon.service", "", ""},
		{"0::/qemu.slice/100.scopes", "", ""},
	} {
		vmid, ctid := guestOfCgroup(tc.in)
		if vmid != tc.vmid || ctid != tc.ctid {
			t.Errorf("guestOfCgroup(%q) = %q %q, want %q %q", tc.in, vmid, ctid, tc.vmid, tc.ctid)
		}
	}
}

func TestReadPVEStorage(t *testing.T) {
	withFile(t, &pveStorageCfg, storageCfg+"\nlvmthin: second\n\tvgname vmdata\n\tthinpool pool2\n# lvm: commented\n")
	cfg := readPVEStorage()
	if want := map[string]string{"sanvg": "san-lvm"}; !reflect.DeepEqual(cfg.vgs, want) {
		t.Errorf("vgs = %v, want %v", cfg.vgs, want)
	}
	if want := map[string]string{"pve/data": "local-lvm", "vmdata/pool2": "second"}; !reflect.DeepEqual(cfg.thinPools, want) {
		t.Errorf("thinPools = %v, want %v", cfg.thinPools, want)
	}
}

func TestGuestVolumeOwner(t *testing.T) {
	for lv, want := range map[string]string{
		"vm-100-disk-0":                  "VMID 100",
		"base-9000-disk-1":               "VMID 9000",
		"vm-100-state-before-upgrade":    "VMID 100",
		"vm-100-cloudinit":               "VMID 100",
		"snap_vm-101-disk-0_nightly":     "VMID 101",
		"snap_something":                 "snapshot",
		"root":                           "",
		"data":                           "",
		"vm-disk-0":                      "",
		"myvm-100-disk-0":                "",
		"vm-100-disk-0-part1":            "VMID 100",
		"subvol-101-disk-0":              "",
		"vm-100-disk-0@before-upgrade-1": "VMID 100",
	} {
		if got := guestVolumeOwner(lv); got != want {
			t.Errorf("guestVolumeOwner(%q) = %q, want %q", lv, got, want)
		}
	}
}

// stat follows a descriptor's link to the open file itself, and on a hard
// NFS mount whose server is gone that never returns; a Proxmox host with a
// dead NFS storage has processes holding disk images there. Only links into
// /dev are stat'ed.
func TestHolderScanStatsOnlyDeviceNodes(t *testing.T) {
	proc := newFakeProc(t, map[string]string{"/dev/sdb": "8:16"})
	proc.process(2100, "kvm", "0::/qemu.slice/100.scope",
		"/mnt/pve/nfs-store/images/100/vm-100-disk-0.qcow2:rw", "anon_inode:[eventfd]:rw", "/dev/sdb:rw")
	var statted []string
	fake := blockDeviceNumber
	blockDeviceNumber = func(path string) (string, bool) {
		target, _ := os.Readlink(path)
		statted = append(statted, target)
		return fake(path)
	}
	holders, err := openHolders(map[string]bool{"8:16": true})
	if err != nil || len(holders) != 1 || holders[0].guest() != "VM 100" || !holders[0].writer {
		t.Fatalf("holders = %+v, %v", holders, err)
	}
	if !reflect.DeepEqual(statted, []string{"/dev/sdb"}) {
		t.Errorf("stat'ed %q, want only /dev/sdb", statted)
	}
}
