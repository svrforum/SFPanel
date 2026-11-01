package taskjob

// A file step writes one file, behind the file manager's own guards: its
// write-path check (files.ValidatePathForWrite), its mode rule
// (files.ParseFileMode) and its reading of an owner (files.ResolveOwner).
// Unlike the editor's Save it keeps no .bak: a task runs again and again,
// and each run would leave one more.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/feature/files"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// maxFileContent is the file manager's limit on what one save writes.
const maxFileContent = 10 << 20

// mkdirAll makes a new file's directories; a variable so a test can stand in
// for a link planted while they are made.
var mkdirAll = os.MkdirAll

// FileRunner runs file steps.
type FileRunner struct{}

// Check refuses a path the file manager would not write.
func (FileRunner) Check(spec tasks.JobSpec) tasks.CheckVerdict {
	if err := files.ValidatePathForWrite(spec.File.Path); err != nil {
		return tasks.CheckVerdict{Refused: true, Code: response.ErrInvalidPath, Reason: err.Error()}
	}
	return tasks.CheckVerdict{}
}

// Start writes the file, unless it already holds the content, mode and
// owner the step asks for.
func (FileRunner) Start(_ context.Context, job *Job) error {
	res := writeFileStep(job.Spec.File)
	appendLog(job, job.Spec.File.Path+": "+res.Summary)
	if err := job.Finish(res); err != nil {
		logger().Error("recording a job's end", "id", job.Spec.ID, "error", err)
	}
	return nil
}

func refusedResult(code, msg string) Result {
	return Result{Status: tasks.JobFailed, Summary: code + ": " + msg}
}

func fileFailed(err error, changed bool) Result {
	code := response.ErrFileError
	if errors.Is(err, os.ErrPermission) {
		code = response.ErrPermissionDenied
	}
	return Result{Status: tasks.JobFailed, Changed: changed, Summary: code + ": " + response.SanitizeOutput(err.Error())}
}

// writeFileStep applies step. An empty mode keeps the file's mode (0644 for
// a new file); an empty owner keeps its owner (the panel's for a new one).
// Changed is set when content, mode or owner was different.
func writeFileStep(step *tasks.FileStep) Result {
	if err := files.ValidatePathForWrite(step.Path); err != nil {
		return refusedResult(response.ErrInvalidPath, err.Error())
	}
	if len(step.Content) > maxFileContent {
		return refusedResult(response.ErrInvalidRequest, "the content is larger than 10 MiB")
	}
	path := filepath.Clean(step.Path)
	mode, setMode := os.FileMode(0o644), step.Mode != ""
	if setMode {
		m, err := files.ParseFileMode(step.Mode)
		if err != nil {
			return refusedResult(response.ErrInvalidRequest, err.Error())
		}
		mode = m
	}
	uid, gid := -1, -1
	if step.Owner != "" {
		u, g, _ := strings.Cut(step.Owner, ":")
		var err error
		if uid, gid, err = files.ResolveOwner(u, g); err != nil {
			return refusedResult(response.ErrInvalidRequest, err.Error())
		}
	}

	st, err := os.Stat(path)
	switch {
	case err == nil && !st.Mode().IsRegular():
		return refusedResult(response.ErrInvalidPath, "this path is not a regular file")
	case err == nil:
		sys, _ := st.Sys().(*syscall.Stat_t)
		same, rerr := holds(path, st, step.Content)
		if rerr != nil {
			return fileFailed(rerr, false)
		}
		modeOK := !setMode || st.Mode().Perm() == mode
		ownerOK := sys == nil || ((uid < 0 || int(sys.Uid) == uid) && (gid < 0 || int(sys.Gid) == gid))
		if same && modeOK && ownerOK {
			return Result{Status: tasks.JobSucceeded, Summary: "unchanged"}
		}
		if same {
			return setAttrs(path, setMode, mode, uid, gid)
		}
		if !setMode {
			mode = st.Mode().Perm()
		}
		// A rewrite is a new inode: keep the owner the file had.
		if sys != nil {
			if uid < 0 {
				uid = int(sys.Uid)
			}
			if gid < 0 {
				gid = int(sys.Gid)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		if err := mkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fileFailed(err, false)
		}
		// MkdirAll walks links already on disk: judge the path again now
		// that its directories exist, before anything is written into them.
		if err := files.ValidatePathForWrite(step.Path); err != nil {
			return refusedResult(response.ErrInvalidPath, err.Error())
		}
	default:
		return fileFailed(err, false)
	}
	if err := writeAtomic(path, []byte(step.Content), mode, uid, gid); err != nil {
		return fileFailed(err, false)
	}
	return Result{Status: tasks.JobSucceeded, Changed: true, Summary: "written"}
}

// holds reports whether the file at path, of st, holds content.
func holds(path string, st os.FileInfo, content string) (bool, error) {
	if st.Size() != int64(len(content)) {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(b, []byte(content)), nil
}

// setAttrs changes the mode and owner of a file whose content is already
// right.
func setAttrs(path string, setMode bool, mode os.FileMode, uid, gid int) Result {
	if setMode {
		if err := os.Chmod(path, mode); err != nil {
			return fileFailed(err, false)
		}
	}
	if uid >= 0 || gid >= 0 {
		if err := os.Chown(path, uid, gid); err != nil {
			return fileFailed(err, setMode)
		}
	}
	return Result{Status: tasks.JobSucceeded, Changed: true, Summary: "mode or owner set"}
}

// writeAtomic replaces path with content: a temporary file beside it,
// written, given its mode and owner, flushed to disk, then renamed over the
// old one, so a reader sees the old file or the new, never half of one.
func writeAtomic(path string, content []byte, mode os.FileMode, uid, gid int) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".sfpanel-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if _, err := f.Write(content); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if uid >= 0 || gid >= 0 {
		if err := f.Chown(uid, gid); err != nil {
			return fail(err)
		}
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
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
