package alert

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// lanScan is a stored scan with something of every kind in it.
var lanScan = &AlertLanEvent{
	Network:    "10.20.30.0/24",
	FinishedAt: time.Date(2026, 11, 22, 4, 0, 0, 0, time.UTC),
	New: []LanSeenDevice{
		{IP: "10.20.30.40", MAC: "02:42:ac:14:1e:28"},
		{IP: "10.20.30.51", MAC: "02:42:ac:14:1e:33", Vendor: "Espressif", Hostname: "plug.lan"},
		{IP: "10.20.30.52", MAC: "02:42:ac:14:1e:34", Hostname: "phone.lan"},
	},
	Down: []LanKnownDevice{
		{MAC: "02:42:ac:14:1e:32", Name: "Front door", IP: "10.20.30.50", Vendor: "Example Electronics", Hostname: "door.lan", LastSeen: "2026-11-22T02:13:08Z"},
		{MAC: "02:42:ac:14:1e:35", IP: "10.20.30.53", Hostname: "printer.lan", LastSeen: "2026-11-22T01:00:00Z"},
		{MAC: "02:42:ac:14:1e:36", IP: "10.20.30.54", LastSeen: "2026-11-21T23:59:59Z"},
	},
	Back: []LanKnownDevice{
		{MAC: "02:42:ac:14:1e:37", Name: "Garage", IP: "10.20.30.55", LastSeen: "2026-11-22T04:00:00Z"},
		{MAC: "02:42:ac:14:1e:38", IP: "10.20.30.56", LastSeen: "2026-11-22T04:00:00Z"},
	},
	Conflicts: []LanAddressConflict{
		{IP: "10.20.30.12", MACs: []string{"02:42:ac:14:1e:4d"}, Vendor: "Example Electronics", Certain: true},
		{IP: "10.20.30.14", MACs: []string{"02:42:ac:14:1e:4e"}, Certain: true},
		{IP: "10.20.30.13", MACs: []string{"02:42:ac:14:1e:0d", "02:42:ac:14:1e:4f"}},
		{IP: "10.20.30.15", MACs: []string{"02:42:ac:14:1e:0f", "02:42:ac:14:1e:5a", "02:42:ac:14:1e:5b"}},
	},
}

type firing struct{ rule, subject, message string }

// Each LAN rule type fires once per thing of its kind in the scan, with the
// subject its cooldown runs per and the message; with its own severity. A
// rule switched off, or scoped to another node, fires nothing, and a rule of
// another type is not a LAN rule's business.
func TestLanDispatcher_FiresEachRuleType(t *testing.T) {
	db := openMigratedDB(t)
	rule := func(name, typ, severity string, enabled int, nodes string) {
		t.Helper()
		id := insertRule(t, db, name, typ, `{}`, enabled)
		if _, err := db.Exec(`UPDATE alert_rules SET severity=? WHERE id=?`, severity, id); err != nil {
			t.Fatal(err)
		}
		if nodes != "" {
			if _, err := db.Exec(`UPDATE alert_rules SET node_scope='specific', node_ids=? WHERE id=?`, nodes, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	rule("new", "lan_new_device", "info", 1, "")
	rule("conflict", "lan_address_conflict", "critical", 1, `["node-a"]`)
	rule("down", "lan_device_down", "warning", 1, "")
	rule("new-off", "lan_new_device", "info", 0, "")
	rule("down-elsewhere", "lan_device_down", "warning", 1, `["node-b"]`)
	insertRule(t, db, "containers", "container_down", `{"container_pattern":"*"}`, 1)
	insertRule(t, db, "cpu", "cpu", `{"operator":">","threshold":-1}`, 1)

	rec := &fireRecorder{}
	NewLanDispatcherWithIdentity(db, rec, fakeNodeIdentity{id: "node-a"}).Dispatch(context.Background(), lanScan)

	var got []firing
	severity := map[string]string{}
	for _, f := range rec.fires {
		got = append(got, firing{f.RuleName, f.Subject, f.Message})
		if f.Type == "" || f.ChannelIDs != "[1]" || f.Cooldown != 300 {
			t.Errorf("%s: fired as %+v, without the rule's type, channels or cooldown", f.RuleName, f)
		}
		severity[f.RuleName] = f.Severity
	}
	want := []firing{
		{"new", "02:42:ac:14:1e:28", "New device on 10.20.30.0/24: 10.20.30.40 (02:42:ac:14:1e:28)"},
		{"new", "02:42:ac:14:1e:33", "New device on 10.20.30.0/24: 10.20.30.51 (02:42:ac:14:1e:33, Espressif, plug.lan)"},
		{"new", "02:42:ac:14:1e:34", "New device on 10.20.30.0/24: 10.20.30.52 (02:42:ac:14:1e:34, phone.lan)"},
		{"conflict", "10.20.30.0/24/10.20.30.12", "Address conflict on 10.20.30.0/24: 10.20.30.12 is a server's address, and 02:42:ac:14:1e:4d (Example Electronics) answered on it."},
		{"conflict", "10.20.30.0/24/10.20.30.14", "Address conflict on 10.20.30.0/24: 10.20.30.14 is a server's address, and 02:42:ac:14:1e:4e answered on it."},
		{"conflict", "10.20.30.0/24/10.20.30.13", "Suspected address conflict on 10.20.30.0/24: 10.20.30.13 was held by 02:42:ac:14:1e:0d and 02:42:ac:14:1e:4f over the same period."},
		{"conflict", "10.20.30.0/24/10.20.30.15", "Suspected address conflict on 10.20.30.0/24: 10.20.30.15 was held by 02:42:ac:14:1e:0f, 02:42:ac:14:1e:5a and 02:42:ac:14:1e:5b over the same period."},
		{"down", "02:42:ac:14:1e:32", "Front door (10.20.30.50, 02:42:ac:14:1e:32) was not found by the last 2 scans of 10.20.30.0/24. Last seen 2026-11-22 02:13 UTC."},
		{"down", "02:42:ac:14:1e:35", "printer.lan (10.20.30.53, 02:42:ac:14:1e:35) was not found by the last 2 scans of 10.20.30.0/24. Last seen 2026-11-22 01:00 UTC."},
		{"down", "02:42:ac:14:1e:36", "02:42:ac:14:1e:36 (10.20.30.54) was not found by the last 2 scans of 10.20.30.0/24. Last seen 2026-11-21 23:59 UTC."},
		{"down", "02:42:ac:14:1e:37/back", "Garage (10.20.30.55) is back on 10.20.30.0/24."},
		{"down", "02:42:ac:14:1e:38/back", "02:42:ac:14:1e:38 (10.20.30.56) is back on 10.20.30.0/24."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fired\n  %v\nwant\n  %v", fmtFirings(got), fmtFirings(want))
	}
	if want := map[string]string{"new": "info", "conflict": "critical", "down": "warning"}; !reflect.DeepEqual(severity, want) {
		t.Errorf("severities %v, want each rule's own %v", severity, want)
	}
}

func fmtFirings(list []firing) string {
	var b strings.Builder
	for _, f := range list {
		b.WriteString("\n  " + f.rule + " [" + f.subject + "] " + f.message)
	}
	return b.String()
}

// The cooldown runs per rule and subject, through the manager: two new
// devices are two alerts; the same conflict in two scans is one; a device
// down and then back is two, the second not held back by the first.
func TestLanDispatcher_CooldownPerSubject(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		scans []*AlertLanEvent
		want  []string
	}{
		{"two new devices", "lan_new_device", []*AlertLanEvent{{
			Network: "10.20.30.0/24",
			New:     []LanSeenDevice{{IP: "10.20.30.40", MAC: "02:42:ac:14:1e:28"}, {IP: "10.20.30.41", MAC: "02:42:ac:14:1e:29"}},
		}}, []string{
			"New device on 10.20.30.0/24: 10.20.30.40 (02:42:ac:14:1e:28)",
			"New device on 10.20.30.0/24: 10.20.30.41 (02:42:ac:14:1e:29)",
		}},
		{"the same conflict in two scans", "lan_address_conflict", []*AlertLanEvent{
			{Network: "10.20.30.0/24", FinishedAt: lanScan.FinishedAt, Conflicts: lanScan.Conflicts[2:3]},
			{Network: "10.20.30.0/24", FinishedAt: lanScan.FinishedAt.Add(15 * time.Minute), Conflicts: lanScan.Conflicts[2:3]},
		}, []string{
			"Suspected address conflict on 10.20.30.0/24: 10.20.30.13 was held by 02:42:ac:14:1e:0d and 02:42:ac:14:1e:4f over the same period.",
		}},
		{"down, then back", "lan_device_down", []*AlertLanEvent{
			{Network: "10.20.30.0/24", Down: lanScan.Down[:1]},
			{Network: "10.20.30.0/24", Back: []LanKnownDevice{{MAC: "02:42:ac:14:1e:32", Name: "Front door", IP: "10.20.30.50"}}},
		}, []string{
			"Front door (10.20.30.50, 02:42:ac:14:1e:32) was not found by the last 2 scans of 10.20.30.0/24. Last seen 2026-11-22 02:13 UTC.",
			"Front door (10.20.30.50) is back on 10.20.30.0/24.",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedDB(t)
			insertRule(t, db, tc.name, tc.typ, `{}`, 1)
			m := NewManager(db)
			var delivered []string
			m.deliver = func(f AlertFire) []string {
				delivered = append(delivered, f.Message)
				return []string{"stub"}
			}
			d := NewLanDispatcher(db, m)
			for _, ev := range tc.scans {
				d.Dispatch(context.Background(), ev)
				// Each fire is sent before the next scan, as the workers would.
				for drained := false; !drained; {
					select {
					case f := <-m.fireQueue:
						m.runFire(f)
					default:
						drained = true
					}
				}
			}
			if !reflect.DeepEqual(delivered, tc.want) {
				t.Errorf("delivered\n  %s\nwant\n  %s", strings.Join(delivered, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}
