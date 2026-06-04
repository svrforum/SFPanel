package appstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BuildCatalogBundle reads the per-app catalog under root (categories.json,
// index.json, apps/<id>/metadata.json) and returns the deterministic bundled
// catalog.json bytes: {"categories":[...],"apps":[...]} with apps sorted by id.
// Per-app metadata is embedded verbatim (json.RawMessage) so the source files
// stay the single source of truth. Used by `make appstore-catalog` and the
// TestCatalogBundleUpToDate guard.
func BuildCatalogBundle(root string) ([]byte, error) {
	catsRaw, err := os.ReadFile(filepath.Join(root, "categories.json"))
	if err != nil {
		return nil, fmt.Errorf("read categories.json: %w", err)
	}
	var categories []json.RawMessage
	if err := json.Unmarshal(catsRaw, &categories); err != nil {
		return nil, fmt.Errorf("parse categories.json: %w", err)
	}

	idxRaw, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("read index.json: %w", err)
	}
	var ids []string
	if err := json.Unmarshal(idxRaw, &ids); err != nil {
		return nil, fmt.Errorf("parse index.json: %w", err)
	}
	sort.Strings(ids)

	apps := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		mRaw, err := os.ReadFile(filepath.Join(root, "apps", id, "metadata.json"))
		if err != nil {
			return nil, fmt.Errorf("read %s metadata.json: %w", id, err)
		}
		if !json.Valid(mRaw) {
			return nil, fmt.Errorf("%s/metadata.json is not valid JSON", id)
		}
		var canon bytes.Buffer
		if err := json.Compact(&canon, mRaw); err != nil {
			return nil, fmt.Errorf("compact %s metadata.json: %w", id, err)
		}
		apps = append(apps, json.RawMessage(canon.Bytes()))
	}

	bundle := struct {
		Categories []json.RawMessage `json:"categories"`
		Apps       []json.RawMessage `json:"apps"`
	}{categories, apps}

	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
