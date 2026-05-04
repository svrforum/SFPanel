package compose

import (
	"fmt"

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

	// Image diff
	imgs := []ImageChange{}
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if existed && d.Image != p.Image {
			imgs = append(imgs, ImageChange{Service: name, From: d.Image, To: p.Image})
		}
	}
	if len(imgs) > 0 {
		res.ByCategory["image"] = imgs
	}

	// Service-level summary (only counts services that exist in both with at least one change).
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if !existed {
			continue
		}
		if d.Image != p.Image {
			res.Summary.Modified++
		}
	}

	return res, nil
}
