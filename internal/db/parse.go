package db

import "regexp"

// Parsers used by the migration backfill bridge to figure out what
// pre-versioned hosts already have. Kept here rather than inline so
// migrations.go reads as just the schema log + transaction wiring.

var (
	createTableRe = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\b`)
	createIndexRe = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\b`)
	alterAddRe    = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+(\w+)\b`)
)

// extractCreateTarget returns (objectName, kind, true) for CREATE TABLE / CREATE INDEX.
// kind is "table" or "index" (matching sqlite_master.type values).
func extractCreateTarget(stmt string) (string, string, bool) {
	if m := createTableRe.FindStringSubmatch(stmt); m != nil {
		return m[1], "table", true
	}
	if m := createIndexRe.FindStringSubmatch(stmt); m != nil {
		return m[1], "index", true
	}
	return "", "", false
}

// extractAlterAddColumn returns (table, column, true) for ALTER TABLE … ADD COLUMN.
func extractAlterAddColumn(stmt string) (string, string, bool) {
	m := alterAddRe.FindStringSubmatch(stmt)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}
