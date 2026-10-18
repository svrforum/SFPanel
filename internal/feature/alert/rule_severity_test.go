package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// fireRecorder keeps every fire it is handed.
type fireRecorder struct{ fires []AlertFire }

func (r *fireRecorder) Fire(_ context.Context, f AlertFire) { r.fires = append(r.fires, f) }

// TestRuleSeverity_ListedIsWhatItFiresWith: the form offered a severity for
// every type, and an OOM rule fires critical whatever it was given, so one
// created with the form's default listed itself as Warning and arrived as
// Critical. What a rule stores and lists is the severity it fires with.
func TestRuleSeverity_ListedIsWhatItFiresWith(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	// As the page creates them: the form's default severity, and one picked.
	oom := createdID(t, callJSON(t, h.CreateRule, http.MethodPost, ruleCreateBody("oom", "container_oom", "warning"), 0))
	down := createdID(t, callJSON(t, h.CreateRule, http.MethodPost, ruleCreateBody("down", "container_down", "info"), 0))
	// As an earlier version stored one.
	legacy := insertRule(t, db, "oom-before", "container_oom", `{"container_pattern":"*"}`, 1)

	stored := func(when string) {
		t.Helper()
		var sev string
		if err := db.QueryRow("SELECT severity FROM alert_rules WHERE id=?", oom).Scan(&sev); err != nil {
			t.Fatal(err)
		}
		if sev != "critical" {
			t.Errorf("%s, the OOM rule is stored with severity %q, and fires critical", when, sev)
		}
	}
	stored("created")
	if rec := callJSON(t, h.UpdateRule, http.MethodPut, `{"severity":"info"}`, oom); rec.Code != http.StatusOK {
		t.Fatalf("update: status %d: %s", rec.Code, rec.Body)
	}
	stored("updated to info")

	rec := callJSON(t, h.ListRules, http.MethodGet, "", 0)
	var env struct {
		Data []AlertRule `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("rules in %s: %v", rec.Body, err)
	}
	listed := map[int]string{}
	for _, r := range env.Data {
		listed[r.ID] = r.Severity
	}

	rr := &fireRecorder{}
	d := NewContainerDispatcher(db, rr)
	for _, ev := range []string{"oom", "die"} {
		d.Dispatch(context.Background(), &AlertContainerEvent{ID: "c1", Name: "app", Type: ev})
	}
	fired := map[int]string{}
	for _, f := range rr.fires {
		fired[f.RuleID] = f.Severity
	}

	for name, id := range map[string]int{"OOM rule": oom, "container_down rule": down, "OOM rule stored before": legacy} {
		if fired[id] == "" {
			t.Errorf("%s did not fire", name)
			continue
		}
		if listed[id] != fired[id] {
			t.Errorf("%s lists severity %q and fires %q", name, listed[id], fired[id])
		}
	}
	if fired[down] != "info" {
		t.Errorf("container_down rule fired %q, want the info it was given", fired[down])
	}
}
