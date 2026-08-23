package files

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The trash lives at a fixed path. Point it at a temp directory for the
// duration of a test so the suite never touches the real one.
func withTempTrash(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	trashRootOverride = dir
	t.Cleanup(func() { trashRootOverride = "" })
	return dir
}

func TestMoveToTrashRecordsWhereItCameFrom(t *testing.T) {
	trash := withTempTrash(t)
	work := t.TempDir()
	victim := filepath.Join(work, "notes.txt")
	if err := os.WriteFile(victim, []byte("valuable"), 0o644); err != nil {
		t.Fatal(err)
	}

	moved, err := moveToTrash(victim)
	if err != nil {
		t.Fatalf("moveToTrash: %v", err)
	}
	if !moved {
		t.Skip("trash is on a different filesystem here; the caller deletes outright in that case")
	}
	if _, err := os.Stat(victim); err == nil {
		t.Error("the original is still in place")
	}

	entries, err := os.ReadDir(trash)
	if err != nil {
		t.Fatal(err)
	}
	var meta trashMeta
	var found bool
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(trash, e.Name()))
		if json.Unmarshal(raw, &meta) == nil {
			found = true
		}
	}
	if !found {
		t.Fatal("no sidecar written; a restore would have nowhere to put the file back")
	}
	// The origin is the only thing that makes a restore meaningful.
	if meta.OriginalPath != victim {
		t.Errorf("originalPath = %q, want %q", meta.OriginalPath, victim)
	}
}

// A restore must never clobber. The original name being free is the whole
// reason it is safe; overwriting a newer file to recover an older one is the
// opposite of what was asked for.
func TestRestoreRefusesToOverwrite(t *testing.T) {
	withTempTrash(t)
	work := t.TempDir()
	victim := filepath.Join(work, "conf.yml")
	if err := os.WriteFile(victim, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	moved, err := moveToTrash(victim)
	if err != nil || !moved {
		t.Skipf("could not stage the trash: moved=%v err=%v", moved, err)
	}
	// Something new takes the name while the old copy sits in the trash.
	if err := os.WriteFile(victim, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(trashDir())
	var id string
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			id = e.Name()
		}
	}
	status, code := postJSON(t, (&Handler{}).RestoreFromTrash, "/files/trash/restore", map[string]any{"id": id})
	if status != http.StatusConflict {
		t.Fatalf("restore over an existing file returned %d/%s, want 409", status, code)
	}
	if got, _ := os.ReadFile(victim); string(got) != "new" {
		t.Errorf("the newer file was overwritten: %q", got)
	}
}

// The id names a file inside the trash and must not be able to name anything
// else, or a caller could move a system file over a path of their choosing.
func TestRestoreRejectsTraversalIds(t *testing.T) {
	withTempTrash(t)
	for _, id := range []string{"../../etc/passwd", "..", ".", "sub/dir", `back\slash`} {
		status, _ := postJSON(t, (&Handler{}).RestoreFromTrash, "/files/trash/restore",
			map[string]any{"id": id, "to": filepath.Join(t.TempDir(), "out")})
		if status == http.StatusOK {
			t.Errorf("restore accepted the id %q", id)
		}
	}
}

// Without a sweeper the trash is not a safety net but a slow leak.
func TestSweepTrashRemovesOnlyExpiredEntries(t *testing.T) {
	trash := withTempTrash(t)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		t.Fatal(err)
	}

	write := func(name string, age time.Duration) {
		path := filepath.Join(trash, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		meta, _ := json.Marshal(trashMeta{OriginalPath: "/somewhere/" + name, DeletedAt: time.Now().Add(-age)})
		if err := os.WriteFile(path+".meta.json", meta, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("fresh", time.Hour)
	write("stale", trashRetention+time.Hour)

	removed, err := SweepTrash()
	if err != nil {
		t.Fatalf("SweepTrash: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(trash, "fresh")); err != nil {
		t.Error("an entry inside the retention window was swept")
	}
	if _, err := os.Stat(filepath.Join(trash, "stale")); err == nil {
		t.Error("an expired entry survived the sweep")
	}
	if _, err := os.Stat(filepath.Join(trash, "stale.meta.json")); err == nil {
		t.Error("the expired entry's sidecar was left behind")
	}
}
