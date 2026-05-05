package appstore

import "gopkg.in/yaml.v3"

// extractImageRefs parses a compose YAML and returns the `image:` refs of
// every service. Returns nil on parse failure (caller should treat as
// "no images to verify"). Used by the install flow to drive cosign
// verification BEFORE `docker compose pull` runs.
func extractImageRefs(composeYAML string) []string {
	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return nil
	}
	out := make([]string, 0, len(doc.Services))
	for _, svc := range doc.Services {
		if svc.Image != "" {
			out = append(out, svc.Image)
		}
	}
	return out
}
