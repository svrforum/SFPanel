package appstore

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultForkCategory = "내 Templates"

// ExtractForkMeta builds an AppStoreMeta + compose YAML pair for a fork
// from a running stack's deployed compose YAML and current env values.
// Pure function — no I/O. Safe for unit tests.
func ExtractForkMeta(stackName, composeYAML string, envValues map[string]string, user UserForkInput) (AppStoreMeta, string) {
	cat := user.Category
	if cat == "" {
		cat = defaultForkCategory
	}
	meta := AppStoreMeta{
		ID:          "fork-" + shortID(),
		Name:        user.Name,
		Description: map[string]string{"ko": user.Description, "en": user.Description},
		Category:    cat,
		Version:     "1.0.0",
		Source:      "fork:" + stackName,
		Env:         extractEnvDefs(composeYAML, envValues),
	}
	return meta, composeYAML
}

// extractEnvDefs walks services.<svc>.environment and produces one
// AppStoreEnvDef per unique env key. Default value comes from the
// runtime envValues map (so the fork captures the current state, not
// the YAML's hardcoded default which may be a placeholder like
// `${VAR:-fallback}`).
func extractEnvDefs(composeYAML string, envValues map[string]string) []AppStoreEnvDef {
	var doc struct {
		Services map[string]struct {
			Environment any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return nil
	}
	keys := map[string]bool{}
	for _, svc := range doc.Services {
		switch env := svc.Environment.(type) {
		case map[string]any:
			for k := range env {
				keys[k] = true
			}
		case []any:
			for _, item := range env {
				if s, ok := item.(string); ok {
					if eq := strings.Index(s, "="); eq > 0 {
						keys[s[:eq]] = true
					}
				}
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	out := make([]AppStoreEnvDef, 0, len(sortedKeys))
	for _, k := range sortedKeys {
		out = append(out, AppStoreEnvDef{
			Key:     k,
			Label:   map[string]string{"ko": k, "en": k},
			Type:    "string",
			Default: envValues[k],
		})
	}
	return out
}

// shortID returns 8 hex characters from crypto/rand.
func shortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
