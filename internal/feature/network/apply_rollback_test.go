package network

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

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

func doApplyNetplan(t *testing.T, cmd exec.Commander) *httptest.ResponseRecorder {
	t.Helper()
	h := &Handler{Cmd: cmd}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/network/apply", nil), rec)
	if err := h.ApplyNetplan(c); err != nil {
		t.Fatalf("ApplyNetplan: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("ApplyNetplan: %d %s", rec.Code, rec.Body)
	}
	return rec
}

func saveInterface(t *testing.T, iface, body string) {
	t.Helper()
	if rec := doConfigureInterface(t, iface, body); rec.Code != http.StatusOK {
		t.Fatalf("save %s: %d %s", body, rec.Code, rec.Body)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const dhcpConfig = "network:\n  version: 2\n  ethernets:\n    eth0:\n      dhcp4: true\n"

// The page saves first and applies second, and Save is what rewrites the
// file. Apply used to snapshot the files as they stood when it ran — the ones
// Save had just written — so an unconfirmed change "rolled back" to itself,
// and an address that cut the operator off stayed in place. (#114)
func TestRollbackAfterSaveThenApplyRestoresTheConfigFromBeforeTheSave(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	path := writeFile(t, dir, "50-cloud-init.yaml", dhcpConfig)

	saveInterface(t, "eth0", `{"dhcp4":false,"addresses":["192.0.2.10/24"]}`)
	if readString(t, path) == dhcpConfig {
		t.Fatal("the save did not change the file, so this test would prove nothing")
	}
	doApplyNetplan(t, exec.NewMockCommander())
	performNetplanRollback()

	if got := readString(t, path); got != dhcpConfig {
		t.Errorf("rollback left\n%s\nwant the config from before the save\n%s", got, dhcpConfig)
	}
	if _, recorded, _ := loadNetplanBaseline(); recorded {
		t.Error("the baseline outlived the rollback")
	}
}

// Once a change is confirmed it is the configuration to go back to, not the
// one before it.
func TestRollbackGoesBackToTheLastConfirmedConfig(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	path := writeFile(t, dir, "50-cloud-init.yaml", dhcpConfig)

	saveInterface(t, "eth0", `{"dhcp4":false,"addresses":["192.0.2.10/24"]}`)
	doApplyNetplan(t, exec.NewMockCommander())
	if !confirmNetplan() {
		t.Fatal("the apply left nothing to confirm")
	}
	confirmed := readString(t, path)

	saveInterface(t, "eth0", `{"addresses":["192.0.2.20/24"]}`)
	doApplyNetplan(t, exec.NewMockCommander())
	performNetplanRollback()

	if got := readString(t, path); got != confirmed {
		t.Errorf("rollback left\n%s\nwant the confirmed config\n%s", got, confirmed)
	}
}

// Two saves and then one apply: the way back is from before the first.
func TestRollbackAfterTwoSavesGoesBackToBeforeTheFirst(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	path := writeFile(t, dir, "50-cloud-init.yaml", dhcpConfig)

	saveInterface(t, "eth0", `{"dhcp4":false,"addresses":["192.0.2.10/24"]}`)
	saveInterface(t, "eth0", `{"mtu":1400}`)
	doApplyNetplan(t, exec.NewMockCommander())
	performNetplanRollback()

	if got := readString(t, path); got != dhcpConfig {
		t.Errorf("rollback left\n%s\nwant the config from before the first save\n%s", got, dhcpConfig)
	}
}

// netplan applies /etc/netplan at boot, so a saved change the host rebooted
// onto is the configuration it runs. A baseline from before that boot must
// not take it back.
func TestABaselineFromBeforeARebootIsNotTheWayBack(t *testing.T) {
	dir := withNetplanDir(t)
	resetRollback(t)
	path := writeFile(t, dir, "50-cloud-init.yaml", dhcpConfig)

	saveInterface(t, "eth0", `{"dhcp4":false,"addresses":["192.0.2.10/24"]}`)
	booted := readString(t, path)
	if err := os.WriteFile(bootIDPath, []byte("boot-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	saveInterface(t, "eth0", `{"addresses":["192.0.2.20/24"]}`)
	doApplyNetplan(t, exec.NewMockCommander())
	performNetplanRollback()

	if got := readString(t, path); got != booted {
		t.Errorf("rollback left\n%s\nwant the config the host booted with\n%s", got, booted)
	}
}

// The baseline is a copy of netplan files, which can hold Wi-Fi keys.
func TestTheBaselineIsReadableByItsOwnerOnly(t *testing.T) {
	dir := withNetplanDir(t)
	writeFile(t, dir, "50-cloud-init.yaml", dhcpConfig)

	saveInterface(t, "eth0", `{"mtu":1400}`)

	info, err := os.Stat(netplanBaselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("baseline mode %o, want 600", perm)
	}
}
