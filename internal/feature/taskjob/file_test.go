package taskjob

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/feature/files"
	"github.com/svrforum/SFPanel/internal/tasks"
)

func fileSpec(step int, f tasks.FileStep) tasks.JobSpec {
	return kindSpec(step, tasks.StepFile, func(s *tasks.JobSpec) { s.File = &f })
}

// A file step refuses what the file manager refuses, for the same reason:
// a path that is not absolute, and a link — dangling, so resolving it fails —
// whose target is in the panel's credential directory. The link is not
// replaced. A mode the chmod endpoint refuses, and an owner the chown
// endpoint cannot resolve, are refused the same way.
func TestAFileStepIsRefusedForTheEndpointsReasons(t *testing.T) {
	dir := t.TempDir()
	// Were the guard gone, the relative path would land here, not in the
	// package's directory.
	t.Chdir(dir)
	link := filepath.Join(dir, "innocent.conf")
	if err := os.Symlink("/etc/sfpanel/task-job-test-target", link); err != nil {
		t.Fatal(err)
	}
	_, modeErr := files.ParseFileMode("4755")
	_, _, ownerErr := files.ResolveOwner("no-such-user-sfpanel-test", "")
	for _, tc := range []struct {
		label string
		step  tasks.FileStep
		code  string
		msg   string
	}{
		{"a relative path", tasks.FileStep{Path: "etc/motd", Content: "x"}, response.ErrInvalidPath, files.ValidatePathForWrite("etc/motd").Error()},
		{"a link into /etc/sfpanel", tasks.FileStep{Path: link, Content: "x"}, response.ErrInvalidPath, files.ValidatePathForWrite(link).Error()},
		{"a setuid mode", tasks.FileStep{Path: filepath.Join(dir, "a"), Content: "x", Mode: "4755"}, response.ErrInvalidRequest, modeErr.Error()},
		{"an unknown owner", tasks.FileStep{Path: filepath.Join(dir, "b"), Content: "x", Owner: "no-such-user-sfpanel-test"}, response.ErrInvalidRequest, ownerErr.Error()},
	} {
		t.Run(tc.label, func(t *testing.T) {
			k := newKindNode(t, exec.NewMockCommander())
			r := k.run(t, fileSpec(0, tc.step))
			if r.status != tasks.JobFailed || r.summary != tc.code+": "+tc.msg || r.changed {
				t.Errorf("job ended %s changed=%v %q, want failed %q", r.status, r.changed, r.summary, tc.code+": "+tc.msg)
			}
		})
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v %v", fi, err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was written: %v", name, err)
		}
	}
}

// A file step writes only what differs. The same content, mode and owner is
// no change; the same content with another mode changes the mode in place;
// other content replaces the file, keeping its mode when the step names
// none, and leaves no backup or temporary file beside it.
func TestAFileStepWritesOnlyWhatDiffers(t *testing.T) {
	k := newKindNode(t, exec.NewMockCommander())
	dir := filepath.Join(t.TempDir(), "conf.d")
	path := filepath.Join(dir, "app.conf")
	me := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())

	for i, tc := range []struct {
		label   string
		step    tasks.FileStep
		changed bool
		content string
		mode    os.FileMode
	}{
		{"a new file", tasks.FileStep{Path: path, Content: "one\n"}, true, "one\n", 0o644},
		{"the same content", tasks.FileStep{Path: path, Content: "one\n"}, false, "one\n", 0o644},
		{"the same content, another mode", tasks.FileStep{Path: path, Content: "one\n", Mode: "0600"}, true, "one\n", 0o600},
		{"the same content and mode", tasks.FileStep{Path: path, Content: "one\n", Mode: "600"}, false, "one\n", 0o600},
		{"the same owner", tasks.FileStep{Path: path, Content: "one\n", Owner: me}, false, "one\n", 0o600},
		{"other content", tasks.FileStep{Path: path, Content: "two\n"}, true, "two\n", 0o600},
		{"other content of the same length", tasks.FileStep{Path: path, Content: "six\n", Owner: me}, true, "six\n", 0o600},
	} {
		r := k.run(t, fileSpec(i, tc.step))
		if r.status != tasks.JobSucceeded || r.changed != tc.changed {
			t.Errorf("%s: %s changed=%v %q, want succeeded changed=%v", tc.label, r.status, r.changed, r.summary, tc.changed)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != tc.content {
			t.Errorf("%s: the file holds %q (%v), want %q", tc.label, b, err, tc.content)
		}
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.label, err)
		}
		if st.Mode().Perm() != tc.mode {
			t.Errorf("%s: mode %v, want %v", tc.label, st.Mode().Perm(), tc.mode)
		}
		if sys := st.Sys().(*syscall.Stat_t); int(sys.Uid) != os.Getuid() {
			t.Errorf("%s: owner %d", tc.label, sys.Uid)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 {
		t.Errorf("the directory holds %s, want app.conf alone (no .bak, no temporary file)", strings.Join(names, ", "))
	}
}
