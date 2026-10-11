package disk

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func swapMock(blkid exec.MockResult) *exec.MockCommander {
	cmd := guardMock()
	cmd.Outputs["blkid"] = blkid
	return cmd
}

// The dialog's own example was /dev/sdb2, which the handler refused; and on
// an empty device swap goes on, the list catches up (udev) and it is used.
func TestDeviceSwapOnAnEmptyPartition(t *testing.T) {
	unmountedAndFree(t)
	newFakeProc(t, nil)
	fakeBlockDevices(t, "sdb2")
	cmd := swapMock(exec.MockResult{Err: exitStatus(t, 2)})
	rec := call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"device":"/dev/sdb2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if got, want := callNames(cmd.Calls), []string{"lsblk", "blkid", "mkswap", "udevadm", "swapon"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
	if c := findCall(cmd.Calls, "mkswap"); c == nil || !reflect.DeepEqual(c.Args, []string{"/dev/sdb2"}) {
		t.Errorf("mkswap %v", c)
	}
}

// mkswap writes over whatever is on the device: a data partition's file
// system, a disk's partition table. Without force that is refused, and the
// refusal says what is there.
func TestDeviceSwapRefusesADeviceThatHoldsData(t *testing.T) {
	unmountedAndFree(t)
	newFakeProc(t, nil)
	fakeBlockDevices(t, "sdb2", "sdc")
	for _, tc := range []struct {
		name, device string
		blkid        exec.MockResult
		signature    string
	}{
		{"a file system", "sdb2", exec.MockResult{Output: "DEVNAME=/dev/sdb2\nUUID=0f3c2a5e\nTYPE=ext4\nPART_ENTRY_SCHEME=gpt\nPART_ENTRY_TYPE=" + linuxFSGUID + "\n"}, "ext4"},
		{"a partition table", "sdc", exec.MockResult{Output: "DEVNAME=/dev/sdc\nPTUUID=1\nPTTYPE=gpt\n"}, "gpt partition table"},
		{"more than one signature", "sdb2", exec.MockResult{Err: exitStatus(t, 8)}, "more than one signature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := swapMock(tc.blkid)
			rec := call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"device":"`+tc.device+`"}`)
			assertRefused(t, rec, http.StatusConflict, "DISK_NOT_EMPTY", map[string]string{"device": tc.device, "signature": tc.signature})
			if ran(cmd, "mkswap") {
				t.Error("mkswap ran over data without force")
			}
		})
	}

	// A partition's own table entry is not content: blkid -p reports it for
	// an empty partition too.
	cmd := swapMock(exec.MockResult{Output: "DEVNAME=/dev/sdb2\nPART_ENTRY_SCHEME=gpt\nPART_ENTRY_TYPE=" + linuxFSGUID + "\nPART_ENTRY_NUMBER=2\n"})
	if rec := call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"device":"sdb2"}`); rec.Code != http.StatusOK || !ran(cmd, "mkswap") {
		t.Errorf("an empty partition: %d %s", rec.Code, rec.Body.String())
	}

	// With force the operator has seen what is there; blkid is not asked.
	cmd = swapMock(exec.MockResult{Output: "TYPE=ext4\n"})
	if rec := call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"device":"sdb2","force":true}`); rec.Code != http.StatusOK || !ran(cmd, "mkswap") || ran(cmd, "blkid") {
		t.Errorf("with force: %d %s %v", rec.Code, rec.Body.String(), callNames(cmd.Calls))
	}
}

// Swap on a device gets format's checks: a device the kernel holds, one
// mounted at a system path.
func TestDeviceSwapRefusesDevicesInUse(t *testing.T) {
	newFakeProc(t, nil)
	fakeBlockDevices(t, "sdb2")

	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	stubDeviceInUse(t, func(dev string) (bool, error) { return dev == "/dev/sdb2", nil })
	cmd := swapMock(exec.MockResult{Err: exitStatus(t, 2)})
	rec := call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"device":"sdb2","force":true}`)
	assertRefused(t, rec, http.StatusConflict, "DEVICE_IN_USE", nil)
	if ran(cmd, "mkswap") {
		t.Error("mkswap ran on a device in use")
	}

	findDeviceMountpoint = func(string) (string, error) { return "/boot", nil }
	cmd = swapMock(exec.MockResult{Err: exitStatus(t, 2)})
	rec = call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"device":"sdb2","force":true}`)
	assertRefused(t, rec, http.StatusBadRequest, "INVALID_DEVICE", nil)
	if !strings.Contains(rec.Body.String(), "protected system path") || ran(cmd, "mkswap") {
		t.Errorf("mounted at /boot: %s", rec.Body.String())
	}
}

// stubStatfs makes the swap file's directory look like a file system of the
// given type with plenty of room.
func stubStatfs(t *testing.T, magic int64) {
	t.Helper()
	orig := statfs
	t.Cleanup(func() { statfs = orig })
	statfs = func(path string, st *syscall.Statfs_t) error {
		*st = syscall.Statfs_t{Bsize: 4096, Bavail: 1 << 30}
		st.Type = magic
		return nil
	}
}

// fileWritingCommander is a MockCommander whose dd writes its output file, as
// the real one does, so the steps after it find a file.
type fileWritingCommander struct{ *exec.MockCommander }

func (f fileWritingCommander) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	if name == "dd" {
		for _, a := range args {
			if of, ok := strings.CutPrefix(a, "of="); ok {
				if err := os.WriteFile(of, []byte("zeros"), 0o600); err != nil {
					return "", err
				}
			}
		}
	}
	return f.MockCommander.RunCtx(ctx, name, args...)
}

const (
	ext4Magic = 0xef53
)

// OpenZFS has no swap files: dd and mkswap succeeded and swapon failed with
// "Invalid argument". A Proxmox VE install on ZFS has no swap, so this is the
// first thing tried there.
func TestSwapFileOnZFSIsRefused(t *testing.T) {
	stubStatfs(t, zfsMagic)
	path := filepath.Join(t.TempDir(), "swapfile")
	cmd := mock(nil, "dd", "mkswap", "swapon")
	rec := call(t, (*Handler).CreateSwap, cmd, http.MethodPost, `{"path":"`+path+`","size_mb":64}`)
	assertRefused(t, rec, http.StatusBadRequest, "SWAP_UNSUPPORTED_FS", map[string]string{"fs": "ZFS"})
	if ran(cmd, "dd") {
		t.Error("dd ran on ZFS")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a file was left at %s", path)
	}
}

// On btrfs a swap file must be NOCOW, which only takes on an empty file:
// dd's copy-on-write file was refused by swapon.
func TestSwapFileOnBtrfsIsMadeNoCOW(t *testing.T) {
	stubStatfs(t, btrfsMagic)
	path := filepath.Join(t.TempDir(), "swapfile")
	cmd := mock(nil, "dd", "mkswap", "swapon", "chattr")
	rec := call(t, (*Handler).CreateSwap, fileWritingCommander{cmd}, http.MethodPost, `{"path":"`+path+`","size_mb":64}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if got, want := callNames(cmd.Calls), []string{"chattr", "dd", "mkswap", "swapon"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
	if c := findCall(cmd.Calls, "chattr"); c == nil || !reflect.DeepEqual(c.Args, []string{"+C", path}) {
		t.Errorf("chattr %v", c)
	}

	// On ext4 nothing of the sort.
	stubStatfs(t, ext4Magic)
	path = filepath.Join(t.TempDir(), "swapfile")
	cmd = mock(nil, "dd", "mkswap", "swapon", "chattr")
	if rec := call(t, (*Handler).CreateSwap, fileWritingCommander{cmd}, http.MethodPost, `{"path":"`+path+`","size_mb":64}`); rec.Code != http.StatusOK || ran(cmd, "chattr") {
		t.Errorf("ext4: %d %s %v", rec.Code, rec.Body.String(), callNames(cmd.Calls))
	}
}

// Any step that fails after the file exists removes it: a failed swapon left
// the file behind, and the next attempt was refused for overwriting it.
func TestSwapFileIsRemovedWhenALaterStepFails(t *testing.T) {
	for _, step := range []string{"mkswap", "swapon"} {
		t.Run(step, func(t *testing.T) {
			stubStatfs(t, ext4Magic)
			path := filepath.Join(t.TempDir(), "swapfile")
			cmd := mock(map[string]exec.MockResult{step: {Output: "swapon: " + path + ": Invalid argument", Err: errors.New("exit status 255")}}, "dd", "mkswap", "swapon")
			rec := call(t, (*Handler).CreateSwap, fileWritingCommander{cmd}, http.MethodPost, `{"path":"`+path+`","size_mb":64}`)
			assertRefused(t, rec, http.StatusInternalServerError, "SWAP_ERROR", nil)
			if !strings.Contains(rec.Body.String(), step+" failed") {
				t.Errorf("not the %s failure: %s", step, rec.Body.String())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s failed and the file stayed at %s", step, path)
			}
		})
	}
}
