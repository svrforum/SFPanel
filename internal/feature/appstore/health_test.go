package appstore

import "testing"

func TestClassifyComposeHealth(t *testing.T) {
	ndjson := `{"Service":"app","State":"running","Health":"healthy"}
{"Service":"db","State":"running","Health":""}`
	if got := classifyComposeHealth(ndjson); got != "healthy" {
		t.Errorf("ndjson all-running=%q want healthy", got)
	}
	arr := `[{"Service":"app","State":"running","Health":"starting"}]`
	if got := classifyComposeHealth(arr); got != "starting" {
		t.Errorf("array starting=%q want starting", got)
	}
	if got := classifyComposeHealth(`[{"Service":"app","State":"restarting","Health":""}]`); got != "starting" {
		t.Errorf("restarting=%q want starting", got)
	}
	if got := classifyComposeHealth(""); got != "unknown" {
		t.Errorf("empty=%q want unknown", got)
	}
	if got := classifyComposeHealth(`[{"Service":"app","State":"exited","Health":""}]`); got != "starting" {
		t.Errorf("exited=%q want starting", got)
	}
}
