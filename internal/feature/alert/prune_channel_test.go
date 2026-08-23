package alert

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openPruneTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE alert_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, channel_ids TEXT NOT NULL DEFAULT '[]')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func rulesChannels(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query("SELECT name, channel_ids FROM alert_rules ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, ids string
		if err := rows.Scan(&name, &ids); err != nil {
			t.Fatal(err)
		}
		out[name] = ids
	}
	return out
}

func prune(t *testing.T, db *sql.DB, id int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneChannelFromRules(tx, id); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The reason this is a decode-filter-encode and not an UPDATE with a string
// replace: deleting channel 1 out of [1,12] must not leave [2].
func TestPruneChannelDoesNotMatchSubstrings(t *testing.T) {
	db := openPruneTestDB(t)
	db.Exec(`INSERT INTO alert_rules (name, channel_ids) VALUES
		('two-digit', '[1,12]'), ('only-twelve', '[12]'), ('only-one', '[1]')`)

	prune(t, db, 1)

	got := rulesChannels(t, db)
	if got["two-digit"] != "[12]" {
		t.Errorf("two-digit = %s, want [12] — channel 12 must survive deleting channel 1", got["two-digit"])
	}
	if got["only-twelve"] != "[12]" {
		t.Errorf("only-twelve = %s, want [12] (untouched)", got["only-twelve"])
	}
	if got["only-one"] != "[]" {
		t.Errorf("only-one = %s, want []", got["only-one"])
	}
}

// A rule left with no channels is the state that used to be invisible: it
// reported itself Active and reached nobody. Pruning must produce an empty
// array, not null and not a rule that still names the dead channel.
func TestPruneChannelEmptiesARuleThatOnlyHadIt(t *testing.T) {
	db := openPruneTestDB(t)
	db.Exec(`INSERT INTO alert_rules (name, channel_ids) VALUES ('lonely', '[7]')`)

	prune(t, db, 7)

	if got := rulesChannels(t, db)["lonely"]; got != "[]" {
		t.Errorf("lonely = %s, want []", got)
	}
}

func TestPruneChannelLeavesUnparseableRulesAlone(t *testing.T) {
	db := openPruneTestDB(t)
	db.Exec(`INSERT INTO alert_rules (name, channel_ids) VALUES ('broken', 'not json'), ('fine', '[3,4]')`)

	prune(t, db, 3)

	got := rulesChannels(t, db)
	if got["broken"] != "not json" {
		t.Errorf("broken = %s; a row that will not parse must be left as it was", got["broken"])
	}
	if got["fine"] != "[4]" {
		t.Errorf("fine = %s, want [4]", got["fine"])
	}
}

func TestPruneChannelIsANoOpWhenNobodyReferencesIt(t *testing.T) {
	db := openPruneTestDB(t)
	db.Exec(`INSERT INTO alert_rules (name, channel_ids) VALUES ('a', '[1,2]')`)

	prune(t, db, 99)

	if got := rulesChannels(t, db)["a"]; got != "[1,2]" {
		t.Errorf("a = %s, want [1,2] unchanged", got)
	}
}
