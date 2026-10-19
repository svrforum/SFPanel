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
	sign, accept := proxySecretsAt("legacy", "random", "", now)
	if sign != "legacy" || !slices.Equal(accept, []string{"legacy", "random"}) {
		t.Fatalf("before rotation: %q %v", sign, accept)
	}
	// The random secret not replicated yet: the derived one alone.
	sign, accept = proxySecretsAt("legacy", "", "", now)
	if sign != "legacy" || !slices.Equal(accept, []string{"legacy"}) {
		t.Fatalf("no replicated secret: %q %v", sign, accept)
	}
	rotated := now.Add(-time.Minute).Format(time.RFC3339)
	sign, accept = proxySecretsAt("legacy", "random", rotated, now)
	if sign != "random" || !slices.Equal(accept, []string{"random", "legacy"}) {
		t.Fatalf("inside the grace: %q %v", sign, accept)
	}
	rotated = now.Add(-legacyProxyGrace - time.Second).Format(time.RFC3339)
	sign, accept = proxySecretsAt("legacy", "random", rotated, now)
	if sign != "random" || !slices.Equal(accept, []string{"random"}) {
		t.Fatalf("after the grace the derived secret must be dead: %q %v", sign, accept)
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
