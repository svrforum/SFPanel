package network

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func resetRollback(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		netplanRollback.Lock()
		if netplanRollback.timer != nil {
			netplanRollback.timer.Stop()
		}
		clearNetplanRollbackLocked()
		netplanRollback.Unlock()
	})
}

// The scenario the whole thing exists for: an apply that takes the host off
// the network. No confirmation can arrive, because arriving requires the
// network. The previous files must come back.
func TestRollbackRestoresTheFilesWhenNobodyConfirms(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	path := writeFile(t, dir, "01-netcfg.yaml", "network:\n  version: 2\n  ethernets:\n    eth0:\n      dhcp4: true\n")

	before, err := snapshotNetplan()
	if err != nil {
		t.Fatal(err)
	}

	// The apply lands, changing the file.
	writeFile(t, dir, "01-netcfg.yaml", "network:\n  version: 2\n  ethernets:\n    eth0:\n      addresses: [10.9.9.9/24]\n")

	cmd := &exec.MockCommander{}
	armNetplanRollback(before, dir, cmd)
	performNetplanRollback()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before[path]) {
		t.Errorf("file was not restored:\ngot  %q\nwant %q", got, before[path])
	}
	// And the restored config has to be put into effect, not just written.
	var applied bool
	for _, call := range cmd.Calls {
		if call.Name == "netplan" && len(call.Args) > 0 && call.Args[0] == "apply" {
			applied = true
		}
	}
	if !applied {
		t.Error("the snapshot was restored on disk but never applied")
	}
}

// A file the apply introduced has to go, or it keeps overriding the
// restored ones by netplan's lexical ordering.
func TestRollbackRemovesFilesTheApplyAdded(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	writeFile(t, dir, "01-netcfg.yaml", "network:\n  version: 2\n")

	before, err := snapshotNetplan()
	if err != nil {
		t.Fatal(err)
	}
	added := writeFile(t, dir, "99-sfpanel.yaml", "network:\n  version: 2\n  ethernets:\n    eth0:\n      dhcp4: false\n")

	armNetplanRollback(before, dir, &exec.MockCommander{})
	performNetplanRollback()

	if _, err := os.Stat(added); !os.IsNotExist(err) {
		t.Errorf("%s survived the rollback and would keep overriding the restored config", added)
	}
}

// Confirming cancels it. The file the operator applied stays.
func TestConfirmKeepsTheAppliedConfig(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	path := writeFile(t, dir, "01-netcfg.yaml", "network:\n  version: 2\n  ethernets:\n    eth0:\n      dhcp4: true\n")

	before, _ := snapshotNetplan()
	applied := "network:\n  version: 2\n  ethernets:\n    eth0:\n      addresses: [10.9.9.9/24]\n"
	writeFile(t, dir, "01-netcfg.yaml", applied)

	armNetplanRollback(before, dir, &exec.MockCommander{})
	if !confirmNetplan() {
		t.Fatal("confirm reported nothing pending, but an apply was armed")
	}
	if _, pending := pendingNetplanRollback(); pending {
		t.Error("still pending after a confirmation")
	}

	// The timer must be genuinely dead, not merely marked confirmed.
	performNetplanRollback()
	got, _ := os.ReadFile(path)
	if string(got) != applied {
		t.Errorf("the confirmed configuration was reverted anyway:\ngot %q", got)
	}
}

func TestConfirmWithNothingPending(t *testing.T) {
	resetRollback(t)
	if confirmNetplan() {
		t.Error("reported a pending rollback when none was armed")
	}
}

func TestPendingReportsADeadlineInTheFuture(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	writeFile(t, dir, "01-netcfg.yaml", "network:\n  version: 2\n")
	before, _ := snapshotNetplan()

	armNetplanRollback(before, dir, &exec.MockCommander{})
	deadline, pending := pendingNetplanRollback()
	if !pending {
		t.Fatal("nothing pending right after arming")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > netplanRollbackTimeout {
		t.Errorf("deadline is %v away, want between 0 and %v", remaining, netplanRollbackTimeout)
	}
}
