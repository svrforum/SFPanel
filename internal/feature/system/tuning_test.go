package system

import (
	"fmt"
	"strings"
	"testing"
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
