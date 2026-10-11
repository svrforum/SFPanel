package featurecluster

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// The orchestrator's update request has to do two things at once: carry
// force=true past system/update's quorum guard, and still authenticate as an
// internal request. The MAC covers the query, so a header signed for the bare
// path would be rejected for the forced one — the node would then answer 401
// instead of updating, a failure that looks nothing like its cause.
func TestOrchestratedUpdateIsForcedAndSignedAsSent(t *testing.T) {
	u, err := url.Parse(orchestratedUpdatePath)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("force") != "true" {
		t.Fatalf("%q does not bypass the quorum guard: it would refuse the old leader of a two-node cluster", orchestratedUpdatePath)
	}

	auth.SetClusterProxySecret("orchestrated-update-test")
	defer auth.SetClusterProxySecret("")

	req := httptest.NewRequest("POST", "https://127.0.0.1:3628"+orchestratedUpdatePath, nil)
	req.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2("POST", orchestratedUpdatePath))
	if !auth.IsInternalProxyRequest(req) {
		t.Fatal("a header signed for orchestratedUpdatePath is not accepted for it")
	}

	// The pitfall the constant exists to prevent: signing the path without
	// its query.
	bare := httptest.NewRequest("POST", "https://127.0.0.1:3628"+orchestratedUpdatePath, nil)
	bare.Header.Set(auth.InternalProxyHeaderV2, auth.SignProxyRequestV2("POST", u.Path))
	if auth.IsInternalProxyRequest(bare) {
		t.Fatal("a header signed without the query was accepted; the query is no longer covered by the MAC")
	}
}

// A rolling update waited for each node with nothing but its heartbeat
// status, which stays online for a few seconds after the update is
// triggered. On a live 2-node cluster the orchestrator declared the follower
// back while its old process was still shutting down, then handed it
// leadership and restarted itself. Back means online on another version.
func TestBackOnNewVersion(t *testing.T) {
	metrics := func(version string) []*cluster.NodeMetrics {
		return []*cluster.NodeMetrics{{NodeID: "other", Version: "9.9.9"}, {NodeID: "node-b", Version: version}}
	}
	online := map[string]cluster.NodeStatus{"node-b": cluster.StatusOnline}
	for _, tc := range []struct {
		name    string
		health  map[string]cluster.NodeStatus
		metrics []*cluster.NodeMetrics
		before  string
		want    bool
	}{
		{"still online on the old version: not back yet", online, metrics("0.78.11"), "0.78.11", false},
		{"online on the new version", online, metrics("0.78.12"), "0.78.11", true},
		{"new version, but not online", map[string]cluster.NodeStatus{"node-b": cluster.StatusSuspect}, metrics("0.78.12"), "0.78.11", false},
		{"no metrics from it yet", online, []*cluster.NodeMetrics{{NodeID: "other", Version: "0.78.12"}}, "0.78.11", false},
		{"no version reported", online, metrics(""), "0.78.11", false},
		{"version before unknown: online will do", online, metrics("0.78.11"), "", true},
	} {
		if got := backOnNewVersion(tc.health, tc.metrics, "node-b", tc.before); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
