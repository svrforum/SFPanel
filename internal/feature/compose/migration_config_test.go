package compose

import (
	"reflect"
	"testing"
)

// realConfigJSON mirrors the shape emitted by `docker compose config
// --format json` (Compose v2/v5): ports.published is a JSON string, binds carry
// an absolute resolved source, devices are objects with a "source", and the
// top-level volumes map flags external. Pinned to observed output so the parser
// can't drift from reality.
const realConfigJSON = `{
  "name": "demo",
  "services": {
    "app": {
      "image": "alpine:latest",
      "devices": [
        {"source": "/dev/dri", "target": "/dev/dri", "permissions": "rwm"}
      ],
      "ports": [
        {"mode": "ingress", "target": 8096, "published": "8096", "protocol": "tcp"},
        {"mode": "ingress", "target": 3000, "protocol": "tcp"}
      ],
      "volumes": [
        {"type": "bind", "source": "/opt/stacks/demo/config", "target": "/config", "bind": {}},
        {"type": "bind", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock", "bind": {}},
        {"type": "volume", "source": "data", "target": "/var/lib/data", "volume": {}},
        {"type": "volume", "source": "shared", "target": "/srv/shared", "volume": {}}
      ]
    }
  },
  "volumes": {
    "data": {"name": "demo_data"},
    "shared": {"name": "shared", "external": true}
  }
}`

func TestParseStackConfig(t *testing.T) {
	facts, err := parseStackConfig([]byte(realConfigJSON), "/opt/stacks/demo")
	if err != nil {
		t.Fatalf("parseStackConfig: %v", err)
	}

	// Host ports: 8096 published, 3000 has no published field (random host port) → skipped.
	if !reflect.DeepEqual(facts.HostPorts, []int{8096}) {
		t.Errorf("HostPorts = %v, want [8096]", facts.HostPorts)
	}

	// Binds: in-stack config (copy) + system docker.sock (no copy).
	wantBinds := map[string]MountSpec{
		"/opt/stacks/demo/config": {Host: "/opt/stacks/demo/config", Kind: "in-stack", Copy: true},
		"/var/run/docker.sock":    {Host: "/var/run/docker.sock", Kind: "system", Copy: false},
	}
	if len(facts.Binds) != len(wantBinds) {
		t.Fatalf("Binds = %+v, want %d entries", facts.Binds, len(wantBinds))
	}
	for _, b := range facts.Binds {
		want, ok := wantBinds[b.Host]
		if !ok {
			t.Errorf("unexpected bind %+v", b)
			continue
		}
		if !reflect.DeepEqual(b, want) {
			t.Errorf("bind %s = %+v, want %+v", b.Host, b, want)
		}
	}

	// Volumes: data (internal, copy) + shared (external, no copy).
	wantVols := map[string]VolumeSpec{
		"data":   {Compose: "data", Docker: "demo_data", External: false, Copy: true},
		"shared": {Compose: "shared", Docker: "shared", External: true, Copy: false},
	}
	if len(facts.Volumes) != len(wantVols) {
		t.Fatalf("Volumes = %+v, want %d entries", facts.Volumes, len(wantVols))
	}
	for _, v := range facts.Volumes {
		want, ok := wantVols[v.Compose]
		if !ok {
			t.Errorf("unexpected volume %+v", v)
			continue
		}
		if !reflect.DeepEqual(v, want) {
			t.Errorf("volume %s = %+v, want %+v", v.Compose, v, want)
		}
	}

	// Devices: host path before the ':' (here just the source).
	if !reflect.DeepEqual(facts.Devices, []string{"/dev/dri"}) {
		t.Errorf("Devices = %v, want [/dev/dri]", facts.Devices)
	}

	// Flags.
	if !facts.HasSystemBind {
		t.Error("HasSystemBind = false, want true")
	}
	if !facts.HasExternalVolume {
		t.Error("HasExternalVolume = false, want true")
	}
	if !facts.HasDevice {
		t.Error("HasDevice = false, want true")
	}
}

// TestParseStackConfigDedup verifies that ports, binds, volumes, and devices
// repeated across services are collapsed.
func TestParseStackConfigDedup(t *testing.T) {
	const j = `{
  "name": "dup",
  "services": {
    "a": {
      "ports": [{"target": 80, "published": "80", "protocol": "tcp"}],
      "devices": [{"source": "/dev/dri", "target": "/dev/dri"}],
      "volumes": [
        {"type": "bind", "source": "/opt/stacks/dup/data", "target": "/data"},
        {"type": "volume", "source": "vol", "target": "/v"}
      ]
    },
    "b": {
      "ports": [{"target": 80, "published": "80", "protocol": "tcp"}],
      "devices": [{"source": "/dev/dri", "target": "/dev/dri"}],
      "volumes": [
        {"type": "bind", "source": "/opt/stacks/dup/data", "target": "/data2"},
        {"type": "volume", "source": "vol", "target": "/v2"}
      ]
    }
  },
  "volumes": {"vol": {"name": "dup_vol"}}
}`
	facts, err := parseStackConfig([]byte(j), "/opt/stacks/dup")
	if err != nil {
		t.Fatalf("parseStackConfig: %v", err)
	}
	if !reflect.DeepEqual(facts.HostPorts, []int{80}) {
		t.Errorf("HostPorts = %v, want [80]", facts.HostPorts)
	}
	if len(facts.Binds) != 1 {
		t.Errorf("Binds = %+v, want 1 (deduped)", facts.Binds)
	}
	if len(facts.Volumes) != 1 {
		t.Errorf("Volumes = %+v, want 1 (deduped)", facts.Volumes)
	}
	if !reflect.DeepEqual(facts.Devices, []string{"/dev/dri"}) {
		t.Errorf("Devices = %v, want [/dev/dri] (deduped)", facts.Devices)
	}
	if facts.HasSystemBind || facts.HasExternalVolume {
		t.Errorf("flags = system:%v external:%v, want both false", facts.HasSystemBind, facts.HasExternalVolume)
	}
	if !facts.HasDevice {
		t.Error("HasDevice = false, want true")
	}
}

// TestParseStackConfigPublishedNumber covers older Compose builds that emit
// ports.published as a JSON number rather than a string.
func TestParseStackConfigPublishedNumber(t *testing.T) {
	const j = `{
  "name": "n",
  "services": {"a": {"ports": [{"target": 443, "published": 8443, "protocol": "tcp"}]}}
}`
	facts, err := parseStackConfig([]byte(j), "/opt/stacks/n")
	if err != nil {
		t.Fatalf("parseStackConfig: %v", err)
	}
	if !reflect.DeepEqual(facts.HostPorts, []int{8443}) {
		t.Errorf("HostPorts = %v, want [8443]", facts.HostPorts)
	}
}

// TestParseStackConfigPortRange covers a published value emitted as a range
// string ("9000-9002"); EVERY port in the range is collected so the port-conflict
// pre-flight doesn't miss the tail of a range.
func TestParseStackConfigPortRange(t *testing.T) {
	const j = `{
  "name": "r",
  "services": {"a": {"ports": [{"target": 9000, "published": "9000-9002", "protocol": "tcp"}]}}
}`
	facts, err := parseStackConfig([]byte(j), "/opt/stacks/r")
	if err != nil {
		t.Fatalf("parseStackConfig: %v", err)
	}
	if !reflect.DeepEqual(facts.HostPorts, []int{9000, 9001, 9002}) {
		t.Errorf("HostPorts = %v, want [9000 9001 9002]", facts.HostPorts)
	}
}

// TestHostPortsRangeCap verifies an oversized range is capped rather than
// ballooning the port set (defends the pre-flight against a hostile mapping).
func TestHostPortsRangeCap(t *testing.T) {
	got := hostPorts([]byte(`"1-65535"`))
	if len(got) != maxPortRangeExpansion {
		t.Fatalf("len=%d, want capped at %d", len(got), maxPortRangeExpansion)
	}
	if got[0] != 1 {
		t.Errorf("first port=%d, want 1", got[0])
	}
}

// TestParseStackConfigDeviceShort covers the short device form where source and
// target are the same path; only the host source is collected.
func TestParseStackConfigDeviceShort(t *testing.T) {
	const j = `{
  "name": "d",
  "services": {"a": {"devices": [{"source": "/dev/snd", "target": "/dev/snd"}]}}
}`
	facts, err := parseStackConfig([]byte(j), "/opt/stacks/d")
	if err != nil {
		t.Fatalf("parseStackConfig: %v", err)
	}
	if !reflect.DeepEqual(facts.Devices, []string{"/dev/snd"}) {
		t.Errorf("Devices = %v, want [/dev/snd]", facts.Devices)
	}
}

func TestParseStackConfigInvalidJSON(t *testing.T) {
	if _, err := parseStackConfig([]byte("not json"), "/opt/stacks/x"); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
