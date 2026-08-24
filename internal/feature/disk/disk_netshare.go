package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/config"
)

// HTTP surface for SMB/NFS network shares. See netshare.go for the fstab
// model and why /etc/fstab is the only place state lives.
//
// Per-node by design: mounts belong to the host that makes them, so in a
// cluster these routes are reached with ?node= like any other local operation.
// Nothing here replicates through the FSM.

// fstabMu serialises the read-modify-write cycle on /etc/fstab.
//
// Add and Remove each read the file, edit the parsed lines and write the whole
// thing back. Two of those interleaving means the second write is based on a
// snapshot taken before the first, and one of the entries silently disappears.
// Echo serves handlers concurrently, so this is reachable with two browser
// tabs, not just in theory.
var fstabMu sync.Mutex

// netShareOpTimeout bounds one mount/umount/probe.
//
// mount.nfs against an unreachable server keeps retrying for about two
// minutes by default. That outlives the browser's own 30s request timeout, so
// the UI gets no answer at all while a subprocess stays wedged on the host.
// Bounding the attempt server-side means the API always replies with
// something the operator can act on.
const netShareOpTimeout = 25 * time.Second

type netShareRequest struct {
	Type       string `json:"type"`   // cifs | smb | nfs
	Server     string `json:"server"` // IP or hostname
	Share      string `json:"share"`  // SMB share name, or NFS export path
	MountPoint string `json:"mount_point"`
	Username   string `json:"username"` // SMB only
	Password   string `json:"password"` // SMB only, never echoed back
	Domain     string `json:"domain"`   // SMB only
	Options    string `json:"options"`  // extra mount options
	ReadOnly   bool   `json:"read_only"`
}

type mountPointRequest struct {
	MountPoint string `json:"mount_point"`
}

// resolved holds a request after validation, ready to act on.
type resolvedShare struct {
	typ        ShareType
	source     string
	mountPoint string
	// runtimeOpts is what gets handed to mount(8); fstabOpts is what gets
	// persisted. They differ only in that credentials= is present in both but
	// the panel's boot-safety options matter only in fstab.
	runtimeOpts string
	fstabOpts   string
	credPath    string
}

// runBounded runs a command under netShareOpTimeout, still cancelling early if
// the client disconnects.
func (h *Handler) runBounded(c echo.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(c.Request().Context(), netShareOpTimeout)
	defer cancel()
	out, err := h.Cmd.RunCtx(ctx, name, args...)
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("timed out after %s — the server did not respond", netShareOpTimeout)
	}
	return out, err
}

// requiredTool maps a share type to the mount helper it needs.
func requiredTool(t ShareType) (binary, pkg string) {
	if t == ShareSMB {
		return "mount.cifs", "cifs-utils"
	}
	return "mount.nfs", "nfs-common"
}

// CheckNetShareTools reports which mount helpers are present. The UI uses this
// to offer a one-click install instead of failing a mount with a bare
// "unknown filesystem type" from the kernel, which explains nothing.
func (h *Handler) CheckNetShareTools(c echo.Context) error {
	return response.OK(c, map[string]interface{}{
		"cifs": map[string]interface{}{
			"installed": h.Cmd.Exists("mount.cifs"),
			"package":   "cifs-utils",
		},
		"nfs": map[string]interface{}{
			"installed": h.Cmd.Exists("mount.nfs"),
			"package":   "nfs-common",
		},
	})
}

// InstallNetShareTools installs the mount helper for one share type.
func (h *Handler) InstallNetShareTools(c echo.Context) error {
	typ, err := validateShareType(c.QueryParam("type"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}
	_, pkg := requiredTool(typ)

	out, err := h.Cmd.RunCtx(c.Request().Context(), "apt-get", "install", "-y", pkg)
	output := strings.TrimSpace(out)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInstallError,
			fmt.Sprintf("apt install failed: %s", response.SanitizeOutput(output)))
	}
	return response.OK(c, map[string]interface{}{
		"message": pkg + " installed successfully",
		"output":  response.SanitizeOutput(output),
	})
}

// ListNetShares returns every SMB/NFS entry in fstab plus its live state.
// Hand-written entries are included and flagged unmanaged, so the page shows
// the host's real picture rather than only what the panel created.
func (h *Handler) ListNetShares(c echo.Context) error {
	content, err := os.ReadFile(fstabPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			fmt.Sprintf("failed to read %s: %v", fstabPath, err))
	}

	mounted := h.mountedNetShares(c)
	shares := make([]NetShare, 0, 4)

	for _, l := range parseFstab(string(content)) {
		if !l.isEntry || !isNetworkFstype(l.fstype) {
			continue
		}
		typ := normaliseFstype(l.fstype)
		source := fstabUnescape(l.spec)
		mp := fstabUnescape(l.target)
		server, share, _ := parseSource(typ, source)

		s := NetShare{
			Type:       string(typ),
			Source:     source,
			Server:     server,
			Share:      share,
			MountPoint: mp,
			Options:    l.options,
			Managed:    l.managed,
		}
		if typ == ShareSMB {
			if _, statErr := os.Stat(credentialsPathFor(mp)); statErr == nil {
				s.HasCredentials = true
			}
		}
		if live, ok := mounted[filepath.Clean(mp)]; ok {
			s.Mounted = true
			s.TotalBytes = live.total
			s.UsedBytes = live.used
		}
		shares = append(shares, s)
	}

	return response.OK(c, map[string]interface{}{"shares": shares})
}

type liveMount struct {
	total int64
	used  int64
}

// mountedNetShares reads live mount state. findmnt is authoritative here
// (fstab only says what *should* be mounted) and its JSON output avoids
// parsing the column-aligned text form.
func (h *Handler) mountedNetShares(c echo.Context) map[string]liveMount {
	out := map[string]liveMount{}
	raw, err := h.Cmd.RunCtx(c.Request().Context(),
		"findmnt", "--json", "--bytes", "--types", "cifs,smbfs,nfs,nfs4",
		"-o", "TARGET,SIZE,USED")
	if err != nil || strings.TrimSpace(raw) == "" {
		// No matching mounts makes findmnt exit non-zero with empty output.
		// That is the normal "nothing mounted yet" case, not a failure.
		return out
	}
	var parsed struct {
		Filesystems []struct {
			Target string          `json:"target"`
			Size   json.RawMessage `json:"size"`
			Used   json.RawMessage `json:"used"`
		} `json:"filesystems"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return out
	}
	for _, fs := range parsed.Filesystems {
		out[filepath.Clean(fs.Target)] = liveMount{
			total: flexibleInt(fs.Size),
			used:  flexibleInt(fs.Used),
		}
	}
	return out
}

// flexibleInt reads a findmnt numeric field, which is a JSON number on newer
// util-linux and a quoted string on older builds.
func flexibleInt(raw json.RawMessage) int64 {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// resolve validates a request and derives everything needed to act on it.
func (h *Handler) resolve(req netShareRequest) (*resolvedShare, error) {
	typ, err := validateShareType(req.Type)
	if err != nil {
		return nil, err
	}
	if err := validateServer(req.Server); err != nil {
		return nil, err
	}
	if err := validateShareName(typ, req.Share); err != nil {
		return nil, err
	}
	if err := validateDiskPath(req.MountPoint); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(req.MountPoint) {
		return nil, fmt.Errorf("mount point must be an absolute path")
	}
	// Same deny-list the local-filesystem mount path uses: without it an
	// operator can shadow /etc or /var/lib/sfpanel with a remote share and
	// lock themselves out of the host.
	if isProtectedMountpoint(req.MountPoint) {
		return nil, fmt.Errorf("mount point is a system path and cannot be used")
	}

	userOpts, err := sanitizeUserOptions(req.Options)
	if err != nil {
		return nil, err
	}

	mp := filepath.Clean(req.MountPoint)
	r := &resolvedShare{
		typ:        typ,
		source:     buildSource(typ, req.Server, req.Share),
		mountPoint: mp,
	}

	opts := make([]string, 0, 6)
	if req.ReadOnly {
		opts = append(opts, "ro")
	}
	if typ == ShareSMB && (req.Username != "" || req.Password != "") {
		r.credPath = credentialsPathFor(mp)
		opts = append(opts, "credentials="+r.credPath)
	}
	if userOpts != "" {
		opts = append(opts, userOpts)
	}

	// fstab keeps mount.nfs's default retry: at boot a slow-to-wake NAS
	// deserves the patience, and nofail already means a failure is harmless.
	r.fstabOpts = strings.Join(append(append([]string{}, opts...), baseShareOptions), ",")

	// The interactive path must not: retry=0 makes mount.nfs give up after one
	// attempt instead of grinding for two minutes behind an API call.
	runtime := append([]string{}, opts...)
	if typ == ShareNFS && !hasOptionKey(runtime, "retry") {
		runtime = append(runtime, "retry=0")
	}
	r.runtimeOpts = strings.Join(runtime, ",")
	if r.runtimeOpts == "" {
		r.runtimeOpts = "defaults"
	}
	return r, nil
}

// hasOptionKey reports whether opts already sets key.
func hasOptionKey(opts []string, key string) bool {
	for _, o := range opts {
		if o == key || strings.HasPrefix(o, key+"=") {
			return true
		}
	}
	return false
}

// writeCredentials writes the SMB credentials file 0600. Written before the
// mount attempt and removed again if the mount fails, so a rejected share
// never leaves a password behind on disk.
func writeCredentials(path, username, password, domain string) error {
	if err := os.MkdirAll(smbCredDir, 0700); err != nil {
		return fmt.Errorf("failed to create credentials directory: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "username=%s\n", username)
	fmt.Fprintf(&b, "password=%s\n", password)
	if domain != "" {
		fmt.Fprintf(&b, "domain=%s\n", domain)
	}
	// AtomicWriteFile creates the temp file with the final mode, so the
	// password is never briefly readable at 0644.
	return config.AtomicWriteFile(path, []byte(b.String()), 0600)
}

// TestNetShare mounts a share read-only at a temporary location, then unmounts
// it. This is the "does this actually work" check the UI runs before saving,
// so a typo in a share name or password surfaces as a clear message instead of
// a broken fstab entry.
func (h *Handler) TestNetShare(c echo.Context) error {
	var req netShareRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	// The probe never touches the caller's mount point, so accept a request
	// that has not chosen one yet.
	if req.MountPoint == "" {
		req.MountPoint = "/mnt/sfpanel-share-test"
	}
	r, err := h.resolve(req)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}
	if bin, pkg := requiredTool(r.typ); !h.Cmd.Exists(bin) {
		return response.Fail(c, http.StatusPreconditionFailed, response.ErrShareToolMissing,
			fmt.Sprintf("%s is required for %s shares; install it first", pkg, r.typ))
	}

	tmp, err := os.MkdirTemp("", "sfpanel-share-*")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
			fmt.Sprintf("failed to create probe directory: %v", err))
	}
	// Removed only after the probe mount is released; os.Remove on a live
	// mount point fails and would leave the directory behind.
	defer func() { _ = os.Remove(tmp) }()

	credPath := ""
	if r.credPath != "" {
		// Throwaway credentials file, unique per request: a fixed name would
		// let two concurrent tests overwrite each other's password and probe
		// with the wrong one.
		if err := os.MkdirAll(smbCredDir, 0700); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
		}
		f, err := os.CreateTemp(smbCredDir, ".probe-*.cred")
		if err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
		}
		credPath = f.Name()
		_ = f.Close()
		if err := writeCredentials(credPath, req.Username, req.Password, req.Domain); err != nil {
			_ = os.Remove(credPath)
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
		}
		defer func() { _ = os.Remove(credPath) }()
		r.runtimeOpts = strings.Replace(r.runtimeOpts, "credentials="+r.credPath, "credentials="+credPath, 1)
	}

	opts := r.runtimeOpts
	if !strings.Contains(opts, "ro") {
		opts = "ro," + opts
	}
	out, mountErr := h.runBounded(c, "mount", "-t", string(r.typ), "-o", opts, r.source, tmp)
	if mountErr != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrShareUnreachable,
			fmt.Sprintf("could not mount %s: %s", r.source,
				response.SanitizeOutput(strings.TrimSpace(out))))
	}
	if uOut, uErr := h.unmountProbe(c, tmp); uErr != nil {
		// The probe answered, so the share itself is fine; report the stray
		// mount rather than claiming a clean result.
		return response.OK(c, map[string]interface{}{
			"ok": true,
			"warning": "share is reachable, but the probe mount at " + tmp +
				" could not be released: " + response.SanitizeOutput(strings.TrimSpace(uOut)),
		})
	}
	return response.OK(c, map[string]interface{}{"ok": true, "message": "share is reachable"})
}

// unmountProbe releases the temporary mount made by TestNetShare.
//
// A cifs mount is briefly busy right after mount(8) returns — the kernel
// client is still settling — so an immediate umount fails with EBUSY and
// leaks the mount. Observed against a live Samba server: the same
// mount/umount pair succeeds by hand, where process startup supplies the few
// milliseconds this retry does.
//
// The lazy fallback is safe *here specifically*: this is a throwaway mount
// under a private temp directory that nothing else can be using. Removing a
// real share deliberately does not do this — there an EBUSY means something
// is genuinely using the data and the operator needs to know.
func (h *Handler) unmountProbe(c echo.Context, target string) (string, error) {
	var out string
	var err error
	for _, wait := range []time.Duration{0, 200 * time.Millisecond, 500 * time.Millisecond} {
		if wait > 0 {
			time.Sleep(wait)
		}
		if out, err = h.runBounded(c, "umount", target); err == nil {
			return out, nil
		}
	}
	// Still busy: detach it so the probe cannot leave a live mount behind.
	return h.runBounded(c, "umount", "-l", target)
}

// AddNetShare mounts a share and persists it to fstab.
//
// Order matters: the share is mounted first and only written to fstab once
// that succeeded. Writing fstab first would persist an entry nobody has proven
// works, which is exactly how a bad fstab line reaches the next boot.
func (h *Handler) AddNetShare(c echo.Context) error {
	var req netShareRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	r, err := h.resolve(req)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}
	if bin, pkg := requiredTool(r.typ); !h.Cmd.Exists(bin) {
		return response.Fail(c, http.StatusPreconditionFailed, response.ErrShareToolMissing,
			fmt.Sprintf("%s is required for %s shares; install it first", pkg, r.typ))
	}

	// Held across read-modify-write; see fstabMu. Taken before the mount so a
	// concurrent add cannot slip its entry in between our mount and our write.
	fstabMu.Lock()
	defer fstabMu.Unlock()

	// Refuse to take over a mount point somebody else already owns.
	content, err := os.ReadFile(fstabPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			fmt.Sprintf("failed to read %s: %v", fstabPath, err))
	}
	lines := parseFstab(string(content))
	if i := findEntryIndex(lines, r.mountPoint); i >= 0 && !lines[i].managed {
		return response.Fail(c, http.StatusConflict, response.ErrShareExists,
			fmt.Sprintf("%s already has an fstab entry that was not created by the panel", r.mountPoint))
	}

	if r.credPath != "" {
		if err := writeCredentials(r.credPath, req.Username, req.Password, req.Domain); err != nil {
			return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, err.Error())
		}
	}
	if err := os.MkdirAll(r.mountPoint, 0755); err != nil {
		h.cleanupCredentials(r.credPath)
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
			fmt.Sprintf("failed to create mount point: %v", err))
	}

	out, mountErr := h.runBounded(c, "mount", "-t", string(r.typ), "-o", r.runtimeOpts, r.source, r.mountPoint)
	if mountErr != nil {
		h.cleanupCredentials(r.credPath)
		return response.Fail(c, http.StatusBadRequest, response.ErrShareUnreachable,
			fmt.Sprintf("mount failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}

	// Mounted successfully — now it is safe to persist.
	lines, err = upsertShareEntry(lines, r.source, r.mountPoint, string(r.typ), r.fstabOpts)
	if err != nil {
		return response.Fail(c, http.StatusConflict, response.ErrShareExists, err.Error())
	}
	if err := writeFstab(lines); err != nil {
		// The mount is live and usable; only persistence failed. Say so
		// plainly rather than reporting success.
		return response.Fail(c, http.StatusInternalServerError, response.ErrFstabWrite,
			fmt.Sprintf("share is mounted but could not be saved to %s: %v", fstabPath, err))
	}

	return response.OK(c, map[string]interface{}{
		"message":     fmt.Sprintf("%s mounted at %s", r.source, r.mountPoint),
		"mount_point": r.mountPoint,
	})
}

// RemoveNetShare unmounts a share, drops its fstab entry and deletes its
// credentials file.
func (h *Handler) RemoveNetShare(c echo.Context) error {
	mp, errResp := h.bindMountPoint(c)
	if errResp != nil {
		return errResp
	}

	fstabMu.Lock()
	defer fstabMu.Unlock()

	content, err := os.ReadFile(fstabPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			fmt.Sprintf("failed to read %s: %v", fstabPath, err))
	}
	lines := parseFstab(string(content))
	if i := findEntryIndex(lines, mp); i >= 0 && !lines[i].managed {
		return response.Fail(c, http.StatusForbidden, response.ErrShareNotManaged,
			fmt.Sprintf("%s was not added by the panel; remove it from %s by hand", mp, fstabPath))
	}

	// Unmount first. A lazy unmount is deliberately not used: if something is
	// still using the share the operator should know, not get a mount that
	// disappears from under a running process.
	if out, uErr := h.runBounded(c, "umount", mp); uErr != nil {
		msg := response.SanitizeOutput(strings.TrimSpace(out))
		if !strings.Contains(msg, "not mounted") && !strings.Contains(msg, "no mount point") {
			return response.Fail(c, http.StatusConflict, response.ErrUnmountError,
				fmt.Sprintf("could not unmount %s: %s", mp, msg))
		}
	}

	lines, err = removeShareEntry(lines, mp)
	if err != nil {
		return response.Fail(c, http.StatusForbidden, response.ErrShareNotManaged, err.Error())
	}
	if err := writeFstab(lines); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFstabWrite,
			fmt.Sprintf("failed to update %s: %v", fstabPath, err))
	}
	h.cleanupCredentials(credentialsPathFor(mp))

	return response.OK(c, map[string]string{"message": mp + " removed"})
}

// MountNetShare mounts an existing fstab entry by mount point. Options come
// from fstab, so nothing caller-supplied reaches mount(8) here.
func (h *Handler) MountNetShare(c echo.Context) error {
	mp, errResp := h.bindMountPoint(c)
	if errResp != nil {
		return errResp
	}
	if err := os.MkdirAll(mp, 0755); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMountError,
			fmt.Sprintf("failed to create mount point: %v", err))
	}
	out, err := h.runBounded(c, "mount", mp)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrShareUnreachable,
			fmt.Sprintf("mount failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}
	return response.OK(c, map[string]string{"message": mp + " mounted"})
}

// UnmountNetShare unmounts a share but leaves its fstab entry in place.
func (h *Handler) UnmountNetShare(c echo.Context) error {
	mp, errResp := h.bindMountPoint(c)
	if errResp != nil {
		return errResp
	}
	out, err := h.runBounded(c, "umount", mp)
	if err != nil {
		return response.Fail(c, http.StatusConflict, response.ErrUnmountError,
			fmt.Sprintf("umount failed: %s", response.SanitizeOutput(strings.TrimSpace(out))))
	}
	return response.OK(c, map[string]string{"message": mp + " unmounted"})
}

// DiscoverNetShares lists the shares a server offers, so the operator can pick
// one instead of typing a name they have to get exactly right.
func (h *Handler) DiscoverNetShares(c echo.Context) error {
	typ, err := validateShareType(c.QueryParam("type"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}
	server := c.QueryParam("server")
	if err := validateServer(server); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	if typ == ShareNFS {
		if !h.Cmd.Exists("showmount") {
			return response.Fail(c, http.StatusPreconditionFailed, response.ErrShareToolMissing,
				"nfs-common is required to list NFS exports; install it first")
		}
		out, err := h.runBounded(c, "showmount", "-e", "--no-headers", server)
		if err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrShareUnreachable,
				fmt.Sprintf("could not list exports on %s: %s", server,
					response.SanitizeOutput(strings.TrimSpace(out))))
		}
		return response.OK(c, map[string]interface{}{"shares": parseShowmount(out)})
	}

	if !h.Cmd.Exists("smbclient") {
		return response.Fail(c, http.StatusPreconditionFailed, response.ErrShareToolMissing,
			"smbclient is required to browse SMB shares; install it first")
	}
	args := []string{"-L", server, "-g"}
	if u := c.QueryParam("username"); u != "" {
		// Password arrives in the body-less query only as a username; the
		// password comes from a header-free POST elsewhere. Anonymous browse
		// is the common case for discovery.
		args = append(args, "-U", u)
	} else {
		args = append(args, "-N")
	}
	out, err := h.runBounded(c, "smbclient", args...)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrShareUnreachable,
			fmt.Sprintf("could not browse %s: %s", server,
				response.SanitizeOutput(strings.TrimSpace(out))))
	}
	return response.OK(c, map[string]interface{}{"shares": parseSmbclientList(out)})
}

// ---------- helpers ----------

// bindMountPoint reads and validates a mount point from the request body.
func (h *Handler) bindMountPoint(c echo.Context) (string, error) {
	var req mountPointRequest
	if err := c.Bind(&req); err != nil {
		return "", response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if err := validateDiskPath(req.MountPoint); err != nil {
		return "", response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint, err.Error())
	}
	if isProtectedMountpoint(req.MountPoint) {
		return "", response.Fail(c, http.StatusBadRequest, response.ErrInvalidMountpoint,
			"mount point is a system path")
	}
	return filepath.Clean(req.MountPoint), nil
}

// cleanupCredentials removes a credentials file, ignoring absence.
func (h *Handler) cleanupCredentials(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// writeFstab validates and atomically replaces fstab, keeping one backup.
// A half-written or malformed fstab can drop the host into an emergency shell
// at next boot, so the content is re-parsed before it is allowed anywhere near
// the real file.
func writeFstab(lines []fstabLine) error {
	content := renderFstab(lines)
	if err := validateFstabDocument(content); err != nil {
		return fmt.Errorf("refusing to write malformed fstab: %w", err)
	}
	if existing, err := os.ReadFile(fstabPath); err == nil {
		// Best-effort backup; a failure here must not block the write, but
		// the comment saying it "is worth not silently skipping" sat above a
		// discarded error, which is exactly silently skipping. This copy is
		// the operator's only undo if the panel ever writes a valid-but-wrong
		// fstab, so its absence is worth a line in the log.
		if err := config.AtomicWriteFile(fstabPath+".sfpanel.bak", existing, 0644); err != nil {
			slog.Warn("could not back up fstab before rewriting it",
				"component", "disk", "path", fstabPath, "error", err)
		}
	}
	return config.AtomicWriteFile(fstabPath, []byte(content), 0644)
}

// parseShowmount turns `showmount -e --no-headers` output into export paths.
// Lines look like "/export/media 192.168.1.0/24"; only the path is wanted.
func parseShowmount(out string) []string {
	shares := make([]string, 0, 4)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Export list") {
			continue
		}
		if fields := strings.Fields(line); len(fields) > 0 {
			shares = append(shares, fields[0])
		}
	}
	return shares
}

// parseSmbclientList reads `smbclient -L host -g` output. The grepable form is
// "Disk|name|comment" per line; printers and IPC$ are not mountable.
func parseSmbclientList(out string) []string {
	shares := make([]string, 0, 4)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.Split(line, "|")
		if len(parts) < 2 || !strings.EqualFold(parts[0], "Disk") {
			continue
		}
		name := strings.TrimSpace(parts[1])
		// Administrative shares end in '$' (IPC$, ADMIN$, C$) and are not
		// what anyone means by "connect my NAS".
		if name == "" || strings.HasSuffix(name, "$") {
			continue
		}
		shares = append(shares, name)
	}
	return shares
}
