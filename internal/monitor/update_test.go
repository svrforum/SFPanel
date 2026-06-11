package monitor

import "testing"

// setCachedLatest swaps the package-level update cache for the test and
// restores it afterwards (the cache is process-global).
func setCachedLatest(t *testing.T, v string) {
	t.Helper()
	updateMu.Lock()
	prev := cachedLatest
	cachedLatest = v
	updateMu.Unlock()
	t.Cleanup(func() {
		updateMu.Lock()
		cachedLatest = prev
		updateMu.Unlock()
	})
}

func TestGetUpdateInfo(t *testing.T) {
	cases := []struct {
		name    string
		latest  string
		current string
		want    bool
	}{
		{"no cached release yet", "", "0.13.0", false},
		{"same version", "0.13.0", "0.13.0", false},
		{"same version with v prefix", "0.13.0", "v0.13.0", false},
		{"older current", "0.13.1", "0.13.0", true},
		{"older current with v prefix", "0.13.1", "v0.13.0", true},
		// `git describe` interim build past the latest release — base
		// version compares equal, must not nag.
		{"dev suffix of latest", "0.13.0", "0.13.0-8-g61f85c0", false},
		{"dev suffix behind latest", "0.13.1", "0.13.0-8-g61f85c0", true},
		// Locally newer than the published release (pre-release tag pulled,
		// build ahead of the release pipeline) — a downgrade is not an update.
		{"locally newer", "0.13.0", "0.14.0", false},
		// Plain "dev" ldflags fallback in cmd/sfpanel — unparseable, never
		// flagged as updatable.
		{"plain dev fallback", "0.13.0", "dev", false},
		{"empty current", "0.13.0", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setCachedLatest(t, tc.latest)
			got := GetUpdateInfo(tc.current)
			if got.UpdateAvailable != tc.want {
				t.Errorf("GetUpdateInfo(%q) with latest=%q: UpdateAvailable = %v, want %v",
					tc.current, tc.latest, got.UpdateAvailable, tc.want)
			}
			if tc.want && got.LatestVersion != tc.latest {
				t.Errorf("LatestVersion = %q, want %q", got.LatestVersion, tc.latest)
			}
			if !tc.want && got.LatestVersion != "" {
				t.Errorf("LatestVersion = %q, want empty when no update", got.LatestVersion)
			}
		})
	}
}
