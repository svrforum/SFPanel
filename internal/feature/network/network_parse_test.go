package network

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// ---------- resolvectl fixtures ----------

// Modern systemd-resolved `resolvectl status` shape: multiple servers on one
// "DNS Servers:" line, "DNS Domain:" with a wrapped continuation line for the
// extra ~in-addr.arpa search domains.
const resolvectlModernFixture = `Global
         Protocols: -LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
  resolv.conf mode: stub

Link 2 (eth0)
    Current Scopes: DNS
         Protocols: +DefaultRoute -LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 1.1.1.1
       DNS Servers: 1.1.1.1 8.8.8.8 192.168.1.1

Link 4 (tailscale0)
    Current Scopes: DNS
         Protocols: -DefaultRoute -LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
       DNS Servers: 100.100.100.100 fd7a:115c:a1e0::53
        DNS Domain: example.ts.net
                    ~68.100.in-addr.arpa ~69.100.in-addr.arpa
`

// Legacy systemd-resolved (Ubuntu 20.04, systemd 245) shape: one server per
// line — the second and subsequent servers appear as bare continuation lines
// without the "DNS Servers:" prefix.
const resolvectlLegacyFixture = `Link 2 (eth0)
      Current Scopes: DNS
       LLMNR setting: yes
MulticastDNS setting: no
      DNSSEC setting: no
         DNS Servers: 192.168.1.1
                      8.8.8.8
`

func TestParseResolvectlOutput(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantServers []string
		wantSearch  []string
	}{
		{
			// Was a frozen limitation: the wrapped continuation under
			// "DNS Domain:" has no prefix and used to be dropped, so the
			// routing-only search domains never appeared.
			name:  "modern multi-server output",
			input: resolvectlModernFixture,
			wantServers: []string{
				"1.1.1.1", "8.8.8.8", "192.168.1.1",
				"100.100.100.100", "fd7a:115c:a1e0::53",
			},
			wantSearch: []string{"example.ts.net", "~68.100.in-addr.arpa", "~69.100.in-addr.arpa"},
		},
		{
			// Was a frozen limitation, and the one that mattered most:
			// systemd 245 (Ubuntu 20.04) prints one server per line, so a
			// host with three resolvers reported exactly one.
			name:        "legacy one-server-per-line output",
			input:       resolvectlLegacyFixture,
			wantServers: []string{"192.168.1.1", "8.8.8.8"},
			wantSearch:  []string{},
		},
		{
			// The keys that follow a value block must end it. "Current DNS
			// Server:" in particular is a single address that would look
			// exactly like a continuation if only the fields were checked.
			name: "a following key ends the block",
			input: "       DNS Servers: 1.1.1.1\n" +
				"                     8.8.8.8\n" +
				"Current DNS Server: 9.9.9.9\n" +
				"         Protocols: -LLMNR\n",
			wantServers: []string{"1.1.1.1", "8.8.8.8"},
			wantSearch:  []string{},
		},
		{
			// A blank line ends it too — links are separated that way.
			name: "a blank line ends the block",
			input: "       DNS Servers: 1.1.1.1\n" +
				"\n" +
				"8.8.8.8\n",
			wantServers: []string{"1.1.1.1"},
			wantSearch:  []string{},
		},
		{
			name:        "empty input yields empty non-nil slices",
			input:       "",
			wantServers: []string{},
			wantSearch:  []string{},
		},
		{
			name:        "non-IP tokens are filtered by net.ParseIP",
			input:       "DNS Servers: not-an-ip 8.8.8.8\n",
			wantServers: []string{"8.8.8.8"},
			wantSearch:  []string{},
		},
		{
			name:        "Current DNS Server line is not collected",
			input:       "Current DNS Server: 9.9.9.9\n",
			wantServers: []string{},
			wantSearch:  []string{},
		},
		{
			// No de-duplication: a server listed in both the Global
			// and a Link section appears twice (frozen behavior).
			name:        "duplicate servers across sections are kept",
			input:       "Global\n       DNS Servers: 8.8.8.8\n\nLink 2 (eth0)\n       DNS Servers: 8.8.8.8\n",
			wantServers: []string{"8.8.8.8", "8.8.8.8"},
			wantSearch:  []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseResolvectlOutput(tc.input)
			if !reflect.DeepEqual(got.Servers, tc.wantServers) {
				t.Errorf("Servers = %#v, want %#v", got.Servers, tc.wantServers)
			}
			if !reflect.DeepEqual(got.Search, tc.wantSearch) {
				t.Errorf("Search = %#v, want %#v", got.Search, tc.wantSearch)
			}
		})
	}
}

func TestParseRouteLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Route
	}{
		{
			name: "default route with src token ignored",
			line: "default via 192.168.1.1 dev eth0 proto dhcp src 192.168.1.100 metric 100",
			want: Route{Destination: "default", Gateway: "192.168.1.1", Interface: "eth0", Metric: 100, Protocol: "dhcp"},
		},
		{
			name: "link-scope kernel route",
			line: "192.168.1.0/24 dev eth0 proto kernel scope link src 192.168.1.100 metric 100",
			want: Route{Destination: "192.168.1.0/24", Interface: "eth0", Metric: 100, Protocol: "kernel", Scope: "link"},
		},
		{
			name: "linkdown trailer ignored",
			line: "172.21.0.0/16 dev br-test0 proto kernel scope link src 172.21.0.1 linkdown",
			want: Route{Destination: "172.21.0.0/16", Interface: "br-test0", Protocol: "kernel", Scope: "link"},
		},
		{
			name: "IPv6 destination with pref token ignored",
			line: "fe80::/64 dev eth0 proto kernel metric 256 pref medium",
			want: Route{Destination: "fe80::/64", Interface: "eth0", Metric: 256, Protocol: "kernel"},
		},
		{
			name: "truncated line does not panic (i+1 guard)",
			line: "default via",
			want: Route{Destination: "default"},
		},
		{
			name: "non-numeric metric stays zero",
			line: "10.0.0.0/8 via 10.0.0.1 metric abc",
			want: Route{Destination: "10.0.0.0/8", Gateway: "10.0.0.1"},
		},
		{
			name: "empty line yields zero Route",
			line: "",
			want: Route{},
		},
		{
			// Was a frozen quirk: the keyword was read as the destination
			// and the real network dropped, so the routes table showed a
			// destination called "blackhole".
			name: "blackhole keeps its type and its destination",
			line: "blackhole 10.0.0.0/24 proto static",
			want: Route{Type: "blackhole", Destination: "10.0.0.0/24", Protocol: "static"},
		},
		{
			name: "unreachable route",
			line: "unreachable 192.0.2.0/24 dev eth0 metric 100",
			want: Route{Type: "unreachable", Destination: "192.0.2.0/24", Interface: "eth0", Metric: 100},
		},
		{
			name: "prohibit route",
			line: "prohibit 198.51.100.0/24",
			want: Route{Type: "prohibit", Destination: "198.51.100.0/24"},
		},
		{
			// A destination that happens to start with the same letters is
			// not a type keyword — matching on a prefix instead of the whole
			// field would eat it.
			name: "destination is not mistaken for a type keyword",
			line: "blackholenet via 10.0.0.1",
			want: Route{Destination: "blackholenet", Gateway: "10.0.0.1"},
		},
		{
			name: "type keyword with nothing after it",
			line: "blackhole",
			want: Route{Type: "blackhole"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRouteLine(tc.line)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseRouteLine(%q) = %+v, want %+v", tc.line, got, tc.want)
			}
		})
	}
}

const ipRouteFixture = `default via 192.168.1.1 dev eth0 proto dhcp metric 100

192.168.1.0/24 dev eth0 proto kernel scope link src 192.168.1.100 metric 100
172.17.0.0/16 dev docker0 proto kernel scope link src 172.17.0.1 linkdown
`

func TestParseRoutes(t *testing.T) {
	m := exec.NewMockCommander()
	m.SetOutput("ip", ipRouteFixture, nil)
	h := &Handler{Cmd: m}

	routes, err := h.parseRoutes()
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	// The blank line in the middle of the fixture is skipped.
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d: %+v", len(routes), routes)
	}
	wantCall := exec.MockCall{Name: "ip", Args: []string{"route", "show"}}
	if !reflect.DeepEqual(m.Calls[0], wantCall) {
		t.Errorf("call = %+v, want %+v", m.Calls[0], wantCall)
	}

	// Command failure propagates the error and returns a nil slice.
	m2 := exec.NewMockCommander()
	m2.SetOutput("ip", "", errors.New("boom"))
	h2 := &Handler{Cmd: m2}
	routes, err = h2.parseRoutes()
	if err == nil {
		t.Fatal("expected error when ip route show fails")
	}
	if routes != nil {
		t.Errorf("routes = %+v, want nil on error", routes)
	}
}

// doConfigureInterface invokes the ConfigureInterface handler with a JSON body
// and returns the recorder (echo context pattern per firewall_docker_test.go).
func doConfigureInterface(t *testing.T, iface, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := &Handler{Cmd: exec.NewMockCommander()}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/network/interfaces/x", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("name")
	c.SetParamValues(iface)
	if err := h.ConfigureInterface(c); err != nil {
		t.Fatalf("ConfigureInterface: %v", err)
	}
	return rec
}

// All rejection cases below fail validation before any netplan file access,
// so they need no netplanDir override and never touch the filesystem.
func TestConfigureInterface_Validation(t *testing.T) {
	cases := []struct {
		name        string
		iface       string
		body        string
		wantErrCode string
	}{
		{
			name:        "path traversal interface name rejected",
			iface:       "../etc",
			body:        `{}`,
			wantErrCode: "INVALID_NAME",
		},
		{
			name:        "address without CIDR suffix rejected",
			iface:       "eth0",
			body:        `{"addresses":["192.168.1.10"]}`,
			wantErrCode: "INVALID_VALUE",
		},
		{
			name:        "IPv6 gateway4 rejected",
			iface:       "eth0",
			body:        `{"gateway4":"fd00::1"}`,
			wantErrCode: "INVALID_VALUE",
		},
		{
			name:        "IPv4 gateway6 without colon rejected",
			iface:       "eth0",
			body:        `{"gateway6":"1.2.3.4"}`,
			wantErrCode: "INVALID_VALUE",
		},
		{
			name:        "non-IP DNS entry rejected",
			iface:       "eth0",
			body:        `{"dns":["not-ip"]}`,
			wantErrCode: "INVALID_VALUE",
		},
		{
			name:        "MTU below 576 rejected",
			iface:       "eth0",
			body:        `{"mtu":575}`,
			wantErrCode: "INVALID_VALUE",
		},
		{
			name:        "MTU above 9216 rejected",
			iface:       "eth0",
			body:        `{"mtu":9217}`,
			wantErrCode: "INVALID_VALUE",
		},
		{
			name:        "malformed JSON body rejected",
			iface:       "eth0",
			body:        `{"mtu":`,
			wantErrCode: "INVALID_REQUEST",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doConfigureInterface(t, tc.iface, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantErrCode) {
				t.Errorf("body = %s, want error code %q", rec.Body.String(), tc.wantErrCode)
			}
		})
	}
}

// MockCommander keys outputs by command name only (mock.go), so both
// `netplan generate` and `netplan apply` share the "netplan" result — a
// "generate succeeds, apply fails" scenario cannot be simulated. Only the
// all-success and generate-failure paths are covered.
func TestApplyNetplan(t *testing.T) {
	newCtx := func() (echo.Context, *httptest.ResponseRecorder) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/network/apply", nil)
		rec := httptest.NewRecorder()
		return e.NewContext(req, rec), rec
	}

	t.Run("generate then apply succeed", func(t *testing.T) {
		m := exec.NewMockCommander()
		h := &Handler{Cmd: m}
		c, rec := newCtx()
		if err := h.ApplyNetplan(c); err != nil {
			t.Fatalf("ApplyNetplan: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if len(m.Calls) != 2 {
			t.Fatalf("expected 2 netplan calls, got %d: %+v", len(m.Calls), m.Calls)
		}
		wantGen := exec.MockCall{Name: "netplan", Args: []string{"generate"}}
		wantApply := exec.MockCall{Name: "netplan", Args: []string{"apply"}}
		if !reflect.DeepEqual(m.Calls[0], wantGen) {
			t.Errorf("call 0 = %+v, want %+v", m.Calls[0], wantGen)
		}
		if !reflect.DeepEqual(m.Calls[1], wantApply) {
			t.Errorf("call 1 = %+v, want %+v", m.Calls[1], wantApply)
		}
	})

	t.Run("generate failure stops before apply", func(t *testing.T) {
		m := exec.NewMockCommander()
		m.SetOutput("netplan", "error: bad yaml", errors.New("exit 1"))
		h := &Handler{Cmd: m}
		c, rec := newCtx()
		if err := h.ApplyNetplan(c); err != nil {
			t.Fatalf("ApplyNetplan: %v", err)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "NETPLAN_ERROR") {
			t.Errorf("body = %s, want NETPLAN_ERROR", rec.Body.String())
		}
		if len(m.Calls) != 1 {
			t.Errorf("expected 1 call (generate only), got %d: %+v", len(m.Calls), m.Calls)
		}
	})
}
