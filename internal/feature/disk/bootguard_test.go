package disk

import (
	"errors"
	"net/http"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

const (
	espGUID      = "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"
	biosBootGUID = "21686148-6449-6e6f-744e-656564454649"
	linuxFSGUID  = "0fc63daf-8483-4772-8e79-3d69d8477de4"
)

// Proxmox keeps its ESPs unmounted — proxmox-boot-tool mounts one only to
// copy a kernel in — and a BIOS boot partition has no file system to mount,
// so the mount guard never saw either. Delete, format, mkswap, pvcreate and
// RAID went through, and a single-disk host had no boot loader at its next
// boot.
func TestBootPartitionsAreRefused(t *testing.T) {
	unmountedAndFree(t)
	newFakeProc(t, nil)
	fakeBlockDevices(t, "nvme0n1", "nvme0n1p1", "nvme0n1p2", "sda1", "md0")

	esp := lsblkOne("nvme0n1p2", "gpt", espGUID, "vfat", "1A2B-3C4D")
	for _, tc := range []struct {
		name    string
		lsblk   string
		device  string // what the refusal names
		guarded guardedCase
	}{
		{"format an ESP", esp, "nvme0n1p2",
			guardedCase{"", (*Handler).FormatPartition, http.MethodPost, `{"device":"nvme0n1p2","fs_type":"ext4"}`, nil, "mkfs.ext4"}},
		{"delete a BIOS boot partition", lsblkOne("nvme0n1p1", "gpt", biosBootGUID, "", ""), "nvme0n1p1",
			guardedCase{"", (*Handler).DeletePartition, http.MethodDelete, "", []string{"device", "nvme0n1", "number", "1"}, "parted"}},
		{"delete an MBR EFI partition", lsblkOne("sda1", "dos", "0xef", "vfat", "5E6F-7A8B"), "sda1",
			guardedCase{"", (*Handler).DeletePartition, http.MethodDelete, "", []string{"device", "sda", "number", "1"}, "parted"}},
		{"swap over an ESP", esp, "nvme0n1p2",
			guardedCase{"", (*Handler).CreateSwap, http.MethodPost, `{"device":"nvme0n1p2","force":true}`, nil, "mkswap"}},
		{"pvcreate across a disk that holds the boot partitions", lsblkPVEBootDisk, "nvme0n1p1",
			guardedCase{"", (*Handler).CreatePV, http.MethodPost, `{"device":"/dev/nvme0n1"}`, nil, "pvcreate"}},
		{"RAID over an ESP", esp, "nvme0n1p2",
			guardedCase{"", (*Handler).CreateRAID, http.MethodPost, `{"name":"md9","level":"raid1","devices":["nvme0n1p2","sda1"]}`, nil, "mdadm"}},
		{"an ESP added to an array", esp, "nvme0n1p2",
			guardedCase{"", (*Handler).AddRAIDDisk, http.MethodPost, `{"device":"nvme0n1p2"}`, []string{"name", "md0"}, "mdadm"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := guardMock()
			cmd.Outputs["lsblk"] = exec.MockResult{Output: tc.lsblk}
			g := tc.guarded
			rec := call(t, g.run, cmd, g.method, g.body, g.params...)
			assertRefused(t, rec, http.StatusConflict, "DISK_BOOT_PARTITION", map[string]string{"device": tc.device})
			if ran(cmd, g.changer) {
				t.Errorf("%s ran on a boot partition", g.changer)
			}
		})
	}

	// The pool member next to them is no boot partition (the kernel holds
	// it, which deviceInUse answers for).
	cmd := guardMock()
	cmd.Outputs["lsblk"] = exec.MockResult{Output: lsblkOne("nvme0n1p2", "gpt", "6a898cc3-1dd2-11b2-99a6-080020736631", "zfs_member", "1")}
	if rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"nvme0n1p2","fs_type":"ext4"}`); rec.Code != http.StatusOK {
		t.Errorf("a ZFS member is not a boot partition: %d %s", rec.Code, rec.Body.String())
	}
}

// proxmox-boot-tool lists the ESPs it keeps a kernel on by file system UUID;
// on Proxmox VE such a partition is a boot partition whatever its type says.
func TestProxmoxBootUUIDsMarkBootPartitions(t *testing.T) {
	unmountedAndFree(t)
	newFakeProc(t, nil)
	fakeBlockDevices(t, "sdb2")
	withFile(t, &proxmoxBootUUIDs, "1A2B-3C4D\n9E8F-7A6B\n")
	partition := lsblkOne("sdb2", "gpt", linuxFSGUID, "vfat", "1a2b-3c4d")
	format := func() (*exec.MockCommander, int, string) {
		cmd := guardMock()
		cmd.Outputs["lsblk"] = exec.MockResult{Output: partition}
		rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"sdb2","fs_type":"ext4"}`)
		return cmd, rec.Code, rec.Body.String()
	}

	// Ubuntu has no such list; the file means nothing there.
	if cmd, code, body := format(); code != http.StatusOK || !ran(cmd, "mkfs.ext4") {
		t.Fatalf("on Ubuntu: %d %s", code, body)
	}

	onPVE(t)
	cmd := guardMock()
	cmd.Outputs["lsblk"] = exec.MockResult{Output: partition}
	rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"sdb2","fs_type":"ext4"}`)
	assertRefused(t, rec, http.StatusConflict, "DISK_BOOT_PARTITION", map[string]string{"device": "sdb2"})
	if ran(cmd, "mkfs.ext4") {
		t.Error("mkfs ran on a proxmox-boot ESP")
	}
}

// Without lsblk's answer the partition is not known not to be a boot one.
func TestBootGuardFailsClosed(t *testing.T) {
	unmountedAndFree(t)
	newFakeProc(t, nil)
	fakeBlockDevices(t, "sdb2")
	cmd := guardMock()
	cmd.Outputs["lsblk"] = exec.MockResult{Output: "lsblk: /dev/sdb2: not a block device", Err: errors.New("exit status 32")}
	rec := call(t, (*Handler).FormatPartition, cmd, http.MethodPost, `{"device":"sdb2","fs_type":"ext4"}`)
	assertRefused(t, rec, http.StatusInternalServerError, "DISK_ERROR", nil)
	if ran(cmd, "mkfs.ext4") {
		t.Error("mkfs ran without knowing what the partition was")
	}

	// Nor is a partition about to be deleted.
	cmd = guardMock()
	cmd.Outputs["lsblk"] = exec.MockResult{Output: "lsblk: /dev/sdb2: not a block device", Err: errors.New("exit status 32")}
	rec = call(t, (*Handler).DeletePartition, cmd, http.MethodDelete, "", "device", "sdb", "number", "2")
	assertRefused(t, rec, http.StatusInternalServerError, "DISK_ERROR", nil)
	if ran(cmd, "parted") {
		t.Error("parted ran without knowing what the partition was")
	}
}

// Debian's installer on a BIOS machine with guided LVM: sda1 /boot, sda2 an
// extended partition, sda5 inside it the physical volume under root. The
// kernel holds sda5, not the container, so deleting sda2 passed the in-use
// check and parted wrote a table without sda5 — refused on screen, gone at
// the next boot.
func TestExtendedPartitionWithLogicalsIsRefused(t *testing.T) {
	unmountedAndFree(t)
	newFakeProc(t, nil)
	extended := lsblkOne("sda2", "dos", "0x5", "", "")
	for _, tc := range []struct {
		name    string
		lsblk   string
		numbers []int
		refused bool
	}{
		{"an extended partition holding logical ones", extended, []int{1, 2, 5, 6}, true},
		{"an empty extended partition", extended, []int{1, 2}, false},
		{"a primary partition beside logical ones", lsblkOne("sda2", "dos", "0x83", "ext4", "x"), []int{1, 2, 5}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubPartitionNumbers(t, tc.numbers, tc.numbers)
			cmd := guardMock()
			cmd.Outputs["lsblk"] = exec.MockResult{Output: tc.lsblk}
			rec := call(t, (*Handler).DeletePartition, cmd, http.MethodDelete, "", "device", "sda", "number", "2")
			if tc.refused {
				assertRefused(t, rec, http.StatusConflict, "DEVICE_IN_USE", map[string]string{"device": "sda2", "logical": "sda5, sda6"})
				if ran(cmd, "parted") {
					t.Error("parted ran on an extended partition holding logical ones")
				}
				return
			}
			if rec.Code != http.StatusOK || !ran(cmd, "parted") {
				t.Errorf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
