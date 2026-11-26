package featureserveragents

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

const (
	// backupStamp names a backup by when it was made, in UTC.
	backupStamp = "20060102T150405Z"
	// backupsKept is how many backups of one file are kept.
	backupsKept = 3
)

var backupSuffix = regexp.MustCompile(`^\.sfpanel-[0-9]{8}T[0-9]{6}Z\.bak$`)

// writeConf replaces a file under the root with data. What it held before
// is kept beside it as <path>.sfpanel-<UTC time>.bak — the three newest
// backups of a file, no more — and the new file keeps its mode and owner.
// A link at path is kept and the file it leads to is replaced: a config
// linked from elsewhere stays the one the agent reads. Only links the
// panel's own user made are followed, for /var/ossec/etc is the wazuh
// group's to write in. When the file already holds data it is left alone
// and backup is ""; otherwise backup is the backup's path as the host sees
// it.
func (h *Host) writeConf(path string, data []byte) (backup string, err error) {
	p, err := h.linkedFile(path)
	if err != nil {
		return "", err
	}
	old, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if bytes.Equal(old, data) {
		return "", nil
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	mode, uid, gid := fi.Mode().Perm(), -1, -1
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		uid, gid = int(st.Uid), int(st.Gid)
	}
	backup = path + ".sfpanel-" + h.Now().UTC().Format(backupStamp) + ".bak"
	// A backup made earlier in the same second is older than old, so it is
	// the one kept.
	if err := writeNew(h.path(backup), old, mode, uid, gid); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if err := writeAtomic(p, data, mode, uid, gid); err != nil {
		return "", err
	}
	h.pruneBackups(h.path(path))
	return backup, nil
}

// maxLinks bounds a chain of links, as the kernel's own limit does.
const maxLinks = 40

// linkedFile is the file under the root that path leads to: path itself, or
// where its links lead, an absolute link read under the root. A link the
// panel's user did not make is refused.
func (h *Host) linkedFile(path string) (string, error) {
	p := h.path(path)
	for range maxLinks {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			return p, nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != processUID() {
			return "", fmt.Errorf("%s is a link this panel's user did not make; it is not written through", path)
		}
		target, err := os.Readlink(p)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			p = h.path(target)
		} else {
			p = filepath.Join(filepath.Dir(p), target)
		}
	}
	return "", fmt.Errorf("%s leads through more than %d links", path, maxLinks)
}

// writeFile puts a file under the root that apt reads — a keyring, a source,
// a pin — readable by all, replacing any file there without a backup: apt
// warns about every file in its directories with a name it does not read.
func (h *Host) writeFile(path string, data []byte) error {
	p := h.path(path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeAtomic(p, data, 0o644, -1, -1)
}

// pruneBackups removes all but the newest backups of the file at p. Their
// names sort by the time in them.
func (h *Host) pruneBackups(p string) {
	dir, base := filepath.Split(p)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var backups []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, base) && backupSuffix.MatchString(name[len(base):]) {
			backups = append(backups, name)
		}
	}
	slices.Sort(backups)
	for _, name := range backups[:max(len(backups)-backupsKept, 0)] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			logger().Warn("removing an old config backup", "path", filepath.Join(dir, name), "error", err)
		}
	}
}

// writeNew creates a file that must not exist yet with content, mode and
// owner (-1 keeps the process's), and flushes it to disk.
func writeNew(path string, content []byte, mode os.FileMode, uid, gid int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := finishFile(f, content, mode, uid, gid); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// writeAtomic replaces path with content: a temporary file beside it,
// written, given its mode and owner (-1 keeps the process's), flushed to
// disk, then renamed over the old one, so a reader sees the old file or the
// new, never half of one.
func writeAtomic(path string, content []byte, mode os.FileMode, uid, gid int) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".sfpanel-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := finishFile(f, content, mode, uid, gid); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// The rename is durable once the directory is.
	if d, err := os.Open(dir); err == nil {
		if err := d.Sync(); err != nil {
			logger().Warn("flushing a directory", "dir", dir, "error", err)
		}
		_ = d.Close()
	}
	return nil
}

// finishFile writes content to a new file, sets its mode and owner, flushes
// it and closes it; it closes f whatever happens.
func finishFile(f *os.File, content []byte, mode os.FileMode, uid, gid int) error {
	err := func() error {
		if _, err := f.Write(content); err != nil {
			return err
		}
		if err := f.Chmod(mode); err != nil {
			return err
		}
		if uid >= 0 || gid >= 0 {
			if err := f.Chown(uid, gid); err != nil {
				return err
			}
		}
		return f.Sync()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// processUID is the user the panel runs as; a variable for the tests.
var processUID = os.Geteuid
