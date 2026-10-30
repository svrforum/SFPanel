package featurecluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// getOverview answers GET /cluster/overview and returns its data.
func getOverview(t *testing.T, h *Handler) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := h.GetOverview(echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/cluster/overview", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("overview: %d %v (body %s)", rec.Code, err, rec.Body.String())
	}
	return out.Data
}

// The leader alone asks GitHub for the latest release, so its overview is
// where the page learns which nodes are behind; until the leader has asked,
// the field is left out rather than sent empty.
func TestTheLeadersOverviewCarriesTheLatestRelease(t *testing.T) {
	h := &Handler{Manager: tokenLeader(t)}
	orig := latestRelease
	t.Cleanup(func() { latestRelease = orig })

	latestRelease = func() string { return "" }
	if v, ok := getOverview(t, h)["latest_version"]; ok {
		t.Fatalf("an overview before the first check carries latest_version %v", v)
	}
	latestRelease = func() string { return "0.80.0" }
	if v := getOverview(t, h)["latest_version"]; v != "0.80.0" {
		t.Fatalf("latest_version = %v, want 0.80.0", v)
	}
}
