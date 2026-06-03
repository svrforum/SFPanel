package system

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	name, err := createBackupFile(backupDir, dbPath, cfgPath, "")
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
