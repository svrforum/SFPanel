package system

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	commonExec "github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
)

// Distributions raise kernel defaults over time. A fixed "recommended" value
// then becomes a downgrade — Ubuntu ships vm.max_map_count at 1048576 on
// current kernels, four times the figure a 2020-era tuning guide gives.
func TestKeepCurrentIfBetter(t *testing.T) {
	cases := []struct {
		key, current, recommended, want, why string
	}{
		{"vm.max_map_count", "1048576", "262144", "1048576",
			"the kernel default already exceeds the recommendation"},
		{"vm.max_map_count", "65530", "262144", "262144",
			"below the recommendation, so raise it"},
		{"vm.min_free_kbytes", "262144", "65536", "262144",
			"shrinking the emergency reserve costs OOM headroom"},
		{"kernel.unprivileged_bpf_disabled", "2", "1", "2",
			"2 is stricter than 1, and the kernel refuses to lower it"},
		{"net.core.somaxconn", "65535", "65535", "65535",
			"equal values stay put"},
		// Keys not on the list keep the recommendation, whichever way it goes.
		{"vm.swappiness", "100", "10", "10",
			"swappiness is not monotonic — lower is not simply worse"},
		{"net.ipv4.tcp_fastopen", "0", "3", "3", "plain toggle"},
		// Unparseable input must not silently drop the recommendation.
		{"vm.max_map_count", "", "262144", "262144", "unreadable current value"},
		{"net.ipv4.ip_local_port_range", "32768\t60999", "10240 65535", "10240 65535",
			"non-numeric pair is not comparable"},
	}
	for _, tc := range cases {
		got := keepCurrentIfBetter(tc.key, tc.current, tc.recommended)
		if got != tc.want {
			t.Errorf("keepCurrentIfBetter(%s, %q, %q) = %q, want %q — %s",
				tc.key, tc.current, tc.recommended, got, tc.want, tc.why)
		}
	}
}

// rp_filter must stay in loose mode. Strict mode drops packets whose reply
// would leave by a different interface, which is normal on a host running
// Docker bridges or a Tailscale subnet route — and is how a working peer
// silently becomes unreachable.
func TestReversePathFilterStaysLoose(t *testing.T) {
	for _, cat := range buildRecommendations(4, 16, 16<<30) {
		for _, p := range cat.Params {
			if !strings.Contains(p.Key, "rp_filter") {
				continue
			}
			if p.Recommended != "2" {
				t.Errorf("%s recommends %q; strict mode breaks asymmetric routing on Docker/Tailscale hosts",
					p.Key, p.Recommended)
			}
		}
	}
}

// The low end of the ephemeral range must stay clear of registered service
// ports, or an outbound connection made early in boot can squat on the port a
// service is about to bind.
func TestEphemeralPortRangeAvoidsServicePorts(t *testing.T) {
	for _, cat := range buildRecommendations(4, 16, 16<<30) {
		for _, p := range cat.Params {
			if p.Key != "net.ipv4.ip_local_port_range" {
				continue
			}
			var lo, hi int
			if _, err := fmt.Sscanf(p.Recommended, "%d %d", &lo, &hi); err != nil {
				t.Fatalf("unparseable port range %q: %v", p.Recommended, err)
			}
			if lo < 10000 {
				t.Errorf("ephemeral range starts at %d; that overlaps registered service ports", lo)
			}
			if hi <= lo {
				t.Errorf("port range %q is inverted", p.Recommended)
			}
		}
	}
}

// Proxmox VE ships bridge-nf-call-* off so the guests' bridged frames bypass
// the host's iptables; recommending them on (and ip_forward, and conntrack
// sizes its firewall owns) fought Proxmox for every VM's traffic. On a
// Proxmox host those keys are never recommended, so Apply cannot write them.
func TestProxmoxKeepsForwardingBridgeFilteringAndConntrack(t *testing.T) {
	platform.SetForTest(t, platform.Info{PVE: true, NetworkStack: platform.StackIfupdown2})
	for _, cat := range buildRecommendations(8, 64, 64<<30) {
		if cat.Name == "conntrack" {
			t.Errorf("conntrack category recommended on Proxmox: %+v", cat.Params)
		}
		for _, p := range cat.Params {
			if p.Key == "net.ipv4.ip_forward" || strings.HasPrefix(p.Key, "net.bridge.") || strings.HasPrefix(p.Key, "net.netfilter.") {
				t.Errorf("%s recommended on Proxmox", p.Key)
			}
		}
	}

	// Off Proxmox, forwarding stays recommended for Docker.
	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	found := false
	for _, cat := range buildRecommendations(8, 64, 64<<30) {
		for _, p := range cat.Params {
			found = found || p.Key == "net.ipv4.ip_forward"
		}
	}
	if !found {
		t.Error("net.ipv4.ip_forward no longer recommended off Proxmox")
	}
}

func TestTuningStatusShowsWhatProxmoxManages(t *testing.T) {
	status := func() []TuningParam {
		t.Helper()
		rec := httptest.NewRecorder()
		h := &TuningHandler{Cmd: commonExec.NewMockCommander()}
		if err := h.GetTuningStatus(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/system/tuning", nil), rec)); err != nil {
			t.Fatal(err)
		}
		var got struct {
			Data struct {
				Managed []TuningParam `json:"managed"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got.Data.Managed
	}

	platform.SetForTest(t, platform.Info{PVE: true, NetworkStack: platform.StackIfupdown2})
	managed := status()
	// ip_forward exists on every kernel, so it is always listed, with the
	// value the host has.
	var forward *TuningParam
	for i := range managed {
		if managed[i].Key == "net.ipv4.ip_forward" {
			forward = &managed[i]
		}
	}
	if forward == nil || (forward.Current != "0" && forward.Current != "1") || forward.Recommended != "" {
		t.Errorf("managed = %+v, want ip_forward with its current value and no recommendation", managed)
	}

	platform.SetForTest(t, platform.Info{NetworkStack: platform.StackNetplan})
	if managed := status(); len(managed) != 0 {
		t.Errorf("managed off Proxmox = %+v, want none", managed)
	}
}
