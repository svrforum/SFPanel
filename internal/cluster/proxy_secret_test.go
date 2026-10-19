package cluster

import (
	"slices"
	"testing"
	"time"
)

func TestProxySecretsAt(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	// Before the rotation: sign with the CA-derived secret — the only one a
	// node older than this code understands — and accept both.
	sign, accept := proxySecretsAt("legacy", "random", "", now, false)
	if sign != "legacy" || !slices.Equal(accept, []string{"legacy", "random"}) {
		t.Fatalf("before rotation: %q %v", sign, accept)
	}
	// The random secret not replicated yet: the derived one alone.
	sign, accept = proxySecretsAt("legacy", "", "", now, false)
	if sign != "legacy" || !slices.Equal(accept, []string{"legacy"}) {
		t.Fatalf("no replicated secret: %q %v", sign, accept)
	}
	rotated := now.Add(-time.Minute).Format(time.RFC3339)
	sign, accept = proxySecretsAt("legacy", "random", rotated, now, false)
	if sign != "random" || !slices.Equal(accept, []string{"random", "legacy"}) {
		t.Fatalf("inside the grace: %q %v", sign, accept)
	}
	rotated = now.Add(-legacyProxyGrace - time.Second).Format(time.RFC3339)
	sign, accept = proxySecretsAt("legacy", "random", rotated, now, false)
	if sign != "random" || !slices.Equal(accept, []string{"random"}) {
		t.Fatalf("after the grace the derived secret must be dead: %q %v", sign, accept)
	}
}

// Agents hold the CA certificate, from which the derived secret is computed.
// Once any agent exists the derived secret is never accepted, even inside the
// grace after the switch — whatever the clocks say.
func TestNoDerivedSecretOnceAgentsExist(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, rotated := range []string{"", now.Add(-time.Minute).Format(time.RFC3339)} {
		sign, accept := proxySecretsAt("legacy", "random", rotated, now, true)
		if sign != "random" || !slices.Equal(accept, []string{"random"}) {
			t.Fatalf("rotated %q with agents: sign %q accept %v", rotated, sign, accept)
		}
	}
	// Without agents nothing changes.
	if sign, accept := proxySecretsAt("legacy", "random", "", now, false); sign != "legacy" || !slices.Equal(accept, []string{"legacy", "random"}) {
		t.Fatalf("before the switch, no agents: %q %v", sign, accept)
	}
}

// Agents never report the managers' proxy-secret capability; the switch is
// decided by the managers.
func TestRotationMembersAreManagers(t *testing.T) {
	state := ClusterState{Nodes: map[string]*Node{
		"m1": {ID: "m1", Kind: KindManager}, "m0": {ID: "m0"}, "a1": {ID: "a1", Kind: KindAgent},
	}}
	got := rotationMembers(state)
	slices.Sort(got)
	if !slices.Equal(got, []string{"m0", "m1"}) {
		t.Fatalf("rotation members %v", got)
	}
}

func TestShouldRotate(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	seeded := now.Add(-2 * time.Minute)
	members := []string{"self", "b", "c"}
	online := map[string]NodeStatus{"b": StatusOnline, "c": StatusOnline}
	capable := map[string][]string{"b": {CapProxySecret}, "c": {"other", CapProxySecret}}
	if !shouldRotate(seeded, now, "self", members, online, capable) {
		t.Fatal("every member online and capable: should rotate")
	}
	if shouldRotate(seeded, now, "self", members, online, map[string][]string{"b": {CapProxySecret}}) {
		t.Fatal("rotated while c had not reported the capability (an older binary)")
	}
	if shouldRotate(seeded, now, "self", members, map[string]NodeStatus{"b": StatusOnline, "c": StatusOffline}, capable) {
		t.Fatal("rotated while c was offline")
	}
	if !shouldRotate(seeded, now, "self", []string{"self"}, nil, nil) {
		t.Fatal("a one-manager cluster should rotate")
	}
}

// Every member loads the replicated secret on its own 15-second tick; the
// leader must not start signing with it before they can accept it.
func TestShouldRotateWaitsAfterSeeding(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if shouldRotate(now.Add(-30*time.Second), now, "self", []string{"self"}, nil, nil) {
		t.Fatal("rotated 30 seconds after the secret was seeded")
	}
	if shouldRotate(time.Time{}, now, "self", []string{"self"}, nil, nil) {
		t.Fatal("rotated with no seeding time")
	}
}
