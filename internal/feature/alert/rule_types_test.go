package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/monitor"
)

// formRuleTypesUntil132 is every type the rule form offered up to v0.78.19, in
// its order. Nothing ever evaluated service, login or package.
var formRuleTypesUntil132 = []string{
	"cpu", "memory", "disk",
	"container_down", "container_oom", "container_restart_loop", "container_unhealthy",
	"service", "login", "package",
}

// listenerEvents are the event types monitor/docker_events.go hands the
// container dispatcher (normalizeAction).
var listenerEvents = []string{"start", "stop", "die", "oom", "kill", "restart", "healthy", "unhealthy"}

// ruleCreateBody is a rule as the page posts it, with a condition that fires
// on anything: a threshold below every reading, every container, one restart.
func ruleCreateBody(name, typ, severity string) string {
	condition := `{"operator":">","threshold":-1}`
	if strings.HasPrefix(typ, "container_") {
		condition = `{"container_pattern":"*","threshold_count":1,"window_seconds":300}`
	}
	body, _ := json.Marshal(map[string]any{
		"name": name, "type": typ, "condition": condition, "channel_ids": "[1]",
		"severity": severity, "cooldown": 300, "node_scope": "all", "node_ids": "[]", "enabled": true,
	})
	return string(body)
}

// fireEverything runs every rule in db past everything that can fire one: the
// periodic host-metric pass, and each event the docker listener forwards, for
// a container every pattern matches, on the node identity names (nil: a
// single node). It returns the ids of the rules that fired.
func fireEverything(t *testing.T, db *sql.DB, identity NodeIdentity) map[int]bool {
	t.Helper()
	if _, err := monitor.GetMetrics(); err != nil {
		t.Fatalf("host metrics unreadable here, so no host rule can fire: %v", err)
	}
	// Dispatch reads restart times while its rule rows are still open, which
	// takes a second connection: the panel's pool has four (internal/db).
	db.SetMaxOpenConns(4)
	m := NewManagerWithIdentity(db, identity) // not started: fires wait in its queue
	m.evaluate()
	d := NewContainerDispatcherWithIdentity(db, m, identity)
	for _, ev := range listenerEvents {
		// The listener stores an event before dispatching it, which is what
		// the restart-loop count reads.
		ts := time.Now().UnixMilli()
		if _, err := db.Exec(`INSERT INTO container_events (container_id, container_name, ts, event_type) VALUES ('c1', 'app', ?, ?)`, ts, ev); err != nil {
			t.Fatal(err)
		}
		d.Dispatch(context.Background(), &AlertContainerEvent{ID: "c1", Name: "app", Type: ev, TS: ts})
	}
	fired := map[int]bool{}
	for {
		select {
		case f := <-m.fireQueue:
			fired[f.RuleID] = true
		default:
			return fired
		}
	}
}

// createdID reads the id out of a CreateRule response.
func createdID(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var env struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.ID == 0 {
		t.Fatalf("no rule id in %s (%v)", rec.Body, err)
	}
	return env.Data.ID
}

// TestCreateRule_NoTypeIsAcceptedAndNeverFires is #132. The form offered
// Service, Login and Package, CreateRule stored whatever type it was given,
// and nothing evaluates those three: the rule was saved, listed itself as
// Active and never sent anything. Each type the form offered is created the
// way the page creates it and put in front of everything that can fire a
// rule. Every type has to fire, or be refused for that reason.
func TestCreateRule_NoTypeIsAcceptedAndNeverFires(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}

	created := map[int]string{}
	var refused []string
	for _, typ := range append(formRuleTypesUntil132, "made_up") {
		rec := callJSON(t, h.CreateRule, http.MethodPost, ruleCreateBody(typ+"-rule", typ, "warning"), 0)
		if rec.Code == http.StatusOK {
			created[createdID(t, rec)] = typ
			continue
		}
		refused = append(refused, typ)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 200 or 400: %s", typ, rec.Code, rec.Body)
		}
		if e := failure(t, rec); e.Code != response.ErrAlertRuleTypeNotEvaluated || e.Details["type"] != typ {
			t.Errorf("%s: refused with %s %q %v, want %s naming the type", typ, e.Code, e.Message, e.Details, response.ErrAlertRuleTypeNotEvaluated)
		}
	}

	fired := fireEverything(t, db, nil)
	for id, typ := range created {
		if !fired[id] {
			t.Errorf("%s: CreateRule stored it, and nothing that fires a rule fired it", typ)
		}
	}
	if want := []string{"service", "login", "package", "made_up"}; !reflect.DeepEqual(refused, want) {
		t.Errorf("refused %v, want %v", refused, want)
	}
}

// insertRule stores a rule row directly, the way an earlier version left it.
func insertRule(t *testing.T, db *sql.DB, name, typ, condition string, enabled int) int {
	t.Helper()
	res, err := db.Exec(`INSERT INTO alert_rules (name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids, enabled)
		VALUES (?, ?, ?, '[1]', 'warning', 300, 'all', '[]', ?)`, name, typ, condition, enabled)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(id)
}

// storedRule is the columns an update could change, as stored.
func storedRule(t *testing.T, db *sql.DB, id int) string {
	t.Helper()
	var name, typ, condition, severity string
	var enabled int
	if err := db.QueryRow("SELECT name, type, condition, severity, enabled FROM alert_rules WHERE id=?", id).
		Scan(&name, &typ, &condition, &severity, &enabled); err != nil {
		t.Fatalf("rule %d: %v", id, err)
	}
	b, _ := json.Marshal([]any{name, typ, condition, severity, enabled})
	return string(b)
}

// TestUpdateRule_ARuleNothingEvaluatesCanOnlyBeDeleted: rules of those types
// are still in the databases of the panels that offered them. They stay
// deletable and nothing more. Switched on, renamed or retyped, the rule would
// still never have sent anything, while looking as if it does.
func TestUpdateRule_ARuleNothingEvaluatesCanOnlyBeDeleted(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	// What the page stored for a Login rule: its threshold field, and enabled.
	id := insertRule(t, db, "failed-logins", "login", `{"operator":">","threshold":90}`, 0)
	before := storedRule(t, db, id)

	for _, body := range []string{`{"enabled":true}`, `{"enabled":false}`, `{"name":"renamed"}`, `{"type":"cpu"}`} {
		rec := callJSON(t, h.UpdateRule, http.MethodPut, body, id)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", body, rec.Code, rec.Body)
			continue
		}
		if e := failure(t, rec); e.Code != response.ErrAlertRuleTypeNotEvaluated || e.Details["type"] != "login" {
			t.Errorf("%s: refused with %s %q %v, want %s naming login", body, e.Code, e.Message, e.Details, response.ErrAlertRuleTypeNotEvaluated)
		}
	}
	if after := storedRule(t, db, id); after != before {
		t.Errorf("stored rule became %s, was %s", after, before)
	}

	if rec := callJSON(t, h.DeleteRule, http.MethodDelete, "", id); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d: %s", rec.Code, rec.Body)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM alert_rules WHERE id=?", id).Scan(&n); err != nil || n != 0 {
		t.Errorf("rule still there after delete (%d, %v)", n, err)
	}
}

// TestUpdateRule_RefusesATypeNothingEvaluates: and no rule can be changed into
// one, while an ordinary update still goes through.
func TestUpdateRule_RefusesATypeNothingEvaluates(t *testing.T) {
	db := openMigratedDB(t)
	h := &Handler{DB: db}
	id := insertRule(t, db, "cpu-high", "cpu", `{"operator":">","threshold":90}`, 1)
	before := storedRule(t, db, id)

	for _, typ := range []string{"service", "login", "package", "made_up", ""} {
		body, _ := json.Marshal(map[string]any{"type": typ})
		rec := callJSON(t, h.UpdateRule, http.MethodPut, string(body), id)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("type %q: status %d, want 400: %s", typ, rec.Code, rec.Body)
			continue
		}
		if e := failure(t, rec); e.Code != response.ErrAlertRuleTypeNotEvaluated || e.Details["type"] != typ {
			t.Errorf("type %q: refused with %s %q %v, want %s naming it", typ, e.Code, e.Message, e.Details, response.ErrAlertRuleTypeNotEvaluated)
		}
	}
	if after := storedRule(t, db, id); after != before {
		t.Errorf("stored rule became %s, was %s", after, before)
	}

	if rec := callJSON(t, h.UpdateRule, http.MethodPut, `{"enabled":false}`, id); rec.Code != http.StatusOK {
		t.Fatalf("disabling a cpu rule: status %d: %s", rec.Code, rec.Body)
	}
	var enabled int
	if err := db.QueryRow("SELECT enabled FROM alert_rules WHERE id=?", id).Scan(&enabled); err != nil || enabled != 0 {
		t.Errorf("enabled %d (%v) after disabling, want 0", enabled, err)
	}
}
