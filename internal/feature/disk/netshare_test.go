package disk

import (
	"strings"
	"testing"
)

// The options field is the injection surface: it is the one part of an fstab
// line a caller controls verbatim. Whitespace splits the line into extra
// fields, a newline forges a whole new entry, and a credential option leaks a
// password into a world-readable file.
func TestSanitizeUserOptions_RejectsInjection(t *testing.T) {
	rejected := []struct {
		opts   string
		reason string
	}{
		{"ro nosuid", "a space ends the options field and shifts dump/pass"},
		{"ro\tnosuid", "a tab splits the field just like a space"},
		{"ro\n//evil/share /mnt/x cifs rw 0 0", "a newline forges an entire fstab entry"},
		{"ro,#comment", "'#' starts an fstab comment"},
		{"password=hunter2", "a password would land in world-readable fstab"},
		{"credentials=/etc/shadow", "the panel owns credentials= and points it at its own 0600 file"},
		{"PASSWORD=hunter2", "the credential check must be case-insensitive"},
		{"username=admin", "credentials belong in the 0600 file, not fstab"},
		{"nofail", "boot-safety options are the panel's to set"},
		{"ro,$(reboot)", "shell metacharacters have no business in an option"},
		{"ro,`id`", "backticks likewise"},
		{"ro;nosuid", "';' is not a valid option separator"},
	}
	for _, tc := range rejected {
		if _, err := sanitizeUserOptions(tc.opts); err == nil {
			t.Errorf("sanitizeUserOptions(%q) accepted it; %s", tc.opts, tc.reason)
		}
	}

	accepted := []string{
		"",
		"ro",
		"ro,noexec,nosuid",
		"uid=1000,gid=1000",
		"vers=3.0",
		"file_mode=0644,dir_mode=0755",
		"rsize=131072,wsize=131072",
		"sec=krb5",
		"iocharset=utf8",
	}
	for _, opts := range accepted {
		if _, err := sanitizeUserOptions(opts); err != nil {
			t.Errorf("sanitizeUserOptions(%q) rejected a legitimate option set: %v", opts, err)
		}
	}
}

func TestValidateServer(t *testing.T) {
	ok := []string{"192.168.1.50", "nas", "nas.local", "nas-01.home.arpa", "fd00::1", "10.0.0.1"}
	for _, s := range ok {
		if err := validateServer(s); err != nil {
			t.Errorf("validateServer(%q) rejected a valid server: %v", s, err)
		}
	}
	bad := []string{"", "nas;reboot", "nas/../etc", "-nas", "nas..local", "192.168.1.50 extra", "nas\nevil"}
	for _, s := range bad {
		if err := validateServer(s); err == nil {
			t.Errorf("validateServer(%q) accepted an invalid server", s)
		}
	}
}

func TestValidateShareName(t *testing.T) {
	// Consumer NAS boxes ship shares with spaces and parentheses.
	for _, s := range []string{"photos", "Public Share", "media_2024", "Backup (old)", "docs$"} {
		if err := validateShareName(ShareSMB, s); err != nil {
			t.Errorf("validateShareName(SMB, %q) rejected a valid share: %v", s, err)
		}
	}
	for _, s := range []string{"", "photos/../etc", "photos\nx", "photos;id", `photos\x`} {
		if err := validateShareName(ShareSMB, s); err == nil {
			t.Errorf("validateShareName(SMB, %q) accepted an invalid share", s)
		}
	}

	for _, s := range []string{"/export/media", "/volume1/photos", "/srv/nfs"} {
		if err := validateShareName(ShareNFS, s); err != nil {
			t.Errorf("validateShareName(NFS, %q) rejected a valid export: %v", s, err)
		}
	}
	for _, s := range []string{"", "export/media", "/export/../etc", "/export nfs", "/export;id"} {
		if err := validateShareName(ShareNFS, s); err == nil {
			t.Errorf("validateShareName(NFS, %q) accepted an invalid export", s)
		}
	}
}

func TestSourceRoundTrip(t *testing.T) {
	cases := []struct {
		t             ShareType
		server, share string
		wantSource    string
	}{
		{ShareSMB, "192.168.1.50", "photos", "//192.168.1.50/photos"},
		{ShareSMB, "nas.local", "Public Share", "//nas.local/Public Share"},
		{ShareNFS, "192.168.1.50", "/export/media", "192.168.1.50:/export/media"},
	}
	for _, tc := range cases {
		got := buildSource(tc.t, tc.server, tc.share)
		if got != tc.wantSource {
			t.Errorf("buildSource(%s,%q,%q) = %q, want %q", tc.t, tc.server, tc.share, got, tc.wantSource)
		}
		server, share, ok := parseSource(tc.t, got)
		if !ok || server != tc.server || share != tc.share {
			t.Errorf("parseSource(%s,%q) = (%q,%q,%v), want (%q,%q,true)",
				tc.t, got, server, share, ok, tc.server, tc.share)
		}
	}
}

// A share named "Public Share" must survive the trip through fstab, where
// whitespace is the field separator.
func TestFstabEscapeRoundTrip(t *testing.T) {
	for _, s := range []string{"/mnt/nas", "//nas/Public Share", `weird\path`, "with#hash", "tab\there"} {
		esc := fstabEscape(s)
		if strings.ContainsAny(esc, " \t\n#") {
			t.Errorf("fstabEscape(%q) = %q still contains a field-breaking character", s, esc)
		}
		if got := fstabUnescape(esc); got != s {
			t.Errorf("round trip of %q gave %q", s, got)
		}
	}
}

func TestUpsertAndRemoveShareEntry(t *testing.T) {
	const original = `# /etc/fstab
UUID=1234 / ext4 defaults 0 1
/dev/sdb1 /data xfs defaults 0 2
`
	lines := parseFstab(original)

	lines, err := upsertShareEntry(lines, "//nas/photos", "/mnt/nas/photos", "cifs",
		"credentials=/etc/sfpanel/smb-credentials/mnt_nas_photos.cred,"+baseShareOptions)
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	out := renderFstab(lines)

	if !strings.Contains(out, netShareMarker) {
		t.Error("managed entry was written without its marker, so it can never be identified again")
	}
	if !strings.Contains(out, "nofail") || !strings.Contains(out, "_netdev") {
		t.Error("boot-safety options missing: an unreachable NAS could then block boot")
	}
	if !strings.Contains(out, "UUID=1234 / ext4 defaults 0 1") {
		t.Error("pre-existing entries must be preserved byte-for-byte")
	}
	if err := validateFstabDocument(out); err != nil {
		t.Errorf("rendered fstab is malformed: %v", err)
	}

	// Re-parse and confirm the entry is recognised as ours.
	reparsed := parseFstab(out)
	i := findEntryIndex(reparsed, "/mnt/nas/photos")
	if i < 0 {
		t.Fatal("entry not found after round trip")
	}
	if !reparsed[i].managed {
		t.Error("entry did not survive the round trip as managed")
	}

	// Upsert again — must replace, not duplicate.
	updated, err := upsertShareEntry(reparsed, "//nas/photos", "/mnt/nas/photos", "cifs", "ro,"+baseShareOptions)
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}
	if n := strings.Count(renderFstab(updated), "/mnt/nas/photos"); n != 1 {
		t.Errorf("re-adding the same mount point produced %d entries, want 1", n)
	}

	// Removal takes the marker with it.
	removed, err := removeShareEntry(updated, "/mnt/nas/photos")
	if err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	final := renderFstab(removed)
	if strings.Contains(final, "/mnt/nas/photos") {
		t.Error("entry survived removal")
	}
	if strings.Contains(final, netShareMarker) {
		t.Error("marker was orphaned by removal")
	}
	if !strings.Contains(final, "/dev/sdb1 /data xfs defaults 0 2") {
		t.Error("removal damaged an unrelated entry")
	}
}

// Hand-written entries belong to the operator. The panel lists them but must
// never rewrite or delete them.
func TestUnmanagedEntriesAreProtected(t *testing.T) {
	lines := parseFstab("nas:/export /mnt/media nfs defaults 0 0\n")

	if _, err := upsertShareEntry(lines, "//evil/share", "/mnt/media", "cifs", baseShareOptions); err == nil {
		t.Error("upsert overwrote an fstab entry the panel did not create")
	}
	if _, err := removeShareEntry(lines, "/mnt/media"); err == nil {
		t.Error("remove deleted an fstab entry the panel did not create")
	}
}

func TestRemoveShareEntryIsIdempotent(t *testing.T) {
	lines := parseFstab("UUID=1234 / ext4 defaults 0 1\n")
	out, err := removeShareEntry(lines, "/mnt/absent")
	if err != nil {
		t.Fatalf("removing an absent entry should be a no-op, got: %v", err)
	}
	if !strings.Contains(renderFstab(out), "UUID=1234") {
		t.Error("removing an absent entry damaged the file")
	}
}

func TestValidateFstabDocument(t *testing.T) {
	if err := validateFstabDocument("# comment\n\nUUID=1 / ext4 defaults 0 1\n"); err != nil {
		t.Errorf("valid document rejected: %v", err)
	}
	if err := validateFstabDocument("UUID=1 / ext4\n"); err == nil {
		t.Error("an entry with too few fields was accepted; that can drop the host into an emergency shell")
	}
}

func TestCredentialsPathIsContained(t *testing.T) {
	for _, mp := range []string{"/mnt/nas/photos", "/mnt/../etc/shadow", "/", "/mnt/a b"} {
		got := credentialsPathFor(mp)
		if !strings.HasPrefix(got, smbCredDir+"/") {
			t.Errorf("credentialsPathFor(%q) = %q escaped %s", mp, got, smbCredDir)
		}
		if strings.Contains(strings.TrimPrefix(got, smbCredDir+"/"), "/") {
			t.Errorf("credentialsPathFor(%q) = %q nested below the credentials dir", mp, got)
		}
	}
}

func TestValidateShareType(t *testing.T) {
	for in, want := range map[string]ShareType{
		"cifs": ShareSMB, "smb": ShareSMB, "samba": ShareSMB,
		"nfs": ShareNFS, "nfs4": ShareNFS,
	} {
		got, err := validateShareType(in)
		if err != nil || got != want {
			t.Errorf("validateShareType(%q) = (%q,%v), want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ext4", "sshfs", "cifs;id"} {
		if _, err := validateShareType(in); err == nil {
			t.Errorf("validateShareType(%q) accepted an unsupported type", in)
		}
	}
}

// Output parsers. CLAUDE.md calls these out as needing tests: they turn
// loosely-specified command output into the list an operator picks from, and
// a silent mis-parse shows an empty or wrong share list with no error.

func TestParseShowmount(t *testing.T) {
	// Real `showmount -e --no-headers` output: export path, then the client
	// spec, separated by whitespace. Some builds print the header anyway.
	const out = `Export list for 192.168.1.50:
/volume1/photos 192.168.1.0/24
/volume1/media  *
/export/backup  192.168.1.10,192.168.1.11

`
	got := parseShowmount(out)
	want := []string{"/volume1/photos", "/volume1/media", "/export/backup"}
	if len(got) != len(want) {
		t.Fatalf("parseShowmount returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("export %d = %q, want %q", i, got[i], want[i])
		}
	}
	if n := len(parseShowmount("")); n != 0 {
		t.Errorf("empty output produced %d exports", n)
	}
}

func TestParseSmbclientList(t *testing.T) {
	// `smbclient -L host -g` emits "Type|Name|Comment" per line. Only Disk
	// entries are mountable; printers and IPC are not, and the admin shares
	// ending in '$' are not what anyone means by connecting a NAS.
	const out = `Disk|photos|Family photos
Disk|Public Share|
Disk|IPC$|IPC Service
Disk|ADMIN$|Remote Admin
Disk|C$|Default share
Printer|HP-LaserJet|Office printer
IPC|IPC$|IPC Service
Server|NAS01|
Workgroup|WORKGROUP|NAS01
`
	got := parseSmbclientList(out)
	want := []string{"photos", "Public Share"}
	if len(got) != len(want) {
		t.Fatalf("parseSmbclientList returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("share %d = %q, want %q", i, got[i], want[i])
		}
	}
	if n := len(parseSmbclientList("")); n != 0 {
		t.Errorf("empty output produced %d shares", n)
	}
	// Malformed lines must be skipped, not panic or leak through.
	if n := len(parseSmbclientList("garbage\n|\nDisk|\n")); n != 0 {
		t.Errorf("malformed output produced %d shares", n)
	}
}

// findmnt reports SIZE/USED as a JSON number on util-linux >= 2.38 and as a
// quoted string before that. Getting this wrong silently zeroes the usage
// column instead of failing.
func TestFlexibleInt(t *testing.T) {
	cases := map[string]int64{
		`123456`:   123456,
		`"123456"`: 123456,
		`0`:        0,
		`""`:       0,
		`null`:     0,
		`"abc"`:    0,
		``:         0,
	}
	for raw, want := range cases {
		if got := flexibleInt([]byte(raw)); got != want {
			t.Errorf("flexibleInt(%s) = %d, want %d", raw, got, want)
		}
	}
}

// df lists mounts in kernel order, so a freshly attached network drive lands
// at the bottom of a list dominated by container layers — the one place an
// operator who just connected it will not look.
func TestSortFilesystems(t *testing.T) {
	fs := []Filesystem{
		{Source: "overlay", FsType: "overlay", MountPoint: "/var/lib/docker/rootfs/overlayfs/aaa"},
		{Source: "/dev/sda2", FsType: "ext4", MountPoint: "/"},
		{Source: "tmpfs", FsType: "tmpfs", MountPoint: "/run"},
		{Source: "//nas/photos", FsType: "cifs", MountPoint: "/mnt/photos"},
		{Source: "/dev/sda1", FsType: "vfat", MountPoint: "/boot/efi"},
		{Source: "192.168.1.50:/volume1/media", FsType: "nfs4", MountPoint: "/mnt/media"},
	}
	sortFilesystems(fs)

	got := make([]string, len(fs))
	for i, f := range fs {
		got[i] = f.MountPoint
	}
	want := []string{
		"/mnt/media",  // network, by mount point
		"/mnt/photos", // network
		"/",           // block device, by mount point
		"/boot/efi",   // block device
		"/run",        // pseudo
		"/var/lib/docker/rootfs/overlayfs/aaa",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Re-sorting an already-sorted list must not move anything: the page polls,
// and rows that reshuffle under the cursor are worse than a bad order.
func TestSortFilesystemsIsStable(t *testing.T) {
	fs := []Filesystem{
		{Source: "//nas/a", FsType: "cifs", MountPoint: "/mnt/a"},
		{Source: "/dev/sda2", FsType: "ext4", MountPoint: "/"},
	}
	sortFilesystems(fs)
	first := append([]Filesystem{}, fs...)
	sortFilesystems(fs)
	for i := range fs {
		if fs[i].MountPoint != first[i].MountPoint {
			t.Fatalf("second sort reordered: %v then %v", first, fs)
		}
	}
}
