package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// The guards here run before every handler that changes a device: format,
// mount, swap on a device, partition create/delete/table, pvcreate, RAID
// create/add, LV remove/resize. deviceInUse sees only what the kernel itself
// holds; these see what it does not:
//
//   - a virtual machine's or container's disk. QEMU opens its disks without
//     O_EXCL, and a stopped guest's volume is open to nobody, so mkfs, parted
//     and mkswap went straight through a guest's disk;
//   - a boot partition. Proxmox VE keeps its EFI System partitions unmounted
//     (proxmox-boot-tool mounts them only to sync kernels), so no mount check
//     sees them, and a BIOS boot partition never has a file system to mount.

// The host paths the guards read. Variables so a test stands in for the host:
// TestMain points every one of them at an empty scratch tree.
var (
	procDir          = "/proc"
	sysClassBlockDir = "/sys/class/block"
	pveStorageCfg    = "/etc/pve/storage.cfg"
	proxmoxBootUUIDs = "/etc/kernel/proxmox-boot-uuids"
	// udev's by-name links to zvols and RBD images, e.g.
	// /dev/zvol/rpool/data/vm-100-disk-0 -> ../../../zd16.
	volumeLinkDirs = []string{"/dev/zvol", "/dev/rbd", "/dev/rbd-pve"}
)

// refusal is why a guard turned a change down: the status, code and English
// message the handler answers with, and the details the page words it with.
type refusal struct {
	status  int
	code    string
	message string
	details map[string]string
}

func (r *refusal) send(c echo.Context) error {
	return response.FailWith(c, r.status, r.code, r.message, r.details)
}

func guestRefusal(device, owner string) *refusal {
	return &refusal{http.StatusConflict, response.ErrDiskGuestVolume,
		fmt.Sprintf("%s belongs to a virtual machine or container (%s) and cannot be changed here", device, owner),
		map[string]string{"device": device, "owner": owner}}
}

// ---------- Guest volumes ----------

// checkGuestVolume refuses a change to the device called name (a kernel name:
// sdb1, zd16, dm-3) when a guest uses it. Two things tell:
//
//   - on Proxmox VE, the name: a zvol (zd*), an RBD image (rbd*) or an NBD
//     export (nbd*) is a guest volume, and so is an LVM volume named the way
//     Proxmox names guest disks or kept in a VG or thin pool that
//     /etc/pve/storage.cfg gives to a storage. A stopped guest is caught here;
//   - anywhere, a process that has the device open — the device, the disk it
//     is a partition of, or anything stacked on either — when it is a guest's
//     process, or has it open for writing. smartd, multipathd and udev read
//     disks without owning them, and do not count.
//
// withPartitions takes in what is on a whole disk's partitions too, for a
// change that rewrites the disk; adding a partition leaves them alone.
//
// A writer that is not a guest is answered with DEVICE_IN_USE naming it, not
// called a virtual machine's disk.
func checkGuestVolume(name string, withPartitions bool) *refusal {
	related := relatedDevices(name, withPartitions)
	if platform.Get().PVE {
		cfg := readPVEStorage()
		for _, d := range related {
			if owner := pveDeviceOwner(d, cfg); owner != "" {
				return guestRefusal(name, owner)
			}
		}
	}
	holders, err := openHolders(deviceNumbers(related))
	if err != nil {
		return &refusal{http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot tell whether %s is open: %v", name, err), nil}
	}
	for _, h := range holders {
		if owner := h.guest(); owner != "" {
			return guestRefusal(name, owner)
		}
	}
	for _, h := range holders {
		if h.writer {
			return &refusal{http.StatusConflict, response.ErrDeviceInUse,
				fmt.Sprintf("%s is open for writing by %s — release it first", name, h),
				map[string]string{"device": name, "holder": h.String()}}
		}
	}
	return nil
}

// checkGuestLV refuses a change to logical volume vg/lv on Proxmox VE when it
// is a guest's volume or lives in a VG or thin pool a storage owns. pool is the
// thin pool it is in, if any. Elsewhere LVM's names say nothing about owners.
func checkGuestLV(vg, lv, pool string) *refusal {
	if !platform.Get().PVE {
		return nil
	}
	if owner := pveLVOwner(readPVEStorage(), vg, lv, pool); owner != "" {
		return guestRefusal(vg+"/"+lv, owner)
	}
	return nil
}

// checkStoragePool refuses removing thin pool vg/lv on Proxmox VE when an
// "lvmthin" storage keeps its guests' volumes in it: the storage would point
// at nothing. Growing the pool stays allowed; it is how such a storage grows.
func checkStoragePool(vg, lv string) *refusal {
	if !platform.Get().PVE {
		return nil
	}
	if id := readPVEStorage().thinPools[vg+"/"+lv]; id != "" {
		return guestRefusal(vg+"/"+lv, "storage "+id)
	}
	return nil
}

// relatedDevices is name and every device whose use makes changing name
// unsafe: the disk a partition is on, with withPartitions the partitions of a
// disk, and whatever is stacked on any of those (device-mapper, md), all the
// way up. A device sysfs does not know is only itself.
func relatedDevices(name string, withPartitions bool) []string {
	out := []string{name}
	seen := map[string]bool{name: true}
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if parent := parentDisk(name); parent != "" {
		add(parent)
	} else if withPartitions {
		for _, p := range partitionsOf(name) {
			add(p)
		}
	}
	for i := 0; i < len(out); i++ {
		for _, h := range dirNames(filepath.Join(sysClassBlockDir, out[i], "holders")) {
			add(h)
		}
	}
	return out
}

// parentDisk is the disk partition name is on, or "" when name is not a
// partition. sysfs keeps a partition's directory inside its disk's.
func parentDisk(name string) string {
	if !fileExists(filepath.Join(sysClassBlockDir, name, "partition")) {
		return ""
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(sysClassBlockDir, name))
	if err != nil {
		return ""
	}
	return filepath.Base(filepath.Dir(dir))
}

// partitionsOf lists the partitions of disk name.
func partitionsOf(name string) []string {
	var out []string
	for _, e := range dirNames(filepath.Join(sysClassBlockDir, name)) {
		if fileExists(filepath.Join(sysClassBlockDir, name, e, "partition")) {
			out = append(out, e)
		}
	}
	return out
}

// deviceNumbers is the "major:minor" of each device, as sysfs writes it.
func deviceNumbers(names []string) map[string]bool {
	nums := map[string]bool{}
	for _, n := range names {
		if b, err := os.ReadFile(filepath.Join(sysClassBlockDir, n, "dev")); err == nil {
			if num := strings.TrimSpace(string(b)); num != "" {
				nums[num] = true
			}
		}
	}
	return nums
}

// holder is a process with a device open.
type holder struct {
	pid    int
	comm   string
	vmid   string // a Proxmox VM's QEMU process
	ctid   string // a process inside an LXC container
	writer bool   // has it open for writing
}

func (h holder) String() string { return fmt.Sprintf("%s (pid %d)", h.comm, h.pid) }

// guest names the guest h runs for, or "" when h is not one.
func (h holder) guest() string {
	switch {
	case h.vmid != "":
		return "VM " + h.vmid
	case h.ctid != "":
		return "CT " + h.ctid
	case h.comm == "kvm" || strings.HasPrefix(h.comm, "qemu"):
		return h.String()
	}
	return ""
}

// openHolders lists the processes, other than the panel, that have one of the
// devices nums ("major:minor") open, by walking /proc/<pid>/fd.
//
// Only links into /dev are stat'ed. stat follows the link to the open file
// itself, and on a hard NFS mount whose server has gone away that blocks for
// good — a Proxmox host with a dead NFS storage has processes holding files
// there. A device node is on devtmpfs, or a container's own /dev.
func openHolders(nums map[string]bool) ([]holder, error) {
	if len(nums) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	var out []holder
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		fdDir := filepath.Join(procDir, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // exited, or not ours to read
		}
		held, writer := false, false
		for _, fd := range fds {
			link := filepath.Join(fdDir, fd.Name())
			target, err := os.Readlink(link)
			if err != nil || !strings.HasPrefix(target, "/dev/") {
				continue
			}
			if num, ok := blockDeviceNumber(link); ok && nums[num] {
				held = true
				writer = writer || openForWriting(filepath.Join(procDir, e.Name(), "fdinfo", fd.Name()))
			}
		}
		if held {
			h := readHolder(pid)
			h.writer = writer
			out = append(out, h)
		}
	}
	return out, nil
}

// openForWriting reads an fd's access mode from /proc/<pid>/fdinfo/<fd>,
// whose "flags:" line holds the open(2) flags in octal.
func openForWriting(fdinfo string) bool {
	data, err := os.ReadFile(fdinfo)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "flags:"); ok {
			flags, err := strconv.ParseUint(strings.TrimSpace(v), 8, 64)
			return err == nil && flags&uint64(syscall.O_ACCMODE) != uint64(syscall.O_RDONLY)
		}
	}
	return false
}

// blockDeviceNumber is the "major:minor" of the block device path leads to;
// ok is false for anything else. A variable because a test has no block
// device to stat.
var blockDeviceNumber = statBlockDeviceNumber

func statBlockDeviceNumber(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return "", false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return devNumber(uint64(st.Rdev)), true
}

// devNumber splits a Linux dev_t into "major:minor" the way glibc's
// major()/minor() do.
func devNumber(rdev uint64) string {
	major := (rdev&0x00000000000fff00)>>8 | (rdev&0xfffff00000000000)>>32
	minor := rdev&0x00000000000000ff | (rdev&0x00000ffffff00000)>>12
	return fmt.Sprintf("%d:%d", major, minor)
}

func readHolder(pid int) holder {
	dir := filepath.Join(procDir, strconv.Itoa(pid))
	h := holder{pid: pid}
	if b, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
		h.comm = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
		h.vmid, h.ctid = guestOfCgroup(string(b))
	}
	return h
}

var (
	qemuScopeRe = regexp.MustCompile(`/qemu\.slice/(\d+)\.scope(?:/|$)`)
	lxcCgroupRe = regexp.MustCompile(`/lxc(?:\.payload\.|/)(\d+)(?:/|$)`)
)

// guestOfCgroup reads which guest a process runs for from /proc/<pid>/cgroup:
// Proxmox starts each VM's QEMU in /qemu.slice/<vmid>.scope, and a container's
// processes run under /lxc/<vmid>/ (/lxc.payload.<vmid>/ in later LXC).
func guestOfCgroup(data string) (vmid, ctid string) {
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		if m := qemuScopeRe.FindStringSubmatch(parts[2]); m != nil {
			return m[1], ""
		}
		if m := lxcCgroupRe.FindStringSubmatch(parts[2]); m != nil {
			return "", m[1]
		}
	}
	return "", ""
}

// ---------- Proxmox VE names ----------

var (
	zvolNameRe = regexp.MustCompile(`^zd\d+(?:p\d+)?$`)
	rbdNameRe  = regexp.MustCompile(`^rbd\d+(?:p\d+)?$`)
	nbdNameRe  = regexp.MustCompile(`^nbd\d+(?:p\d+)?$`)
	// Proxmox names a guest's volumes after its VMID: vm-<id>-disk-<n> and,
	// for a template, base-<id>-disk-<n>; vm-<id>-state-<snapshot> holds
	// saved RAM, vm-<id>-cloudinit the cloud-init drive, and an LVM-thin
	// snapshot is snap_<volume>_<snapshot>.
	guestVolumeRe = regexp.MustCompile(`^(?:snap_)?(?:vm|base)-(\d+)-(?:disk-\d+|state-|cloudinit)`)
)

// guestVolumeOwner names the guest a volume called lv belongs to, by Proxmox's
// naming, or "" for a name Proxmox does not give its volumes.
func guestVolumeOwner(lv string) string {
	if m := guestVolumeRe.FindStringSubmatch(lv); m != nil {
		return "VMID " + m[1]
	}
	if strings.HasPrefix(lv, "snap_") {
		return "snapshot"
	}
	return ""
}

// pveStorage is what /etc/pve/storage.cfg says about LVM: the VGs an "lvm"
// storage keeps guest volumes in, and the thin pools an "lvmthin" storage
// does, each with the storage's ID.
type pveStorage struct {
	vgs       map[string]string // vg -> storage
	thinPools map[string]string // "vg/pool" -> storage
}

// readPVEStorage reads storage.cfg. A file that cannot be read (pmxcfs down,
// /etc/pve empty) leaves both maps empty; the names still tell guest volumes.
func readPVEStorage() pveStorage {
	cfg := pveStorage{vgs: map[string]string{}, thinPools: map[string]string{}}
	data, err := os.ReadFile(pveStorageCfg)
	if err != nil {
		return cfg
	}
	var typ, id, vg, pool string
	flush := func() {
		switch {
		case typ == "lvm" && vg != "":
			cfg.vgs[vg] = id
		case typ == "lvmthin" && vg != "" && pool != "":
			cfg.thinPools[vg+"/"+pool] = id
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			// "lvmthin: local-lvm" opens a storage; its properties follow,
			// indented.
			flush()
			typ, id, vg, pool = "", "", "", ""
			if t, rest, ok := strings.Cut(line, ":"); ok {
				typ, id = strings.TrimSpace(t), strings.TrimSpace(rest)
			}
			continue
		}
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch key {
		case "vgname":
			vg = strings.TrimSpace(value)
		case "thinpool":
			pool = strings.TrimSpace(value)
		}
	}
	flush()
	return cfg
}

// pveLVOwner names what owns LV vg/lv on Proxmox VE: the guest it is named
// for, or the storage whose VG or thin pool (pool) it lives in. "" when
// nothing does — the host's own root and swap in VG pve, say.
func pveLVOwner(cfg pveStorage, vg, lv, pool string) string {
	if owner := guestVolumeOwner(lv); owner != "" {
		return owner
	}
	if id := cfg.vgs[vg]; id != "" {
		return "storage " + id
	}
	if pool != "" {
		if id := cfg.thinPools[vg+"/"+pool]; id != "" {
			return "storage " + id
		}
	}
	return ""
}

// pveDeviceOwner names the guest or storage that owns kernel device name on
// Proxmox VE, or "" when nothing does.
func pveDeviceOwner(name string, cfg pveStorage) string {
	switch {
	case zvolNameRe.MatchString(name):
		return linkedVolumeOwner(name, "zvol")
	case rbdNameRe.MatchString(name):
		return linkedVolumeOwner(name, "RBD")
	case nbdNameRe.MatchString(name):
		return "NBD"
	}
	vg, lv, _, ok := lvmVolume(name)
	if !ok {
		return ""
	}
	if owner := pveLVOwner(cfg, vg, lv, thinPoolOf(name)); owner != "" {
		return owner
	}
	// A storage's thin pool itself, and the hidden volumes it is built from.
	pool := strings.TrimSuffix(strings.TrimSuffix(lv, "_tdata"), "_tmeta")
	if id := cfg.thinPools[vg+"/"+pool]; id != "" {
		return "storage " + id
	}
	return ""
}

// linkedVolumeOwner names a zvol or RBD device by the volume udev links to
// it: "VMID 100" for rpool/data/vm-100-disk-0, the volume's path for one
// Proxmox did not name, or only kind when no link points at it.
func linkedVolumeOwner(name, kind string) string {
	for _, dir := range volumeLinkDirs {
		var found string
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.Type()&fs.ModeSymlink == 0 {
				return nil
			}
			if target, err := os.Readlink(path); err == nil && filepath.Base(target) == name {
				found, _ = filepath.Rel(dir, path)
				return filepath.SkipAll
			}
			return nil
		})
		if found == "" {
			continue
		}
		if owner := guestVolumeOwner(filepath.Base(found)); owner != "" && owner != "snapshot" {
			return owner
		}
		return kind + " " + found
	}
	return kind
}

// lvmVolume reads the VG and LV behind device-mapper device name from sysfs.
// ok is false for a device that is not an LVM volume.
func lvmVolume(name string) (vg, lv, layer string, ok bool) {
	dm := filepath.Join(sysClassBlockDir, name, "dm")
	uuid, err := os.ReadFile(filepath.Join(dm, "uuid"))
	if err != nil || !strings.HasPrefix(string(uuid), "LVM-") {
		return "", "", "", false
	}
	dmName, err := os.ReadFile(filepath.Join(dm, "name"))
	if err != nil {
		return "", "", "", false
	}
	vg, lv, layer = splitDMName(strings.TrimSpace(string(dmName)))
	return vg, lv, layer, vg != "" && lv != ""
}

// splitDMName undoes LVM's device-mapper naming: VG and LV joined by one
// hyphen with every hyphen inside either doubled, so pve-vm--100--disk--0 is
// pve/vm-100-disk-0, and an internal layer after another single hyphen
// (pve-data-tpool is the pool device of pve/data).
func splitDMName(dm string) (vg, lv, layer string) {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(dm); i++ {
		if dm[i] != '-' {
			cur.WriteByte(dm[i])
			continue
		}
		if i+1 < len(dm) && dm[i+1] == '-' {
			cur.WriteByte('-')
			i++
			continue
		}
		parts = append(parts, cur.String())
		cur.Reset()
	}
	parts = append(parts, cur.String())
	if len(parts) < 2 {
		return "", "", ""
	}
	return parts[0], parts[1], strings.Join(parts[2:], "-")
}

// thinPoolOf is the thin pool a thin volume's device is built on: a thin
// volume's device-mapper table points at the pool's "-tpool" device.
func thinPoolOf(name string) string {
	for _, s := range dirNames(filepath.Join(sysClassBlockDir, name, "slaves")) {
		if _, lv, layer, ok := lvmVolume(s); ok && layer == "tpool" {
			return lv
		}
	}
	return ""
}

// ---------- Boot partitions ----------

// Partition types that hold a boot loader, by GPT type GUID or MBR type code.
var bootPartTypes = map[string]string{
	"c12a7328-f81f-11d2-ba4b-00a0c93ec93b": "EFI System",
	"0xef":                                 "EFI System",
	"21686148-6449-6e6f-744e-656564454649": "BIOS boot",
}

// proxmoxBootESPs is the set of ESP file system UUIDs proxmox-boot-tool keeps
// in sync, upper-cased. Only Proxmox VE has the list.
func proxmoxBootESPs() map[string]bool {
	esps := map[string]bool{}
	if !platform.Get().PVE {
		return esps
	}
	data, err := os.ReadFile(proxmoxBootUUIDs)
	if err != nil {
		return esps
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			esps[strings.ToUpper(f[0])] = true
		}
	}
	return esps
}

// bootKind says what kind of boot partition a device is, or "" when it is
// not one: an EFI System or BIOS boot partition by type, or on Proxmox VE a
// vfat ESP that proxmox-boot-tool keeps a kernel on.
func bootKind(devType, partType, fsType, uuid string, esps map[string]bool) string {
	if devType != "part" {
		return ""
	}
	if kind := bootPartTypes[strings.ToLower(partType)]; kind != "" {
		return kind
	}
	if fsType == "vfat" && uuid != "" && esps[strings.ToUpper(uuid)] {
		return "proxmox-boot"
	}
	return ""
}

// blockFacts is what lsblk knows about a device, for the boot and extended
// partition checks.
type blockFacts struct {
	Name     string       `json:"name"`
	Type     string       `json:"type"`
	PtType   string       `json:"pttype"`
	PartType string       `json:"parttype"`
	FsType   string       `json:"fstype"`
	UUID     string       `json:"uuid"`
	Children []blockFacts `json:"children"`
}

// blockFacts lists devPath and, for a disk, what is on it — its partitions,
// and the devices stacked on those. The device itself comes first.
func (h *Handler) blockFacts(ctx context.Context, devPath string) ([]blockFacts, error) {
	out, err := h.Cmd.RunCtx(ctx, "lsblk", "-J", "-o", "NAME,TYPE,PTTYPE,PARTTYPE,FSTYPE,UUID", devPath)
	if err != nil {
		return nil, fmt.Errorf("lsblk: %s", response.SanitizeOutput(strings.TrimSpace(out)))
	}
	var raw struct {
		BlockDevices []blockFacts `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	var facts []blockFacts
	for _, d := range raw.BlockDevices {
		facts = append(facts, d)
		facts = append(facts, d.Children...)
	}
	if len(facts) == 0 {
		return nil, fmt.Errorf("lsblk did not list %s", devPath)
	}
	return facts, nil
}

// checkBootPartition refuses a change to devPath when it is a boot partition
// or, for a whole disk, holds one: formatting the disk takes the partition
// with it.
func (h *Handler) checkBootPartition(ctx context.Context, devPath string) *refusal {
	facts, err := h.blockFacts(ctx, devPath)
	if err != nil {
		return &refusal{http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot tell whether %s is a boot partition: %v", devPath, err), nil}
	}
	return bootRefusal(facts)
}

func bootRefusal(facts []blockFacts) *refusal {
	esps := proxmoxBootESPs()
	for _, f := range facts {
		if kind := bootKind(f.Type, f.PartType, f.FsType, f.UUID, esps); kind != "" {
			return &refusal{http.StatusConflict, response.ErrDiskBootPartition,
				fmt.Sprintf("%s is a boot partition (%s); changing it can leave the host unable to boot", f.Name, kind),
				map[string]string{"device": f.Name}}
		}
	}
	return nil
}

// DOS extended partition type codes: the container logical partitions live in.
var extendedPartTypes = map[string]bool{"0x5": true, "0xf": true, "0x85": true}

// extendedRefusal refuses deleting a DOS extended partition while logical
// partitions (5 and up) are inside it: deleting it deletes them. The kernel
// does not hold the container when it holds a logical partition in it, so
// deviceInUse passed sda2 while sda5 was the LVM physical volume under the
// root file system — Debian's guided LVM layout on a BIOS machine — and
// parted rewrote the table before failing to tell the kernel.
func extendedRefusal(disk string, facts []blockFacts) *refusal {
	if len(facts) == 0 || facts[0].PtType != "dos" || !extendedPartTypes[strings.ToLower(facts[0].PartType)] {
		return nil
	}
	nums, err := partitionNumbers(disk)
	if err != nil {
		return &refusal{http.StatusInternalServerError, response.ErrDiskError,
			fmt.Sprintf("cannot list the partitions of %s: %v", disk, err), nil}
	}
	var logical []int
	for n := range nums {
		if n >= 5 {
			logical = append(logical, n)
		}
	}
	if len(logical) == 0 {
		return nil
	}
	sort.Ints(logical)
	names := make([]string, len(logical))
	for i, n := range logical {
		names[i] = strings.TrimPrefix(partitionDevice(disk, n), "/dev/")
	}
	return &refusal{http.StatusConflict, response.ErrDeviceInUse,
		fmt.Sprintf("%s is an extended partition holding %s — delete those first", facts[0].Name, strings.Join(names, ", ")),
		map[string]string{"device": facts[0].Name, "logical": strings.Join(names, ", ")}}
}

// ---------- helpers ----------

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dirNames lists the entries of dir, or nothing when it cannot be read.
func dirNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}
