package appstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// catalogDir is the in-repo App Store catalog, relative to this package.
const catalogDir = "../../../appstore"

// TestCatalogValid validates the repo's App Store catalog so that any
// malformed contribution (bad JSON, missing files, unknown category,
// orphaned app folder, invalid port/env definition) fails CI under
// `go test ./...`. The catalog directory is tracked in-repo, so its absence
// is a checkout/path regression and fails the test rather than skipping.
func TestCatalogValid(t *testing.T) {
	if _, err := os.Stat(catalogDir); os.IsNotExist(err) {
		// appstore/ is tracked in-repo (196 files), so on any normal checkout
		// it is present. Its absence means a path/checkout regression, not an
		// environment limitation — fail loudly instead of silently passing.
		t.Fatalf("catalog directory %s not present (expected in-repo)", catalogDir)
	}

	// --- categories.json ---
	catData, err := os.ReadFile(filepath.Join(catalogDir, "categories.json"))
	if err != nil {
		t.Fatalf("read categories.json: %v", err)
	}
	var categories []AppStoreCategory
	if err := json.Unmarshal(catData, &categories); err != nil {
		t.Fatalf("parse categories.json: %v", err)
	}
	validCategory := make(map[string]bool, len(categories))
	for _, c := range categories {
		if c.ID == "" {
			t.Errorf("categories.json: category with empty id")
			continue
		}
		validCategory[c.ID] = true
	}

	// --- index.json ---
	indexData, err := os.ReadFile(filepath.Join(catalogDir, "index.json"))
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var index []string
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("parse index.json: %v", err)
	}
	indexed := make(map[string]bool, len(index))
	for _, id := range index {
		if indexed[id] {
			t.Errorf("index.json: duplicate app id %q", id)
		}
		indexed[id] = true
	}

	appsDir := filepath.Join(catalogDir, "apps")

	// --- every indexed app has a valid folder + metadata ---
	for _, id := range index {
		appDir := filepath.Join(appsDir, id)
		info, err := os.Stat(appDir)
		if err != nil || !info.IsDir() {
			t.Errorf("app %q: missing folder apps/%s/", id, id)
			continue
		}

		composePath := filepath.Join(appDir, "docker-compose.yml")
		if _, err := os.Stat(composePath); err != nil {
			t.Errorf("app %q: missing docker-compose.yml", id)
		}

		metaPath := filepath.Join(appDir, "metadata.json")
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			t.Errorf("app %q: missing metadata.json", id)
			continue
		}

		var meta AppStoreMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			t.Errorf("app %q: metadata.json does not parse: %v", id, err)
			continue
		}

		if meta.ID != id {
			t.Errorf("app %q: metadata id %q does not match folder name", id, meta.ID)
		}
		if meta.Name == "" {
			t.Errorf("app %q: name is empty", id)
		}
		if meta.Description["ko"] == "" {
			t.Errorf("app %q: description.ko is empty", id)
		}
		if meta.Description["en"] == "" {
			t.Errorf("app %q: description.en is empty", id)
		}
		if !validCategory[meta.Category] {
			t.Errorf("app %q: category %q not found in categories.json", id, meta.Category)
		}
		if meta.Version == "" {
			t.Errorf("app %q: version is empty", id)
		}
		for _, p := range meta.Ports {
			if p < 1 || p > 65535 {
				t.Errorf("app %q: port %d out of range (1-65535)", id, p)
			}
		}

		for _, env := range meta.Env {
			switch env.Type {
			case "", "text", "port", "password", "select", "path":
				// ok ("" is treated as text; "path" is a host filesystem
				// path field, rendered as a text input by the frontend)
			default:
				t.Errorf("app %q: env %q has invalid type %q (want port|password|select|text|path)", id, env.Key, env.Type)
			}
			if env.Type == "select" && len(env.Options) == 0 {
				t.Errorf("app %q: env %q is type select but has no options", id, env.Key)
			}
		}
	}

	// --- orphan check: every apps/<id> folder must be in index.json ---
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		t.Fatalf("read apps dir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !indexed[e.Name()] {
			t.Errorf("app folder apps/%s/ is not listed in index.json (orphan)", e.Name())
		}
	}
}

// TestCatalogBundleUpToDate fails if appstore/catalog.json is stale relative
// to the per-app source files. A contributor who edits an app must run
// `make appstore-catalog` and commit the regenerated bundle.
func TestCatalogBundleUpToDate(t *testing.T) {
	if _, err := os.Stat(catalogDir); os.IsNotExist(err) {
		// appstore/ is tracked in-repo (196 files), so on any normal checkout
		// it is present. Its absence means a path/checkout regression, not an
		// environment limitation — fail loudly instead of silently passing.
		t.Fatalf("catalog directory %s not present (expected in-repo)", catalogDir)
	}
	want, err := os.ReadFile(filepath.Join(catalogDir, "catalog.json"))
	if err != nil {
		t.Fatalf("appstore/catalog.json missing or unreadable — run `make appstore-catalog` and commit: %v", err)
	}
	got, err := BuildCatalogBundle(catalogDir)
	if err != nil {
		t.Fatalf("BuildCatalogBundle: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("appstore/catalog.json is stale — run `make appstore-catalog` and commit the result")
	}
}
