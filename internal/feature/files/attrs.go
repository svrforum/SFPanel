package files

import (
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// OwnerInfo names the account and group a file belongs to.
//
// The listing showed a permission string like drwxr-xr-x and stopped there,
// which answers "what may the owner do" without ever saying who the owner is.
// The single most common problem on a homelab box is a container writing files
// as a uid the operator's account cannot touch, and the panel could show the
// symptom but none of the cause.
type OwnerInfo struct {
	UID   uint32 `json:"uid"`
	GID   uint32 `json:"gid"`
	User  string `json:"user,omitempty"`
	Group string `json:"group,omitempty"`
}

// ownerLookup caches uid/gid → name resolution for one request batch.
//
// Resolving through os/user reads /etc/passwd for every call. A directory of a
// thousand files owned by the same account would otherwise parse that file a
// thousand times, and the answers do not change within a listing.
type ownerLookup struct {
	users  map[uint32]string
	groups map[uint32]string
}

func newOwnerLookup() *ownerLookup {
	return &ownerLookup{users: map[uint32]string{}, groups: map[uint32]string{}}
}

func (l *ownerLookup) describe(info os.FileInfo) *OwnerInfo {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	out := &OwnerInfo{UID: stat.Uid, GID: stat.Gid}

	if name, seen := l.users[stat.Uid]; seen {
		out.User = name
	} else if u, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10)); err == nil {
		l.users[stat.Uid] = u.Username
		out.User = u.Username
	} else {
		// A uid with no passwd entry is exactly the container case worth
		// showing. Cache the miss so the failed lookup is not repeated per row.
		l.users[stat.Uid] = ""
	}

	if name, seen := l.groups[stat.Gid]; seen {
		out.Group = name
	} else if g, err := user.LookupGroupId(strconv.FormatUint(uint64(stat.Gid), 10)); err == nil {
		l.groups[stat.Gid] = g.Name
		out.Group = g.Name
	} else {
		l.groups[stat.Gid] = ""
	}

	return out
}

// ChangeMode sets a path's permission bits.
//
// POST /files/chmod  { path, mode: "0644", recursive?: bool }
func (h *Handler) ChangeMode(c echo.Context) error {
	var req struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if err := validatePathForWrite(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	req.Path = filepath.Clean(req.Path)

	mode, err := parseFileMode(req.Mode)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	// Refuse to act on a symlink.
	//
	// chmod follows one, so clicking Permissions on a link row would change
	// the TARGET's mode with nothing on screen saying so — the operator names
	// one file and a different one changes. Linux has no lchmod and stores no
	// permissions on a link anyway, so there is nothing sensible to do here
	// except say which file was actually meant. (A link into the panel's
	// credential directory is already refused by validatePathForWrite, which
	// resolves before it checks; this is about the ordinary case.)
	if info, lerr := os.Lstat(req.Path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath,
			"This is a symbolic link; change permissions on the file it points to")
	}

	if req.Recursive {
		// Walk rather than shelling out, so a failure names the path it
		// happened on instead of returning chmod's exit status.
		err = filepath.Walk(req.Path, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			// Never follow a symlink while walking a tree: a link planted in
			// the tree would otherwise redirect the chmod outside it.
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			return os.Chmod(p, mode)
		})
	} else {
		err = os.Chmod(req.Path, mode)
	}
	if err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Path not found")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{"path": req.Path, "mode": fmt.Sprintf("%04o", mode.Perm())})
}

// ChangeOwner sets a path's owner and group.
//
// POST /files/chown  { path, user, group, recursive?: bool }
//
// Names or numeric ids are both accepted: a container's uid frequently has no
// passwd entry, and that is precisely the case an operator needs to fix.
func (h *Handler) ChangeOwner(c echo.Context) error {
	var req struct {
		Path      string `json:"path"`
		User      string `json:"user"`
		Group     string `json:"group"`
		Recursive bool   `json:"recursive"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	if err := validatePathForWrite(req.Path); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidPath, err.Error())
	}
	req.Path = filepath.Clean(req.Path)

	uid, gid, err := resolveOwner(req.User, req.Group)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	apply := func(p string) error { return os.Lchown(p, uid, gid) }
	if req.Recursive {
		err = filepath.Walk(req.Path, func(p string, _ os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			return apply(p)
		})
	} else {
		err = apply(req.Path)
	}
	if err != nil {
		if os.IsPermission(err) {
			return response.Fail(c, http.StatusForbidden, response.ErrPermissionDenied, "Permission denied")
		}
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "Path not found")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]interface{}{"path": req.Path, "uid": uid, "gid": gid})
}

// parseFileMode accepts an octal permission string, with or without a leading
// zero, and refuses anything that is not one.
//
// Only the permission bits are honoured. A caller passing 4755 would otherwise
// set setuid through a file manager, which is not an operation that should be
// reachable by typing four digits into a text field.
func parseFileMode(raw string) (os.FileMode, error) {
	if raw == "" {
		return 0, fmt.Errorf("mode is required")
	}
	if len(raw) > 4 {
		return 0, fmt.Errorf("mode must be three or four octal digits")
	}
	parsed, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("mode must be octal, for example 0644")
	}
	mode := os.FileMode(parsed)
	if mode&^os.ModePerm != 0 {
		return 0, fmt.Errorf("only permission bits may be set; setuid, setgid and sticky are not settable here")
	}
	return mode.Perm(), nil
}

// resolveOwner turns a user and group — by name or by number — into ids.
// An empty field means "leave this one alone", which chown expresses as -1.
func resolveOwner(userRef, groupRef string) (int, int, error) {
	uid, gid := -1, -1

	if userRef != "" {
		if n, err := strconv.Atoi(userRef); err == nil {
			uid = n
		} else if u, err := user.Lookup(userRef); err == nil {
			uid, _ = strconv.Atoi(u.Uid)
		} else {
			return 0, 0, fmt.Errorf("unknown user %q", userRef)
		}
	}
	if groupRef != "" {
		if n, err := strconv.Atoi(groupRef); err == nil {
			gid = n
		} else if g, err := user.LookupGroup(groupRef); err == nil {
			gid, _ = strconv.Atoi(g.Gid)
		} else {
			return 0, 0, fmt.Errorf("unknown group %q", groupRef)
		}
	}
	if uid == -1 && gid == -1 {
		return 0, 0, fmt.Errorf("a user or a group is required")
	}
	return uid, gid, nil
}
