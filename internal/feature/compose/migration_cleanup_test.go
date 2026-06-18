package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupOrphanMigrationStaging(t *testing.T) {
	root := t.TempDir()
	mk := func(name string) {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(".mig-pkg-123")          // pure temp → removed
	mk(".migrate-stage-abc")    // pure temp → removed
	mk("gone.migbak")           // backup with no live orig → restored to "gone"
	mk("live")                  // live stack...
	mk("live.migbak")           // ...with a backup → both left for the operator
	mk("normal")                // unrelated stack → untouched

	CleanupOrphanMigrationStaging(root)

	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	if exists(".mig-pkg-123") || exists(".migrate-stage-abc") {
		t.Error("staging dirs must be swept")
	}
	if exists("gone.migbak") || !exists("gone") {
		t.Error("orphan backup with no live orig must be restored to the original name")
	}
	if !exists("live") || !exists("live.migbak") {
		t.Error("backup with a live orig must be left untouched for the operator")
	}
	if !exists("normal") {
		t.Error("unrelated stack must be untouched")
	}
}
