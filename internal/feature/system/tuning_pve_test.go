package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

func recommended(cats []TuningCategory) map[string]string {
	rec := map[string]string{}
	for _, cat := range cats {
		for _, p := range cat.Params {
			rec[p.Key] = p.Recommended
		}
	}
	return rec
}

// On a Proxmox host the panel recommends what fits a hypervisor: not the
// reverse-path filter an EVPN with two exit nodes needs off, not the dirty
// ratios ZFS and cache-less guest disks never go through, not the limits
// Proxmox and systemd raise for guests themselves, and kernel pointers kept
// for root's perf and bpftrace.
func TestProxmoxGetsWhatFitsAHypervisor(t *testing.T) {
	notOnProxmox := []string{
		"net.ipv4.conf.all.rp_filter", "net.ipv4.conf.default.rp_filter", "net.core.bpf_jit_harden",
		"vm.dirty_ratio", "vm.dirty_background_ratio", "vm.vfs_cache_pressure",
		"net.ipv4.tcp_fastopen", "net.ipv4.tcp_no_metrics_save", "net.ipv4.ip_local_port_range",
		"fs.inotify.max_user_watches", "fs.inotify.max_user_instances", "fs.aio-max-nr",
		"vm.max_map_count", "kernel.pid_max", "fs.file-max",
	}

	platform.SetForTest(t, platform.Info{PVE: true, NetworkStack: platform.StackIfupdown2})
	rec := recommended(buildRecommendations(16, 123, 123<<30))
	for _, k := range notOnProxmox {
		if v, ok := rec[k]; ok {
			t.Errorf("%s = %s recommended on Proxmox", k, v)
		}
	}
	if rec["kernel.kptr_restrict"] != "1" {
		t.Errorf("kernel.kptr_restrict = %q on Proxmox, want 1", rec["kernel.kptr_restrict"])
	}
	for _, k := range []string{"fs.aio-max-nr", "vm.max_map_count", "kernel.pid_max"} {
		if !pveManaged(k) {
			t.Errorf("%s is not shown as Proxmox's", k)
		}
	}

	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	rec = recommended(buildRecommendations(16, 123, 123<<30))
	for _, k := range notOnProxmox {
		if _, ok := rec[k]; !ok {
			t.Errorf("%s no longer recommended off Proxmox", k)
		}
	}
	if rec["kernel.kptr_restrict"] != "2" {
		t.Errorf("kernel.kptr_restrict = %q off Proxmox, want 2", rec["kernel.kptr_restrict"])
	}
}

// unprivileged_bpf_disabled=1 cannot be changed back before a reboot, so the
// rollback after an unconfirmed apply could not undo it. 2 blocks the same
// callers and can be. A host already at 1 keeps it: writing anything else
// fails, and with it the apply.
func TestUnprivilegedBPFIsBlockedTheWayARollbackCanUndo(t *testing.T) {
	if rec := recommended(buildRecommendations(4, 8, 8<<30)); rec["kernel.unprivileged_bpf_disabled"] != "2" {
		t.Errorf("kernel.unprivileged_bpf_disabled = %q, want 2", rec["kernel.unprivileged_bpf_disabled"])
	}
	for current, want := range map[string]string{"0": "2", "1": "1", "2": "2"} {
		if got := keepCurrentIfBetter("kernel.unprivileged_bpf_disabled", current, "2"); got != want {
			t.Errorf("current %s: recommend %s, want %s", current, got, want)
		}
	}
}

// Yama's 2 is the stopgap for a ptrace dumpability bypass; recommending 1
// over it undid the mitigation.
func TestPtraceScopeIsNotLowered(t *testing.T) {
	if got := keepCurrentIfBetter("kernel.yama.ptrace_scope", "2", "1"); got != "2" {
		t.Errorf("ptrace_scope 2 recommended down to %s", got)
	}
}

// Since 6.16 the kernel lets TCP's receive window grow to 1/128 of memory,
// up to 32 MiB. The panel capped it at 16 MiB, below what the kernel had.
func TestTCPReceiveBufferGrowsAsFarAsTheKernelLets(t *testing.T) {
	for _, tc := range []struct {
		ramGB float64
		bytes uint64
		max   string
	}{{123, 123 << 30, "33554432"}, {8, 8 << 30, "33554432"}, {1, 1 << 30, "8388608"}} {
		rmem := recommended(buildRecommendations(4, tc.ramGB, tc.bytes))["net.ipv4.tcp_rmem"]
		if f := strings.Fields(rmem); len(f) != 3 || f[2] != tc.max {
			t.Errorf("%g GiB: tcp_rmem %q, want a max of %s", tc.ramGB, rmem, tc.max)
		}
	}
	if got := keepCurrentIfBetter("net.ipv4.tcp_rmem", "4096 131072 67108864", "4096 131072 33554432"); got != "4096 131072 67108864" {
		t.Errorf("a host with a bigger max is recommended %q", got)
	}
	if got := keepCurrentIfBetter("net.ipv4.tcp_rmem", "4096 131072 8388608", "4096 131072 33554432"); got != "4096 131072 33554432" {
		t.Errorf("a host with a smaller max is recommended %q", got)
	}
}

// The kernel sends redirects on an interface if all or the interface allows
// it: all=0 by itself left every interface sending them.
func TestSendRedirectsOffForNewInterfacesToo(t *testing.T) {
	rec := recommended(buildRecommendations(4, 8, 8<<30))
	if rec["net.ipv4.conf.all.send_redirects"] != "0" || rec["net.ipv4.conf.default.send_redirects"] != "0" {
		t.Errorf("send_redirects all=%q default=%q, want both 0", rec["net.ipv4.conf.all.send_redirects"], rec["net.ipv4.conf.default.send_redirects"])
	}
}

// sysctlFiles makes a sysctl.d directory with the panel's file in it, and an
// /etc/sysctl.conf beside it, and points the handler there.
func sysctlFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, "sysctl.d")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	prevDir, prevConf, prevEtc := operatorSysctlDir, sysctlConfPath, etcSysctlConf
	t.Cleanup(func() { operatorSysctlDir, sysctlConfPath, etcSysctlConf = prevDir, prevConf, prevEtc })
	operatorSysctlDir, sysctlConfPath, etcSysctlConf = d, filepath.Join(d, "99-sfpanel-tuning.conf"), filepath.Join(dir, "sysctl.conf")
	for name, content := range files {
		path := filepath.Join(d, name)
		if name == "sysctl.conf" {
			path = etcSysctlConf
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// What an operator's own sysctl files set is theirs. Ubuntu before 25.04
// ships its defaults in /etc/sysctl.d as well; those, owned by a package, are
// the panel's to tune — except /etc/sysctl.conf, which the admin writes.
func TestOperatorSysctlFilesAreTold(t *testing.T) {
	d := sysctlFiles(t, map[string]string{
		"10-kernel-hardening.conf": "kernel.kptr_restrict = 1\n",
		"50-tuning.conf":           "# mine\nvm.swappiness = 2\n-net/ipv4/tcp_fastopen=1\nnet.ipv4.conf.*.rp_filter = 0\n",
		"99-sfpanel-tuning.conf":   "vm.dirty_ratio = 15\n",
		"99-swappiness.conf":       "vm.swappiness = 10\n",
		"sysctl.conf":              "vm.vfs_cache_pressure = 500\n",
	})
	if err := os.Symlink(etcSysctlConf, filepath.Join(d, "99-sysctl.conf")); err != nil {
		t.Fatal(err)
	}
	cmd := commonExec.NewMockCommander()
	cmd.SetOutput("dpkg-query", "procps: "+filepath.Join(d, "10-kernel-hardening.conf")+"\nprocps: "+filepath.Join(d, "99-sysctl.conf")+"\n"+
		"dpkg-query: no path found matching pattern "+filepath.Join(d, "50-tuning.conf")+"\n", nil)

	set := readOperatorSysctls(cmd)
	for key, want := range map[string]string{
		"vm.swappiness":               "99-swappiness.conf=10",
		"net.ipv4.tcp_fastopen":       "50-tuning.conf=1",
		"net.ipv4.conf.all.rp_filter": "50-tuning.conf=0",
		"vm.vfs_cache_pressure":       "99-sysctl.conf=500",
		"kernel.kptr_restrict":        "",
		"vm.dirty_ratio":              "",
	} {
		got := ""
		if s, ok := set.lookup(key); ok {
			got = s.file + "=" + s.value
		}
		if got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}

// Applying leaves what an operator's file sets alone, and takes out of the
// panel's file what an older panel put there over it.
func TestApplyLeavesTheOperatorsSettings(t *testing.T) {
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	sysctlFiles(t, map[string]string{
		"60-mine.conf":           "vm.swappiness = 2\n",
		"99-sfpanel-tuning.conf": "# SFPanel System Tuning\nvm.swappiness = 10\nnet.ipv4.tcp_syncookies = 1\n",
	})
	t.Cleanup(func() {
		_ = (&TuningHandler{}).ConfirmTuning(echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder()))
	})

	h := &TuningHandler{Cmd: commonExec.NewMockCommander()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/system/tuning/apply", strings.NewReader(`{"categories":["memory"]}`))
	req.Header.Set("Content-Type", "application/json")
	if err := h.ApplyTuning(echo.New().NewContext(req, rec)); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("apply: %v %d %s", err, rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(sysctlConfPath)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		if k, _, ok := parseSysctlLine(line); ok {
			keys = append(keys, k)
		}
	}
	if strings.Contains(" "+strings.Join(keys, " ")+" ", " vm.swappiness ") {
		t.Errorf("the panel's file still sets vm.swappiness, which 60-mine.conf does:\n%s", data)
	}
	if !strings.Contains(string(data), "vm.min_free_kbytes = ") || !strings.Contains(string(data), "net.ipv4.tcp_syncookies = 1") {
		t.Errorf("the rest of the panel's file is gone:\n%s", data)
	}
}

func tuningStatus(t *testing.T) (managed []TuningParam, operator []OperatorParam, advisories []TuningAdvisory) {
	t.Helper()
	rec := httptest.NewRecorder()
	h := &TuningHandler{Cmd: commonExec.NewMockCommander()}
	if err := h.GetTuningStatus(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/tuning", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data struct {
			Managed    []TuningParam    `json:"managed"`
			Operator   []OperatorParam  `json:"operator"`
			Advisories []TuningAdvisory `json:"advisories"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Data.Managed, got.Data.Operator, got.Data.Advisories
}

// The status says where an operator's setting comes from, and on Proxmox what
// the admin guide warns of: swap on a zvol, and a ZFS ARC with no limit.
func TestTuningStatusTellsTheOperatorsFilesAndProxmoxsWarnings(t *testing.T) {
	sysctlFiles(t, map[string]string{"99-pve-tuning.conf": "vm.swappiness = 2\n"})
	dir := t.TempDir()
	prevSwaps, prevARC := procSwapsPath, zfsARCMaxPath
	t.Cleanup(func() { procSwapsPath, zfsARCMaxPath = prevSwaps, prevARC })
	procSwapsPath, zfsARCMaxPath = filepath.Join(dir, "swaps"), filepath.Join(dir, "zfs_arc_max")
	swaps := "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n/dev/zd0                                partition\t16777212\t1024\t\t-2\n"
	if err := os.WriteFile(procSwapsPath, []byte(swaps), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zfsARCMaxPath, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	platform.SetForTest(t, platform.Info{PVE: true, NetworkStack: platform.StackIfupdown2})
	_, operator, advisories := tuningStatus(t)
	if len(operator) != 1 || operator[0].Key != "vm.swappiness" || operator[0].File != "99-pve-tuning.conf" || operator[0].Value != "2" {
		t.Errorf("operator = %+v, want vm.swappiness = 2 from 99-pve-tuning.conf", operator)
	}
	want := []TuningAdvisory{{ID: "swap_on_zvol", Detail: "/dev/zd0"}, {ID: "zfs_arc_unlimited"}}
	if len(advisories) != 2 || advisories[0] != want[0] || advisories[1] != want[1] {
		t.Errorf("advisories = %+v, want %+v", advisories, want)
	}

	if err := os.WriteFile(zfsARCMaxPath, []byte("13251903488\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, advisories := tuningStatus(t); len(advisories) != 1 || advisories[0].ID != "swap_on_zvol" {
		t.Errorf("with an ARC limit, advisories = %+v, want the swap one alone", advisories)
	}

	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	if _, _, advisories := tuningStatus(t); len(advisories) != 0 {
		t.Errorf("off Proxmox, advisories = %+v", advisories)
	}
}
