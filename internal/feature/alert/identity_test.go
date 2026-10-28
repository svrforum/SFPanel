package alert

import (
	"context"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/monitor"
)

type fixedIdentity string

func (f fixedIdentity) LocalNodeID() string { return string(f) }

// A rule scoped to other nodes applied everywhere on a node that became a
// cluster member without a restart: its identity stayed nil, "every rule".
func TestSetIdentityScopesRulesFromThen(t *testing.T) {
	m := NewManagerWithIdentity(nil, nil)
	if !ruleAppliesToNode(m.currentIdentity(), "specific", `["n2"]`) {
		t.Fatal("without an identity every rule applies")
	}
	m.SetIdentity(fixedIdentity("n1"))
	if ruleAppliesToNode(m.currentIdentity(), "specific", `["n2"]`) {
		t.Fatal("a rule for n2 applies on n1")
	}
	d := NewContainerDispatcherWithIdentity(nil, nil, nil)
	d.SetIdentity(fixedIdentity("n1"))
	if ruleAppliesToNode(d.currentIdentity(), "specific", `["n2"]`) {
		t.Fatal("a container rule for n2 applies on n1")
	}
}

// The identity SetIdentity gives is the one both firing paths scope by: the
// periodic host-metric pass and the container events. Rules for n1 fire in
// the same pass as a control, so n2's staying quiet is the scope's doing and
// not a pass that fired nothing.
func TestSetIdentityScopesTheRulesThatFire(t *testing.T) {
	if _, err := monitor.GetMetrics(); err != nil {
		t.Fatalf("host metrics unreadable here, so no host rule can fire: %v", err)
	}
	db := openMigratedDB(t)
	db.SetMaxOpenConns(4)
	scoped := func(name, typ, condition, node string) int {
		id := insertRule(t, db, name, typ, condition, 1)
		if _, err := db.Exec(`UPDATE alert_rules SET node_scope='specific', node_ids=? WHERE id=?`, `["`+node+`"]`, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	const anyCPU, anyContainer = `{"operator":">","threshold":-1}`, `{"container_pattern":"*"}`
	names := map[int]string{
		scoped("cpu-n1", "cpu", anyCPU, "n1"):                   "cpu-n1",
		scoped("cpu-n2", "cpu", anyCPU, "n2"):                   "cpu-n2",
		scoped("down-n1", "container_down", anyContainer, "n1"): "down-n1",
		scoped("down-n2", "container_down", anyContainer, "n2"): "down-n2",
	}
	// One pass of each path, on a manager and dispatcher built as a node
	// that booted standalone builds them, after set has run on them. Fresh
	// each time: a fire left queued holds its rule's slot, and a second pass
	// on the same manager would stay quiet for that reason alone.
	fired := func(set func(*Manager, *ContainerDispatcher)) map[string]bool {
		m := NewManagerWithIdentity(db, nil) // not started: fires wait in its queue
		d := NewContainerDispatcherWithIdentity(db, m, nil)
		set(m, d)
		m.evaluate()
		d.Dispatch(context.Background(), &AlertContainerEvent{ID: "c1", Name: "app", Type: "die", TS: time.Now().UnixMilli()})
		got := map[string]bool{}
		for {
			select {
			case f := <-m.fireQueue:
				got[names[f.RuleID]] = true
			default:
				return got
			}
		}
	}

	standalone := fired(func(*Manager, *ContainerDispatcher) {})
	if len(standalone) != 4 {
		t.Fatalf("with no identity every rule fires; fired %v", standalone)
	}
	member := fired(func(m *Manager, d *ContainerDispatcher) {
		m.SetIdentity(fixedIdentity("n1"))
		d.SetIdentity(fixedIdentity("n1"))
	})
	if len(member) != 2 || !member["cpu-n1"] || !member["down-n1"] {
		t.Fatalf("on n1 only n1's rules fire; fired %v", member)
	}
}
