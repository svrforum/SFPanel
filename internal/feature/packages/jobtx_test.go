package packages

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// refusal unwraps what StartJobTransaction refused with, failing the test
// when it was not a Refusal.
func refusal(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v (%T), want a *Refusal", err, err)
	}
	return r
}

// A task job installing several packages on a Proxmox VE host is refused
// the way the Packages page is: apt is asked once what installing all of them
// would bring, and the engine it names is the reason.
func TestStartJobTransactionRefusesDockerOnPVE(t *testing.T) {
	h, f, _ := txSetup(t, true)
	f.setArgs("apt-get -s install -- htop docker-buildx", "Inst htop (3.4.1-4 Debian:13.1/stable [amd64])\n"+simBuildxBringsDocker, nil)

	id, err := h.StartJobTransaction("install", []string{"htop", "docker-buildx"}, true)
	r := refusal(t, err)
	if id != "" || r.Status != http.StatusConflict || r.Code != response.ErrDockerOnPVE || r.Details["package"] != "docker.io" {
		t.Errorf("got id %q, %+v; want 409 %s naming docker.io", id, r, response.ErrDockerOnPVE)
	}
	var sims [][]string
	for _, c := range f.calls("apt-get") {
		if slices.Contains(c.Args, "-s") {
			sims = append(sims, c.Args)
		}
	}
	if want := [][]string{{"-s", "install", "--", "htop", "docker-buildx"}}; !reflect.DeepEqual(sims, want) {
		t.Errorf("simulations %q, want one over every package: %q", sims, want)
	}
	if len(f.calls("systemd-run")) != 0 || slices.ContainsFunc(f.calls("apt-get"), func(c exec.MockCall) bool { return slices.Equal(c.Args, []string{"update"}) }) {
		t.Errorf("something ran after the refusal: %+v", f.all())
	}
	if currentTx(t, h) != nil {
		t.Error("a refused transaction was recorded")
	}

	// Away from Proxmox apt is not even asked.
	h, f, _ = txSetup(t, false)
	if _, err := h.StartJobTransaction("install", []string{"docker.io"}, false); err != nil {
		t.Fatalf("install away from Proxmox: %v", err)
	}
	settle(t, h)
	if slices.ContainsFunc(f.calls("apt-get"), func(c exec.MockCall) bool { return slices.Contains(c.Args, "-s") }) {
		t.Errorf("simulated an install on a host that is not Proxmox: %+v", f.all())
	}
}

// Several packages are one apt-get install, one unit, one transaction, and
// JobTransaction reads how it ended.
func TestStartJobTransactionInstallsSeveralPackagesInOneTransaction(t *testing.T) {
	h, f, dir := txSetup(t, false)
	id, err := h.StartJobTransaction("install", []string{"htop", "jq"}, false)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, h)

	want := append([]string{"/bin/sh", "-c", txScript, "sh", "apt-get"}, exec.AptDpkgOptions...)
	want = append(want, "install", "-y", "--", "htop", "jq")
	if got := unitCommand(t, f); !reflect.DeepEqual(got, want) {
		t.Errorf("unit runs %q\nwant      %q", got, want)
	}
	running, result, code, logPath, err := h.JobTransaction(id)
	if err != nil || running || result != txSuccess || code != 0 || logPath != filepath.Join(dir, id+".log") {
		t.Errorf("JobTransaction = %v %q %d %q %v, want a success with exit code 0 logged at %s", running, result, code, logPath, err, filepath.Join(dir, id+".log"))
	}
	if tx := currentTx(t, h); tx == nil || tx.ID != id || tx.Kind != txInstall || !slices.Equal(tx.Packages, []string{"htop", "jq"}) {
		t.Errorf("GET /packages/transaction = %+v, want the job's install of htop and jq", tx)
	}
}

// remove takes several names as well, the way exec.AptRemove runs it.
func TestStartJobTransactionRemovesSeveralPackages(t *testing.T) {
	h, f, _ := txSetup(t, false)
	f.exit = 100
	id, err := h.StartJobTransaction("remove", []string{"htop", "jq"}, false)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, h)
	want := []string{"/bin/sh", "-c", txScript, "sh", "apt-get", "remove", "-y", "--", "htop", "jq"}
	if got := unitCommand(t, f); !reflect.DeepEqual(got, want) {
		t.Errorf("unit runs %q\nwant      %q", got, want)
	}
	if _, result, code, _, _ := h.JobTransaction(id); result != txFailed || code != 100 {
		t.Errorf("result %q code %d, want failed with 100", result, code)
	}
}

// An upgrade job refreshes the lists first when asked, and runs the
// platform's verb, as the endpoint's upgrade does.
func TestStartJobTransactionUpgradesAfterTheLists(t *testing.T) {
	h, f, _ := txSetup(t, true)
	if _, err := h.StartJobTransaction("upgrade", nil, true); err != nil {
		t.Fatal(err)
	}
	settle(t, h)
	want := append([]string{"/bin/sh", "-c", txScript, "sh"}, upgradeArgv(nil)...)
	if got := unitCommand(t, f); !reflect.DeepEqual(got, want) || !slices.Contains(got, "dist-upgrade") {
		t.Errorf("unit runs %q\nwant      %q", got, want)
	}
	calls := f.all()
	upd := slices.IndexFunc(calls, func(c exec.MockCall) bool { return c.Name == "apt-get" && slices.Equal(c.Args, []string{"update"}) })
	run := slices.IndexFunc(calls, func(c exec.MockCall) bool { return c.Name == "systemd-run" })
	if upd < 0 || upd > run {
		t.Errorf("apt-get update at %d, systemd-run at %d: %+v", upd, run, calls)
	}
}

// "update" alone is a transaction too: apt-get update in the panel, as
// before an upgrade, with no unit to launch. It records its end in the log,
// so a panel that restarted still reads it as a success.
func TestAnUpdateJobIsATransactionOfItsOwn(t *testing.T) {
	h, f, _ := txSetup(t, true)
	f.setArgs("apt-get update", updateEnterprise401, errors.New("exit status 100"))
	id, err := h.StartJobTransaction("update", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, h)
	if n := len(f.calls("systemd-run")); n != 0 {
		t.Errorf("systemd-run ran %d times for a lists refresh", n)
	}
	if _, result, code, _, _ := h.JobTransaction(id); result != txSuccess || code != 0 {
		t.Errorf("result %q code %d, want a success: a repository that refused is a warning", result, code)
	}

	restarted := &Handler{Cmd: f}
	if running, result, code, _, err := restarted.JobTransaction(id); err != nil || running || result != txSuccess || code != 0 {
		t.Errorf("after a restart: %v %q %d %v, want a success", running, result, code, err)
	}
}

// Every refusal carries the reason code the endpoints answer with.
func TestStartJobTransactionRefusesForTheEndpointsReasons(t *testing.T) {
	for _, tc := range []struct {
		label    string
		kind     string
		packages []string
		locked   bool
		status   int
		code     string
	}{
		{"an unknown kind", "purge", []string{"htop"}, false, http.StatusBadRequest, response.ErrInvalidRequest},
		{"an update naming packages", "update", []string{"htop"}, false, http.StatusBadRequest, response.ErrInvalidRequest},
		{"an install of nothing", "install", nil, false, http.StatusBadRequest, response.ErrMissingFields},
		{"a removal of nothing", "remove", []string{}, false, http.StatusBadRequest, response.ErrMissingFields},
		{"a name shaped like a flag", "install", []string{"htop", "--reinstall"}, false, http.StatusBadRequest, response.ErrInvalidPackageName},
		{"a name with a space", "upgrade", []string{"bad name"}, false, http.StatusBadRequest, response.ErrInvalidPackageName},
		{"dpkg held by another process (install)", "install", []string{"htop"}, true, http.StatusConflict, response.ErrAPTInstallError},
		{"dpkg held by another process (upgrade)", "upgrade", nil, true, http.StatusConflict, response.ErrAPTUpgradeError},
		{"dpkg held by another process (remove)", "remove", []string{"htop"}, true, http.StatusConflict, response.ErrAPTRemoveError},
		{"dpkg held by another process (update)", "update", nil, true, http.StatusConflict, response.ErrAPTUpdateError},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h, f, _ := txSetup(t, false)
			dpkgLockHeld = func() bool { return tc.locked }
			_, err := h.StartJobTransaction(tc.kind, tc.packages, false)
			r := refusal(t, err)
			if r.Status != tc.status || r.Code != tc.code {
				t.Errorf("got %d %s (%s), want %d %s", r.Status, r.Code, r.Message, tc.status, tc.code)
			}
			if n := len(f.calls("systemd-run")) + len(f.calls("apt-get")); n != 0 {
				t.Errorf("ran apt or a unit: %+v", f.all())
			}
		})
	}

	// A transaction already running is named, as the endpoints name it.
	h, f, _ := txSetup(t, false)
	f.hold = true
	first, err := h.StartJobTransaction("install", []string{"htop"}, false)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the unit to start", func() bool { return f.started() != "" })
	_, err = h.StartJobTransaction("upgrade", nil, false)
	r := refusal(t, err)
	if r.Status != http.StatusConflict || r.Code != response.ErrAPTTransactionRunning || r.Details["id"] != first || r.Details["kind"] != txInstall {
		t.Errorf("while one runs: %+v, want 409 %s naming %s", r, response.ErrAPTTransactionRunning, first)
	}
	if running, _, code, _, _ := h.JobTransaction(first); !running || code != -1 {
		t.Errorf("running %v code %d, want running with no exit code yet", running, code)
	}
	f.end(f.started(), 0)
	settle(t, h)
}

func TestJobTransactionKnowsOnlyTransactionsThatExist(t *testing.T) {
	h, _, _ := txSetup(t, false)
	for _, id := range []string{"", "../../etc/passwd", "20261101-120000-abcd"} {
		if _, _, _, _, err := h.JobTransaction(id); !errors.Is(err, ErrNoTransaction) {
			t.Errorf("JobTransaction(%q) err = %v, want ErrNoTransaction", id, err)
		}
	}
}

// The summary apt-get prints before it changes anything says how many
// packages the transaction touches: upgraded, newly installed and removed.
func TestChangedPackagesReadsTheAptSummary(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	for _, tc := range []struct {
		label string
		log   string
		n     int
		ok    bool
	}{
		{"an upgrade", "Reading package lists...\n3 upgraded, 1 newly installed, 2 to remove and 5 not upgraded.\nNeed to get 12.3 MB of archives.\n", 6, true},
		{"nothing to do", "htop is already the newest version (3.4.1-4).\n0 upgraded, 0 newly installed, 0 to remove and 111 not upgraded.\n", 0, true},
		{"a removal", "The following packages will be REMOVED:\n  htop\n0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 1, true},
		{"the last summary counts", "0 upgraded, 0 newly installed, 0 to remove and 2 not upgraded.\n2 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 2, true},
		{"no summary", ">>> apt-get update\nHit:1 http://deb.debian.org/debian trixie InRelease\n", 0, false},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(tc.label, " ", "-")+".log")
		if err := os.WriteFile(path, []byte(tc.log), 0o600); err != nil {
			t.Fatal(err)
		}
		if n, ok := h.ChangedPackages(path); n != tc.n || ok != tc.ok {
			t.Errorf("%s: ChangedPackages = %d %v, want %d %v", tc.label, n, ok, tc.n, tc.ok)
		}
	}
	if n, ok := h.ChangedPackages(filepath.Join(dir, "missing.log")); n != 0 || ok {
		t.Errorf("a missing log: %d %v, want 0 false", n, ok)
	}
}
