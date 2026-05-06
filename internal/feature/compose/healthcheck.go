package compose

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// HealthcheckSpec is the input shape from the composer dialog.
type HealthcheckSpec struct {
	TestType    string `json:"test_type"`    // "CMD-SHELL" | "CMD" | "NONE"
	TestValue   string `json:"test_value"`   // command for CMD-SHELL; pipe-separated argv for CMD; ignored for NONE
	Interval    string `json:"interval"`     // Go duration: "30s", "1m30s"
	Timeout     string `json:"timeout"`
	Retries     int    `json:"retries"`
	StartPeriod string `json:"start_period"`
}

// ErrServiceNotFound is returned when the named service doesn't exist.
var ErrServiceNotFound = errors.New("compose: service not found")

// ErrHealthcheckExists is returned when a healthcheck is already
// present and replace=false. The composer surfaces this so the
// operator can opt in to overwriting.
var ErrHealthcheckExists = errors.New("compose: healthcheck already present (set replace=true to overwrite)")

// ApplyHealthcheck inserts or replaces the healthcheck block on the
// named service. yaml.v3 Node API is used so anchors, comments, and
// key ordering survive untouched.
func ApplyHealthcheck(yamlContent string, service string, spec HealthcheckSpec, replace bool) (string, error) {
	if err := spec.validate(); err != nil {
		return "", err
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return "", fmt.Errorf("parse compose: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", errors.New("empty compose document")
	}

	svcNode, err := findServiceNode(&root, service)
	if err != nil {
		return "", err
	}

	hcKeyNode, hcValNode := findChild(svcNode, "healthcheck")
	if hcKeyNode != nil && !replace {
		return "", ErrHealthcheckExists
	}

	newHC := buildHealthcheckNode(spec)
	if hcKeyNode == nil {
		// Append healthcheck at end of service map.
		svcNode.Content = append(svcNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "healthcheck"},
			newHC,
		)
	} else {
		// Replace the existing value node, preserving key node (and any
		// comments attached to it).
		_ = hcValNode
		// Find the val index to swap in place.
		for i := 0; i+1 < len(svcNode.Content); i += 2 {
			if svcNode.Content[i] == hcKeyNode {
				svcNode.Content[i+1] = newHC
				break
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", fmt.Errorf("encode compose: %w", err)
	}
	enc.Close()
	return buf.String(), nil
}

// validate rejects malformed input before it touches the YAML tree.
func (s HealthcheckSpec) validate() error {
	switch s.TestType {
	case "CMD-SHELL", "CMD", "NONE":
	default:
		return fmt.Errorf("invalid test_type %q (want CMD-SHELL|CMD|NONE)", s.TestType)
	}
	if s.TestType == "NONE" {
		return nil // other fields ignored
	}
	if strings.TrimSpace(s.TestValue) == "" {
		return errors.New("test_value required for CMD-SHELL and CMD")
	}
	for _, d := range []struct{ name, val string }{
		{"interval", s.Interval}, {"timeout", s.Timeout}, {"start_period", s.StartPeriod},
	} {
		if d.val == "" {
			return fmt.Errorf("%s required", d.name)
		}
		if _, err := time.ParseDuration(d.val); err != nil {
			return fmt.Errorf("%s must be a Go duration (e.g. 30s, 1m30s): %w", d.name, err)
		}
	}
	if s.Retries <= 0 {
		return errors.New("retries must be positive")
	}
	return nil
}

// findServiceNode returns the *yaml.Node for `services.<name>` (a
// MappingNode), or ErrServiceNotFound.
func findServiceNode(root *yaml.Node, service string) (*yaml.Node, error) {
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("compose root is not a mapping")
	}
	_, services := findChild(doc, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, errors.New("services block missing or malformed")
	}
	_, svc := findChild(services, service)
	if svc == nil {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, service)
	}
	if svc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("service %s is not a mapping", service)
	}
	return svc, nil
}

// findChild looks up a key in a MappingNode and returns (keyNode, valueNode)
// or (nil, nil) if absent. Mapping nodes interleave keys and values in
// Content: [k1, v1, k2, v2, ...].
func findChild(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return k, m.Content[i+1]
		}
	}
	return nil, nil
}

// buildHealthcheckNode constructs the yaml.Node for the healthcheck
// MappingNode value. The shape:
//
//	healthcheck:
//	  test: ["CMD-SHELL", "<value>"]    # or ["CMD", arg1, arg2, ...] or ["NONE"]
//	  interval: 30s
//	  timeout: 10s
//	  retries: 3
//	  start_period: 30s
func buildHealthcheckNode(s HealthcheckSpec) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addKV := func(k string, v *yaml.Node) {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			v,
		)
	}
	addKV("test", buildTestNode(s))
	if s.TestType == "NONE" {
		return m
	}
	addKV("interval", scalar(s.Interval))
	addKV("timeout", scalar(s.Timeout))
	addKV("retries", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", s.Retries)})
	addKV("start_period", scalar(s.StartPeriod))
	return m
}

func buildTestNode(s HealthcheckSpec) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	switch s.TestType {
	case "NONE":
		seq.Content = []*yaml.Node{scalar("NONE")}
	case "CMD-SHELL":
		seq.Content = []*yaml.Node{scalar("CMD-SHELL"), scalar(s.TestValue)}
	case "CMD":
		argv := strings.Split(s.TestValue, "|")
		seq.Content = append(seq.Content, scalar("CMD"))
		for _, a := range argv {
			seq.Content = append(seq.Content, scalar(a))
		}
	}
	return seq
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// ParseHealthcheck reads the existing healthcheck for a service. Returns
// (zero-value, false, nil) if the service has no healthcheck block.
func ParseHealthcheck(yamlContent string, service string) (HealthcheckSpec, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return HealthcheckSpec{}, false, fmt.Errorf("parse compose: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return HealthcheckSpec{}, false, nil
	}
	svc, err := findServiceNode(&root, service)
	if err != nil {
		return HealthcheckSpec{}, false, err
	}
	_, hc := findChild(svc, "healthcheck")
	if hc == nil || hc.Kind != yaml.MappingNode {
		return HealthcheckSpec{}, false, nil
	}

	var spec HealthcheckSpec
	_, testNode := findChild(hc, "test")
	if testNode != nil && testNode.Kind == yaml.SequenceNode && len(testNode.Content) > 0 {
		head := testNode.Content[0].Value
		switch head {
		case "NONE":
			spec.TestType = "NONE"
		case "CMD-SHELL":
			spec.TestType = "CMD-SHELL"
			if len(testNode.Content) >= 2 {
				spec.TestValue = testNode.Content[1].Value
			}
		case "CMD":
			spec.TestType = "CMD"
			parts := make([]string, 0, len(testNode.Content)-1)
			for _, n := range testNode.Content[1:] {
				parts = append(parts, n.Value)
			}
			spec.TestValue = strings.Join(parts, "|")
		}
	}
	if _, n := findChild(hc, "interval"); n != nil {
		spec.Interval = n.Value
	}
	if _, n := findChild(hc, "timeout"); n != nil {
		spec.Timeout = n.Value
	}
	if _, n := findChild(hc, "retries"); n != nil {
		fmt.Sscanf(n.Value, "%d", &spec.Retries)
	}
	if _, n := findChild(hc, "start_period"); n != nil {
		spec.StartPeriod = n.Value
	}
	return spec, true, nil
}
