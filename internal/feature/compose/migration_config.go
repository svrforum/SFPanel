package compose

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// stackConfigFacts are the migration-relevant facts extracted from a stack's
// fully-resolved compose config. They feed the pre-flight (StackPorts,
// HasSystemBind, HasExternalVolume, HasDevice) and the bundle manifest
// (Binds/Volumes/Devices).
type stackConfigFacts struct {
	HostPorts         []int
	Binds             []MountSpec
	Volumes           []VolumeSpec
	Devices           []string
	HasSystemBind     bool
	HasExternalVolume bool
	HasDevice         bool
}

// composeConfig mirrors the subset of `docker compose config --format json` we
// consume. Pinned to the observed Compose v2/v5 shape:
//   - ports[].published is a string ("8096") on current Compose, a number on
//     older builds; json.Number accepts both. Absent published = random host
//     port → skipped. A "start-end" range published value contributes its start.
//   - volumes[].type is "bind" or "volume"; source is the resolved absolute host
//     path (bind) or the compose volume name (volume).
//   - devices[].source is the host device path (the part before ':' in the short
//     form).
//   - top-level volumes[<name>] carries the resolved docker name and an external
//     flag (absent when false).
type composeConfig struct {
	Services map[string]struct {
		Ports []struct {
			Published json.RawMessage `json:"published"`
		} `json:"ports"`
		Volumes []struct {
			Type   string `json:"type"`
			Source string `json:"source"`
		} `json:"volumes"`
		Devices []struct {
			Source string `json:"source"`
		} `json:"devices"`
	} `json:"services"`
	Volumes map[string]struct {
		Name     string `json:"name"`
		External bool   `json:"external"`
	} `json:"volumes"`
}

// parseStackConfig extracts migration-relevant facts from the JSON produced by
// `docker compose config --format json`. workingDir is the stack's working
// directory (for classifying bind sources via classifyBind). It is pure and
// unit-tested against real compose-config output.
func parseStackConfig(configJSON []byte, workingDir string) (stackConfigFacts, error) {
	var cfg composeConfig
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return stackConfigFacts{}, fmt.Errorf("parse compose config: %w", err)
	}

	var facts stackConfigFacts
	portSet := map[int]bool{}
	bindSeen := map[string]bool{}
	volSeen := map[string]bool{}
	devSeen := map[string]bool{}

	for _, svc := range cfg.Services {
		for _, p := range svc.Ports {
			port, ok := hostPort(p.Published)
			if !ok {
				continue // no published port (random host port)
			}
			portSet[port] = true
		}

		for _, v := range svc.Volumes {
			switch v.Type {
			case "bind":
				if v.Source == "" || bindSeen[v.Source] {
					continue
				}
				bindSeen[v.Source] = true
				kind := classifyBind(v.Source, workingDir)
				facts.Binds = append(facts.Binds, MountSpec{
					Host: v.Source,
					Kind: kind,
					Copy: kind != "system",
				})
				if kind == "system" {
					facts.HasSystemBind = true
				}
			case "volume":
				if v.Source == "" || volSeen[v.Source] {
					continue
				}
				volSeen[v.Source] = true
				external := cfg.Volumes[v.Source].External
				docker := cfg.Volumes[v.Source].Name
				if docker == "" {
					docker = v.Source
				}
				facts.Volumes = append(facts.Volumes, VolumeSpec{
					Compose:  v.Source,
					Docker:   docker,
					External: external,
					Copy:     !external,
				})
				if external {
					facts.HasExternalVolume = true
				}
			}
		}

		for _, d := range svc.Devices {
			host := d.Source
			if i := strings.IndexByte(host, ':'); i >= 0 {
				host = host[:i]
			}
			if host == "" || devSeen[host] {
				continue
			}
			devSeen[host] = true
			facts.Devices = append(facts.Devices, host)
		}
	}

	for p := range portSet {
		facts.HostPorts = append(facts.HostPorts, p)
	}
	sort.Ints(facts.HostPorts)
	facts.HasDevice = len(facts.Devices) > 0

	return facts, nil
}

// hostPort resolves a compose ports[].published raw value to an integer host
// port. published is emitted as a JSON string ("8096") on current Compose and a
// number (8096) on older builds; a range string ("9000-9001") contributes its
// start. An absent/empty value (random host port) returns ok=false.
func hostPort(published json.RawMessage) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(published)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i] // "9000-9001" → start of range
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
