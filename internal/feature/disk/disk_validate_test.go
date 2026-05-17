package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateDeviceName_RejectsSlash(t *testing.T) {
	// The previous regex allowed '/' to support an "vg0/lv0" idea, but
	// every actual caller prepends "/dev/" before calling out to mdadm /
	// lvm / mkfs. Allowing '/' lets a caller submit `sda/anything` which
	// then becomes `/dev/sda/anything` — an unrelated kernel device the
	// operator never intended to target. LVM names use validateLVMName,
	// which is separate.
	for _, name := range []string{
		"sda/anything",
		"vg0/lv0",
		"foo/bar/baz",
		"/sda",
		"sda/",
	} {
		if err := validateDeviceName(name); err == nil {
			t.Errorf("validateDeviceName(%q) should be rejected — '/' is not a valid device name char", name)
		}
	}
}

func TestValidateDeviceName_StillAcceptsLegitimate(t *testing.T) {
	for _, name := range []string{
		"sda",
		"sda1",
		"nvme0n1",
		"nvme0n1p1",
		"md0",
		"dm-0",
		"vda",
	} {
		if err := validateDeviceName(name); err != nil {
			t.Errorf("validateDeviceName(%q) should be accepted: %v", name, err)
		}
	}
}

func TestVerifyBlockDevice_AcceptsRealBlockDevice(t *testing.T) {
	// /dev/null is a character device, not block — should be rejected.
	// /dev/loop0 may or may not exist; use a deterministic real block
	// device discovery instead: stat existing devices under /dev.
	candidates, _ := filepath.Glob("/dev/sd*")
	candidates = append(candidates, "/dev/null") // negative case
	saw := map[string]bool{}
	for _, dev := range candidates {
		info, err := os.Stat(dev)
		if err != nil {
			continue
		}
		isBlock := info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0
		err = verifyBlockDevice(dev)
		if isBlock {
			if err != nil {
				t.Errorf("verifyBlockDevice(%q) rejected a real block device: %v", dev, err)
			}
			saw["block"] = true
		} else {
			if err == nil {
				t.Errorf("verifyBlockDevice(%q) accepted a non-block device", dev)
			}
			saw["non-block"] = true
		}
	}
	if !saw["non-block"] {
		t.Skip("no non-block device found to test rejection path (CI sandbox?)")
	}
}

func TestVerifyBlockDevice_RejectsMissing(t *testing.T) {
	if err := verifyBlockDevice("/dev/this-cannot-exist-zz9"); err == nil {
		t.Error("verifyBlockDevice should reject a non-existent device")
	}
}
