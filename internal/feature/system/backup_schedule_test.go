package system

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	sfdb "github.com/svrforum/SFPanel/internal/db"
)

func TestBackupFileRe(t *testing.T) {
	valid := []string{
		"sfpanel-backup-20260603-101500.tar.gz",
		"sfpanel-backup-00000000-000000.tar.gz",
	}
	invalid := []string{
		"", "backup.tar.gz", "sfpanel-backup-2026-06-03.tar.gz",
		"../../etc/passwd", "sfpanel-backup-20260603-101500.tar.gz/../x",
		"sfpanel-backup-20260603-101500.zip", "sfpanel-backup-2026060-101500.tar.gz",
	}
	for _, s := range valid {
		if !backupFileRe.MatchString(s) {
			t.Errorf("expected %q to be a valid backup name", s)
		}
	}
	for _, s := range invalid {
		if backupFileRe.MatchString(s) {
			t.Errorf("expected %q to be rejected (traversal / wrong shape)", s)
		}
	}
}

func TestCreateBackupFile_ProducesValidArchive(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sfpanel.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(dbPath, []byte("dbdata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("cfgdata"), 0600); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(dir, "backups")
	// nil db: exercises the raw-file fallback (no live connection to snapshot).
	name, err := createBackupFile(nil, backupDir, dbPath, cfgPath, "")
	if err != nil {
		t.Fatalf("createBackupFile: %v", err)
	}
	if !backupFileRe.MatchString(name) {
		t.Errorf("created name %q does not match the expected pattern", name)
	}

	// No leftover .tmp file from the temp+rename write.
	if entries, _ := os.ReadDir(backupDir); len(entries) != 1 {
		t.Errorf("backup dir has %d entries, want exactly 1 (no .tmp leftover)", len(entries))
	}

	// The archive is a readable gzip+tar containing the db and config entries.
	f, err := os.Open(filepath.Join(backupDir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("archive is not valid gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		found[hdr.Name] = true
	}
	if !found["sfpanel.db"] || !found["config.yaml"] {
		t.Errorf("archive missing expected entries, got %v", found)
	}
}

// extractDBFromArchive pulls the "sfpanel.db" entry out of a backup tar.gz
// and writes it to a temp file, returning the path.
func extractDBFromArchive(t *testing.T, archivePath string) string {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("archive is not valid gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			t.Fatalf("archive missing sfpanel.db entry: %v", err)
		}
		if hdr.Name != "sfpanel.db" {
			continue
		}
		out := filepath.Join(t.TempDir(), "extracted.db")
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read sfpanel.db entry: %v", err)
		}
		if err := os.WriteFile(out, data, 0600); err != nil {
			t.Fatal(err)
		}
		return out
	}
}

// TestCreateBackupFile_WALRowsIncluded is the regression test for the
// WAL-consistency defect: rows committed to a live WAL-mode database sit in
// the -wal sidecar until a checkpoint, and the old plain os.Open+io.Copy
// backup silently omitted them. The VACUUM INTO snapshot must capture them.
func TestCreateBackupFile_WALRowsIncluded(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sfpanel.db")
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("cfgdata"), 0600); err != nil {
		t.Fatal(err)
	}

	db, err := sfdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open WAL db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE wal_probe (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	const rows = 10
	for i := 0; i < rows; i++ {
		if _, err := db.Exec(`INSERT INTO wal_probe (v) VALUES (?)`, fmt.Sprintf("row-%d", i)); err != nil {
			t.Fatalf("insert probe row: %v", err)
		}
	}
	// Precondition: the probe rows must still live in the -wal sidecar, or
	// this test would pass even with the broken plain-copy backup.
	if info, err := os.Stat(dbPath + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty -wal sidecar (precondition), info=%v err=%v", info, err)
	}

	backupDir := filepath.Join(dir, "backups")
	name, err := createBackupFile(db, backupDir, dbPath, cfgPath, "")
	if err != nil {
		t.Fatalf("createBackupFile: %v", err)
	}

	extracted := extractDBFromArchive(t, filepath.Join(backupDir, name))
	check, err := sql.Open("sqlite", extracted)
	if err != nil {
		t.Fatalf("open extracted db: %v", err)
	}
	defer check.Close()
	var n int
	if err := check.QueryRow(`SELECT COUNT(*) FROM wal_probe`).Scan(&n); err != nil {
		t.Fatalf("backup is missing the wal_probe table (stale pre-WAL copy?): %v", err)
	}
	if n != rows {
		t.Errorf("backup contains %d probe rows, want %d (WAL content lost)", n, rows)
	}
	// No snapshot temp dir left behind next to the DB.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && e.Name() != "backups" {
			t.Errorf("leftover temp dir %q after backup", e.Name())
		}
	}
}

func TestListAndPruneBackups(t *testing.T) {
	backupDir := t.TempDir()

	// Drop three validly-named archives with increasing mtimes.
	base := time.Unix(1_700_000_000, 0)
	names := []string{
		"sfpanel-backup-20260601-101500.tar.gz",
		"sfpanel-backup-20260602-101500.tar.gz",
		"sfpanel-backup-20260603-101500.tar.gz",
	}
	for i, n := range names {
		p := filepath.Join(backupDir, n)
		if err := os.WriteFile(p, []byte("archive"), 0600); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	// A non-matching file must be ignored by the lister.
	if err := os.WriteFile(filepath.Join(backupDir, "notes.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	files, err := listBackupFiles(backupDir)
	if err != nil {
		t.Fatalf("listBackupFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("listBackupFiles returned %d, want 3 (notes.txt must be excluded)", len(files))
	}
	// Newest first.
	if files[0].Name != "sfpanel-backup-20260603-101500.tar.gz" {
		t.Errorf("newest-first ordering wrong, got %s", files[0].Name)
	}

	pruneBackups(backupDir, 2)
	files, _ = listBackupFiles(backupDir)
	if len(files) != 2 {
		t.Fatalf("after prune to 2, have %d, want 2", len(files))
	}
	// The oldest (20260601) should be the one removed.
	for _, f := range files {
		if f.Name == "sfpanel-backup-20260601-101500.tar.gz" {
			t.Error("prune kept the oldest archive instead of removing it")
		}
	}

	// Missing dir → empty, not error.
	empty, err := listBackupFiles(filepath.Join(backupDir, "missing"))
	if err != nil || len(empty) != 0 {
		t.Errorf("missing dir: got %d, err=%v; want 0,nil", len(empty), err)
	}
}
