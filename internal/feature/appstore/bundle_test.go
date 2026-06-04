package appstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildCatalogBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "categories.json"),
		[]byte(`[{"id":"media","name":{"en":"Media"},"icon":"m"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`["b","a"]`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		appDir := filepath.Join(dir, "apps", id)
		if err := os.MkdirAll(appDir, 0755); err != nil {
			t.Fatal(err)
		}
		meta := `{"id":"` + id + `","name":"` + id + `","category":"media","version":"1.0.0","ports":[80]}`
		if err := os.WriteFile(filepath.Join(appDir, "metadata.json"), []byte(meta), 0644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := BuildCatalogBundle(dir)
	if err != nil {
		t.Fatalf("BuildCatalogBundle: %v", err)
	}
	var bundle struct {
		Categories []json.RawMessage `json:"categories"`
		Apps       []json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(out, &bundle); err != nil {
		t.Fatalf("bundle not valid JSON: %v", err)
	}
	if len(bundle.Categories) != 1 || len(bundle.Apps) != 2 {
		t.Fatalf("got %d cats %d apps, want 1/2", len(bundle.Categories), len(bundle.Apps))
	}
	var first struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(bundle.Apps[0], &first)
	if first.ID != "a" {
		t.Fatalf("apps not sorted: first=%s want a", first.ID)
	}
	out2, _ := BuildCatalogBundle(dir)
	if string(out) != string(out2) {
		t.Fatal("BuildCatalogBundle is not deterministic")
	}
}
