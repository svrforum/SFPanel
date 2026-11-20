package featuremonitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

func TestGuestMACs(t *testing.T) {
	for _, tc := range []struct {
		name string
		conf string
		want []string
	}{
		{
			name: "a VM's address is the value of its model key",
			conf: "boot: order=scsi0\ncores: 2\nnet0: virtio=02:42:AC:11:00:02,bridge=vmbr0,firewall=1\nostype: l26\n",
			want: []string{"02:42:AC:11:00:02"},
		},
		{
			name: "a container's address is hwaddr",
			conf: "arch: amd64\nhostname: store-two\nnet0: name=eth0,bridge=vmbr0,firewall=1,hwaddr=02:42:AC:11:00:06,ip=dhcp,type=veth\n",
			want: []string{"02:42:AC:11:00:06"},
		},
		{
			// Proxmox writes the keys sorted as strings, so net10 comes
			// before net2 in the file; net0 is still the first NIC.
			name: "several NICs and models, in NIC order",
			conf: "net1: e1000=02:42:AC:11:00:03,bridge=vmbr1\nnet10: rtl8139=02:42:AC:11:00:05,bridge=vmbr0\nnet2: vmxnet3=02:42:AC:11:00:04,bridge=vmbr0,tag=20\nnet0: virtio=02:42:AC:11:00:02,bridge=vmbr0\n",
			want: []string{"02:42:AC:11:00:02", "02:42:AC:11:00:03", "02:42:AC:11:00:04", "02:42:AC:11:00:05"},
		},
		{
			name: "the older macaddr= form",
			conf: "net0: model=virtio,macaddr=02:42:AC:11:00:07,bridge=vmbr0\n",
			want: []string{"02:42:AC:11:00:07"},
		},
		{
			name: "lower-case input is upper-cased",
			conf: "net0: virtio=02:42:ac:11:00:0a,bridge=vmbr0\nnet1: name=eth1,bridge=vmbr1,hwaddr=02:42:ac:11:00:0b,ip=dhcp\n",
			want: []string{"02:42:AC:11:00:0A", "02:42:AC:11:00:0B"},
		},
		{
			name: "one address on two NICs is named once",
			conf: "net0: virtio=02:42:AC:11:00:02,bridge=vmbr0\nnet1: virtio=02:42:ac:11:00:02,bridge=vmbr1\nnet2: virtio=02:42:AC:11:00:03,bridge=vmbr1\n",
			want: []string{"02:42:AC:11:00:02", "02:42:AC:11:00:03"},
		},
		{
			// What follows the first section header is a snapshot, or
			// pending changes: NICs the guest does not have now.
			name: "snapshot sections are not read",
			conf: "#app server\nnet0: virtio=02:42:AC:11:00:02,bridge=vmbr0\nparent: before-upgrade\n\n[before-upgrade]\nnet0: virtio=02:42:AC:11:00:09,bridge=vmbr0\nnet1: virtio=02:42:AC:11:00:0C,bridge=vmbr0\nsnaptime: 1700000000\n",
			want: []string{"02:42:AC:11:00:02"},
		},
		{
			name: "no NIC",
			conf: "cores: 2\nmemory: 2048\nscsi0: local-lvm:vm-120-disk-0,size=32G\n",
			want: []string{},
		},
		{
			name: "a NIC line with no address",
			conf: "net0: virtio,bridge=vmbr0\nnet1: name=eth1,bridge=vmbr1,ip=dhcp\nnet2: virtio=02:42:AC:11:00:04,bridge=vmbr0\n",
			want: []string{"02:42:AC:11:00:04"},
		},
		{
			name: "a key that is not netN is not a NIC",
			conf: "#net0: virtio=02:42:AC:11:00:0D\nnetwork: 02:42:AC:11:00:0E\nnetx: virtio=02:42:AC:11:00:0F\n0: virtio=02:42:AC:11:00:10\n",
			want: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := guestMACs([]byte(tc.conf))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// resourcesFixture is what `pvesh get /cluster/resources --type vm
// --output-format json` answers, in the shape the design names: two nodes, a
// template, a guest Proxmox has no figures for yet (no name, status unknown).
const resourcesFixture = `[
 {"id":"qemu/120","vmid":120,"name":"app-one","node":"node-a","type":"qemu","status":"running","template":0,"maxcpu":4,"cpu":0.02,"mem":1073741824,"maxmem":4294967296,"uptime":86400,"tags":"web;prod","disk":0,"maxdisk":34359738368},
 {"id":"lxc/105","vmid":105,"name":"store-two","node":"node-a","type":"lxc","status":"stopped","template":0,"maxcpu":2,"mem":0,"maxmem":536870912,"uptime":0,"tags":"db,backup ops"},
 {"id":"qemu/101","vmid":101,"node":"node-a","type":"qemu","status":"unknown"},
 {"id":"qemu/900","vmid":900,"name":"tmpl-base","node":"node-a","type":"qemu","status":"stopped","template":1,"maxcpu":2,"maxmem":2147483648},
 {"id":"qemu/130","vmid":130,"name":"elsewhere","node":"node-b","type":"qemu","status":"running","template":0,"maxcpu":8,"maxmem":8589934592,"uptime":60},
 {"id":"lxc/110","vmid":110,"name":"half-core","node":"node-a","type":"lxc","status":"running","template":0,"maxcpu":0.5,"mem":104857600,"maxmem":268435456,"uptime":3600,"tags":";;edge; "},
 {"id":"storage/node-a/local","node":"node-a","type":"storage","status":"available","storage":"local"}
]`

func TestParseGuestResources(t *testing.T) {
	got, err := parseGuestResources([]byte(resourcesFixture), "node-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []Guest{
		{VMID: 101, Name: "", Kind: "qemu", Status: "unknown", Tags: []string{}, MACs: []string{}},
		{VMID: 105, Name: "store-two", Kind: "lxc", Status: "stopped", CPUs: 2, MaxMem: 536870912, Tags: []string{"db", "backup", "ops"}, MACs: []string{}},
		{VMID: 110, Name: "half-core", Kind: "lxc", Status: "running", CPUs: 0.5, Mem: 104857600, MaxMem: 268435456, Uptime: 3600, Tags: []string{"edge"}, MACs: []string{}},
		{VMID: 120, Name: "app-one", Kind: "qemu", Status: "running", CPUs: 4, Mem: 1073741824, MaxMem: 4294967296, Uptime: 86400, Tags: []string{"web", "prod"}, MACs: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseGuestResourcesNotJSON(t *testing.T) {
	if _, err := parseGuestResources([]byte("perl: warning: Setting locale failed.\n"), "node-a"); err == nil {
		t.Error("no error for output that is not JSON")
	}
}

func TestGuestNodeName(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("nodes/node-a", filepath.Join(dir, "local")); err != nil {
		t.Fatal(err)
	}
	if got := guestNodeName(dir); got != "node-a" {
		t.Errorf("with the local link: %q, want node-a", got)
	}

	// Without the link the node is named after the host, as Proxmox names it.
	host, err := os.Hostname()
	if err != nil {
		t.Skip(err)
	}
	short, _, _ := strings.Cut(host, ".")
	if got := guestNodeName(t.TempDir()); got != short {
		t.Errorf("without the local link: %q, want the short host name %q", got, short)
	}
}

// guestsHandler serves a Proxmox VE host (or not) from a scratch /etc/pve
// whose local node is node-a.
func guestsHandler(t *testing.T, pve bool, cmd exec.Commander) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink("nodes/node-a", filepath.Join(dir, "local")); err != nil {
		t.Fatal(err)
	}
	return &Handler{
		Cmd:      cmd,
		Platform: func() platform.Info { return platform.Info{PVE: pve} },
		PVEDir:   dir,
	}, dir
}

func writeGuestConf(t *testing.T, dir, rel, conf string) {
	t.Helper()
	p := filepath.Join(dir, "nodes", "node-a", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getGuests(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/guests", nil), rec)
	if err := h.ListGuests(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestListGuestsOnProxmox(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("pvesh", resourcesFixture, nil)
	h, dir := guestsHandler(t, true, mock)
	writeGuestConf(t, dir, "qemu-server/120.conf", "net0: virtio=02:42:ac:11:00:02,bridge=vmbr0\nnet1: virtio=02:42:ac:11:00:03,bridge=vmbr1\n")
	writeGuestConf(t, dir, "lxc/105.conf", "net0: name=eth0,bridge=vmbr0,hwaddr=02:42:ac:11:00:06,ip=dhcp\n")
	writeGuestConf(t, dir, "lxc/110.conf", "hostname: half-core\n")
	// 101 has no config at all, and a VM's config is not looked for under lxc.
	writeGuestConf(t, dir, "lxc/101.conf", "net0: name=eth0,bridge=vmbr0,hwaddr=02:42:ac:11:00:09\n")

	rec := getGuests(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	want := []string{
		"pvesh get /cluster/resources --type vm --output-format json",
	}
	var calls []string
	for _, c := range mock.Calls {
		calls = append(calls, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("commands %q, want %q", calls, want)
	}

	var body struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	got := map[string]string{}
	var order []string
	for _, g := range body.Data {
		vmid := string(g["vmid"])
		order = append(order, vmid)
		got[vmid] = string(g["macs"])
		for _, field := range []string{"vmid", "name", "kind", "status", "cpus", "mem", "maxmem", "uptime", "tags", "macs"} {
			if _, ok := g[field]; !ok {
				t.Errorf("guest %s has no %q", vmid, field)
			}
		}
	}
	if !reflect.DeepEqual(order, []string{"101", "105", "110", "120"}) {
		t.Errorf("guests %v, want 101 105 110 120", order)
	}
	for vmid, macs := range map[string]string{
		"120": `["02:42:AC:11:00:02","02:42:AC:11:00:03"]`,
		"105": `["02:42:AC:11:00:06"]`,
		"110": `[]`, // a config with no NIC
		"101": `[]`, // no config to read: still listed
	} {
		if got[vmid] != macs {
			t.Errorf("guest %s macs %s, want %s", vmid, got[vmid], macs)
		}
	}
}

// Most nodes are not Proxmox hosts. They answer an empty list, and the reason
// is that nothing was asked of pvesh, not that pvesh answered nothing.
func TestListGuestsNotProxmox(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("pvesh", resourcesFixture, nil)
	h, _ := guestsHandler(t, false, mock)

	rec := getGuests(t, h)
	if len(mock.Calls) != 0 {
		t.Errorf("commands run on a host that is not Proxmox: %+v", mock.Calls)
	}
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("got %d %s, want 200 with data []", rec.Code, rec.Body.String())
	}
}

// On a Proxmox host a guest list that could not be read is an error, never
// an empty list: the page has to be able to say it could not load the guests
// rather than that there are none.
func TestListGuestsPveshFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		out    string
		err    error
		reason string
	}{
		{"pvesh fails", "ipcc_send_rec[1] failed: Unknown error -1\n", errors.New("exit status 255"), "pvesh failed: ipcc_send_rec[1] failed"},
		{"pvesh is missing", "", errors.New(`exec: "pvesh": executable file not found in $PATH`), `pvesh failed: exec: "pvesh": executable file not found`},
		{"pvesh times out", "", errors.New("command timed out"), "pvesh failed: command timed out"},
		{"pvesh answers something that is not JSON", "perl: warning: Setting locale failed.\n", nil, "cannot read pvesh output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := exec.NewMockCommander()
			mock.SetOutput("pvesh", tc.out, tc.err)
			h, _ := guestsHandler(t, true, mock)

			rec := getGuests(t, h)
			var body response.Response
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%v: %s", err, rec.Body.String())
			}
			if rec.Code != http.StatusInternalServerError || body.Error == nil || body.Error.Code != response.ErrPVEGuestsError {
				t.Fatalf("got %d %s, want 500 %s", rec.Code, rec.Body.String(), response.ErrPVEGuestsError)
			}
			if !strings.Contains(body.Error.Message, tc.reason) {
				t.Errorf("message %q, want it to say %q", body.Error.Message, tc.reason)
			}
		})
	}
}

// deadlineCommander records whether pvesh was given a deadline.
type deadlineCommander struct {
	*exec.MockCommander
	deadline time.Time
	bounded  bool
}

func (d *deadlineCommander) RunCtx(ctx context.Context, name string, args ...string) (string, error) {
	d.deadline, d.bounded = ctx.Deadline()
	return d.MockCommander.RunCtx(ctx, name, args...)
}

// pvesh can hang on a node whose cluster file system has lost quorum; the
// request must not wait for it longer than the board waits for a node.
func TestListGuestsBoundsPvesh(t *testing.T) {
	cmd := &deadlineCommander{MockCommander: exec.NewMockCommander()}
	cmd.SetOutput("pvesh", "[]", nil)
	h, _ := guestsHandler(t, true, cmd)

	start := time.Now()
	if rec := getGuests(t, h); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !cmd.bounded {
		t.Fatal("pvesh ran without a deadline")
	}
	if d := cmd.deadline.Sub(start); d > 15*time.Second {
		t.Errorf("pvesh may run for %s", d)
	}
}
