package compose

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DiffSummary counts service-level changes.
type DiffSummary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

// ImageChange describes an image:tag change for one service.
type ImageChange struct {
	Service string `json:"service"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// SetChange covers ports/volumes/env-style fields where the diff is a pair of
// added/removed string slices.
type SetChange struct {
	Service string   `json:"service"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// ScalarChange covers restart policy and similar string fields.
type ScalarChange struct {
	Service string `json:"service"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// HealthcheckChange flags any difference in the healthcheck block.
// The actual block content is rendered raw (the frontend pretty-prints).
type HealthcheckChange struct {
	Service string `json:"service"`
	From    string `json:"from"` // "" if absent
	To      string `json:"to"`   // "" if absent
}

// DiffResult is the full payload returned to the frontend.
type DiffResult struct {
	Summary    DiffSummary    `json:"summary"`
	ByCategory map[string]any `json:"by_category"`
	RawDiff    string         `json:"raw_diff"`
}

type composeDoc struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image       string         `yaml:"image"`
	Ports       []any          `yaml:"ports"`
	Volumes     []any          `yaml:"volumes"`
	Environment any            `yaml:"environment"` // map or list
	Restart     string         `yaml:"restart"`
	Healthcheck map[string]any `yaml:"healthcheck"`
}

// ComputeDiff returns a categorized diff between two compose YAML strings.
func ComputeDiff(deployedYAML, proposedYAML string) (*DiffResult, error) {
	var deployed, proposed composeDoc
	if err := yaml.Unmarshal([]byte(deployedYAML), &deployed); err != nil {
		return nil, fmt.Errorf("parse deployed yaml: %w", err)
	}
	if err := yaml.Unmarshal([]byte(proposedYAML), &proposed); err != nil {
		return nil, fmt.Errorf("parse proposed yaml: %w", err)
	}

	res := &DiffResult{
		ByCategory: map[string]any{
			"image":       []ImageChange{},
			"ports":       []SetChange{},
			"volumes":     []SetChange{},
			"env":         []SetChange{},
			"restart":     []ScalarChange{},
			"healthcheck": []HealthcheckChange{},
		},
	}

	// Track per-service modification flag — increments Summary.Modified once
	// per service regardless of how many categories changed inside it.
	modified := map[string]bool{}
	addedSvcs := map[string]bool{}
	removedSvcs := map[string]bool{}
	for name := range proposed.Services {
		if _, ok := deployed.Services[name]; !ok {
			addedSvcs[name] = true
		}
	}
	for name := range deployed.Services {
		if _, ok := proposed.Services[name]; !ok {
			removedSvcs[name] = true
		}
	}

	// Image
	imgs := []ImageChange{}
	for _, name := range sortedKeys(proposed.Services) {
		p := proposed.Services[name]
		d, existed := deployed.Services[name]
		if existed && d.Image != p.Image {
			imgs = append(imgs, ImageChange{Service: name, From: d.Image, To: p.Image})
			modified[name] = true
		}
	}
	if len(imgs) > 0 {
		res.ByCategory["image"] = imgs
	}

	// Ports / Volumes — both are []any of strings; canonicalize to []string.
	ports := setDiff(deployed.Services, proposed.Services, func(s composeService) []string {
		return toStringSlice(s.Ports)
	}, modified)
	if len(ports) > 0 {
		res.ByCategory["ports"] = ports
	}
	volumes := setDiff(deployed.Services, proposed.Services, func(s composeService) []string {
		return toStringSlice(s.Volumes)
	}, modified)
	if len(volumes) > 0 {
		res.ByCategory["volumes"] = volumes
	}

	// Environment — accept map or list, normalize to KEY=VAL form.
	env := setDiff(deployed.Services, proposed.Services, func(s composeService) []string {
		return normalizeEnv(s.Environment)
	}, modified)
	if len(env) > 0 {
		res.ByCategory["env"] = env
	}

	// Restart
	rs := []ScalarChange{}
	for _, name := range sortedKeys(proposed.Services) {
		p := proposed.Services[name]
		d, existed := deployed.Services[name]
		if existed && d.Restart != p.Restart {
			rs = append(rs, ScalarChange{Service: name, From: d.Restart, To: p.Restart})
			modified[name] = true
		}
	}
	if len(rs) > 0 {
		res.ByCategory["restart"] = rs
	}

	// Healthcheck — compare via marshaled form (cheap deep-equal).
	hcs := []HealthcheckChange{}
	for _, name := range sortedKeys(proposed.Services) {
		p := proposed.Services[name]
		d, existed := deployed.Services[name]
		if !existed {
			continue
		}
		dStr := marshalBlock(d.Healthcheck)
		pStr := marshalBlock(p.Healthcheck)
		if dStr != pStr {
			hcs = append(hcs, HealthcheckChange{Service: name, From: dStr, To: pStr})
			modified[name] = true
		}
	}
	if len(hcs) > 0 {
		res.ByCategory["healthcheck"] = hcs
	}

	res.Summary.Added = len(addedSvcs)
	res.Summary.Removed = len(removedSvcs)
	res.Summary.Modified = len(modified)

	res.RawDiff = rawLineDiff(deployedYAML, proposedYAML)

	return res, nil
}

// rawLineDiff returns a tiny line-level diff of the form
//
//	--- deployed
//	+++ proposed
//	@@ -i +i @@
//	- old line
//	+ new line
//
// It's not a real unified diff (no context), but enough for the
// frontend's "원본 텍스트" tab where Monaco DiffEditor is doing the
// real rendering. We just need *something* the operator can copy.
func rawLineDiff(deployed, proposed string) string {
	if deployed == proposed {
		return ""
	}
	dLines := splitLines(deployed)
	pLines := splitLines(proposed)
	var b strings.Builder
	b.WriteString("--- deployed\n+++ proposed\n")
	n := len(dLines)
	if len(pLines) > n {
		n = len(pLines)
	}
	for i := 0; i < n; i++ {
		var dl, pl string
		if i < len(dLines) {
			dl = dLines[i]
		}
		if i < len(pLines) {
			pl = pLines[i]
		}
		if dl == pl {
			continue
		}
		fmt.Fprintf(&b, "@@ -%d +%d @@\n", i+1, i+1)
		if i < len(dLines) {
			fmt.Fprintf(&b, "-%s\n", dl)
		}
		if i < len(pLines) {
			fmt.Fprintf(&b, "+%s\n", pl)
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	// Trim trailing empty caused by terminal newline — keeps diff symmetric.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// setDiff returns one SetChange per service whose extract() differs.
// Output is sorted by service name for deterministic ordering.
func setDiff(deployed, proposed map[string]composeService, extract func(composeService) []string, modified map[string]bool) []SetChange {
	out := []SetChange{}
	for _, name := range sortedKeys(proposed) {
		p := proposed[name]
		d, existed := deployed[name]
		if !existed {
			continue
		}
		ds := stringSet(extract(d))
		ps := stringSet(extract(p))
		added, removed := []string{}, []string{}
		for v := range ps {
			if !ds[v] {
				added = append(added, v)
			}
		}
		for v := range ds {
			if !ps[v] {
				removed = append(removed, v)
			}
		}
		if len(added) == 0 && len(removed) == 0 {
			continue
		}
		out = append(out, SetChange{Service: name, Added: sorted(added), Removed: sorted(removed)})
		modified[name] = true
	}
	return out
}

// sortedKeys returns the keys of m in alphabetical order.
func sortedKeys(m map[string]composeService) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toStringSlice(v []any) []string {
	out := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func normalizeEnv(env any) []string {
	out := []string{}
	switch e := env.(type) {
	case map[string]any:
		for k, v := range e {
			out = append(out, fmt.Sprintf("%s=%v", k, v))
		}
	case []any:
		for _, item := range e {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func stringSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func sorted(items []string) []string {
	out := make([]string, len(items))
	copy(out, items)
	sort.Strings(out)
	return out
}

func marshalBlock(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return ""
	}
	return string(out)
}
