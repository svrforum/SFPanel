package disk

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// mainCleanup gives TestMain the Helper and Cleanup that platform.SetForTest
// wants from a test.
type mainCleanup struct{ fns []func() }

func (m *mainCleanup) Helper()          {}
func (m *mainCleanup) Cleanup(f func()) { m.fns = append(m.fns, f) }

// TestMain keeps every test off the host. The device guards read /proc,
// sysfs, /dev and Proxmox's files; here they all point into an empty scratch
// tree, the platform is a plain Ubuntu, and a test fills in what it needs.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "sfpanel-disk-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	procDir = filepath.Join(root, "proc")
	sysClassBlockDir = filepath.Join(root, "sys", "class", "block")
	sysBlockDir = filepath.Join(root, "sys", "block")
	pveStorageCfg = filepath.Join(root, "etc", "pve", "storage.cfg")
	proxmoxBootUUIDs = filepath.Join(root, "etc", "kernel", "proxmox-boot-uuids")
	volumeLinkDirs = []string{filepath.Join(root, "dev", "zvol")}
	blockDeviceNumber = func(string) (string, bool) { return "", false }

	var mc mainCleanup
	platform.SetForTest(&mc, platform.Info{OSID: "ubuntu", OSVersionID: "24.04", NetworkStack: platform.StackNetplan})
	code := m.Run()
	for i := len(mc.fns) - 1; i >= 0; i-- {
		mc.fns[i]()
	}
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// onPVE makes the test run on a Proxmox VE 9 host.
func onPVE(t *testing.T) {
	t.Helper()
	platform.SetForTest(t, platform.Info{OSID: "debian", OSVersionID: "13", PVE: true, PVEVersion: "9.2.3", NetworkStack: platform.StackIfupdown2})
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withFile points a path variable at a scratch file holding content.
func withFile(t *testing.T, v *string, content string) {
	t.Helper()
	orig := *v
	*v = filepath.Join(t.TempDir(), filepath.Base(orig))
	mustWrite(t, *v, content)
	t.Cleanup(func() { *v = orig })
}

// Proxmox's default LVM-thin storage, in the layout PVE writes.
const storageCfg = `dir: local
	path /var/lib/vz
	content iso,vztmpl,backup

lvmthin: local-lvm
	thinpool data
	vgname pve
	content rootdir,images

lvm: san-lvm
	vgname sanvg
	content images
	shared 1
`

// withZvolLinks lays out /dev/zvol: each volume ("rpool/data/vm-100-disk-0")
// a link to its device ("zd16"), as udev makes them.
func withZvolLinks(t *testing.T, links map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for vol, dev := range links {
		path := filepath.Join(dir, vol)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		rel := strings.Repeat("../", strings.Count(vol, "/")+1)
		if err := os.Symlink(rel+dev, path); err != nil {
			t.Fatal(err)
		}
	}
	orig := volumeLinkDirs
	volumeLinkDirs = []string{dir}
	t.Cleanup(func() { volumeLinkDirs = orig })
}

// fakeSysfs lays out /sys/class/block the kernel's way: each device a link
// into a devices tree in which a partition is a directory inside its disk's.
type fakeSysfs struct {
	t    *testing.T
	root string
}

func newFakeSysfs(t *testing.T) *fakeSysfs {
	t.Helper()
	root := t.TempDir()
	orig := sysClassBlockDir
	sysClassBlockDir = filepath.Join(root, "class", "block")
	t.Cleanup(func() { sysClassBlockDir = orig })
	if err := os.MkdirAll(sysClassBlockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeSysfs{t: t, root: root}
}

func (s *fakeSysfs) add(rel, name, dev string, partition bool) string {
	s.t.Helper()
	dir := filepath.Join(s.root, "devices", rel)
	if err := os.MkdirAll(filepath.Join(dir, "holders"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	mustWrite(s.t, filepath.Join(dir, "dev"), dev+"\n")
	if partition {
		mustWrite(s.t, filepath.Join(dir, "partition"), "1\n")
	}
	if err := os.Symlink(dir, filepath.Join(sysClassBlockDir, name)); err != nil {
		s.t.Fatal(err)
	}
	return dir
}

func (s *fakeSysfs) disk(name, dev string) { s.add(name, name, dev, false) }

func (s *fakeSysfs) part(disk, name, dev string) { s.add(filepath.Join(disk, name), name, dev, true) }

// lv adds device-mapper device name for LVM volume dmName, stacked on slaves.
func (s *fakeSysfs) lv(name, dev, dmName string, slaves ...string) {
	s.t.Helper()
	dir := s.add(filepath.Join("virtual", name), name, dev, false)
	mustWrite(s.t, filepath.Join(dir, "dm", "name"), dmName+"\n")
	mustWrite(s.t, filepath.Join(dir, "dm", "uuid"), "LVM-0123456789abcdef\n")
	for _, sl := range slaves {
		mustWrite(s.t, filepath.Join(dir, "slaves", sl), "")
		mustWrite(s.t, filepath.Join(sysClassBlockDir, sl, "holders", name), "")
	}
}

// fakeProc stands in for /proc: processes with descriptors open on device
// paths, and the device number stat would find behind each path.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T, devs map[string]string) *fakeProc {
	t.Helper()
	root := t.TempDir()
	origDir, origNum := procDir, blockDeviceNumber
	procDir = root
	blockDeviceNumber = func(path string) (string, bool) {
		target, err := os.Readlink(path)
		if err != nil {
			return "", false
		}
		num, ok := devs[target]
		return num, ok
	}
	t.Cleanup(func() { procDir, blockDeviceNumber = origDir, origNum })
	return &fakeProc{t: t, root: root}
}

// process adds pid with comm and cgroup, holding each device in fds open:
// "/dev/sdb:rw" for writing, "/dev/sdb:ro" for reading.
func (p *fakeProc) process(pid int, comm, cgroup string, fds ...string) {
	p.t.Helper()
	dir := filepath.Join(p.root, fmt.Sprint(pid))
	mustWrite(p.t, filepath.Join(dir, "comm"), comm+"\n")
	mustWrite(p.t, filepath.Join(dir, "cgroup"), cgroup+"\n")
	if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
		p.t.Fatal(err)
	}
	// Descriptors that are no device: they must be passed over.
	if err := os.Symlink("socket:[4242]", filepath.Join(dir, "fd", "0")); err != nil {
		p.t.Fatal(err)
	}
	for i, fd := range fds {
		target, mode, _ := strings.Cut(fd, ":")
		n := fmt.Sprint(i + 3)
		if err := os.Symlink(target, filepath.Join(dir, "fd", n)); err != nil {
			p.t.Fatal(err)
		}
		flags := "0100000" // O_RDONLY|O_LARGEFILE
		if mode == "rw" {
			flags = "0140002" // O_RDWR|O_DIRECT|O_LARGEFILE, as QEMU opens a disk
		}
		mustWrite(p.t, filepath.Join(dir, "fdinfo", n), "pos:\t0\nflags:\t"+flags+"\nmnt_id:\t25\n")
	}
}

// fakeBlockDevices makes /dev/<name> look like a block device to the
// handlers' existence checks; a test has none to stat.
func fakeBlockDevices(t *testing.T, names ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, n := range names {
		set["/dev/"+n] = true
	}
	orig := statDev
	statDev = func(path string) (os.FileInfo, error) {
		if set[path] {
			return fakeDevInfo(filepath.Base(path)), nil
		}
		return orig(path)
	}
	t.Cleanup(func() { statDev = orig })
}

type fakeDevInfo string

func (f fakeDevInfo) Name() string       { return string(f) }
func (f fakeDevInfo) Size() int64        { return 0 }
func (f fakeDevInfo) Mode() os.FileMode  { return os.ModeDevice | 0o660 }
func (f fakeDevInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDevInfo) IsDir() bool        { return false }
func (f fakeDevInfo) Sys() any           { return nil }

// unmountedAndFree stubs the mount lookup and the kernel's exclusive-open
// check: the device is neither mounted nor held, so a request reaches the
// guards under test.
func unmountedAndFree(t *testing.T) {
	t.Helper()
	orig := findDeviceMountpoint
	t.Cleanup(func() { findDeviceMountpoint = orig })
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	stubDeviceInUse(t, func(string) (bool, error) { return false, nil })
}

// call runs a handler on a JSON body; params are name, value pairs.
func call(t *testing.T, run func(*Handler, echo.Context) error, cmd exec.Commander, method, body string, params ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	var names, values []string
	for i := 0; i+1 < len(params); i += 2 {
		names, values = append(names, params[i]), append(values, params[i+1])
	}
	c.SetParamNames(names...)
	c.SetParamValues(values...)
	if err := run(&Handler{Cmd: cmd}, c); err != nil {
		t.Fatal(err)
	}
	return rec
}

// assertRefused checks a refusal for its reason — the status, the code and
// the details the page words it with — not only that something failed.
func assertRefused(t *testing.T, rec *httptest.ResponseRecorder, status int, code string, details map[string]string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if rec.Code != status || body.Error.Code != code {
		t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body.String(), status, code)
	}
	for k, v := range details {
		if body.Error.Details[k] != v {
			t.Errorf("details[%q] = %q, want %q (%s)", k, body.Error.Details[k], v, rec.Body.String())
		}
	}
}

// ran reports whether cmd ran name.
func ran(cmd *exec.MockCommander, name string) bool {
	return findCall(cmd.Calls, name) != nil
}

// lsblk answers for the boot check. A Linux data partition, nothing special.
const lsblkDataPartition = `{"blockdevices":[{"name":"vdb1","type":"part","pttype":"gpt","parttype":"0fc63daf-8483-4772-8e79-3d69d8477de4","fstype":"ext4","uuid":"0f3c2a5e-1d4b-4e6f-9a8b-7c6d5e4f3a2b"}]}`

// The disk layout the Proxmox VE installer writes for ZFS: BIOS boot, an ESP
// proxmox-boot-tool keeps unmounted, and the pool member.
const lsblkPVEBootDisk = `{"blockdevices":[{"name":"nvme0n1","type":"disk","pttype":"gpt","parttype":null,"fstype":null,"uuid":null,"children":[
  {"name":"nvme0n1p1","type":"part","pttype":"gpt","parttype":"21686148-6449-6e6f-744e-656564454649","fstype":null,"uuid":null},
  {"name":"nvme0n1p2","type":"part","pttype":"gpt","parttype":"c12a7328-f81f-11d2-ba4b-00a0c93ec93b","fstype":"vfat","uuid":"1A2B-3C4D"},
  {"name":"nvme0n1p3","type":"part","pttype":"gpt","parttype":"6a898cc3-1dd2-11b2-99a6-080020736631","fstype":"zfs_member","uuid":"1234567890123456789"}
]}]}`

// lsblkOne is lsblk's answer for a single partition.
func lsblkOne(name, pttype, parttype, fstype, uuid string) string {
	return fmt.Sprintf(`{"blockdevices":[{"name":%q,"type":"part","pttype":%q,"parttype":%q,"fstype":%q,"uuid":%q}]}`,
		name, pttype, parttype, fstype, uuid)
}

func mock(outputs map[string]exec.MockResult, tools ...string) *exec.MockCommander {
	cmd := toolsAvailable(tools...)
	for k, v := range outputs {
		cmd.Outputs[k] = v
	}
	return cmd
}
