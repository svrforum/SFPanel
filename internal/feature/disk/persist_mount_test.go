package disk

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// nofail is the difference between a missing disk and an unbootable server:
// without it, systemd drops to emergency mode when an fstab entry cannot be
// mounted at boot — an external drive left unplugged, a disk pulled for
// maintenance.
func TestPersistedMountAlwaysCarriesNofail(t *testing.T) {
	cases := map[string]string{
		"no options given":     "",
		"explicit defaults":    "defaults",
		"operator options":     "rw,noatime",
		"nofail already there": "defaults,nofail",
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			out := opts
			if out == "" {
				out = "defaults"
			}
			if !hasMountOption(out, "nofail") {
				out += ",nofail"
			}
			if !hasMountOption(out, "nofail") {
				t.Fatalf("options %q, want nofail", out)
			}
			// And not twice.
			if strings.Count(out, "nofail") != 1 {
				t.Errorf("options %q repeat nofail", out)
			}
		})
	}
}

// A whole-option match, or "nofailover" would be read as nofail.
func TestHasMountOptionMatchesWholeOptions(t *testing.T) {
	if hasMountOption("rw,nofailover", "nofail") {
		t.Error("matched nofail inside nofailover")
	}
	if !hasMountOption("rw, nofail ,noatime", "nofail") {
		t.Error("missed nofail surrounded by spaces")
	}
	if hasMountOption("", "nofail") {
		t.Error("found nofail in an empty option list")
	}
}

// An entry the operator wrote by hand has no marker, and must survive both
// paths — losing somebody's fstab line because they clicked unmount would be
// worse than the mount not persisting at all.
func TestFstabRefusesToTouchHandWrittenEntries(t *testing.T) {
	const handWritten = "/dev/sdb1 /mnt/data ext4 defaults 0 2\n"
	lines := parseFstab(handWritten)

	if _, err := upsertShareEntry(lines, "/dev/sdb1", "/mnt/data", "ext4", "defaults,nofail"); err == nil {
		t.Error("overwrote an entry the panel did not create")
	}
	if _, err := removeShareEntry(lines, "/mnt/data"); err == nil {
		t.Error("removed an entry the panel did not create")
	}
}

// The panel's own entry round-trips: written with the marker, found again,
// and removed cleanly.
func TestPanelOwnEntryRoundTrips(t *testing.T) {
	lines, err := upsertShareEntry(parseFstab(""), "/dev/sdb1", "/mnt/data", "ext4", "defaults,nofail")
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderFstab(lines)
	if !strings.Contains(rendered, "/mnt/data") || !strings.Contains(rendered, "nofail") {
		t.Fatalf("entry not written:\n%s", rendered)
	}
	if !strings.Contains(rendered, netShareMarker) {
		t.Fatalf("entry written without the marker, so nothing can safely remove it later:\n%s", rendered)
	}

	back, err := removeShareEntry(parseFstab(rendered), "/mnt/data")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(renderFstab(back), "/mnt/data") {
		t.Errorf("entry survived removal:\n%s", renderFstab(back))
	}
}

// fstab lines were written by device name, which follows probe order: add or
// swap a disk and /dev/vdb is another disk, or none, at the next boot.
func TestPersistedMountNamesTheFilesystemByUUID(t *testing.T) {
	stubOwnMountNamespace(t, false)
	orig := fstabPath
	t.Cleanup(func() { fstabPath = orig })
	for _, tc := range []struct {
		name  string
		blkid exec.MockResult
		want  string
	}{
		{"filesystem with a UUID", exec.MockResult{Output: "3f1c9a2e-5b7d-4e8f-9a01-6c2d4e8f0a1b\n"}, "UUID=3f1c9a2e-5b7d-4e8f-9a01-6c2d4e8f0a1b"},
		{"no UUID found", exec.MockResult{Err: errors.New("exit status 2")}, "/dev/vdb"},
	} {
		dir := t.TempDir()
		fstabPath = filepath.Join(dir, "fstab")
		mp := filepath.Join(dir, "data")
		cmd := exec.NewMockCommander()
		cmd.Outputs["blkid"] = tc.blkid
		body := fmt.Sprintf(`{"device":"vdb","mount_point":%q,"fs_type":"ext4","persist":true}`, mp)
		req := httptest.NewRequest(http.MethodPost, "/filesystems/mount", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		if err := (&Handler{Cmd: cmd}).MountFilesystem(echo.New().NewContext(req, rec)); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: %v %d %s", tc.name, err, rec.Code, rec.Body.String())
		}
		fstab, _ := os.ReadFile(fstabPath)
		var source string
		for _, line := range strings.Split(string(fstab), "\n") {
			if f := strings.Fields(line); len(f) > 1 && f[1] == mp {
				source = f[0]
			}
		}
		if source != tc.want {
			t.Errorf("%s: fstab names the mount %q, want %q\n%s", tc.name, source, tc.want, fstab)
		}
	}
}
