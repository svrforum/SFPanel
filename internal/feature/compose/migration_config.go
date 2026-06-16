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
			for _, port := range hostPorts(p.Published) {
				portSet[port] = true
			}
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

// maxPortRangeExpansion caps how many ports a single "start-end" published range
// expands to, so a hostile/typo'd "1-65535" mapping can't balloon the port set.
const maxPortRangeExpansion = 1024

// hostPorts resolves a compose ports[].published raw value to its host port(s).
// published is emitted as a JSON string ("8096") on current Compose and a number
// (8096) on older builds; a range string ("9000-9001") expands to EVERY port in
// the range (9000, 9001) so the port-conflict pre-flight doesn't miss the tail of
// a range. An absent/empty value (random host port) yields nil. Oversized or
// malformed ranges fall back to the range start so the check degrades safely.
func hostPorts(published json.RawMessage) []int {
	s := strings.Trim(strings.TrimSpace(string(published)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		start, serr := strconv.Atoi(strings.TrimSpace(s[:i]))
		end, eerr := strconv.Atoi(strings.TrimSpace(s[i+1:]))
		if serr != nil || eerr != nil || start <= 0 || end < start {
			// Malformed range — fall back to the start if it parses, else nothing.
			if serr == nil && start > 0 {
				return []int{start}
			}
			return nil
		}
		if end-start+1 > maxPortRangeExpansion {
			end = start + maxPortRangeExpansion - 1
		}
		ports := make([]int, 0, end-start+1)
		for p := start; p <= end; p++ {
			ports = append(ports, p)
		}
		return ports
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return nil
	}
	return []int{n}
}
