package disk

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// partitionDevice names partition n of disk the way the kernel does: a disk
// whose name ends in a digit takes a "p" before the number, so partition 3 is
// sda3 on sda but nvme0n1p3 on nvme0n1 (likewise mmcblk0p1, loop0p2, md127p1).
func partitionDevice(disk string, n int) string {
	sep := ""
	if disk != "" && disk[len(disk)-1] >= '0' && disk[len(disk)-1] <= '9' {
		sep = "p"
	}
	return fmt.Sprintf("/dev/%s%s%d", disk, sep, n)
}

// deviceInUse reports whether the kernel holds dev: mounted, active swap, an
// LVM physical volume, a RAID member or a ZFS vdev. Only the first of those is
// a mount, so a mount check lets the rest through, and the tools behind it do
// not all stop on their own: parted removes such a partition from the table on
// disk and only then fails to tell the kernel, so the entry is gone at the
// next boot, and mkfs.ntfs -F formats it on the spot. An exclusive open fails
// with EBUSY in exactly these cases. A missing node is not in use: the kernel
// has no such device for anything to hold.
//
// Overridable in tests, which cannot hold a device.
var deviceInUse = func(dev string) (bool, error) {
	f, err := os.OpenFile(dev, os.O_RDONLY|syscall.O_EXCL, 0)
	switch {
	case err == nil:
		_ = f.Close()
		return false, nil
	case errors.Is(err, syscall.EBUSY):
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
