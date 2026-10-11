package disk

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

func deleteReq(t *testing.T, device, number string) (*httptest.ResponseRecorder, echo.Context) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/partitions/"+device+"/"+number, nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("device", "number")
	c.SetParamValues(device, number)
	return rec, c
}

// Format refuses a device mounted at a protected path; delete did not, though
// it is the worse operation — it takes the partition table entry with the
// contents. Deleting the partition behind /boot leaves a host that does not
// come back.
func TestDeletePartitionRefusesProtectedMount(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(dev string) (string, error) {
		if dev != "/dev/sda1" {
			t.Errorf("checked %q; the guard must look at the partition, not the disk", dev)
		}
		return "/boot", nil
	}

	stubDeviceInUse(t, func(string) (bool, error) { return false, nil })

	rec, c := deleteReq(t, "sda", "1")
	h := &Handler{Cmd: partedAvailable()}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// The reason, not just the refusal: a missing parted binary also answers
	// with an error, and that would satisfy a status-only assertion.
	if body := rec.Body.String(); !contains(body, "protected system path") {
		t.Errorf("body = %s, want the protected-path reason", body)
	}
}

// An ordinary mounted partition is not a protected path, but deleting it out
// from under a running mount is still not something to do silently.
func TestDeletePartitionRefusesAnyMountedPartition(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "/mnt/data", nil }
	stubDeviceInUse(t, func(string) (bool, error) { return false, nil })

	rec, c := deleteReq(t, "sdb", "2")
	h := &Handler{Cmd: partedAvailable()}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "unmount it first") {
		t.Errorf("body = %s, want the unmount instruction", body)
	}
}

// An unmounted partition still deletes.
func TestDeletePartitionProceedsWhenUnmounted(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	stubDeviceInUse(t, func(string) (bool, error) { return false, nil })

	rec, c := deleteReq(t, "sdb", "2")
	cmd := partedAvailable()
	cmd.Outputs["lsblk"] = exec.MockResult{Output: lsblkDataPartition}
	h := &Handler{Cmd: cmd}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// The kernel names partition 3 of an NVMe disk nvme0n1p3, not nvme0n13. The
// guards looked the latter up, found nothing mounted there, and passed every
// NVMe partition through.
func TestPartitionDevice(t *testing.T) {
	for _, tc := range []struct {
		disk string
		n    int
		want string
	}{
		{"sda", 3, "/dev/sda3"},
		{"vdb", 1, "/dev/vdb1"},
		{"nvme0n1", 3, "/dev/nvme0n1p3"},
		{"mmcblk0", 1, "/dev/mmcblk0p1"},
		{"loop0", 2, "/dev/loop0p2"},
		{"md127", 1, "/dev/md127p1"},
	} {
		if got := partitionDevice(tc.disk, tc.n); got != tc.want {
			t.Errorf("partitionDevice(%q, %d) = %q, want %q", tc.disk, tc.n, got, tc.want)
		}
	}
}

// An LVM physical volume, a RAID member or a ZFS vdev is in use without being
// mounted. parted removed such a partition from the table on disk and then
// failed to tell the kernel, so the request answered with an error while the
// partition was already gone for the next boot. The refusal has to come
// before parted runs.
func TestDeletePartitionRefusesPartitionInUse(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	stubDeviceInUse(t, func(dev string) (bool, error) {
		if dev != "/dev/nvme0n1p3" {
			t.Errorf("checked %q, want /dev/nvme0n1p3", dev)
		}
		return true, nil
	})

	rec, c := deleteReq(t, "nvme0n1", "3")
	cmd := partedAvailable()
	h := &Handler{Cmd: cmd}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, response.ErrDeviceInUse) {
		t.Errorf("body = %s, want %s", body, response.ErrDeviceInUse)
	}
	assertNotRun(t, cmd, "parted")
}

// When the check itself fails, the partition is not known to be free.
func TestDeletePartitionFailsClosedWhenUseUnknown(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	stubDeviceInUse(t, func(string) (bool, error) { return false, os.ErrPermission })

	rec, c := deleteReq(t, "sdb", "2")
	cmd := partedAvailable()
	h := &Handler{Cmd: cmd}
	if err := h.DeletePartition(c); err != nil {
		t.Fatalf("DeletePartition: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "cannot tell whether /dev/sdb2 is in use") {
		t.Errorf("body = %s, want the in-use check failure", body)
	}
	assertNotRun(t, cmd, "parted")
}

// mkfs.ntfs -F formatted a partition an LVM-style holder had open; the
// mkfs.ext4 and mkfs.vfat refusals are their own, not the handler's.
func TestFormatPartitionRefusesDeviceInUse(t *testing.T) {
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	// /dev/null passes the handler's block-or-character-device stat on any
	// host, so the request reaches the in-use check.
	stubDeviceInUse(t, func(dev string) (bool, error) {
		if dev != "/dev/null" {
			t.Errorf("checked %q, want /dev/null", dev)
		}
		return true, nil
	})

	cmd := &exec.MockCommander{Outputs: map[string]exec.MockResult{"exists:mkfs.ntfs": {}}}
	h := &Handler{Cmd: cmd}
	req := httptest.NewRequest(http.MethodPost, "/filesystems/format",
		strings.NewReader(`{"device":"null","fs_type":"ntfs"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := h.FormatPartition(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("FormatPartition: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, response.ErrDeviceInUse) {
		t.Errorf("body = %s, want %s", body, response.ErrDeviceInUse)
	}
	assertNotRun(t, cmd, "mkfs.ntfs")
}

// The exclusive-open check against real devices. A test cannot hold a device
// itself, so it runs only when given one held (a partition under an active
// device-mapper target or LVM volume, or mounted) and one free, as root:
//
//	SFPANEL_TEST_HELD_DEV=/dev/loop0p2 SFPANEL_TEST_FREE_DEV=/dev/loop0p1
func TestDeviceInUseOnRealDevices(t *testing.T) {
	held, free := os.Getenv("SFPANEL_TEST_HELD_DEV"), os.Getenv("SFPANEL_TEST_FREE_DEV")
	if held == "" || free == "" {
		t.Skip("set SFPANEL_TEST_HELD_DEV and SFPANEL_TEST_FREE_DEV to run against real devices")
	}
	for _, tc := range []struct {
		dev  string
		want bool
	}{
		{held, true},
		{free, false},
		{"/dev/sfpanel-no-such-device", false},
	} {
		got, err := deviceInUse(tc.dev)
		if err != nil {
			t.Fatalf("deviceInUse(%s): %v", tc.dev, err)
		}
		if got != tc.want {
			t.Errorf("deviceInUse(%s) = %v, want %v", tc.dev, got, tc.want)
		}
	}
}

func stubDeviceInUse(t *testing.T, fn func(string) (bool, error)) {
	t.Helper()
	orig := deviceInUse
	t.Cleanup(func() { deviceInUse = orig })
	deviceInUse = fn
}

func assertNotRun(t *testing.T, cmd *exec.MockCommander, name string) {
	t.Helper()
	for _, call := range cmd.Calls {
		if call.Name == name {
			t.Errorf("%s ran (%v) although the device was in use", name, call.Args)
		}
	}
}

// parted must look installed, or the handler's tool check short-circuits
// before the guard under test and every assertion here would pass on a 503.
func partedAvailable() *exec.MockCommander {
	return &exec.MockCommander{
		Outputs: map[string]exec.MockResult{"exists:parted": {}},
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// A new disk has no partition table, and parted refuses mkpart there. That
// came back as a 500 with parted's text; the page can only offer the way out
// when it knows which failure it is.
func TestCreatePartitionOnADiskWithoutAPartitionTable(t *testing.T) {
	cmd := partedAvailable()
	cmd.Outputs["parted"] = exec.MockResult{Output: "Error: /dev/vdb: unrecognised disk label\n", Err: errors.New("exit status 1")}
	req := httptest.NewRequest(http.MethodPost, "/disks/vdb/partitions", strings.NewReader(`{"start":"1MiB","end":"100%","fs_type":"ext4"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("device")
	c.SetParamValues("vdb")
	if err := (&Handler{Cmd: cmd}).CreatePartition(c); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), response.ErrDiskNoPartitionTable) {
		t.Errorf("got %d %s, want 409 %s", rec.Code, rec.Body.String(), response.ErrDiskNoPartitionTable)
	}
}

// exitStatus returns the error os/exec reports for a child that exited with
// code n — blkid exits 2 when a disk holds nothing it recognises.
func exitStatus(t *testing.T, n int) error {
	t.Helper()
	err := osexec.Command("sh", "-c", fmt.Sprintf("exit %d", n)).Run()
	if err == nil {
		t.Fatalf("sh exited 0, want %d", n)
	}
	return err
}

func TestCreatePartitionTable(t *testing.T) {
	disks := t.TempDir()
	if err := os.Mkdir(filepath.Join(disks, "vdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	origDir := sysBlockDir
	t.Cleanup(func() { sysBlockDir = origDir })
	sysBlockDir = disks

	for _, tc := range []struct {
		name   string
		device string
		inUse  bool
		blkid  exec.MockResult
		status int
		code   string
		label  bool
	}{
		{"an empty disk gets a GPT label", "vdb", false, exec.MockResult{Err: exitStatus(t, 2)}, http.StatusOK, "", true},
		{"a disk with a partition table", "vdb", false, exec.MockResult{Output: "DEVNAME=/dev/vdb\nPTUUID=1\nPTTYPE=dos\n"}, http.StatusConflict, response.ErrDiskNotEmpty, false},
		{"a filesystem across the whole disk", "vdb", false, exec.MockResult{Output: "DEVNAME=/dev/vdb\nUUID=1\nTYPE=ext4\n"}, http.StatusConflict, response.ErrDiskNotEmpty, false},
		{"a disk blkid cannot read", "vdb", false, exec.MockResult{Output: "error: /dev/vdb: Permission denied\n", Err: exitStatus(t, 4)}, http.StatusInternalServerError, response.ErrDiskError, false},
		{"a disk in use", "vdb", true, exec.MockResult{Err: exitStatus(t, 2)}, http.StatusConflict, response.ErrDeviceInUse, false},
		{"a partition, not a disk", "vdb1", false, exec.MockResult{Err: exitStatus(t, 2)}, http.StatusBadRequest, response.ErrInvalidDevice, false},
	} {
		stubDeviceInUse(t, func(string) (bool, error) { return tc.inUse, nil })
		cmd := &exec.MockCommander{Outputs: map[string]exec.MockResult{"exists:parted": {}, "exists:blkid": {}, "blkid": tc.blkid}}
		req := httptest.NewRequest(http.MethodPost, "/disks/"+tc.device+"/partition-table", nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("device")
		c.SetParamValues(tc.device)
		if err := (&Handler{Cmd: cmd}).CreatePartitionTable(c); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rec.Code != tc.status || (tc.code != "" && !strings.Contains(rec.Body.String(), tc.code)) {
			t.Errorf("%s: got %d %s, want %d %s", tc.name, rec.Code, rec.Body.String(), tc.status, tc.code)
		}
		labeled := false
		for _, call := range cmd.Calls {
			if call.Name == "parted" {
				labeled = strings.Join(call.Args, " ") == "-s /dev/vdb mklabel gpt"
				if !labeled {
					t.Errorf("%s: parted %v", tc.name, call.Args)
				}
			}
		}
		if labeled != tc.label {
			t.Errorf("%s: wrote a label = %v, want %v", tc.name, labeled, tc.label)
		}
	}
}
