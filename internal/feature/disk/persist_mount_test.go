package disk

import (
	"strings"
	"testing"
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
