package network

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// pveHost is a Proxmox VE 9 node; debianHost a Debian 12 install on ifupdown.
var (
	pveHost = platform.Info{
		OSID: "debian", OSVersionID: "13", PVE: true, PVEVersion: "9.2.3",
		NetworkStack: platform.StackIfupdown2, JournalOnly: true, Resolver: platform.ResolverFile,
	}
	debianHost = platform.Info{
		OSID: "debian", OSVersionID: "12", NetworkStack: platform.StackIfupdown,
		JournalOnly: true, Resolver: platform.ResolverFile,
	}
)

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func serve(t *testing.T, handler echo.HandlerFunc, method, target, body string, params ...string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	if len(params) == 2 {
		c.SetParamNames(params[0])
		c.SetParamValues(params[1])
	}
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("response is not one JSON envelope: %v — %q", err, rec.Body.String())
	}
	return rec, e
}

// On Proxmox VE the bridges in /etc/network/interfaces are what every guest
// hangs off, and on any host but a netplan one the panel's netplan edits
// could only fail. Every change is refused there before anything is touched:
// no netplan file written, no rollback baseline recorded, no command run —
// even with netplan files left on a Proxmox host by its provider's image.
func TestNetworkChangesAreRefusedWhereThePanelDoesNotWriteTheNetwork(t *testing.T) {
	type handlerCase struct {
		name, method, target, body string
		params                     []string
		handler                    func(*Handler) echo.HandlerFunc
	}
	cases := []handlerCase{
		{"configure", http.MethodPut, "/network/interfaces/vmbr0", `{"dhcp4":true}`, []string{"name", "vmbr0"},
			func(h *Handler) echo.HandlerFunc { return h.ConfigureInterface }},
		{"apply", http.MethodPost, "/network/apply", "", nil,
			func(h *Handler) echo.HandlerFunc { return h.ApplyNetplan }},
		{"dns", http.MethodPut, "/network/dns", `{"servers":["192.0.2.53"]}`, nil,
			func(h *Handler) echo.HandlerFunc { return h.ConfigureDNS }},
		{"bond create", http.MethodPost, "/network/bonds", `{"name":"bond0","mode":"active-backup","slaves":["enp1s0","enp2s0"]}`, nil,
			func(h *Handler) echo.HandlerFunc { return h.CreateBond }},
		{"bond delete", http.MethodDelete, "/network/bonds/bond0", "", []string{"name", "bond0"},
			func(h *Handler) echo.HandlerFunc { return h.DeleteBond }},
	}
	const leftover = "network:\n  version: 2\n  ethernets:\n    vmbr0:\n      dhcp4: true\n  bonds:\n    bond0:\n      interfaces: [enp1s0]\n"
	for _, host := range []struct {
		name  string
		info  platform.Info
		stack string
	}{{"proxmox", pveHost, "ifupdown2"}, {"debian", debianHost, "ifupdown"}} {
		for _, tc := range cases {
			t.Run(host.name+"/"+tc.name, func(t *testing.T) {
				dir := withNetplanDir(t)
				resetRollback(t)
				path := writeNetplan(t, dir, "50-cloud-init.yaml", leftover)
				platform.SetForTest(t, host.info)
				m := exec.NewMockCommander()

				rec, e := serve(t, tc.handler(&Handler{Cmd: m}), tc.method, tc.target, tc.body, tc.params...)
				if rec.Code != http.StatusConflict || e.Error.Code != "NETWORK_READ_ONLY" || e.Error.Details["stack"] != host.stack {
					t.Errorf("got %d %s, want 409 NETWORK_READ_ONLY with stack %s", rec.Code, rec.Body.String(), host.stack)
				}
				if got := readString(t, path); got != leftover {
					t.Errorf("netplan file changed:\n%s", got)
				}
				if _, err := os.Stat(netplanBaselinePath); !os.IsNotExist(err) {
					t.Errorf("a rollback baseline was recorded (%v)", err)
				}
				if len(m.Calls) != 0 {
					t.Errorf("ran %+v", m.Calls)
				}
			})
		}
	}
}

func TestCapabilities(t *testing.T) {
	for _, tc := range []struct {
		info platform.Info
		want Capabilities
	}{
		{pveHost, Capabilities{Stack: "ifupdown2", Platform: "pve"}},
		{debianHost, Capabilities{Stack: "ifupdown", Platform: "debian"}},
		{platform.Info{OSID: "debian", NetworkStack: platform.StackUnknown}, Capabilities{Stack: "unknown", Platform: "debian"}},
		{netplanHost, Capabilities{Stack: "netplan", Platform: "ubuntu", Writable: true}},
	} {
		platform.SetForTest(t, tc.info)
		rec, e := serve(t, (&Handler{Cmd: exec.NewMockCommander()}).GetCapabilities, http.MethodGet, "/network/capabilities", "")
		var got Capabilities
		if err := json.Unmarshal(e.Data, &got); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		reason := got.Reason
		got.Reason = ""
		if got != tc.want || (reason == "") != tc.want.Writable {
			t.Errorf("%s: got %+v (reason %q), want %+v", tc.info.NetworkStack, got, reason, tc.want)
		}
	}
}

// `ip -d -j link show` on a Proxmox node with one VM whose NIC has the
// firewall on (tap into fwbr, fwpr/fwln veth pair to vmbr0) and one container.
const pveLinks = `[
{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP","LOWER_UP"],"mtu":65536,"operstate":"UNKNOWN","link_type":"loopback"},
{"ifindex":2,"ifname":"enp1s0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"master":"vmbr0","operstate":"UP","link_type":"ether","address":"02:42:ac:10:00:01","linkinfo":{"info_slave_kind":"bridge","info_slave_data":{"state":"forwarding"}}},
{"ifindex":3,"ifname":"vmbr0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether","address":"02:42:ac:10:00:01","linkinfo":{"info_kind":"bridge","info_data":{"stp_state":0}}},
{"ifindex":4,"ifname":"tap100i0","flags":["BROADCAST","MULTICAST","PROMISC","UP","LOWER_UP"],"mtu":1500,"master":"fwbr100i0","operstate":"UNKNOWN","link_type":"ether","address":"02:42:ac:10:01:00","linkinfo":{"info_kind":"tun","info_data":{"type":"tap"},"info_slave_kind":"bridge"}},
{"ifindex":5,"ifname":"fwbr100i0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether","address":"02:42:ac:10:01:01","linkinfo":{"info_kind":"bridge"}},
{"ifindex":6,"link":"fwln100i0","ifname":"fwpr100p0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"master":"vmbr0","operstate":"UP","link_type":"ether","address":"02:42:ac:10:01:02","linkinfo":{"info_kind":"veth","info_slave_kind":"bridge"}},
{"ifindex":7,"link":"fwpr100p0","ifname":"fwln100i0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"master":"fwbr100i0","operstate":"UP","link_type":"ether","address":"02:42:ac:10:01:03","linkinfo":{"info_kind":"veth","info_slave_kind":"bridge"}},
{"ifindex":8,"ifname":"veth101i0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"master":"vmbr0","operstate":"UP","link_type":"ether","address":"02:42:ac:10:01:04","linkinfo":{"info_kind":"veth","info_slave_kind":"bridge"}}
]`

// The page grouped interfaces by name and speed: on Proxmox every guest NIC's
// tap, firewall bridge and veth pair sat among the host's own interfaces, each
// with a Configure button. The kernel says what each link is and whose port.
func TestParseLinkKindsOnAProxmoxHost(t *testing.T) {
	want := map[string]linkKind{
		"lo":        {},
		"enp1s0":    {master: "vmbr0"},
		"vmbr0":     {kind: "bridge"},
		"tap100i0":  {kind: "tun", master: "fwbr100i0"},
		"fwbr100i0": {kind: "bridge"},
		"fwpr100p0": {kind: "veth", master: "vmbr0"},
		"fwln100i0": {kind: "veth", master: "fwbr100i0"},
		"veth101i0": {kind: "veth", master: "vmbr0"},
	}
	if got := parseLinkKinds(pveLinks); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	if got := parseLinkKinds("Object \"link\" is unknown"); got != nil {
		t.Errorf("unparsable output gave %+v", got)
	}
}

// The memo's comment said a name that went away was asked again when it came
// back; nothing ever removed one. Proxmox makes and drops guest interface
// names all day.
func TestDriverMemoForgetsInterfacesThatAreGone(t *testing.T) {
	driverMemo.Lock()
	prev := driverMemo.byName
	driverMemo.byName = map[string]string{"tap999i0": "tun"}
	driverMemo.Unlock()
	t.Cleanup(func() {
		driverMemo.Lock()
		driverMemo.byName = prev
		driverMemo.Unlock()
	})

	if _, err := (&Handler{Cmd: exec.NewMockCommander()}).gatherInterfaces(); err != nil {
		t.Fatal(err)
	}
	driverMemo.Lock()
	_, kept := driverMemo.byName["tap999i0"]
	driverMemo.Unlock()
	if kept {
		t.Error("an interface that is gone is still in the driver memo")
	}
}

// The listing carries each link's kind and master from that one call. lo is
// on every host; the kernel's answer here is made up.
func TestInterfacesCarryKindAndMaster(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("ip", `[{"ifname":"lo","master":"vmbr9","linkinfo":{"info_kind":"dummy"}}]`, nil)
	ifaces, err := (&Handler{Cmd: m}).gatherInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Name == "lo" {
			if iface.Kind != "dummy" || iface.Master != "vmbr9" {
				t.Errorf("lo: kind %q master %q", iface.Kind, iface.Master)
			}
			return
		}
	}
	t.Skip("no lo on this host")
}
