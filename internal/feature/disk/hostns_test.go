package disk

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// The unit install.sh wrote put the panel in a mount namespace of its own
// (PrivateTmp), where a mount never reached the host: findmnt on the host came
// back empty and a container bound to the mountpoint wrote into the root disk.
// Mounts meant to last run in PID 1's namespace when the panel's is another.

func stubOwnMountNamespace(t *testing.T, own bool) {
	t.Helper()
	orig := ownMountNamespace
	t.Cleanup(func() { ownMountNamespace = orig })
	ownMountNamespace = func() bool { return own }
}

func TestInHostMountNS(t *testing.T) {
	stubOwnMountNamespace(t, true)
	name, args := inHostMountNS("mount", "-t", "ext4", "/dev/vdb", "/mnt/data")
	if got := name + " " + strings.Join(args, " "); got != "nsenter --mount=/proc/1/ns/mnt -- mount -t ext4 /dev/vdb /mnt/data" {
		t.Errorf("in a namespace of its own: %s", got)
	}

	stubOwnMountNamespace(t, false)
	name, args = inHostMountNS("umount", "/mnt/data")
	if got := name + " " + strings.Join(args, " "); got != "umount /mnt/data" {
		t.Errorf("sharing PID 1's namespace: %s", got)
	}
}

func TestMountsRunInTheHostNamespace(t *testing.T) {
	mp := filepath.Join(t.TempDir(), "data")
	for _, tc := range []struct {
		name string
		call func(*Handler, echo.Context) error
		body string
		want string
	}{
		{"disk mount", (*Handler).MountFilesystem,
			fmt.Sprintf(`{"device":"vdb","mount_point":%q,"fs_type":"ext4"}`, mp), "mount -t ext4 /dev/vdb " + mp},
		{"disk unmount", (*Handler).UnmountFilesystem,
			fmt.Sprintf(`{"mount_point":%q}`, mp), "umount " + mp},
		{"share mount", (*Handler).MountNetShare,
			fmt.Sprintf(`{"mount_point":%q}`, mp), "mount " + mp},
		{"share unmount", (*Handler).UnmountNetShare,
			fmt.Sprintf(`{"mount_point":%q}`, mp), "umount " + mp},
	} {
		stubOwnMountNamespace(t, true)
		cmd := exec.NewMockCommander()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		if err := tc.call(&Handler{Cmd: cmd}, echo.New().NewContext(req, rec)); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", tc.name, rec.Code, rec.Body.String())
		}
		want := "nsenter --mount=/proc/1/ns/mnt -- " + tc.want
		var ran []string
		for _, c := range cmd.Calls {
			if !strings.HasPrefix(c.Name, "exists:") {
				ran = append(ran, c.Name+" "+strings.Join(c.Args, " "))
			}
		}
		if len(ran) != 1 || ran[0] != want {
			t.Errorf("%s ran %q, want [%s]", tc.name, ran, want)
		}
	}
}
