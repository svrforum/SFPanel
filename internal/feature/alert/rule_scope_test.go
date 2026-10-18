package alert

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// scopedRuleBody is a cpu rule as the page posts it, with the scope given,
// and a threshold every reading passes.
func scopedRuleBody(name, scope, nodeIDs string) string {
	body, _ := json.Marshal(map[string]any{
		"name": name, "type": "cpu", "condition": `{"operator":">","threshold":-1}`, "channel_ids": "[1]",
		"severity": "warning", "cooldown": 300, "node_scope": scope, "node_ids": nodeIDs, "enabled": true,
	})
	return string(body)
}

// TestCreateRule_NoScopeIsAcceptedAndNeverFires: the form offers Specific
// Nodes, and with no node picked it saved node_scope "specific" and node_ids
// "[]". In a cluster ruleAppliesToNode skips such a rule on every node, so it
// listed itself as Active and never fired. A scope it does not know, or a
// node list that will not parse, is skipped the same way. Every scope has to
// fire on the node it names, or be refused for that reason.
func TestCreateRule_NoScopeIsAcceptedAndNeverFires(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	cases := []struct{ name, scope, nodeIDs string }{
		{"all", "all", "[]"},
		{"the node itself", "specific", `["node-a"]`},
		{"no node", "specific", "[]"},
		{"a node list that does not parse", "specific", "node-a"},
		{"a scope nothing knows", "everywhere", "[]"},
	}
	created := map[int]string{}
	var refused []string
	for _, tc := range cases {
		rec := callJSON(t, h.CreateRule, http.MethodPost, scopedRuleBody(tc.name, tc.scope, tc.nodeIDs), 0)
		if rec.Code == http.StatusOK {
			created[createdID(t, rec)] = tc.name
			continue
		}
		refused = append(refused, tc.name)
		if e := failure(t, rec); rec.Code != http.StatusBadRequest || e.Code != response.ErrInvalidValue || e.Message != nodeScopeRefusal {
			t.Errorf("%s: refused with %d %s %q, want 400 %s %q", tc.name, rec.Code, e.Code, e.Message, response.ErrInvalidValue, nodeScopeRefusal)
		}
	}

	fired := fireEverything(t, db, fakeNodeIdentity{id: "node-a"})
	for id, name := range created {
		if !fired[id] {
			t.Errorf("%s: CreateRule stored it, and it fired on no node", name)
		}
	}
	if want := []string{"no node", "a node list that does not parse", "a scope nothing knows"}; !reflect.DeepEqual(refused, want) {
		t.Errorf("refused %v, want %v", refused, want)
	}
}

// TestUpdateRule_RefusesAScopeThatNamesNoNode: an update can no more set such
// a scope than a create can. One that leaves the scope alone still goes
// through on a rule stored with one before, so it can still be switched off.
func TestUpdateRule_RefusesAScopeThatNamesNoNode(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	id := insertRule(t, db, "cpu-high", "cpu", `{"operator":">","threshold":90}`, 1)
	for _, body := range []string{
		`{"node_scope":"specific"}`,
		`{"node_scope":"specific","node_ids":"[]"}`,
		`{"node_scope":"specific","node_ids":"node-a"}`,
		`{"node_scope":"everywhere"}`,
	} {
		rec := callJSON(t, h.UpdateRule, http.MethodPut, body, id)
		if e := failure(t, rec); rec.Code != http.StatusBadRequest || e.Code != response.ErrInvalidValue || e.Message != nodeScopeRefusal {
			t.Errorf("%s: refused with %d %s %q, want 400 %s %q", body, rec.Code, e.Code, e.Message, response.ErrInvalidValue, nodeScopeRefusal)
		}
	}
	var scope, nodeIDs string
	if err := db.QueryRow("SELECT node_scope, node_ids FROM alert_rules WHERE id=?", id).Scan(&scope, &nodeIDs); err != nil || scope != "all" || nodeIDs != "[]" {
		t.Errorf("scope became %q %q (%v)", scope, nodeIDs, err)
	}

	if rec := callJSON(t, h.UpdateRule, http.MethodPut, `{"node_scope":"specific","node_ids":"[\"node-a\"]"}`, id); rec.Code != http.StatusOK {
		t.Errorf("scoping to a node: status %d: %s", rec.Code, rec.Body)
	}

	if _, err := db.Exec(`UPDATE alert_rules SET node_scope='specific', node_ids='[]' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if rec := callJSON(t, h.UpdateRule, http.MethodPut, `{"enabled":false}`, id); rec.Code != http.StatusOK {
		t.Errorf("switching off a rule stored with no node: status %d: %s", rec.Code, rec.Body)
	}
}
