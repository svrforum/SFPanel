package disk

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// hangingCommander answers df normally except for one mount point, on which it
// blocks until the context expires — a server that has gone away.
type hangingCommander struct {
	exec.MockCommander
	hangOn string
	calls  []string
}

const localDf = "Filesystem     Type  1B-blocks      Used     Avail Use% Mounted on\n" +
	"/dev/sda2      ext4  500000000 200000000 300000000  40% /\n"

func (h *hangingCommander) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	h.calls = append(h.calls, strings.Join(args, " "))
	if len(args) > 0 && args[len(args)-1] == h.hangOn {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if len(args) > 0 && strings.HasPrefix(args[len(args)-1], "/mnt/") {
		mp := args[len(args)-1]
		return "Filesystem     Type  1B-blocks      Used     Avail Use% Mounted on\n" +
			"nas:/vol       nfs4 1000000000 400000000 600000000  40% " + mp + "\n", nil
	}
	return localDf, nil
}

func withMounts(t *testing.T, mounts []mountEntry) {
	t.Helper()
	orig := readMountTable
	readMountTable = func() ([]mountEntry, error) { return mounts, nil }
	t.Cleanup(func() {
		readMountTable = orig
		unresponsiveMemo.Lock()
		unresponsiveMemo.until = map[string]time.Time{}
		unresponsiveMemo.Unlock()
	})
}

// The whole point: one dead share must cost a bounded wait and one badge, not
// the page. The local list and the healthy share both come back, the dead one
// is listed as unresponsive, and the call returns near the per-mount bound
// rather than hanging for as long as the caller is willing to wait.
func TestListFilesystemsSurvivesADeadNetworkMount(t *testing.T) {
	withMounts(t, []mountEntry{
		{source: "/dev/sda2", mountPoint: "/", fstype: "ext4"},
		{source: "nas:/vol", mountPoint: "/mnt/alive", fstype: "nfs4"},
		{source: "dead:/vol", mountPoint: "/mnt/dead", fstype: "nfs4"},
	})
	cmd := &hangingCommander{hangOn: "/mnt/dead"}

	start := time.Now()
	got, err := listFilesystems(context.Background(), cmd)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("listFilesystems: %v", err)
	}

	if elapsed > networkMountTimeout+2*time.Second {
		t.Fatalf("took %v; a dead mount must cost about %v, not the caller's patience", elapsed, networkMountTimeout)
	}

	byMount := map[string]Filesystem{}
	for _, f := range got {
		byMount[f.MountPoint] = f
	}
	if _, ok := byMount["/"]; !ok {
		t.Error("the local filesystem went missing")
	}
	if alive, ok := byMount["/mnt/alive"]; !ok || alive.Unresponsive || alive.Size == 0 {
		t.Errorf("the healthy share was not listed with its numbers: %+v", alive)
	}
	dead, ok := byMount["/mnt/dead"]
	if !ok {
		t.Fatal("the dead share was dropped; it must be listed so the operator can see it is down")
	}
	if !dead.Unresponsive {
		t.Error("the dead share is not marked unresponsive")
	}
	if dead.Size != 0 || dead.Used != 0 {
		t.Errorf("the dead share carries numbers it cannot know: %+v", dead)
	}
}

// The local df must not be the one that touches the network. If it were, a
// dead share would still hang the whole listing before the per-mount probes
// ever ran.
func TestListFilesystemsAsksForLocalOnlyFirst(t *testing.T) {
	withMounts(t, []mountEntry{{source: "/dev/sda2", mountPoint: "/", fstype: "ext4"}})
	cmd := &hangingCommander{}
	if _, err := listFilesystems(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if len(cmd.calls) == 0 || !strings.Contains(cmd.calls[0], "-l") {
		t.Errorf("first df call = %q, want the local-only form", cmd.calls)
	}
}

// A mount that just failed is not probed again for a while. Each probe of a
// dead mount is a df that has to be killed, and on a kernel where a hard NFS
// mount cannot be interrupted it stays behind — so one per interval, not one
// per poll.
func TestListFilesystemsRemembersASilentMount(t *testing.T) {
	withMounts(t, []mountEntry{{source: "dead:/vol", mountPoint: "/mnt/dead", fstype: "nfs4"}})
	cmd := &hangingCommander{hangOn: "/mnt/dead"}

	if _, err := listFilesystems(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	probesAfterFirst := len(cmd.calls)

	start := time.Now()
	got, err := listFilesystems(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.calls) != probesAfterFirst+1 { // +1 is the local df
		t.Errorf("the dead mount was probed again within the memo window (%d calls, want %d)", len(cmd.calls), probesAfterFirst+1)
	}
	if time.Since(start) > time.Second {
		t.Errorf("second listing took %v; a remembered dead mount must not be waited on", time.Since(start))
	}
	var dead *Filesystem
	for i := range got {
		if got[i].MountPoint == "/mnt/dead" {
			dead = &got[i]
		}
	}
	if dead == nil || !dead.Unresponsive {
		t.Errorf("remembered mount not reported as unresponsive: %+v", got)
	}
}

func TestIsRemoteMountCatchesWhatDfWouldBlockOn(t *testing.T) {
	remote := []mountEntry{
		{source: "nas:/vol", fstype: "nfs4"},
		{source: "//nas/share", fstype: "cifs"},
		{source: "user@host:/home", fstype: "fuse.sshfs"},
		{source: "host:/gv0", fstype: "fuse.glusterfs"},
	}
	for _, m := range remote {
		if !isRemoteMount(m) {
			t.Errorf("%+v not treated as remote", m)
		}
	}
	local := []mountEntry{
		{source: "/dev/sda2", fstype: "ext4"},
		{source: "tmpfs", fstype: "tmpfs"},
		{source: "overlay", fstype: "overlay"},
		{source: "/dev/mapper/vg-lv", fstype: "xfs"},
	}
	for _, m := range local {
		if isRemoteMount(m) {
			t.Errorf("%+v wrongly treated as remote", m)
		}
	}
}
