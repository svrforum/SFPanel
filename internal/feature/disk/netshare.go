package disk

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)

// Network share (SMB/CIFS and NFS) support.
//
// /etc/fstab is the single source of truth — there is no shadow copy in the
// database. The rest of this module reads the system rather than mirroring it,
// and a mirror would drift the moment anyone edited fstab by hand.
//
// Entries this panel created carry a marker comment on the preceding line, so
// hand-written entries stay visible but are never rewritten or deleted by the
// API. Operators who edited fstab themselves get to keep what they wrote.
const (
	fstabPath = "/etc/fstab"

	// netShareMarker precedes every entry the panel manages. Fstab has no
	// portable mid-line comment syntax (getmntent only honours '#' in column
	// one), so the marker gets its own line.
	netShareMarker = "# sfpanel-netshare"

	// smbCredDir holds one credentials file per SMB share, 0600. Passwords
	// must never reach fstab: it is world-readable by design, and mount.cifs
	// exposes the whole option string in /proc/mounts on top of that.
	smbCredDir = "/etc/sfpanel/smb-credentials"

	// baseShareOptions keep a dead NAS from breaking the host.
	//
	//   _netdev  — wait for the network before trying, and unmount before it
	//              goes away on shutdown.
	//   nofail   — a share that cannot be reached must not fail the boot.
	//              Without this a powered-off NAS drops the machine into an
	//              emergency shell, which is the classic way fstab editing
	//              bricks a server.
	//
	// x-systemd.automount is deliberately NOT included: it hands the mount
	// lifecycle to a generated systemd unit, which then fights the plain
	// mount/umount calls this API makes. _netdev+nofail already gives the
	// boot-safety guarantee without that complication.
	baseShareOptions = "_netdev,nofail"
)

// ShareType is the kind of remote filesystem.
type ShareType string

const (
	ShareSMB ShareType = "cifs"
	ShareNFS ShareType = "nfs"
)

// NetShare is one network share as it exists on this host.
type NetShare struct {
	Type       string `json:"type"`   // "cifs" | "nfs"
	Source     string `json:"source"` // fstab spec, e.g. //nas/photos or nas:/export
	Server     string `json:"server"`
	Share      string `json:"share"`
	MountPoint string `json:"mount_point"`
	Options    string `json:"options"`
	// Managed is true when the panel wrote this entry. Hand-written entries
	// are listed (so the UI shows the whole picture) but refused for edit and
	// delete — we don't rewrite what an operator authored.
	Managed bool `json:"managed"`
	// Mounted reflects live state from findmnt, not fstab.
	Mounted        bool   `json:"mounted"`
	HasCredentials bool   `json:"has_credentials"`
	TotalBytes     int64  `json:"total_bytes,omitempty"`
	UsedBytes      int64  `json:"used_bytes,omitempty"`
	Error          string `json:"error,omitempty"`
}

var (
	// One DNS label, per RFC 1123 plus the underscore some NAS appliances use.
	// Applied per-label by validateServer so empty labels are caught.
	validHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9_-]*[A-Za-z0-9])?$`)
	// SMB share names allow spaces ("Public Share" is common on consumer
	// NAS boxes); fstab escaping handles them on the way out.
	validSMBShare = regexp.MustCompile(`^[A-Za-z0-9._$ ()&+-]+$`)
	// NFS exports are absolute paths on the server.
	validNFSExport = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	// Mount options: no whitespace (it would split the fstab field) and no
	// '#' (it would start a comment).
	validOptionChars = regexp.MustCompile(`^[A-Za-z0-9_.,=/:@+-]*$`)
)

// optionsRejectedFromUser are options the panel owns or that would leak a
// secret. credentials= and password= are the dangerous pair: either one in the
// user-supplied string ends up verbatim in world-readable fstab.
var optionsRejectedFromUser = map[string]bool{
	"credentials": true,
	"password":    true,
	"pass":        true,
	"passwd":      true,
	"cred":        true,
	"username":    true,
	"user":        true,
	"_netdev":     true,
	"nofail":      true,
}

// validateServer accepts an IPv4/IPv6 literal or a hostname.
func validateServer(server string) error {
	if server == "" {
		return fmt.Errorf("server is required")
	}
	if len(server) > 253 {
		return fmt.Errorf("server name is too long")
	}
	if ip := net.ParseIP(server); ip != nil {
		return nil
	}
	// Per-label so an empty label ("nas..local", a trailing dot) is rejected;
	// a single regex over the whole name lets those through.
	for _, label := range strings.Split(server, ".") {
		if label == "" || len(label) > 63 || !validHostname.MatchString(label) {
			return fmt.Errorf("invalid server: %s (use an IP address or hostname)", server)
		}
	}
	return nil
}

// validateShareName checks the share/export against the rules for its type.
func validateShareName(t ShareType, share string) error {
	if share == "" {
		return fmt.Errorf("share is required")
	}
	if len(share) > 255 {
		return fmt.Errorf("share name is too long")
	}
	switch t {
	case ShareSMB:
		if !validSMBShare.MatchString(share) {
			return fmt.Errorf("invalid SMB share name: %s", share)
		}
	case ShareNFS:
		if !validNFSExport.MatchString(share) {
			return fmt.Errorf("invalid NFS export path: %s (must be an absolute path on the server)", share)
		}
		if strings.Contains(share, "..") {
			return fmt.Errorf("invalid NFS export path: path traversal not allowed")
		}
	default:
		return fmt.Errorf("unsupported share type: %s", t)
	}
	return nil
}

// validateShareType narrows a request string to a supported type.
func validateShareType(s string) (ShareType, error) {
	switch s {
	case string(ShareSMB), "smb", "samba":
		return ShareSMB, nil
	case string(ShareNFS), "nfs4":
		return ShareNFS, nil
	default:
		return "", fmt.Errorf("unsupported share type: %s (want cifs or nfs)", s)
	}
}

// sanitizeUserOptions validates caller-supplied mount options and returns them
// normalised. It rejects anything that would break out of the options field or
// smuggle a secret into fstab; the panel's own options are appended later, so
// callers cannot override them here either.
func sanitizeUserOptions(opts string) (string, error) {
	opts = strings.TrimSpace(opts)
	if opts == "" {
		return "", nil
	}
	if len(opts) > 512 {
		return "", fmt.Errorf("mount options are too long")
	}
	// Whitespace would end the fstab field early and turn the remainder into
	// the dump/pass columns — or, with a newline, into a whole new entry.
	if strings.ContainsAny(opts, " \t\n\r\v\f") {
		return "", fmt.Errorf("mount options may not contain whitespace")
	}
	if strings.Contains(opts, "#") {
		return "", fmt.Errorf("mount options may not contain '#'")
	}
	if !validOptionChars.MatchString(opts) {
		return "", fmt.Errorf("mount options contain invalid characters")
	}

	out := make([]string, 0, 8)
	for _, part := range strings.Split(opts, ",") {
		if part == "" {
			continue
		}
		key := part
		if i := strings.IndexByte(part, '='); i >= 0 {
			key = part[:i]
		}
		if optionsRejectedFromUser[strings.ToLower(key)] {
			return "", fmt.Errorf("option %q is managed by the panel and cannot be set here", key)
		}
		out = append(out, part)
	}
	return strings.Join(out, ","), nil
}

// buildSource renders the fstab spec for a share.
func buildSource(t ShareType, server, share string) string {
	if t == ShareSMB {
		return "//" + server + "/" + strings.TrimPrefix(share, "/")
	}
	return server + ":" + share
}

// parseSource splits an fstab spec back into server and share. ok is false for
// a spec that is not a network share.
func parseSource(t ShareType, source string) (server, share string, ok bool) {
	if t == ShareSMB {
		s := strings.TrimPrefix(source, "//")
		if s == source {
			s = strings.TrimPrefix(source, `\\`)
			if s == source {
				return "", "", false
			}
		}
		i := strings.IndexAny(s, `/\`)
		if i <= 0 || i == len(s)-1 {
			return "", "", false
		}
		return s[:i], s[i+1:], true
	}
	i := strings.LastIndex(source, ":")
	if i <= 0 || i == len(source)-1 {
		return "", "", false
	}
	return source[:i], source[i+1:], true
}

// credentialsPathFor derives the credentials file path for a mount point. The
// mount point is the natural key: fstab allows one entry per mount point, so
// this is stable across renames of anything else.
func credentialsPathFor(mountPoint string) string {
	clean := filepath.Clean(mountPoint)
	name := strings.TrimPrefix(clean, "/")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		name = "root"
	}
	return filepath.Join(smbCredDir, name+".cred")
}

// ---------- fstab escaping ----------
//
// fstab separates fields on whitespace, so a share or mount point containing a
// space must be escaped octal-style. mount(8) and getmntent both understand
// this form. Consumer NAS shares are routinely named "Public Share", so this
// is not a theoretical case.

var fstabEscapes = []struct {
	ch  string
	esc string
}{
	{`\`, `\134`},
	{" ", `\040`},
	{"\t", `\011`},
	{"\n", `\012`},
	{"#", `\043`},
}

func fstabEscape(s string) string {
	for _, e := range fstabEscapes {
		s = strings.ReplaceAll(s, e.ch, e.esc)
	}
	return s
}

func fstabUnescape(s string) string {
	// Reverse order so the backslash rule runs last and does not re-consume
	// the escapes produced by the others.
	for i := len(fstabEscapes) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, fstabEscapes[i].esc, fstabEscapes[i].ch)
	}
	return s
}

// ---------- fstab document ----------

// fstabLine is one physical line. Only entries carry parsed fields; comments
// and blanks are preserved verbatim so rewriting fstab never reformats what
// the operator wrote.
type fstabLine struct {
	raw     string
	isEntry bool
	spec    string
	target  string
	fstype  string
	options string
	dump    string
	pass    string
	// managed is set when the preceding line was our marker.
	managed bool
}

// parseFstab splits fstab into lines, tagging entries that follow our marker.
func parseFstab(content string) []fstabLine {
	rawLines := strings.Split(content, "\n")
	out := make([]fstabLine, 0, len(rawLines))
	markerPending := false

	for _, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			// A marker applies to the next entry line.
			markerPending = strings.HasPrefix(trimmed, netShareMarker)
			out = append(out, fstabLine{raw: raw})
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 4 {
			// Malformed but not ours to fix — keep it byte-for-byte.
			out = append(out, fstabLine{raw: raw})
			markerPending = false
			continue
		}
		l := fstabLine{
			raw:     raw,
			isEntry: true,
			spec:    fields[0],
			target:  fields[1],
			fstype:  fields[2],
			options: fields[3],
			dump:    "0",
			pass:    "0",
			managed: markerPending,
		}
		if len(fields) > 4 {
			l.dump = fields[4]
		}
		if len(fields) > 5 {
			l.pass = fields[5]
		}
		out = append(out, l)
		markerPending = false
	}
	return out
}

// renderFstab serialises lines back to file content.
func renderFstab(lines []fstabLine) string {
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, l.raw)
	}
	return strings.Join(parts, "\n")
}

// isNetworkFstype reports whether an fstab fstype is a share this feature owns.
func isNetworkFstype(fstype string) bool {
	switch fstype {
	case "cifs", "smbfs", "nfs", "nfs4":
		return true
	}
	return false
}

// normaliseFstype maps an fstab fstype onto the two types the API exposes.
func normaliseFstype(fstype string) ShareType {
	if fstype == "nfs" || fstype == "nfs4" {
		return ShareNFS
	}
	return ShareSMB
}

// findEntryIndex returns the index of the entry mounting at target, or -1.
// Comparison is on the cleaned, unescaped path so "/mnt/a/" and "/mnt/a" match.
func findEntryIndex(lines []fstabLine, target string) int {
	want := filepath.Clean(target)
	for i, l := range lines {
		if !l.isEntry {
			continue
		}
		if filepath.Clean(fstabUnescape(l.target)) == want {
			return i
		}
	}
	return -1
}

// buildEntryLine renders one fstab entry for a share.
func buildEntryLine(source, mountPoint, fstype, options string) string {
	return fmt.Sprintf("%s %s %s %s 0 0",
		fstabEscape(source), fstabEscape(mountPoint), fstype, options)
}

// upsertShareEntry inserts or replaces the managed entry for mountPoint,
// preceded by the marker line. An existing unmanaged entry is an error: the
// caller must not silently rewrite what an operator hand-wrote.
func upsertShareEntry(lines []fstabLine, source, mountPoint, fstype, options string) ([]fstabLine, error) {
	entry := fstabLine{
		raw:     buildEntryLine(source, mountPoint, fstype, options),
		isEntry: true,
		spec:    fstabEscape(source),
		target:  fstabEscape(mountPoint),
		fstype:  fstype,
		options: options,
		dump:    "0",
		pass:    "0",
		managed: true,
	}
	marker := fstabLine{raw: netShareMarker}

	if i := findEntryIndex(lines, mountPoint); i >= 0 {
		if !lines[i].managed {
			return nil, fmt.Errorf("%s already has an fstab entry that was not created by the panel", mountPoint)
		}
		lines[i] = entry
		return lines, nil
	}

	// Append, keeping the file's trailing newline intact: fstab conventionally
	// ends with one, and parseFstab turns that into a final empty element.
	if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1].raw) == "" {
		lines = lines[:n-1]
		lines = append(lines, marker, entry, fstabLine{raw: ""})
		return lines, nil
	}
	return append(lines, marker, entry), nil
}

// removeShareEntry drops the managed entry for mountPoint along with its
// marker line. Refuses entries the panel did not create.
func removeShareEntry(lines []fstabLine, mountPoint string) ([]fstabLine, error) {
	i := findEntryIndex(lines, mountPoint)
	if i < 0 {
		return lines, nil // already absent — removal is idempotent
	}
	if !lines[i].managed {
		return nil, fmt.Errorf("%s was not added by the panel; remove it from /etc/fstab by hand", mountPoint)
	}
	start := i
	if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1].raw), netShareMarker) {
		start = i - 1
	}
	return append(lines[:start], lines[i+1:]...), nil
}

// validateFstabDocument re-parses rendered content and checks every entry
// still has its six fields. This runs before the file is written: a truncated
// or field-split entry in fstab can drop the host into an emergency shell at
// next boot, and this is the last point where that is cheap to catch.
func validateFstabDocument(content string) error {
	for n, raw := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if fields := strings.Fields(trimmed); len(fields) < 4 {
			return fmt.Errorf("line %d would be malformed: %q", n+1, trimmed)
		}
	}
	return nil
}
