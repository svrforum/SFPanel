package featurecluster

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/svrforum/SFPanel/internal/auth"
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
