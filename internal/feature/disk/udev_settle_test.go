package disk

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// settledAfter reports whether `udevadm settle` ran after the last call to
// name.
func settledAfter(calls []exec.MockCall, name string) bool {
	last := -1
	for i, call := range calls {
		if call.Name == name {
			last = i
		}
	}
	if last < 0 {
		return false
	}
	for _, call := range calls[last+1:] {
		if call.Name == "udevadm" && len(call.Args) > 0 && call.Args[0] == "settle" {
			return true
		}
	}
	return false
}

// lsblk reads PTTYPE and FSTYPE from udev's database, which catches up with
// the disk a moment after parted or mkfs returns. On a loop device, lsblk
// right after `parted mklabel gpt` showed no partition table 3 times in 3,
// and right after mkfs.ext4 the old filesystem type 10 in 10; after
// `udevadm settle` both were right every time. The page reloads the list as
// soon as the request returns, so it showed the disk unchanged (#113).
func TestDiskChangesWaitForUdevBeforeAnswering(t *testing.T) {
	disks := t.TempDir()
	if err := os.Mkdir(filepath.Join(disks, "vdb"), 0o755); err != nil {
		t.Fatal(err)
	}
	origDir, origMount := sysBlockDir, findDeviceMountpoint
	t.Cleanup(func() { sysBlockDir, findDeviceMountpoint = origDir, origMount })
	sysBlockDir = disks
	findDeviceMountpoint = func(string) (string, error) { return "", nil }
	stubDeviceInUse(t, func(string) (bool, error) { return false, nil })

	for _, tc := range []struct {
		name    string
		changer string
		run     func(h *Handler, c echo.Context) error
		method  string
		target  string
		body    string
		params  []string
	}{
		{"partition table", "parted", (*Handler).CreatePartitionTable, http.MethodPost, "/disks/vdb/partition-table", "", []string{"device", "vdb"}},
		{"new partition", "parted", (*Handler).CreatePartition, http.MethodPost, "/disks/vdb/partitions", `{"start":"1MiB","end":"100%","fs_type":"ext4"}`, []string{"device", "vdb"}},
		{"deleted partition", "parted", (*Handler).DeletePartition, http.MethodDelete, "/disks/vdb/partitions/1", "", []string{"device", "vdb", "number", "1"}},
		// /dev/null passes the handler's block-or-character-device stat.
		{"format", "mkfs.ext4", (*Handler).FormatPartition, http.MethodPost, "/filesystems/format", `{"device":"null","fs_type":"ext4"}`, nil},
	} {
		cmd := &exec.MockCommander{Outputs: map[string]exec.MockResult{
			"exists:parted": {}, "exists:blkid": {}, "exists:mkfs.ext4": {}, "exists:udevadm": {},
			"blkid": {Err: exitStatus(t, 2)}, "lsblk": {Output: lsblkDataPartition},
		}}
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		if tc.params != nil {
			var names, values []string
			for i := 0; i < len(tc.params); i += 2 {
				names, values = append(names, tc.params[i]), append(values, tc.params[i+1])
			}
			c.SetParamNames(names...)
			c.SetParamValues(values...)
		}
		if err := tc.run(&Handler{Cmd: cmd}, c); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
		if !settledAfter(cmd.Calls, tc.changer) {
			t.Errorf("%s: answered without waiting for udev after %s; calls %v", tc.name, tc.changer, cmd.Calls)
		}
	}
}
